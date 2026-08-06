package api

import (
	"context"
	"log"
	"net"
	"path/filepath"
	"strconv"
	"time"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/discovery"
	"github.com/marrasen/marraw/internal/store"
)

// Share mints and manages guest links: one shoot, shared with someone who has
// no marraw and no tailnet account, opened in their browser over a Tailscale
// Funnel URL. The credential and its confinement live in guest.go; this is the
// management surface around it.
//
// Every method except Session is local-windows-only. A share is the owner
// handing out access to their own library, so it is not a decision a paired
// laptop — let alone a guest — gets to make.
//
// Every read-modify-write of the link list is serialized on Deps.guestMu,
// which a guest connecting also takes to stamp its link's last-opened time.
type Share struct {
	deps *Deps
	lib  *Library
}

const shareKey = "shareLinks"

// ShareLink is one share, as the management list sees it.
//
// Unlike RemoteDeviceInfo this does carry the credential, inside URL. It has
// to: "copy the link again" is the whole point of the list, and a link the
// owner cannot re-copy is a link they will delete and re-mint. That is safe
// only because every method here refuses non-local callers.
type ShareLink struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	FolderID   int64     `json:"folderId"`
	Caps       GuestCaps `json:"caps"`
	ExpiresAt  int64     `json:"expiresAt"`
	CreatedAt  int64     `json:"createdAt"`
	LastSeen   int64     `json:"lastSeen"`
	Expired    bool      `json:"expired"`
	PhotoCount int       `json:"photoCount"`
	URL        string    `json:"url"`
	// Online: someone has this link open right now. LastSeen is when it was
	// last opened, which is what the UI shows once they have gone.
	Online bool `json:"online"`
	// ExportName is the export preset downloads are rendered with, as it was
	// named when the link was minted; empty means the defaults.
	ExportName string `json:"exportName"`
	// Reach is who this link was minted for, normalized — the list says so
	// per row, because "who can open this" is the one thing about a share the
	// owner cannot change afterwards.
	Reach ShareReach `json:"reach"`
}

// ShareStatus describes the tunnel the links are served over.
type ShareStatus struct {
	// Available: Tailscale is installed, running and logged in.
	Available bool `json:"available"`
	// Running: a funnel this daemon started is live.
	Running bool `json:"running"`
	// Hostname is the node's public name, shown so the owner can see what
	// they are about to hand out.
	Hostname string `json:"hostname"`
	// Err is Tailscale's own words when publishing failed — an ACL that does
	// not permit Funnel says so far better than we could.
	Err string `json:"err"`
	// LinkCount is how many shares are live, so the UI can warn before the
	// tunnel comes down.
	LinkCount int `json:"linkCount"`
	// Base is the origin a public link minted right now would be served on,
	// empty when this machine has no address anyone else could reach.
	// TailnetBase is the same for a tailnet-only link, empty when this machine
	// is not on a tailnet. The share dialog asks before it offers to mint: a
	// link is only discovered to be unreachable by the person it was sent to,
	// so the owner has to learn it here instead.
	Base        string `json:"base"`
	TailnetBase string `json:"tailnetBase"`
}

// GuestSession is what the shared page needs to know about itself. It is the
// one method a guest may call beyond culling: without it the page would have
// to be told its folder by the URL, and a folder ID in the URL is a thing
// people edit.
type GuestSession struct {
	Name     string    `json:"name"`
	FolderID int64     `json:"folderId"`
	Caps     GuestCaps `json:"caps"`
}

// Session returns the shared album this connection is looking at.
func (s *Share) Session(ctx context.Context) (*GuestSession, error) {
	link := s.deps.GuestLink(ctx)
	if link == nil {
		return nil, aprot.ErrForbidden("not a shared link")
	}
	return &GuestSession{Name: link.Name, FolderID: link.FolderID, Caps: link.Caps}, nil
}

