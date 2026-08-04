package api

import (
	"context"
	"log"
	"net"
	"path/filepath"
	"time"

	"github.com/marrasen/aprot"

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
	out := &ShareStatus{}
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
	base := s.shareBase(ctx)
	now := time.Now()
	for _, g := range s.deps.Tokens.Guests() {
		out = append(out, s.toShareLink(ctx, g, base, now))
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
func (s *Share) CreateLink(ctx context.Context, path string, caps GuestCaps, expiresInHours int, exportPresetID string) (*ShareLink, error) {
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
	link, err := NewGuestLink(folderID, abs, filepath.Base(abs), caps, expiresAt, s.resolveExport(ctx, exportPresetID))
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
	if s.deps.Funnel != nil {
		if err := s.deps.Funnel.Enable(context.WithoutCancel(ctx)); err != nil {
			log.Printf("share: funnel unavailable: %v", err)
		}
	}
	aprot.TriggerRefresh(ctx, shareKey)
	out := s.toShareLink(ctx, link, s.shareBase(ctx), time.Now())
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
	remaining := len(next)
	s.deps.guestMu.Unlock()
	if err != nil {
		return err
	}
	s.deps.DisconnectGuest(id)
	// Nothing left to serve: take the tunnel down rather than leaving the
	// machine published to the internet for a share that no longer exists.
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

// shareBase is the origin share URLs are built on. Three cases, in descending
// order of reach — and the URL always names something that actually answers,
// because a link is discovered to be broken by the person you sent it to.
func (s *Share) shareBase(ctx context.Context) string {
	if s.deps.Funnel != nil {
		// Published: reachable by anyone, which is the point of the feature.
		if base := s.deps.Funnel.BaseURL(ctx); base != "" {
			return base
		}
		// Not published (no Tailscale, or a tailnet that does not permit
		// Funnel) but on a tailnet, and reachable from off this machine: a
		// peer can still open the link by the node's tailnet name. Good enough
		// to share with your own laptop, not with a client.
		if !s.deps.LoopbackOnly {
			if host := s.deps.Funnel.Hostname(ctx); host != "" {
				if _, port, err := net.SplitHostPort(s.deps.ListenAddr); err == nil {
					return "http://" + net.JoinHostPort(host, port)
				}
			}
		}
	}
	if s.deps.ListenAddr == "" {
		return ""
	}
	return "http://" + s.deps.ListenAddr
}

func (s *Share) toShareLink(ctx context.Context, g GuestLink, base string, now time.Time) ShareLink {
	out := ShareLink{
		ID: g.ID, Name: g.Name, Path: g.Path, FolderID: g.FolderID,
		Caps: g.Caps, ExpiresAt: g.ExpiresAt, CreatedAt: g.CreatedAt,
		LastSeen: g.LastSeen, Expired: g.Expired(now),
		Online: s.deps.GuestOnline(g.ID),
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
