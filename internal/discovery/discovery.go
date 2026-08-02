// Package discovery finds other marraw daemons and announces this one.
//
// All of it lives in the daemon rather than the Electron shell for one
// practical reason: mDNS needs a listening UDP socket, and on Windows every
// program that opens one triggers its own firewall prompt. Advertising from
// the shell and serving from the daemon meant two prompts for what a user
// thinks of as one app — and declining the second left the machine reachable
// by address but invisible to a local-network scan, with nothing on screen to
// say so. One program, one rule.
package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/zeroconf/v2"
)

// Service is the DNS-SD service type this daemon advertises and browses for.
const Service = "_marraw._tcp"

// DefaultPort is the daemon's remote port. A bare hostname gets it appended,
// and it is the only port the Tailscale sweep can try — a peer's real port is
// unknowable without asking.
const DefaultPort = 8482

// browseWindow is how long a scan listens for mDNS answers: long enough for a
// sleepy access point, short enough that the UI does not feel stuck.
const browseWindow = 2500 * time.Millisecond

// probeTimeout bounds one /hello check. These are LAN or tailnet round trips;
// a machine that cannot answer this fast is not one we can usefully offer.
const probeTimeout = 1500 * time.Millisecond

// probeConcurrency caps simultaneous /hello checks — a tailnet can have many
// peers and we should not open a socket to all of them at once.
const probeConcurrency = 12

// Hello is what an unauthenticated GET /hello reports about a daemon.
type Hello struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Pairing bool   `json:"pairing"`
	App     string `json:"app"`
}

// Advertiser announces this daemon on the local network. Registration is
// restartable so renaming the machine takes effect without a relaunch.
type Advertiser struct {
	mu      sync.Mutex
	server  *zeroconf.Server
	err     error
	name    string
	port    int
	running bool
}

// Start announces the daemon as name on port, replacing any previous
// announcement. An error is remembered rather than returned fatally: a
// daemon that cannot announce itself is still perfectly usable by address,
// and Status surfaces the failure to the user instead.
func (a *Advertiser) Start(name string, port int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running && a.name == name && a.port == port {
		return
	}
	a.stopLocked()

	server, err := zeroconf.Register(name, Service, "local.", port, []string{"app=marraw"}, nil)
	a.name, a.port, a.err, a.running = name, port, err, err == nil
	if err != nil {
		log.Printf("mdns: cannot announce %q on %d: %v", name, port, err)
		return
	}
	a.server = server
	log.Printf("mdns: announcing %q on port %d", name, port)
}

// Stop withdraws the announcement.
func (a *Advertiser) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopLocked()
	a.err = nil
}

func (a *Advertiser) stopLocked() {
	if a.server != nil {
		a.server.Shutdown()
		a.server = nil
	}
	a.running = false
}

// Status reports whether the announcement is live, and why not if it is not.
func (a *Advertiser) Status() (bool, string) {
	if a == nil {
		return false, ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return false, a.err.Error()
	}
	return a.running, ""
}

// Browse listens for other marraw daemons on the local network and returns
// their "host:port" addresses. The socket is opened for the duration of one
// scan and closed after: a user who never touches remote features should
// never have a multicast listener running on their behalf.
func Browse(ctx context.Context) []string {
	ctx, cancel := context.WithTimeout(ctx, browseWindow)
	defer cancel()

	entries := make(chan *zeroconf.ServiceEntry, 32)
	found := make(chan []string, 1)
	go func() {
		var out []string
		seen := map[string]bool{}
		for e := range entries {
			// Prefer a literal IPv4: resolving the .local name needs working
			// mDNS resolution on top of working mDNS discovery, and on
			// Windows that second half is often missing.
			var host string
			if len(e.AddrIPv4) > 0 {
				host = e.AddrIPv4[0].String()
			} else if e.HostName != "" {
				host = strings.TrimSuffix(e.HostName, ".")
			}
			if host == "" {
				continue
			}
			addr := Normalize(net.JoinHostPort(host, strconv.Itoa(e.Port)))
			if !seen[addr] {
				seen[addr] = true
				out = append(out, addr)
			}
		}
		found <- out
	}()

	if err := zeroconf.Browse(ctx, Service, "local.", entries); err != nil {
		log.Printf("mdns: cannot browse: %v", err)
		close(entries)
		return <-found
	}
	<-ctx.Done()
	// zeroconf closes `entries` itself when the context ends; wait for the
	// collector to drain it.
	return <-found
}

// tailscaleBins are where the CLI lives per platform. There is no
// cross-platform way to ask and no Go binding, but the JSON status output is
// stable — and this is the only route to a tailnet, which mDNS cannot reach
// because multicast does not cross it.
var tailscaleBins = map[string][]string{
	"darwin": {
		"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
		"/usr/local/bin/tailscale",
		"/opt/homebrew/bin/tailscale",
	},
	"windows": {
		`C:\Program Files\Tailscale\tailscale.exe`,
		`C:\Program Files (x86)\Tailscale\tailscale.exe`,
	},
	"linux": {"/usr/bin/tailscale", "/usr/local/bin/tailscale"},
}