// Status reports whether sharing can work on this machine, and how.
// Subscription query: minting or revoking a link pushes an update.
func (s *Share) Status(ctx context.Context) (*ShareStatus, error) {
	if !s.deps.ConnIsLocal(ctx) {
		return nil, aprot.ErrAuthFailed("local windows only")
	}
	aprot.RegisterRefreshTrigger(ctx, shareKey)
	out := &ShareStatus{Base: s.shareBase(ctx, ReachPublic), TailnetBase: s.tailnetBase(ctx)}
	if s.deps.Tokens != nil {
		out.LinkCount = len(s.deps.Tokens.Guests())
	}
	if s.deps.Funnel == nil {
		return out, nil
	}
	st := s.deps.Funnel.Status(ctx)
	out.Available, out.Running, out.Hostname, out.Err = st.Available, st.Running, st.Hostname, st.Err
	return out, nil
}

// ListLinks returns the live shares. Subscription query.
func (s *Share) ListLinks(ctx context.Context) ([]ShareLink, error) {
	if !s.deps.ConnIsLocal(ctx) {
		return nil, aprot.ErrAuthFailed("local windows only")
	}
	aprot.RegisterRefreshTrigger(ctx, shareKey)
	out := []ShareLink{}
	if s.deps.Tokens == nil {
		return out, nil
	}
	// Two bases, resolved once: the guest list is read on every refresh, and
	// each of these can cost a tailscale subprocess on a cold cache.
	bases := map[ShareReach]string{
		ReachPublic:  s.shareBase(ctx, ReachPublic),
		ReachTailnet: s.shareBase(ctx, ReachTailnet),
	}
	now := time.Now()
	for _, g := range s.deps.Tokens.Guests() {
		out = append(out, s.toShareLink(ctx, g, bases[NormalizeReach(g.Reach)], now))
	}
	return out, nil
}

// CreateLink shares one folder and returns the link to send.
//
// expiresInHours 0 never expires. Hours rather than days because the useful
// lifetime of a share is often an afternoon — "look through these before you
// leave" — and the dialog defaults to a bounded one either way: the URL is the
// credential, and it lives in someone else's message history long after the
// shoot is delivered.
//
// exportPresetID names one of the owner's saved export presets to render
// downloads with; empty renders at the endpoint's defaults.
//
// reach picks who the link is for — see ShareReach. It is fixed at mint time,
// and an empty value means public, which is what every link was before the
// choice existed.
func (s *Share) CreateLink(ctx context.Context, path string, caps GuestCaps, expiresInHours int, exportPresetID string, reach ShareReach) (*ShareLink, error) {
	if !s.deps.ConnIsLocal(ctx) {
		return nil, aprot.ErrAuthFailed("local windows only")
	}
	if s.deps.Tokens == nil {
		return nil, aprot.ErrInvalidParams("sharing is unavailable on this daemon")
	}
	if expiresInHours < 0 {
		return nil, aprot.ErrInvalidParams("expiry must not be negative")
	}
	// Scan the folder so the guest has photo rows to list. Deliberately the
	// scanner rather than Library.OpenFolder: sharing a shoot must not move
	// the owner's focus, retarget the watcher, or start a pre-render pass for
	// a folder they are not looking at.
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, aprot.ErrInvalidParams(err.Error())
	}
	recursive := s.lib.scanRecursionFor(ctx, abs)
	folderID, count, err := s.deps.Scanner.OpenFolder(ctx, abs, recursive)
	if err != nil {
		return nil, aprot.ErrInvalidParams(err.Error())
	}

	var expiresAt int64
	if expiresInHours > 0 {
		expiresAt = time.Now().Add(time.Duration(expiresInHours) * time.Hour).UnixMilli()
	}
	link, err := NewGuestLink(folderID, abs, filepath.Base(abs), caps, expiresAt, s.resolveExport(ctx, exportPresetID), reach)
	if err != nil {
		return nil, err
	}
	s.deps.guestMu.Lock()
	links := append(s.deps.Tokens.Guests(), link)
	err = SaveGuestLinks(ctx, s.deps.DB, links)
	if err == nil {
		s.deps.Tokens.SetGuests(links)
	}
	s.deps.guestMu.Unlock()
	if err != nil {
		return nil, err
	}

	// Publish the tunnel now rather than at first visit: the owner is about to
	// send this URL to somebody, and a link that only starts working minutes
	// later reads as a broken link. A funnel that cannot be raised is not
	// fatal — the link still works over the tailnet or the LAN — so the error
	// is reported through Status, not returned here.
	//
	// Only for a public link. Choosing "my devices only" and having the app
	// publish the machine to the internet anyway would make the switch a lie.
	if s.deps.Funnel != nil && link.Reach == ReachPublic {
		if err := s.deps.Funnel.Enable(context.WithoutCancel(ctx)); err != nil {
			log.Printf("share: funnel unavailable: %v", err)
		}
	}
	aprot.TriggerRefresh(ctx, shareKey)
	out := s.toShareLink(ctx, link, s.shareBase(ctx, link.Reach), time.Now())
	out.PhotoCount = count
	return &out, nil
}

