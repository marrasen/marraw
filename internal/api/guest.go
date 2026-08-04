package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
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
}

// Expired reports whether the link has passed its expiry.
func (g GuestLink) Expired(now time.Time) bool {
	return g.ExpiresAt != 0 && now.UnixMilli() >= g.ExpiresAt
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
// Connections that are not guests pass straight through.
func GuestGate(tokens *AuthTokens) aprot.Middleware {
	return func(next aprot.Handler) aprot.Handler {
		return func(ctx context.Context, req *aprot.Request) (any, error) {
			link := guestOf(ctx, tokens)
			if link == nil {
				return next(ctx, req)
			}
			if err := guestAllows(link, req.Method); err != nil {
				return nil, err
			}
			return next(ctx, req)
		}
	}
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

// guestOf resolves the guest link behind a connection, or nil for any other
// caller. The link is looked up by ID on every call rather than cached on the
// connection so that revoking or re-scoping a share takes effect on the live
// connection's next RPC, not at its next reconnect.
func guestOf(ctx context.Context, tokens *AuthTokens) *GuestLink {
	c := aprot.Connection(ctx)
	if c == nil || tokens == nil {
		return nil
	}
	id, ok := strings.CutPrefix(c.UserID(), ConnGuestPrefix)
	if !ok {
		return nil
	}
	now := time.Now()
	for _, g := range tokens.Guests() {
		if g.ID == id && !g.Expired(now) {
			link := g
			return &link
		}
	}
	return nil
}

// GuestLink returns the share link the calling connection authenticated with,
// or nil when the caller is the owner. Handlers use it to scope themselves;
// see Library.ListPhotos.
func (d *Deps) GuestLink(ctx context.Context) *GuestLink {
	return guestOf(ctx, d.Tokens)
}

// guestFolder is the folder a caller is confined to: 0 for the owner, the
// link's folder for a guest. A revoked or expired guest whose connection is
// still open resolves to nil in GuestLink and would read as unconfined here,
// which is why the gate — which rejects that same connection outright — runs
// first.
func (d *Deps) guestFolder(ctx context.Context) int64 {
	if g := d.GuestLink(ctx); g != nil {
		return g.FolderID
	}
	return 0
}

// CheckGuestFolder authorizes a guest's access to a folder by ID. The owner
// passes unconditionally.
func (d *Deps) CheckGuestFolder(ctx context.Context, folderID int64) error {
	return checkFolderScope(d.guestFolder(ctx), folderID)
}

// CheckGuestPhotos authorizes a guest's access to a set of photos. It costs a
// query, so it runs for guests only. Photo IDs are sequential integers a guest
// could simply count through, which is what makes this — not the folder ID in
// the request — the boundary between a shared folder and the rest of the
// library.
func (d *Deps) CheckGuestPhotos(ctx context.Context, ids []int64) error {
	return d.checkPhotosScope(ctx, d.guestFolder(ctx), ids)
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

// checkPhotosScope authorizes access to photo IDs under a confinement.
func (d *Deps) checkPhotosScope(ctx context.Context, scope int64, ids []int64) error {
	if scope == 0 || len(ids) == 0 {
		return nil
	}
	photos, err := d.DB.GetPhotos(ctx, ids)
	if err != nil {
		return aprot.ErrForbidden("not shared")
	}
	// A vanished row is not in the folder either: comparing counts stops an id
	// that resolves to nothing from riding along with legitimate ones.
	if len(photos) != len(ids) {
		return aprot.ErrForbidden("not shared")
	}
	for _, p := range photos {
		if p.FolderID != scope {
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
func NewGuestLink(folderID int64, path, name string, caps GuestCaps, expiresAt int64) (GuestLink, error) {
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
