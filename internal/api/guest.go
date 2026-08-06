package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/store"
)

// GuestLinksKey is the settings-table key holding the share-link list as JSON,
// for the same reasons as RemoteDevicesKey: a small list, always read and
// written whole, and no schema migration.
const GuestLinksKey = "guestLinks"

// A guest link shares one folder with someone outside the library — a client
// picking their favourites in a browser, typically over a Tailscale Funnel URL
// on the public internet. That is a different trust level from every other
// credential this daemon accepts: a paired device is another copy of marraw,
// trusted with the whole library, whereas a guest is a stranger holding a URL
// that may well end up forwarded to a group chat.
//
// So guests are confined twice over. GuestGate refuses every method that is
// not culling, before the handler runs; the handlers that survive the gate
// re-derive the link from the connection and check the folder themselves. The
// belt is the allowlist — anything added to the RPC surface later is denied by
// default — and the braces are the folder check, because an allowlisted method
// still takes a folder ID or photo IDs from the caller.

// GuestCaps is what the owner ticked when minting the link. Absent
// capabilities are absent features: without Cull the page is a viewer, and
// without Edits the guest is served the base rendition even after the owner
// has developed the shot.
type GuestCaps struct {
	Cull      bool `json:"cull"`
	Edits     bool `json:"edits"`
	Downloads bool `json:"downloads"`
}

// ShareReach is who a link is minted for. It decides which address the URL
// names, and since the URL is the credential it cannot be changed afterwards —
// a link that has been sent somewhere cannot be quietly narrowed.
//
// Public raises a Tailscale Funnel and hands out a name the whole internet
// resolves: the client, the band, a phone. Tailnet serves the same page to the
// tailnet only, which is the right answer for your own laptop and no use at
// all for someone who has never heard of Tailscale.
type ShareReach string

const (
	ReachPublic  ShareReach = "public"
	ReachTailnet ShareReach = "tailnet"
)

func ShareReachValues() []ShareReach { return []ShareReach{ReachPublic, ReachTailnet} }

// NormalizeReach resolves an absent or unrecognised value to public. Links
// minted before the choice existed carry no reach at all, and public is what
// they have always been — reading them as tailnet-only would silently break
// links that are out in the world working.
func NormalizeReach(r ShareReach) ShareReach {
	if r == ReachTailnet {
		return ReachTailnet
	}
	return ReachPublic
}

// PublicLinkCount is how many of these links are meant to be reachable from
// the internet, which is the only thing the funnel exists for. Withdrawing the
// last public share takes the tunnel down; the tailnet-only ones never raised
// it and must not hold it up.
func PublicLinkCount(links []GuestLink) int {
	n := 0
	for _, g := range links {
		if NormalizeReach(g.Reach) == ReachPublic {
			n++
		}
	}
	return n
}

// ShareExport is how a guest's downloads are rendered: the owner's chosen
// export preset, resolved to its settings when the link was minted.
//
// A snapshot, not a reference to the preset. A link can live a month, and a
// preset edited or deleted in the meantime must not silently change — or
// break — what someone else's copy of the shoot looks like. Name is kept only
// so the management list can say which preset it came from.
//
// The zero value is not used: a link with no preset carries a nil *ShareExport
// and downloads at the endpoint's own defaults (full resolution, quality 92).
type ShareExport struct {
	Name           string `json:"name"`
	LongEdge       int    `json:"longEdge"`
	JpegQuality    int    `json:"jpegQuality"`
	ColorSpace     string `json:"colorSpace"`
	SharpenTarget  string `json:"sharpenTarget"`
	SharpenAmount  string `json:"sharpenAmount"`
	ExifMode       string `json:"exifMode"`
	RemoveLocation bool   `json:"removeLocation"`
	WatermarkID    string `json:"watermarkId"`
}