// RevokeLink withdraws one share and drops the guest's live connection, so a
// link that has been forwarded somewhere it should not have been stops working
// now rather than at the visitor's next reload.
func (s *Share) RevokeLink(ctx context.Context, id string) error {
	if !s.deps.ConnIsLocal(ctx) {
		return aprot.ErrAuthFailed("local windows only")
	}
	if s.deps.Tokens == nil {
		return aprot.ErrInvalidParams("sharing is unavailable on this daemon")
	}
	s.deps.guestMu.Lock()
	next := []GuestLink{}
	for _, g := range s.deps.Tokens.Guests() {
		if g.ID != id {
			next = append(next, g)
		}
	}
	err := SaveGuestLinks(ctx, s.deps.DB, next)
	if err == nil {
		s.deps.Tokens.SetGuests(next)
	}
	remaining := PublicLinkCount(next)
	s.deps.guestMu.Unlock()
	if err != nil {
		return err
	}
	s.deps.DisconnectGuest(id)
	// No public share left: take the tunnel down rather than leaving the
	// machine published to the internet for a share that no longer exists.
	// Counted over public links only — tailnet-only shares are served off the
	// tailnet listener and never wanted the tunnel in the first place.
	if remaining == 0 && s.deps.Funnel != nil {
		if err := s.deps.Funnel.Disable(context.WithoutCancel(ctx)); err != nil {
			log.Printf("share: withdrawing funnel: %v", err)
		}
	}
	aprot.TriggerRefresh(ctx, shareKey)
	return nil
}

// resolveExport turns a preset ID into the settings a guest's downloads will
// be rendered with. Nil when no preset was chosen, or when the named one has
// since been deleted — a share that quietly renders at the defaults is better
// than one that refuses to mint over a stale ID.
func (s *Share) resolveExport(ctx context.Context, presetID string) *ShareExport {
	if presetID == "" {
		return nil
	}
	for _, p := range jsonSetting(ctx, s.deps.DB, settingUIExportPresets, []ExportPreset(nil)) {
		if p.ID != presetID {
			continue
		}
		// Normalize on read, as the settings reader does: a preset written by
		// an older build may be missing fields that would otherwise render as
		// quality 0.
		o := normalizeExportOptions(p.Options)
		out := &ShareExport{
			Name:           p.Name,
			JpegQuality:    o.JpegQuality,
			ColorSpace:     string(o.ColorSpace),
			SharpenTarget:  string(o.SharpenTarget),
			SharpenAmount:  string(o.SharpenAmount),
			ExifMode:       string(o.ExifMode),
			RemoveLocation: o.RemoveLocation,
			WatermarkID:    o.WatermarkID,
		}
		// "full" keeps EdgePx around for when the user switches back, so only
		// an explicit edge resize becomes a long edge.
		if o.ResizeMode == "edge" {
			out.LongEdge = o.EdgePx
		}
		// Format is deliberately not carried: the download endpoint serves
		// image/jpeg, and a preset set to TIFF or "RAW + XMP" describes a
		// delivery to disk, not a photo someone taps to save on a phone.
		return out
	}
	return nil
}

// ExportCredit returns the artist and copyright the user set in the export
// dialog. A photo downloaded from a share leaves the library exactly as an
// exported one does, so it carries the same credit — read here rather than
// hardcoded in main so the two paths cannot drift.
func ExportCredit(ctx context.Context, db *store.DB) (artist, copyright string) {
	opts := normalizeExportOptions(jsonSetting(ctx, db, settingUIExportOptions, ExportOptions{}))
	return opts.Artist, opts.Copyright
}

