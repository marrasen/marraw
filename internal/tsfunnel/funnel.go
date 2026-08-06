// Package tsfunnel publishes the daemon's HTTP port on the public internet
// through Tailscale Funnel, which is what makes a share link openable by
// someone who has no Tailscale account and is not on the tailnet.
//
// Everything here shells out to the tailscale CLI, for the same reason
// internal/discovery does: there is no Go binding, and the CLI's JSON output
// is the stable interface. It follows that package's policy too — a missing
// or logged-out Tailscale is reported as "unavailable", never as an error the
// user has to dismiss. Sharing is a feature you may not have set up, not a
// failure of the app.
package tsfunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/marrasen/marraw/internal/discovery"
)

// Status is what the share UI needs to know about the tunnel.
type Status struct {
	// Available: the CLI is installed and the node is logged in and running.
	Available bool `json:"available"`
	// Running: a funnel we started is live.
	Running bool `json:"running"`
	// Hostname is the node's public DNS name, e.g. "box.tailnet.ts.net".
	Hostname string `json:"hostname"`
	// Err carries the CLI's own message when something failed, so the dialog
	// can show what Tailscale actually said (an ACL that forbids Funnel, a
	// node that needs re-authenticating) rather than a generic failure.
	Err string `json:"err"`
}

// Manager owns the funnel for one port.
type Manager struct {
	mu      sync.Mutex
	port    int
	started bool
	lastErr string
	// host and ips cache the node's identity on the tailnet, which does not
	// change while the daemon runs and costs a subprocess to read.
	host string
	ips  []string
}

func New(port int) *Manager { return &Manager{port: port} }

// SetPort updates the port the funnel targets. Only meaningful before Enable.
func (m *Manager) SetPort(port int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.port = port
}

// run executes a tailscale subcommand with a bounded timeout, returning the
// combined output so a failure can be shown verbatim.
func run(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	bin := discovery.TailscaleBin()
	if bin == "" {
		return "", fmt.Errorf("tailscale is not installed")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// self reads the node's public hostname and its own addresses on the tailnet.
// Both empty when Tailscale is not installed, not running, or not logged in.
func (m *Manager) self(ctx context.Context) (string, []string) {
	m.mu.Lock()
	cachedHost, cachedIPs := m.host, m.ips
	m.mu.Unlock()
	if cachedHost != "" {
		return cachedHost, cachedIPs
	}
	out, err := run(ctx, 3*time.Second, "status", "--json")
	if err != nil {
		return "", nil
	}
	var status struct {
		BackendState string
		Self         struct {
			DNSName      string
			TailscaleIPs []string
		}
	}
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		return "", nil
	}
	if status.BackendState != "Running" {
		return "", nil
	}
	// DNSName comes back fully qualified with a trailing dot.
	name := strings.TrimSuffix(status.Self.DNSName, ".")
	ips := status.Self.TailscaleIPs
	m.mu.Lock()
	m.host, m.ips = name, ips
	m.mu.Unlock()
	return name, ips
}

// selfDNSName reads the node's public hostname. Empty when Tailscale is not
// installed, not running, or not logged in.
func (m *Manager) selfDNSName(ctx context.Context) string {
	name, _ := m.self(ctx)
	return name
}

// TailnetIPs are this node's own addresses on the tailnet — the ones a peer
// dials, and the only ones a listener can bind to reach the tailnet without
// also answering on the local network.
func (m *Manager) TailnetIPs(ctx context.Context) []string {
	_, ips := m.self(ctx)
	return ips
}

// Status reports what the share UI should show.
func (m *Manager) Status(ctx context.Context) Status {
	host := m.selfDNSName(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	return Status{
		Available: host != "",
		Running:   m.started,
		Hostname:  host,
		Err:       m.lastErr,
	}
}

// Enable publishes the daemon's port. Idempotent: re-running the CLI with the
// same target is how Tailscale itself expects the config to be set, so a
// second call is harmless.
func (m *Manager) Enable(ctx context.Context) error {
	m.mu.Lock()
	port := m.port
	m.mu.Unlock()
	if port == 0 {
		return fmt.Errorf("no port to publish")
	}
	if m.selfDNSName(ctx) == "" {
		err := fmt.Errorf("tailscale is not installed or not logged in")
		m.setErr(err.Error())
		return err
	}
	// --bg: configure and return, rather than holding the tunnel open for as
	// long as the command runs.
	out, err := run(ctx, 20*time.Second, "funnel", "--bg", strconv.Itoa(port))
	if err != nil {
		// The CLI explains refusals better than we can — an ACL that does not
		// grant funnel, a tailnet where it is disabled — so keep its words.
		msg := out
		if msg == "" {
			msg = err.Error()
		}
		m.setErr(msg)
		return fmt.Errorf("%s", msg)
	}
	m.mu.Lock()
	m.started = true
	m.lastErr = ""
	m.mu.Unlock()
	log.Printf("funnel: publishing port %d", port)
	return nil
}

// Disable withdraws the funnel.
//
// It only ever runs when this process turned the funnel on. The CLI's off
// switch in current versions is `funnel reset`, which clears the whole serve
// config — so firing it at a funnel somebody else configured would take down
// their service too. Leaving a tunnel up is the lesser harm, and the share
// links it carries have already been revoked by the time this is called.
func (m *Manager) Disable(ctx context.Context) error {
	m.mu.Lock()
	started := m.started
	m.mu.Unlock()
	if !started {
		return nil
	}
	out, err := run(ctx, 20*time.Second, "funnel", "reset")
	if err != nil {
		msg := out
		if msg == "" {
			msg = err.Error()
		}
		m.setErr(msg)
		return fmt.Errorf("%s", msg)
	}
	m.mu.Lock()
	m.started = false
	m.lastErr = ""
	m.mu.Unlock()
	log.Printf("funnel: withdrawn")
	return nil
}

// Hostname is the node's tailnet name, whether or not anything is published.
func (m *Manager) Hostname(ctx context.Context) string { return m.selfDNSName(ctx) }

// BaseURL is the public origin share links are built on, or "" when nothing is
// published.
//
// Gated on the funnel actually being up, not merely on the node having a name.
// A tailnet that does not permit Funnel still answers `status --json` with a
// perfectly good DNS name, and minting links against it would hand the owner a
// public-looking URL that resolves to nothing — the worst possible failure,
// because it only shows up once someone else has already been sent it.
func (m *Manager) BaseURL(ctx context.Context) string {
	m.mu.Lock()
	started := m.started
	m.mu.Unlock()
	if !started {
		return ""
	}
	host := m.selfDNSName(ctx)
	if host == "" {
		return ""
	}
	return "https://" + host
}

func (m *Manager) setErr(msg string) {
	m.mu.Lock()
	m.lastErr = msg
	m.mu.Unlock()
}