// GuestLink is one shared folder. Path and Name are copied at mint time purely
// so the management list can name the share; the FolderID is what is enforced.
type GuestLink struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Token is the credential itself, and it is the whole of the credential:
	// it rides in the share URL. As with Device.Token it must never leave the
	// daemon except in the response that mints it.
	Token    string    `json:"token"`
	FolderID int64     `json:"folderId"`
	Path     string    `json:"path"`
	Caps     GuestCaps `json:"caps"`
	// ExpiresAt is unix millis; 0 never expires. A link is a URL in someone
	// else's message history forever, so the UI offers an expiry by default.
	ExpiresAt int64 `json:"expiresAt"`
	CreatedAt int64 `json:"createdAt"`
	LastSeen  int64 `json:"lastSeen"`
	// Export renders this link's downloads; nil means the defaults. Links
	// minted before shares could carry a preset unmarshal to nil, which is
	// exactly the behaviour they already had.
	Export *ShareExport `json:"export,omitempty"`
	// Reach is how far this link is served. Empty on links minted before the
	// choice existed; read it through NormalizeReach, never bare.
	Reach ShareReach `json:"reach,omitempty"`
}

// Expired reports whether the link has passed its expiry.
func (g GuestLink) Expired(now time.Time) bool {
	return g.ExpiresAt != 0 && now.UnixMilli() >= g.ExpiresAt
}

// lastSeenThrottle is how stale a link's last-opened stamp may be before a
// reconnect rewrites it. A guest on mobile data reconnects whenever the
// network blips, and every write rewrites the whole settings blob and pushes
// a refresh to every window — for a value the UI renders as "4 minutes ago".
const lastSeenThrottle = time.Minute

// MarkGuestOnline records that a connection is now authenticated as a share
// link, and stamps when the link was last opened.
//
// Called from the auth hook, which has no request context — so the refresh
// goes through the server directly. aprot.TriggerRefresh reads a queue off the
// request context and silently does nothing without one.
func (d *Deps) MarkGuestOnline(ctx context.Context, id string, conn uint64) {
	d.guestMu.Lock()
	if d.guestConns == nil {
		d.guestConns = map[string]map[uint64]struct{}{}
	}
	// aprot runs the auth hook again on a mid-session re-auth, and the token it
	// carries may name a different link. Take the connection out of wherever it
	// was first: otherwise the link it left keeps this connection on its books
	// and reads as "viewing now" until the daemon restarts.
	for other, conns := range d.guestConns {
		if other == id {
			continue
		}
		if _, ok := conns[conn]; ok {
			delete(conns, conn)
			if len(conns) == 0 {
				delete(d.guestConns, other)
			}
		}
	}
	if d.guestConns[id] == nil {
		d.guestConns[id] = map[uint64]struct{}{}
	}
	d.guestConns[id][conn] = struct{}{}
	d.stampLastSeenLocked(ctx, id)
	d.guestMu.Unlock()
	// Unconditional: presence changed even when the throttle skipped the write.
	d.TriggerRefresh(shareKey)
}

// MarkGuestOffline records that a connection holding a share link has gone.
// Called from the disconnect hook, which also has no request context.
func (d *Deps) MarkGuestOffline(id string, conn uint64) {
	d.guestMu.Lock()
	if conns := d.guestConns[id]; conns != nil {
		delete(conns, conn)
		if len(conns) == 0 {
			delete(d.guestConns, id)
		}
	}
	d.guestMu.Unlock()
	d.TriggerRefresh(shareKey)
}

// guestSweepInterval is how often lapsed links are swept off their live
// connections. Expiries are set in hours, so a minute of slack costs nothing
// and keeps the sweep off the hot path.
const guestSweepInterval = time.Minute

// RunGuestSweep enforces link expiry on connections that are already open,
// until ctx is done.
//
// Expiry is otherwise only checked at the door. aprot has no reason to close
// an authenticated socket, and a browser answers pings by itself, so a phone
// left on the shoot overnight keeps its connection long past the link's last
// hour. GuestGate refuses that connection's RPCs — but "the page stops
// working" is what expiry is supposed to mean, not "the page starts erroring".
func (d *Deps) RunGuestSweep(ctx context.Context) {
	t := time.NewTicker(guestSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			d.SweepGuests(now)
		}
	}
}