func tailscaleBin() string {
	for _, bin := range tailscaleBins[runtime.GOOS] {
		if _, err := os.Stat(bin); err == nil {
			return bin
		}
	}
	// Fall back to PATH.
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	return ""
}

// TailscalePeers returns online tailnet peers as "host:port". Silently empty
// when Tailscale is not installed or not running: this is an extra way to
// find machines, never a requirement.
func TailscalePeers(ctx context.Context) []string {
	bin := tailscaleBin()
	if bin == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, bin, "status", "--json").Output()
	if err != nil {
		log.Printf("tailscale: status unavailable: %v", err)
		return nil
	}
	var status struct {
		Peer map[string]struct {
			DNSName      string   `json:"DNSName"`
			HostName     string   `json:"HostName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
			Online       bool     `json:"Online"`
		} `json:"Peer"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		log.Printf("tailscale: unparseable status: %v", err)
		return nil
	}
	var peers []string
	for _, p := range status.Peer {
		if !p.Online {
			continue
		}
		// MagicDNS name if the tailnet has it, else the 100.x address.
		host := strings.TrimSuffix(p.DNSName, ".")
		if host == "" && len(p.TailscaleIPs) > 0 {
			host = p.TailscaleIPs[0]
		}
		if host != "" {
			peers = append(peers, Normalize(host))
		}
	}
	return peers
}

var probeClient = &http.Client{Timeout: probeTimeout}

// Probe asks one address whether it is a marraw daemon and what it calls
// itself. GET /hello is unauthenticated precisely so a machine with no
// credential yet can show a name instead of a bare address.
func Probe(ctx context.Context, hostPort string) (*Hello, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+Normalize(hostPort)+"/hello", nil)
	if err != nil {
		return nil, err
	}
	res, err := probeClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery: %s answered HTTP %d", hostPort, res.StatusCode)
	}
	var hello Hello
	if err := json.NewDecoder(http.MaxBytesReader(nil, res.Body, 4<<10)).Decode(&hello); err != nil {
		return nil, err
	}
	if hello.App != "marraw" {
		return nil, errors.New("discovery: not a marraw daemon")
	}
	if hello.Name == "" {
		hello.Name = Normalize(hostPort)
	}
	return &hello, nil
}

// ProbeAll checks candidates concurrently and returns the ones that answered,
// keyed by address.
func ProbeAll(ctx context.Context, candidates []string) map[string]*Hello {
	var (
		mu  sync.Mutex
		out = make(map[string]*Hello, len(candidates))
		wg  sync.WaitGroup
	)
	gate := make(chan struct{}, probeConcurrency)
	for _, addr := range candidates {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			gate <- struct{}{}
			defer func() { <-gate }()
			hello, err := Probe(ctx, addr)
			if err != nil {
				return
			}
			mu.Lock()
			out[addr] = hello
			mu.Unlock()
		}(addr)
	}
	wg.Wait()
	return out
}

// Normalize turns "host" or "host:port" into "host:port", defaulting the
// port. IPv6 literals and anything already carrying a port pass through. It
// must agree exactly with the shell's saved-connection normalisation, or a
// discovered machine and a saved one look like different hosts.
func Normalize(host string) string {
	h := strings.TrimSpace(host)
	h = strings.TrimPrefix(strings.TrimPrefix(h, "http://"), "https://")
	h = strings.TrimRight(h, "/")
	if h == "" {
		return h
	}
	if !strings.Contains(h, ":") {
		return h + ":" + strconv.Itoa(DefaultPort)
	}
	return h
}

// HostOnly strips the port from a "host:port", handling IPv6 literals.
func HostOnly(hostPort string) string {
	if host, _, err := net.SplitHostPort(hostPort); err == nil {
		return host
	}
	return hostPort
}

// LocalAddresses returns this machine's own addresses — every non-loopback
// interface IP, plus the hostname — so a scan does not offer the user their
// own computer.
func LocalAddresses() map[string]bool {
	self := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	if h, err := os.Hostname(); err == nil && h != "" {
		self[h] = true
		self[h+".local"] = true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return self
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			self[ipnet.IP.String()] = true
		}
	}
	return self
}

// ReachableAddresses returns the "host:port" addresses another machine could
// use to reach this one, so Settings can show them rather than leaving the
// user to guess. Loopback and link-local are left out — they are no use to
// anyone else.
func ReachableAddresses(port int) []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipnet.IP
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.To4() == nil {
			continue
		}
		out = append(out, net.JoinHostPort(ip.String(), strconv.Itoa(port)))
	}
	return out
}

// UITestHostsEnv lets the screenshot harness stand in a scan result. A scan
// on one machine can only ever find nothing — LocalAddresses filters this
// computer out — so there is otherwise no way to exercise the found-rows UI.
const UITestHostsEnv = "MARRAW_UITEST_HOSTS"
