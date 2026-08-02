package api

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/discovery"
)

// System exposes app-level maintenance: preview-cache inspection, clearing,
// and relocation.
type System struct {
	deps *Deps
}

const cacheInfoKey = "cacheInfo"

// customPreviewsSubdir is created inside a user-picked folder so the cache is
// self-contained and safe to wipe without touching the user's other files.
const customPreviewsSubdir = "marraw-previews"

// CacheInfo describes the preview cache's location, disk usage, and cap.
type CacheInfo struct {
	Dir      string `json:"dir"`
	Bytes    int64  `json:"bytes"`
	Files    int64  `json:"files"`
	IsCustom bool   `json:"isCustom"` // false when the default location is in use
	CapBytes int64  `json:"capBytes"` // janitor eviction threshold
}

// GetCacheInfo returns the cache location and current disk usage. Subscription
// query: ClearCache and SetCacheDir push an update. The size is measured by
// walking the cache, so it is fetched on demand rather than folded into the
// always-live app settings.
func (s *System) GetCacheInfo(ctx context.Context) (*CacheInfo, error) {
	aprot.RegisterRefreshTrigger(ctx, cacheInfoKey)
	return s.cacheInfo(ctx), nil
}

func (s *System) cacheInfo(ctx context.Context) *CacheInfo {
	bytes, files := s.deps.Cache.Stat()
	var cap int64
	if s.deps.Janitor != nil {
		cap = s.deps.Janitor.Cap()
	}
	return &CacheInfo{
		Dir:      s.deps.Cache.Dir(),
		Bytes:    bytes,
		Files:    files,
		IsCustom: s.deps.DB.CacheDir(ctx) != "",
		CapBytes: cap,
	}
}

// SetCacheCap adjusts the preview-cache size limit (GiB), effective for the
// janitor's next sweep and persisted for future launches.
func (s *System) SetCacheCap(ctx context.Context, gb int) (*CacheInfo, error) {
	if gb < 1 || gb > 2048 {
		return nil, aprot.ErrInvalidParams("cache cap must be 1..2048 GiB")
	}
	if s.deps.Janitor != nil {
		s.deps.Janitor.SetCap(int64(gb) << 30)
	}
	if err := s.deps.DB.SetSetting(ctx, "cacheCapGB", strconv.Itoa(gb)); err != nil {
		return nil, err
	}
	aprot.TriggerRefresh(ctx, cacheInfoKey)
	return s.cacheInfo(ctx), nil
}

// ClearCache deletes every cached rendition; they regenerate on demand.
func (s *System) ClearCache(ctx context.Context) (*CacheInfo, error) {
	if err := s.deps.Cache.Clear(); err != nil {
		return nil, err
	}
	aprot.TriggerRefresh(ctx, cacheInfoKey)
	return s.cacheInfo(ctx), nil
}

// RemoteAccessInfo describes how this daemon can be reached from another
// machine: the pairing token remote clients authenticate with, the name it
// answers discovery with, and what the daemon is actually listening on.
type RemoteAccessInfo struct {
	PairingToken string `json:"pairingToken"`
	ListenAddr   string `json:"listenAddr"`
	LoopbackOnly bool   `json:"loopbackOnly"`
	// DeviceName is what other machines see when they find this one.
	DeviceName string `json:"deviceName"`
	// PairingOpen reports whether new pairing requests are accepted. The user
	// can shut the door once their machines are paired without giving up
	// remote access itself.
	PairingOpen bool `json:"pairingOpen"`
	// Addresses are the "host:port" addresses another machine can use to
	// reach this one. Shown so nobody has to go hunting for their own IP.
	Addresses []string `json:"addresses"`
	// Advertising reports whether this machine is announcing itself on the
	// local network, and AdvertiseError says why not. A blocked multicast
	// socket is the one failure a user cannot otherwise see: the daemon looks
	// perfectly healthy while being invisible to a scan.
	Advertising    bool   `json:"advertising"`
	AdvertiseError string `json:"advertiseError"`
}

const remoteAccessKey = "remoteAccess"