// SweepGuests drops every live connection whose link has lapsed. Revocation
// disconnects its own guest directly; this is for the links nobody withdrew,
// which simply ran out — and for the sliver between a revocation storing the
// new list and reaching its own DisconnectGuest call.
func (d *Deps) SweepGuests(now time.Time) {
	for _, id := range d.lapsedGuestIDs(now) {
		log.Printf("share: link %s is no longer valid; closing its open page", id)
		d.DisconnectGuest(id)
		// Forget the link here too, rather than waiting for the disconnect
		// hook. Normally the hook gets there first and this is a no-op, but a
		// connection that has already gone without one would otherwise stay on
		// the books and be swept — and logged — once a minute forever.
		d.guestMu.Lock()
		delete(d.guestConns, id)
		d.guestMu.Unlock()
	}
}

// lapsedGuestIDs is the set of links with a connection open that liveGuest
// would no longer resolve. Split out of SweepGuests so the selection can be
// tested without a live server, and so the disconnects happen outside guestMu:
// dropping a connection runs the OnDisconnect hook, which takes that same lock
// to clear the connection out of guestConns.
func (d *Deps) lapsedGuestIDs(now time.Time) []string {
	if d.Tokens == nil {
		return nil
	}
	live := map[string]bool{}
	for _, g := range d.Tokens.Guests() {
		if g.FolderID != 0 && !g.Expired(now) {
			live[g.ID] = true
		}
	}
	d.guestMu.Lock()
	defer d.guestMu.Unlock()
	var lapsed []string
	for id := range d.guestConns {
		if !live[id] {
			lapsed = append(lapsed, id)
		}
	}
	return lapsed
}

// GuestOnline reports whether anyone is holding this link open right now.
func (d *Deps) GuestOnline(id string) bool {
	d.guestMu.Lock()
	defer d.guestMu.Unlock()
	return len(d.guestConns[id]) > 0
}

// stampLastSeenLocked records that a link was just opened. Caller holds
// guestMu.
func (d *Deps) stampLastSeenLocked(ctx context.Context, id string) {
	if d.Tokens == nil || d.DB == nil {
		return
	}
	now := time.Now()
	links := d.Tokens.Guests()
	for i := range links {
		if links[i].ID != id {
			continue
		}
		if now.Sub(time.UnixMilli(links[i].LastSeen)) < lastSeenThrottle {
			return
		}
		links[i].LastSeen = now.UnixMilli()
		d.Tokens.SetGuests(links)
		if err := SaveGuestLinks(ctx, d.DB, links); err != nil {
			log.Printf("share: recording last opened: %v", err)
		}
		return
	}
}

// guestMethods is the entire RPC surface a guest connection may reach. Compare
// against the ~120 methods the registry exposes: everything else — deleting
// photos, browsing the filesystem, exporting, reading back the pairing token —
// is refused by GuestGate before the handler is entered.
//
// SetVisible and SetFocus are prefetch hints with no persistent effect; they
// are here because without them a guest scrolling a folder over a slow link
// waits on renders that could have been warmed.
var guestMethods = map[string]bool{
	"Share.Session":      true,
	"Library.ListPhotos": true,
	"Library.SetVisible": true,
	"Library.SetFocus":   true,
	"Library.SetRating":  true,
	"Library.SetFlag":    true,
}

// guestWriteMethods are the subset of guestMethods that change what the owner
// sees, and so additionally require the Cull capability.
var guestWriteMethods = map[string]bool{
	"Library.SetRating": true,
	"Library.SetFlag":   true,
}