// shareBase is the origin a link of this reach is served on, in descending
// order of preference — and the URL always names something that actually
// answers, because a link is discovered to be broken by the person you sent
// it to.
//
// Computed per read rather than stored on the link: a share minted while
// Tailscale was down starts working the moment it comes back, without the
// owner having to notice and mint another.
func (s *Share) shareBase(ctx context.Context, reach ShareReach) string {
	// Tailnet-only was an explicit choice, so it has no fallbacks. Widening it
	// to the LAN because the tailnet is unreachable would hand out a link that
	// answers to a room the owner did not choose.
	if NormalizeReach(reach) == ReachTailnet {
		return s.tailnetBase(ctx)
	}
	// Published: reachable by anyone, which is the point of the feature.
	if s.deps.Funnel != nil {
		if base := s.deps.Funnel.BaseURL(ctx); base != "" {
			return base
		}
	}
	// Asked for public, but there is no tunnel — no Tailscale, or a tailnet
	// that does not permit Funnel. Fall back rather than go dark: a link that
	// reaches less far than asked is still a link between the owner's own
	// machines, and the dialog says which one this is.
	if base := s.tailnetBase(ctx); base != "" {
		return base
	}
	// The local network. Built from an interface address rather than from
	// ListenAddr, which is the *bind* — "0.0.0.0:8482" is a perfectly good
	// thing to listen on and a URL that resolves nowhere.
	if !s.deps.LoopbackOnly {
		if port := s.listenPort(); port != 0 {
			if addrs := discovery.ReachableAddresses(port); len(addrs) > 0 {
				return "http://" + addrs[0]
			}
		}
	}
	// Every address this daemon answers on is this machine. There is no link
	// to hand out, and the honest answer is none: a URL on 127.0.0.1 opens
	// perfectly in the owner's own browser, which is exactly what makes it
	// such a bad thing to put on the clipboard.
	return ""
}

// tailnetBase is the origin a share is served on to tailnet peers: the node's
// own name, and the guest listener bound to its tailnet addresses.
//
// The dedicated listener is what lets a tailnet-only share work without the
// owner turning remote access on — it carries the share routes and nothing
// else. Without one (Tailscale arrived after the daemon started, or the bind
// failed) this falls back to the daemon's own port, which only answers off
// this machine when remote access is on.
func (s *Share) tailnetBase(ctx context.Context) string {
	if s.deps.Funnel == nil {
		return ""
	}
	host := s.deps.Funnel.Hostname(ctx)
	if host == "" {
		return ""
	}
	if s.deps.GuestTailnetPort != 0 {
		return "http://" + net.JoinHostPort(host, strconv.Itoa(s.deps.GuestTailnetPort))
	}
	if !s.deps.LoopbackOnly {
		if port := s.listenPort(); port != 0 {
			return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
		}
	}
	return ""
}

// listenPort is the port the daemon bound, 0 when it has not bound yet.
func (s *Share) listenPort() int {
	_, portStr, err := net.SplitHostPort(s.deps.ListenAddr)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}
	return port
}

func (s *Share) toShareLink(ctx context.Context, g GuestLink, base string, now time.Time) ShareLink {
	out := ShareLink{
		ID: g.ID, Name: g.Name, Path: g.Path, FolderID: g.FolderID,
		Caps: g.Caps, ExpiresAt: g.ExpiresAt, CreatedAt: g.CreatedAt,
		LastSeen: g.LastSeen, Expired: g.Expired(now),
		Online: s.deps.GuestOnline(g.ID),
		Reach:  NormalizeReach(g.Reach),
	}
	if g.Export != nil {
		out.ExportName = g.Export.Name
	}
	if base != "" {
		out.URL = base + "/s/" + g.Token + "/"
	}
	if photos, err := s.deps.DB.ListPhotos(ctx, g.FolderID); err == nil {
		out.PhotoCount = len(photos)
	}
	return out
}