func (s *System) remoteAccessInfo(ctx context.Context) *RemoteAccessInfo {
	info := &RemoteAccessInfo{
		ListenAddr:   s.deps.ListenAddr,
		LoopbackOnly: s.deps.LoopbackOnly,
		DeviceName:   s.deps.DeviceName(ctx),
		PairingOpen:  s.deps.PairingOpen(ctx),
	}
	if s.deps.Tokens != nil {
		info.PairingToken = s.deps.Tokens.Pairing()
	}
	if !s.deps.LoopbackOnly {
		if _, portStr, err := net.SplitHostPort(s.deps.ListenAddr); err == nil {
			if port, err := strconv.Atoi(portStr); err == nil {
				info.Addresses = discovery.ReachableAddresses(port)
			}
		}
		info.Advertising, info.AdvertiseError = s.deps.Advertiser.Status()
	}
	return info
}

// GetRemoteAccess returns the pairing token, device name and listen address
// for the Settings dialog's Remote section. Subscription query: renaming the
// machine or toggling pairing pushes an update.
func (s *System) GetRemoteAccess(ctx context.Context) (*RemoteAccessInfo, error) {
	aprot.RegisterRefreshTrigger(ctx, remoteAccessKey)
	return s.remoteAccessInfo(ctx), nil
}

// SetDeviceName renames this machine as other machines see it — in their scan
// results and in the approval dialog. An empty name restores the hostname.
func (s *System) SetDeviceName(ctx context.Context, name string) (*RemoteAccessInfo, error) {
	if len([]rune(name)) > 64 {
		return nil, aprot.ErrInvalidParams("name must be 64 characters or fewer")
	}
	if err := s.deps.DB.SetSetting(ctx, DeviceNameKey, name); err != nil {
		return nil, err
	}
	// Re-announce under the new name, so other machines see the rename now
	// rather than after a relaunch.
	s.deps.StartAdvertising(ctx)
	aprot.TriggerRefresh(ctx, remoteAccessKey)
	return s.remoteAccessInfo(ctx), nil
}

// SetPairingOpen turns new pairing requests on or off. Already-approved
// devices keep working either way — this only closes the door to new ones.
func (s *System) SetPairingOpen(ctx context.Context, open bool) (*RemoteAccessInfo, error) {
	v := "true"
	if !open {
		v = "false"
	}
	if err := s.deps.DB.SetSetting(ctx, PairingOpenKey, v); err != nil {
		return nil, err
	}
	aprot.TriggerRefresh(ctx, remoteAccessKey)
	return s.remoteAccessInfo(ctx), nil
}