// GuestGate refuses any RPC a guest connection is not entitled to. It is
// deliberately a method-name allowlist rather than a denylist: a denylist
// silently grants every method added after it was written, and this is the one
// place in marraw where the caller is not the user.
//
// Connections that are not guests pass straight through. Whether a connection
// IS a guest is decided by its identity alone — never by whether its link
// still resolves. Those are different questions, and answering the first with
// the second is what would let a link that has since expired read as the
// owner: the lookup filters expired links out, so a lapsed guest would resolve
// to nil and take the not-a-guest path into the whole registry.
func GuestGate(tokens *AuthTokens) aprot.Middleware {
	return func(next aprot.Handler) aprot.Handler {
		return func(ctx context.Context, req *aprot.Request) (any, error) {
			id, isGuest := guestConnID(ctx)
			if !isGuest {
				return next(ctx, req)
			}
			link := liveGuest(tokens, id)
			if link == nil {
				// Expired, revoked, or a daemon with no token store. A guest
				// whose link has gone is not demoted to a lesser guest — it is
				// refused outright, because there is nothing left to scope it
				// to. Its connection is dropped separately (see SweepGuests).
				return nil, aprot.ErrAuthFailed("this share link is no longer valid")
			}
			if err := guestAllows(link, req.Method); err != nil {
				return nil, err
			}
			return next(ctx, req)
		}
	}
}

// guestConnID reports the share-link ID a connection authenticated as, and
// whether it is a guest at all. Identity only: it does not consult the link
// list, so it keeps answering "yes, a guest" for a link that has since lapsed.
func guestConnID(ctx context.Context) (string, bool) {
	c := aprot.Connection(ctx)
	if c == nil {
		return "", false
	}
	return strings.CutPrefix(c.UserID(), ConnGuestPrefix)
}

// liveGuest returns the named link if it is still mintable into access: known,
// unexpired, and confined to a real folder. A link carrying folder 0 is
// refused rather than trusted — 0 is the unconfined sentinel throughout this
// file, and CreateLink never mints one, so a link that has it came from a
// corrupt or hand-edited settings blob.
func liveGuest(tokens *AuthTokens, id string) *GuestLink {
	if tokens == nil {
		return nil
	}
	now := time.Now()
	for _, g := range tokens.Guests() {
		if g.ID == id && g.FolderID != 0 && !g.Expired(now) {
			link := g
			return &link
		}
	}
	return nil
}

// guestAllows reports whether a guest holding link may call method. Split out
// of GuestGate so the allowlist can be tested without a live connection.
func guestAllows(link *GuestLink, method string) error {
	if !guestMethods[method] {
		return aprot.ErrForbidden("not available on a shared link")
	}
	if guestWriteMethods[method] && !link.Caps.Cull {
		return aprot.ErrForbidden("this link is view-only")
	}
	return nil
}

// GuestLink returns the share link the calling connection authenticated with,
// or nil when the caller is the owner. The link is looked up by ID on every
// call rather than cached on the connection so that revoking or re-scoping a
// share takes effect on the live connection's next RPC, not at its next
// reconnect.
//
// A guest whose link has lapsed also returns nil here, which is why callers
// that need to tell "the owner" from "a guest with nothing left" must use
// guestScope instead. Share.Session is the exception and wants exactly this:
// nil means "no session to describe", whichever of the two it is.
func (d *Deps) GuestLink(ctx context.Context) *GuestLink {
	id, isGuest := guestConnID(ctx)
	if !isGuest {
		return nil
	}
	return liveGuest(d.Tokens, id)
}

// guestScope is the folder a caller is confined to: 0 for the owner, the
// link's folder for a guest. A guest whose link has expired or been revoked
// mid-connection is an error rather than a 0, because 0 means unconfined and
// that connection has just lost every claim it had. GuestGate refuses those
// connections at the door; this is the second lock on the same door.
func (d *Deps) guestScope(ctx context.Context) (int64, error) {
	id, isGuest := guestConnID(ctx)
	if !isGuest {
		return 0, nil
	}
	link := liveGuest(d.Tokens, id)
	if link == nil {
		return 0, aprot.ErrForbidden("not shared")
	}
	return link.FolderID, nil
}

// CheckGuestFolder authorizes a guest's access to a folder by ID. The owner
// passes unconditionally.
func (d *Deps) CheckGuestFolder(ctx context.Context, folderID int64) error {
	scope, err := d.guestScope(ctx)
	if err != nil {
		return err
	}
	return checkFolderScope(scope, folderID)
}

// CheckGuestPhotos authorizes a guest's access to a set of photos. It costs a
// query, so it runs for guests only. Photo IDs are sequential integers a guest
// could simply count through, which is what makes this — not the folder ID in
// the request — the boundary between a shared folder and the rest of the
// library.
func (d *Deps) CheckGuestPhotos(ctx context.Context, ids []int64) error {
	scope, err := d.guestScope(ctx)
	if err != nil {
		return err
	}
	return d.checkPhotosScope(ctx, scope, ids)
}

// checkFolderScope authorizes access to one folder under a confinement, where
// scope 0 is unconfined.
func checkFolderScope(scope, folderID int64) error {
	if scope == 0 || scope == folderID {
		return nil
	}
	// Deliberately the same message an unknown folder would give: a share link
	// should not be an oracle for which folder IDs exist.
	return aprot.ErrForbidden("not shared")
}

// maxScopeIDs bounds how many photo IDs a confined caller may name in one
// request. Comfortably above selecting every frame of a long shoot, and far
// below what a 32 MiB request frame of repeated IDs would otherwise buy —
// which is the point, because this check runs BEFORE any per-method cap and so
// its cost is set by whatever the caller chose to send.
const maxScopeIDs = 10_000

// checkPhotosScope authorizes access to photo IDs under a confinement.
func (d *Deps) checkPhotosScope(ctx context.Context, scope int64, ids []int64) error {
	if scope == 0 || len(ids) == 0 {
		return nil
	}
	if len(ids) > maxScopeIDs {
		return aprot.ErrInvalidParams("too many photos in one request")
	}
	// One query for the whole set, not one per ID: a confined caller picks the
	// length of this list, so it must not cost a round trip each.
	folders, err := d.DB.PhotoFolders(ctx, ids)
	if err != nil {
		return aprot.ErrForbidden("not shared")
	}
	for _, id := range ids {
		// A vanished row is not in the folder either, and is refused rather
		// than skipped: dropping it would let an ID that resolves to nothing
		// ride along with legitimate ones.
		if f, ok := folders[id]; !ok || f != scope {
			return aprot.ErrForbidden("not shared")
		}
	}
	return nil
}

// LoadGuestLinks reads the share-link list from the settings table. As with
// LoadDevices, a corrupt blob reads as an empty list: failing startup over
// unparseable JSON would lock the user out of their own library, and the worst
// case here is that some links stop working.
func LoadGuestLinks(ctx context.Context, db *store.DB) []GuestLink {
	raw, err := db.GetSetting(ctx, GuestLinksKey)
	if err != nil || raw == "" {
		return nil
	}
	var links []GuestLink
	if err := json.Unmarshal([]byte(raw), &links); err != nil {
		return nil
	}
	return links
}

// SaveGuestLinks persists the share-link list.
func SaveGuestLinks(ctx context.Context, db *store.DB, links []GuestLink) error {
	raw, err := json.Marshal(links)
	if err != nil {
		return err
	}
	return db.SetSetting(ctx, GuestLinksKey, string(raw))
}

// NewGuestLink mints a share of one folder. The token is 128 bits from
// crypto/rand: it is a bearer credential on a public URL, so it has to survive
// being guessed at by anyone who knows the funnel hostname.
func NewGuestLink(folderID int64, path, name string, caps GuestCaps, expiresAt int64, export *ShareExport, reach ShareReach) (GuestLink, error) {
	id, err := GeneratePairingToken()
	if err != nil {
		return GuestLink{}, err
	}
	tok, err := GeneratePairingToken()
	if err != nil {
		return GuestLink{}, err
	}
	return GuestLink{
		ID:        id,
		Name:      name,
		Token:     tok,
		FolderID:  folderID,
		Path:      path,
		Caps:      caps,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now().UnixMilli(),
		Export:    export,
		Reach:     NormalizeReach(reach),
	}, nil
}

// matchGuest reports which share link tok is, if any. Like the device scan in
// Match it runs to completion rather than stopping at the first hit, so the
// time taken carries no information about where in the list a token sits.
func matchGuest(links []GuestLink, tok string) *GuestLink {
	var found *GuestLink
	for i := range links {
		if links[i].Token != "" &&
			subtle.ConstantTimeCompare([]byte(tok), []byte(links[i].Token)) == 1 {
			found = &links[i]
		}
	}
	return found
}