// DiscoveredHost is one marraw machine a scan turned up. Source says how it
// was found — "Local network" and "Tailscale" mean different things to
// someone deciding whether a machine is theirs.
type DiscoveredHost struct {
	Host    string `json:"host"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// Pairing is false when that machine has stopped accepting new
	// connections, so the UI can say so instead of offering a dead button.
	Pairing bool `json:"pairing"`
	Source  string `json:"source"` // "mdns" | "tailscale"
}

// ScanForHosts looks for other marraw machines: mDNS on the local network,
// Tailscale peers across a tailnet. Both run concurrently, and every
// candidate is confirmed with GET /hello — which is also what supplies the
// name, so a Tailscale-found machine shows something recognisable rather than
// a bare 100.x address.
//
// exclude carries the connections the caller has already saved; a scan should
// surface machines the user has not set up yet, not repeat the ones they have.
// This machine's own addresses are always excluded.
func (s *System) ScanForHosts(ctx context.Context, exclude []string) ([]DiscoveredHost, error) {
	if raw := os.Getenv(discovery.UITestHostsEnv); raw != "" {
		// Harness stand-in: a scan on one machine can only ever find nothing,
		// since this computer filters itself out.
		var seeded []DiscoveredHost
		if err := json.Unmarshal([]byte(raw), &seeded); err == nil {
			return seeded, nil
		}
	}

	var (
		mdnsHosts []string
		tsHosts   []string
		wg        sync.WaitGroup
	)
	wg.Add(2)
	go func() { defer wg.Done(); mdnsHosts = discovery.Browse(ctx) }()
	go func() { defer wg.Done(); tsHosts = discovery.TailscalePeers(ctx) }()
	wg.Wait()

	self := discovery.LocalAddresses()
	skip := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		skip[discovery.Normalize(e)] = true
	}

	// mDNS wins the label when a machine turns up both ways: "Local network"
	// is the more useful thing to tell someone standing next to it.
	source := map[string]string{}
	var candidates []string
	for _, group := range []struct {
		hosts []string
		label string
	}{{mdnsHosts, "mdns"}, {tsHosts, "tailscale"}} {
		for _, h := range group.hosts {
			if skip[h] || self[discovery.HostOnly(h)] {
				continue
			}
			if _, seen := source[h]; seen {
				continue
			}
			source[h] = group.label
			candidates = append(candidates, h)
		}
	}

	found := discovery.ProbeAll(ctx, candidates)
	out := make([]DiscoveredHost, 0, len(found))
	for addr, hello := range found {
		out = append(out, DiscoveredHost{
			Host:    addr,
			Name:    hello.Name,
			Version: hello.Version,
			Pairing: hello.Pairing,
			Source:  source[addr],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// PairingRequest is one machine waiting to be let in. Code is shown on both
// machines so the person approving can tell one request from another; it is
// not a secret and not a password.
type PairingRequest struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Platform  string `json:"platform"`
	Addr      string `json:"addr"`
	ExpiresAt int64  `json:"expiresAt"`
}

const pairingKey = "pairing"

// ListPairingRequests returns the connection requests waiting for approval.
// Subscription query: a new request, an approval, a denial, or a timeout all
// push an update, which is what opens and closes the dialog.
//
// It answers only windows on this machine. A remote client — including one
// holding a valid device token — always sees an empty list, so nobody can
// approve their own way in from the machine asking to be let in.
func (s *System) ListPairingRequests(ctx context.Context) ([]PairingRequest, error) {
	aprot.RegisterRefreshTrigger(ctx, pairingKey)
	out := []PairingRequest{}
	if s.deps.Pairing == nil || !s.deps.ConnIsLocal(ctx) {
		return out, nil
	}
	for _, r := range s.deps.Pairing.Pending() {
		out = append(out, PairingRequest{
			ID:        r.ID,
			Code:      r.Code,
			Name:      r.Name,
			Platform:  r.Platform,
			Addr:      r.Addr,
			ExpiresAt: r.ExpiresAt.UnixMilli(),
		})
	}
	return out, nil
}

// ResolvePairing approves or denies a waiting request. Approving mints a
// token belonging to that machine alone, so it can later be revoked on its
// own. Local windows only, for the same reason ListPairingRequests is.
func (s *System) ResolvePairing(ctx context.Context, id string, approve bool) error {
	if s.deps.Pairing == nil {
		return aprot.ErrInvalidParams("pairing is not enabled on this daemon")
	}
	if !s.deps.ConnIsLocal(ctx) {
		return aprot.ErrAuthFailed("only a window on this machine can approve a connection")
	}
	defer aprot.TriggerRefresh(ctx, pairingKey)

	if !approve {
		if err := s.deps.Pairing.Resolve(id, false, ""); err != nil {
			return aprot.ErrInvalidParams(err.Error())
		}
		return nil
	}

	req, ok := s.deps.Pairing.Find(id)
	if !ok {
		return aprot.ErrInvalidParams("that request is no longer waiting")
	}
	device, err := NewDevice(req.Name, req.Addr)
	if err != nil {
		return err
	}

	// Persist before handing the token out: a device that authenticates but
	// vanishes on restart would be worse than a failed approval. If the
	// request expired out from under us, put the list back.
	prev := s.deps.Tokens.Devices()
	next := append(prev, device)
	if err := SaveDevices(ctx, s.deps.DB, next); err != nil {
		return err
	}
	s.deps.Tokens.SetDevices(next)
	if err := s.deps.Pairing.Resolve(id, true, device.Token); err != nil {
		s.deps.Tokens.SetDevices(prev)
		_ = SaveDevices(ctx, s.deps.DB, prev)
		return aprot.ErrInvalidParams(err.Error())
	}
	// The machine just approved has to show up in "Approved computers" now,
	// not whenever that list next happens to be re-read.
	aprot.TriggerRefresh(ctx, devicesKey)
	return nil
}

// RemoteDeviceInfo is one approved machine, as shown in Settings. It
// deliberately carries no token: the credential exists only in the daemon and
// on the machine it was minted for.
type RemoteDeviceInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Addr     string `json:"addr"`
	AddedAt  int64  `json:"addedAt"`
	LastSeen int64  `json:"lastSeen"`
}

const devicesKey = "remoteDevices"

// ListRemoteDevices returns the machines approved to connect to this one.
// Subscription query: approving or revoking pushes an update.
func (s *System) ListRemoteDevices(ctx context.Context) ([]RemoteDeviceInfo, error) {
	aprot.RegisterRefreshTrigger(ctx, devicesKey)
	out := []RemoteDeviceInfo{}
	if s.deps.Tokens == nil {
		return out, nil
	}
	for _, d := range s.deps.Tokens.Devices() {
		out = append(out, RemoteDeviceInfo{
			ID: d.ID, Name: d.Name, Addr: d.Addr, AddedAt: d.AddedAt, LastSeen: d.LastSeen,
		})
	}
	return out, nil
}

// RevokeRemoteDevice withdraws one machine's access and drops its live
// connection, so a laptop that has been lost or lent out stops working now
// rather than at its next reconnect. Other devices are untouched.
func (s *System) RevokeRemoteDevice(ctx context.Context, id string) error {
	if s.deps.Tokens == nil {
		return aprot.ErrInvalidParams("no devices on this daemon")
	}
	if !s.deps.ConnIsLocal(ctx) {
		return aprot.ErrAuthFailed("only a window on this machine can revoke a device")
	}
	devices := s.deps.Tokens.Devices()
	next := make([]Device, 0, len(devices))
	found := false
	for _, d := range devices {
		if d.ID == id {
			found = true
			continue
		}
		next = append(next, d)
	}
	if !found {
		return aprot.ErrInvalidParams("unknown device")
	}
	if err := SaveDevices(ctx, s.deps.DB, next); err != nil {
		return err
	}
	s.deps.Tokens.SetDevices(next)
	s.deps.DisconnectDevice(id)
	aprot.TriggerRefresh(ctx, devicesKey)
	return nil
}


// RegeneratePairingToken replaces the pairing token, invalidating the old one
// for new connections. Clients already connected stay until they disconnect.
func (s *System) RegeneratePairingToken(ctx context.Context) (*RemoteAccessInfo, error) {
	tok, err := GeneratePairingToken()
	if err != nil {
		return nil, err
	}
	if err := s.deps.DB.SetSetting(ctx, PairingTokenKey, tok); err != nil {
		return nil, err
	}
	if s.deps.Tokens != nil {
		s.deps.Tokens.SetPairing(tok)
	}
	aprot.TriggerRefresh(ctx, remoteAccessKey)
	return s.remoteAccessInfo(ctx), nil
}

// SetCacheDir relocates the preview cache. An empty path restores the default
// location; otherwise the cache moves into "<path>/marraw-previews". The
// previous cache is wiped (its previews are regenerable), and the change takes
// effect immediately as well as persisting for the next launch.
func (s *System) SetCacheDir(ctx context.Context, path string) (*CacheInfo, error) {
	target := s.deps.DefaultCacheDir
	persist := ""
	if path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, aprot.ErrInvalidParams("invalid cache folder: " + err.Error())
		}
		target = filepath.Join(abs, customPreviewsSubdir)
		persist = target
	}
	if err := s.deps.Cache.Relocate(target); err != nil {
		return nil, aprot.ErrInvalidParams("cannot use that cache folder: " + err.Error())
	}
	if err := s.deps.DB.SetCacheDirSetting(ctx, persist); err != nil {
		return nil, err
	}
	aprot.TriggerRefresh(ctx, cacheInfoKey)
	return s.cacheInfo(ctx), nil
}
