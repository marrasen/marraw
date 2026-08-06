package api

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/marrasen/aprot"

	"github.com/marrasen/marraw/internal/discovery"
	"github.com/marrasen/marraw/internal/store"
)

// The allowlist is the whole of a guest's reach into the registry, so what
// matters is not that culling works but that everything else does not. These
// are the methods a hostile visitor would reach for first: deleting the
// originals, walking the filesystem, writing files, and reading back the
// pairing token — which would upgrade a share link into permanent full access.
func TestGuestGateDeniesEverythingOutsideCulling(t *testing.T) {
	full := &GuestLink{Caps: GuestCaps{Cull: true, Edits: true, Downloads: true}}
	denied := []string{
		"Library.DeletePhotos",
		"Library.ListDir",
		"Library.ListDrives",
		"Library.SetLibraryRoots",
		"Library.RenameFolderOnDisk",
		"Library.OpenFolder",
		"Edits.SetEditParams",
		"Edits.ResetEdits",
		"Edits.ApplyBatchEdit",
		"Export.StartExport",
		"Export.RenderClipboard",
		"Settings.AddWatermarkAsset",
		// The share surface itself: a guest holding one link must not be able
		// to mint another, enumerate the rest, or revoke the owner's.
		"Share.CreateLink",
		"Share.ListLinks",
		"Share.RevokeLink",
		"Share.Status",
		"System.GetRemoteAccess",
		"System.RegeneratePairingToken",
		"System.SetCacheDir",
		"System.ClearCache",
		"System.ResolvePairing",
		"System.RevokeRemoteDevice",
	}
	for _, method := range denied {
		if err := guestAllows(full, method); err == nil {
			t.Errorf("guestAllows(%s) = nil, want refusal even for a fully-capable link", method)
		}
	}
}

func TestGuestGateAllowsCulling(t *testing.T) {
	full := &GuestLink{Caps: GuestCaps{Cull: true}}
	for _, method := range []string{
		"Share.Session",
		"Library.ListPhotos",
		"Library.SetVisible",
		"Library.SetFocus",
		"Library.SetRating",
		"Library.SetFlag",
	} {
		if err := guestAllows(full, method); err != nil {
			t.Errorf("guestAllows(%s) = %v, want nil", method, err)
		}
	}
}

// Without the cull capability the link is a viewer: it may read the folder but
// not write the owner's ratings.
func TestGuestGateViewOnlyLinkCannotWrite(t *testing.T) {
	viewer := &GuestLink{Caps: GuestCaps{Edits: true}}
	for _, method := range []string{"Library.SetRating", "Library.SetFlag"} {
		if err := guestAllows(viewer, method); err == nil {
			t.Errorf("guestAllows(%s) = nil on a view-only link, want refusal", method)
		}
	}
	if err := guestAllows(viewer, "Library.ListPhotos"); err != nil {
		t.Errorf("guestAllows(ListPhotos) = %v on a view-only link, want nil", err)
	}
}

// An unknown method — anything added to the registry after this gate was
// written — must be refused rather than allowed by omission.
func TestGuestGateDeniesUnknownMethod(t *testing.T) {
	if err := guestAllows(&GuestLink{Caps: GuestCaps{Cull: true}}, "Library.SomeMethodAddedLater"); err == nil {
		t.Error("guestAllows(unknown method) = nil, want refusal: the allowlist must fail closed")
	}
}

// The gate decides "is this a guest?" from the connection's identity, never
// from whether its link still resolves. Conflating the two used to mean an
// expired link — a page left open past its last hour — took the not-a-guest
// path and reached the entire registry with the owner's rights.
func TestGuestGateRefusesAConnectionWhoseLinkHasLapsed(t *testing.T) {
	tokens := NewAuthTokens("launch", "pairing")
	live, err := NewGuestLink(7, "/photos/band", "band", GuestCaps{Cull: true}, 0, nil, ReachPublic)
	if err != nil {
		t.Fatalf("NewGuestLink: %v", err)
	}
	lapsed := live
	lapsed.ID, lapsed.ExpiresAt = "lapsed", time.Now().Add(-time.Hour).UnixMilli()
	// A link whose folder is 0 never comes from CreateLink, but a corrupt or
	// hand-edited settings blob unmarshals straight into the sentinel that
	// means "unconfined" everywhere else in this file.
	unconfined := live
	unconfined.ID, unconfined.FolderID = "unconfined", 0
	tokens.SetGuests([]GuestLink{live, lapsed, unconfined})

	for _, tc := range []struct {
		name, connID string
		wantDenied   bool
	}{
		{"live link", live.ID, false},
		{"expired link", lapsed.ID, true},
		{"folder-0 link", unconfined.ID, true},
		{"link revoked mid-connection", "never-existed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			gate := GuestGate(tokens)(func(context.Context, *aprot.Request) (any, error) {
				reached = true
				return nil, nil
			})
			ctx := aprot.WithTestConnectionUser(context.Background(), 1, ConnGuestPrefix+tc.connID)
			// A method NO guest may ever call, so reaching the handler at all
			// means the gate stopped treating this connection as a guest.
			_, err := gate(ctx, &aprot.Request{Method: "Library.DeletePhotos"})
			if reached {
				t.Error("the connection reached Library.DeletePhotos")
			}
			if err == nil {
				t.Error("the gate returned no error")
			}
			// The allowlisted methods are the real test of live vs lapsed.
			reached = false
			_, err = gate(ctx, &aprot.Request{Method: "Library.SetRating"})
			if denied := err != nil; denied != tc.wantDenied {
				t.Errorf("SetRating denied = %v (err %v), want %v", denied, err, tc.wantDenied)
			}
			if reached == tc.wantDenied {
				t.Errorf("SetRating reached handler = %v, want %v", reached, !tc.wantDenied)
			}
		})
	}
}

// The second lock on the same door: even if a lapsed connection reached a
// handler, its confinement must not read as the owner's.
func TestGuestScopeFailsClosedForALapsedLink(t *testing.T) {
	tokens := NewAuthTokens("launch", "pairing")
	tokens.SetGuests([]GuestLink{{
		ID: "lapsed", Token: "tok", FolderID: 7,
		ExpiresAt: time.Now().Add(-time.Hour).UnixMilli(),
	}})
	d := &Deps{Tokens: tokens}

	ctx := aprot.WithTestConnectionUser(context.Background(), 1, ConnGuestPrefix+"lapsed")
	if _, err := d.guestScope(ctx); err == nil {
		t.Error("guestScope(lapsed guest) = nil error, want refusal: 0 means unconfined")
	}
	if err := d.CheckGuestFolder(ctx, 7); err == nil {
		t.Error("CheckGuestFolder(lapsed guest, its own folder) = nil, want refusal")
	}
	if err := d.CheckGuestPhotos(ctx, []int64{1}); err == nil {
		t.Error("CheckGuestPhotos(lapsed guest) = nil, want refusal")
	}

	// The owner, who has no guest prefix, still passes unconfined.
	owner := aprot.WithTestConnectionUser(context.Background(), 2, ConnLocal)
	if scope, err := d.guestScope(owner); err != nil || scope != 0 {
		t.Errorf("guestScope(owner) = (%d, %v), want (0, nil)", scope, err)
	}
}

// A confined caller chooses the length of the ID list the scope check runs on,
// and that check happens before any per-method cap.
func TestCheckPhotosScopeRefusesAnOversizedIDList(t *testing.T) {
	d := &Deps{}
	ids := make([]int64, maxScopeIDs+1)
	if err := d.checkPhotosScope(context.Background(), 7, ids); err == nil {
		t.Error("checkPhotosScope(too many ids) = nil, want refusal before any query runs")
	}
	// The owner is unconfined and never pays for the check at all.
	if err := d.checkPhotosScope(context.Background(), 0, ids); err != nil {
		t.Errorf("checkPhotosScope(owner, many ids) = %v, want nil", err)
	}
}

// A link that runs out while someone is still looking at it has to close their
// page, not merely start refusing its RPCs.
func TestSweepGuestsDropsLapsedConnections(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	live, err := NewGuestLink(1, "/photos/band", "band", GuestCaps{Cull: true}, 0, nil, ReachPublic)
	if err != nil {
		t.Fatalf("NewGuestLink: %v", err)
	}
	lapsed, err := NewGuestLink(2, "/photos/wedding", "wedding", GuestCaps{Cull: true}, 0, nil, ReachPublic)
	if err != nil {
		t.Fatalf("NewGuestLink: %v", err)
	}
	lapsed.ExpiresAt = time.Now().Add(-time.Hour).UnixMilli()
	tokens := NewAuthTokens("launch", "pairing")
	tokens.SetGuests([]GuestLink{live, lapsed})
	d := &Deps{DB: db, Tokens: tokens}

	d.MarkGuestOnline(ctx, live.ID, 1)
	d.MarkGuestOnline(ctx, lapsed.ID, 2)

	// No server wired in, so DisconnectGuest cannot actually close the socket;
	// what this asserts is which links the sweep SELECTS.
	if got := d.lapsedGuestIDs(time.Now()); len(got) != 1 || got[0] != lapsed.ID {
		t.Errorf("lapsedGuestIDs = %v, want just the expired link %s", got, lapsed.ID)
	}
	// Withdrawing the other link makes its open connection lapsed too, which is
	// what closes the window between SetGuests and DisconnectGuest in Revoke.
	tokens.SetGuests(nil)
	got := d.lapsedGuestIDs(time.Now())
	if len(got) != 2 {
		t.Errorf("lapsedGuestIDs after revoking everything = %v, want both connections", got)
	}
}

func TestAuthTokensMatchGuest(t *testing.T) {
	future := time.Now().Add(time.Hour).UnixMilli()
	past := time.Now().Add(-time.Hour).UnixMilli()
	tokens := NewAuthTokens("launch-token", "pairing-token")
	tokens.SetGuests([]GuestLink{
		{ID: "live", Token: "tok-live", FolderID: 7, ExpiresAt: future},
		{ID: "lapsed", Token: "tok-lapsed", FolderID: 8, ExpiresAt: past},
		{ID: "forever", Token: "tok-forever", FolderID: 9},
	})

	m := tokens.Match("tok-live")
	if !m.OK || m.Guest == nil || m.Guest.ID != "live" || m.Guest.FolderID != 7 {
		t.Fatalf("Match(live) = %+v, want the live link", m)
	}
	if m.Launch || m.Pairing || m.DeviceID != "" {
		t.Errorf("Match(live) = %+v, want a guest and nothing else", m)
	}
	if m := tokens.Match("tok-forever"); !m.OK || m.Guest == nil || m.Guest.ID != "forever" {
		t.Errorf("Match(forever) = %+v, want the never-expiring link", m)
	}
	// An expired link is refused at the door, so no handler has to remember to
	// check the expiry itself.
	if m := tokens.Match("tok-lapsed"); m.OK || m.Guest != nil {
		t.Errorf("Match(lapsed) = %+v, want no match", m)
	}
	if m := tokens.Match("tok-nonexistent"); m.OK {
		t.Errorf("Match(unknown) = %+v, want no match", m)
	}
	if m := tokens.Match(""); m.OK {
		t.Errorf("Match(empty) = %+v, want no match", m)
	}

	// Revocation is a list swap; it must take the token with it.
	tokens.SetGuests(nil)
	if m := tokens.Match("tok-live"); m.OK {
		t.Errorf("Match(live) after revocation = %+v, want no match", m)
	}

	// The owner's own credentials keep working and are never mistaken for
	// guests.
	if m := tokens.Match("launch-token"); !m.Launch || m.Guest != nil {
		t.Errorf("Match(launch) = %+v, want a launch match", m)
	}
	if m := tokens.Match("pairing-token"); !m.Pairing || m.Guest != nil {
		t.Errorf("Match(pairing) = %+v, want a pairing match", m)
	}
}

// Photo IDs are sequential integers, so a guest can simply count through them.
// This is the check that stops one shared folder from reaching the others.
func TestCheckPhotosScopeConfinesGuestToItsFolder(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	ids := func(path string, names ...string) []int64 {
		folderID, err := db.UpsertFolder(ctx, path)
		if err != nil {
			t.Fatalf("UpsertFolder: %v", err)
		}
		files := make([]store.FileEntry, len(names))
		for i, n := range names {
			files[i] = store.FileEntry{Name: n, Size: 1, MtimeNs: 1}
		}
		if _, err := db.SyncFolder(ctx, folderID, path, files); err != nil {
			t.Fatalf("SyncFolder: %v", err)
		}
		photos, err := db.ListPhotos(ctx, folderID)
		if err != nil {
			t.Fatalf("ListPhotos: %v", err)
		}
		out := make([]int64, len(photos))
		for i, p := range photos {
			out[i] = p.ID
		}
		return out
	}

	shared := ids("/photos/band-shoot", "a.arw", "b.arw")
	private := ids("/photos/wedding", "secret.arw")
	scope, err := db.UpsertFolder(ctx, "/photos/band-shoot")
	if err != nil {
		t.Fatalf("UpsertFolder: %v", err)
	}
	deps := &Deps{DB: db}

	if err := deps.checkPhotosScope(ctx, scope, shared); err != nil {
		t.Errorf("checkPhotosScope(shared photos) = %v, want nil", err)
	}
	if err := deps.checkPhotosScope(ctx, scope, private); err == nil {
		t.Error("checkPhotosScope(another folder's photos) = nil, want refusal")
	}
	// The interesting case: a real id from the shared folder alongside one
	// from outside it. Checking only the first id would let this through.
	mixed := []int64{shared[0], private[0]}
	if err := deps.checkPhotosScope(ctx, scope, mixed); err == nil {
		t.Error("checkPhotosScope(shared + private) = nil, want refusal")
	}
	// An id that resolves to no row must not pass by shrinking the result set.
	if err := deps.checkPhotosScope(ctx, scope, []int64{shared[0], 999999}); err == nil {
		t.Error("checkPhotosScope(shared + missing) = nil, want refusal")
	}
	// The owner is unconfined.
	if err := deps.checkPhotosScope(ctx, 0, append(append([]int64{}, shared...), private...)); err != nil {
		t.Errorf("checkPhotosScope(owner) = %v, want nil", err)
	}
}

func TestCheckFolderScope(t *testing.T) {
	if err := checkFolderScope(7, 7); err != nil {
		t.Errorf("checkFolderScope(7, 7) = %v, want nil", err)
	}
	if err := checkFolderScope(7, 8); err == nil {
		t.Error("checkFolderScope(7, 8) = nil, want refusal")
	}
	if err := checkFolderScope(0, 8); err != nil {
		t.Errorf("checkFolderScope(owner) = %v, want nil", err)
	}
}

// A share's downloads are rendered from a snapshot of the owner's export
// preset, taken when the link is minted — so editing or deleting the preset
// afterwards cannot change what a link already handed out produces.
func TestResolveExportSnapshotsThePreset(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	presets := []ExportPreset{{
		ID:   "web",
		Name: "Web 2560",
		Options: ExportOptions{
			Format: ExportJPEG, JpegQuality: 88, ResizeMode: "edge", EdgePx: 2560,
			ColorSpace: ColorSpaceSRGB, ExifMode: ExifModeCopyright, WatermarkID: "wm1",
		},
	}, {
		ID:      "full",
		Name:    "Full size",
		Options: ExportOptions{Format: ExportJPEG, JpegQuality: 95, ResizeMode: "full", EdgePx: 2160},
	}}
	raw, err := json.Marshal(presets)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := db.SetSetting(ctx, settingUIExportPresets, string(raw)); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	s := &Share{deps: &Deps{DB: db}}

	got := s.resolveExport(ctx, "web")
	if got == nil {
		t.Fatal("resolveExport(web) = nil, want the preset's settings")
	}
	if got.Name != "Web 2560" || got.LongEdge != 2560 || got.JpegQuality != 88 || got.WatermarkID != "wm1" {
		t.Errorf("resolveExport(web) = %+v", got)
	}

	// "full" keeps EdgePx around for when the user switches back, so it must
	// not leak out as a resize.
	if full := s.resolveExport(ctx, "full"); full == nil || full.LongEdge != 0 {
		t.Errorf("resolveExport(full) = %+v, want no long edge", full)
	}

	// No preset, and a preset deleted since the link was minted, both fall back
	// to the endpoint's defaults rather than refusing to mint.
	if none := s.resolveExport(ctx, ""); none != nil {
		t.Errorf("resolveExport(\"\") = %+v, want nil", none)
	}
	if gone := s.resolveExport(ctx, "deleted-preset"); gone != nil {
		t.Errorf("resolveExport(unknown) = %+v, want nil", gone)
	}
}

// Revoking every link on a shoot fires one call per link at once — the rail's
// "Revoke shared access" does exactly that. Each one rewrites the whole list,
// so without serialization the later write restores what the earlier removed
// and a link the owner believes is withdrawn keeps working.
func TestConcurrentRevokeRemovesEveryLink(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	links := make([]GuestLink, 4)
	for i := range links {
		l, err := NewGuestLink(1, "/photos/band", "band", GuestCaps{Cull: true}, 0, nil, ReachPublic)
		if err != nil {
			t.Fatalf("NewGuestLink: %v", err)
		}
		links[i] = l
	}
	tokens := NewAuthTokens("launch", "pairing")
	tokens.SetGuests(links)
	if err := SaveGuestLinks(ctx, db, links); err != nil {
		t.Fatalf("SaveGuestLinks: %v", err)
	}
	// LoopbackOnly with no connection in context is how a local window reads.
	s := &Share{deps: &Deps{DB: db, Tokens: tokens, LoopbackOnly: true}}

	var wg sync.WaitGroup
	for _, l := range links {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := s.RevokeLink(ctx, id); err != nil {
				t.Errorf("RevokeLink(%s): %v", id, err)
			}
		}(l.ID)
	}
	wg.Wait()

	if got := tokens.Guests(); len(got) != 0 {
		t.Errorf("%d links survived concurrent revocation, want 0", len(got))
	}
	if got := LoadGuestLinks(ctx, db); len(got) != 0 {
		t.Errorf("%d links survived in the store, want 0", len(got))
	}
}

// Presence is a set of connection IDs, not a count, because aprot's auth hook
// also runs on a mid-session re-auth. A counter would climb on every re-auth
// with only one disconnect to balance it, leaving a link stuck reading
// "viewing now" until the daemon restarted.
func TestGuestPresenceTracksConnections(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	link, err := NewGuestLink(1, "/photos/band", "band", GuestCaps{Cull: true}, 0, nil, ReachPublic)
	if err != nil {
		t.Fatalf("NewGuestLink: %v", err)
	}
	tokens := NewAuthTokens("launch", "pairing")
	tokens.SetGuests([]GuestLink{link})
	if err := SaveGuestLinks(ctx, db, []GuestLink{link}); err != nil {
		t.Fatalf("SaveGuestLinks: %v", err)
	}
	d := &Deps{DB: db, Tokens: tokens}

	if d.GuestOnline(link.ID) {
		t.Error("a link nobody has opened reads as online")
	}

	// Two devices on one link — the band's phone and the drummer's laptop.
	d.MarkGuestOnline(ctx, link.ID, 1)
	d.MarkGuestOnline(ctx, link.ID, 2)
	if !d.GuestOnline(link.ID) {
		t.Fatal("link is not online with two connections open")
	}
	// A re-auth on a connection already counted must not add a second tally.
	d.MarkGuestOnline(ctx, link.ID, 1)

	d.MarkGuestOffline(link.ID, 1)
	if !d.GuestOnline(link.ID) {
		t.Error("link went offline while a second connection was still open")
	}
	d.MarkGuestOffline(link.ID, 2)
	if d.GuestOnline(link.ID) {
		t.Error("link still reads as online after every connection closed")
	}
	// A disconnect for a connection that was never counted is harmless.
	d.MarkGuestOffline(link.ID, 99)
	if d.GuestOnline(link.ID) {
		t.Error("an unknown disconnect brought the link back online")
	}
}

// The auth hook runs again on a mid-session re-auth, and the token it carries
// may name a different link. The connection has to leave the first link's books
// on the way, or that link reads as "viewing now" until the daemon restarts.
func TestGuestPresenceFollowsAConnectionThatReauthenticates(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	first, err := NewGuestLink(1, "/photos/band", "band", GuestCaps{Cull: true}, 0, nil, ReachPublic)
	if err != nil {
		t.Fatalf("NewGuestLink: %v", err)
	}
	second, err := NewGuestLink(2, "/photos/wedding", "wedding", GuestCaps{Cull: true}, 0, nil, ReachPublic)
	if err != nil {
		t.Fatalf("NewGuestLink: %v", err)
	}
	tokens := NewAuthTokens("launch", "pairing")
	tokens.SetGuests([]GuestLink{first, second})
	if err := SaveGuestLinks(ctx, db, []GuestLink{first, second}); err != nil {
		t.Fatalf("SaveGuestLinks: %v", err)
	}
	d := &Deps{DB: db, Tokens: tokens}

	d.MarkGuestOnline(ctx, first.ID, 1)
	d.MarkGuestOnline(ctx, second.ID, 1) // same connection, re-authenticated

	if d.GuestOnline(first.ID) {
		t.Error("the link the connection left still reads as being viewed")
	}
	if !d.GuestOnline(second.ID) {
		t.Error("the link the connection moved to does not read as being viewed")
	}
	d.MarkGuestOffline(second.ID, 1)
	if d.GuestOnline(second.ID) {
		t.Error("the link stayed online after its only connection went")
	}
}

// Opening a link records when, so the rail can say "last opened 5 min ago"
// once the visitor has gone.
func TestGuestConnectStampsLastSeen(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	link, err := NewGuestLink(1, "/photos/band", "band", GuestCaps{Cull: true}, 0, nil, ReachPublic)
	if err != nil {
		t.Fatalf("NewGuestLink: %v", err)
	}
	if link.LastSeen != 0 {
		t.Fatalf("a fresh link starts with LastSeen %d, want 0", link.LastSeen)
	}
	tokens := NewAuthTokens("launch", "pairing")
	tokens.SetGuests([]GuestLink{link})
	if err := SaveGuestLinks(ctx, db, []GuestLink{link}); err != nil {
		t.Fatalf("SaveGuestLinks: %v", err)
	}
	d := &Deps{DB: db, Tokens: tokens}

	before := time.Now().UnixMilli()
	d.MarkGuestOnline(ctx, link.ID, 1)
	got := tokens.Guests()[0].LastSeen
	if got < before {
		t.Errorf("LastSeen = %d, want >= %d", got, before)
	}
	// Persisted, not just in memory: the rail must still say when it was last
	// opened after a restart.
	if stored := LoadGuestLinks(ctx, db); len(stored) != 1 || stored[0].LastSeen != got {
		t.Errorf("stored LastSeen = %v, want %d", stored, got)
	}

	// A phone on flaky mobile data reconnects constantly; each rewrite is a
	// whole settings blob and a refresh to every window, for a value rendered
	// as "just now" either way.
	d.MarkGuestOnline(ctx, link.ID, 2)
	if again := tokens.Guests()[0].LastSeen; again != got {
		t.Errorf("a reconnect within the throttle rewrote LastSeen (%d → %d)", got, again)
	}
}

// SetFocus is allowlisted so the shared page can say where the visitor is
// looking, but the value it writes is process-global and steers the owner's
// pre-render pass over the owner's own folder. The guest's id is foreign
// there, so honouring it drags that pass back to position 0.
func TestSetFocusIgnoresGuestsButHonoursTheOwner(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	folderID, err := db.UpsertFolder(ctx, "/photos/band-shoot")
	if err != nil {
		t.Fatalf("UpsertFolder: %v", err)
	}
	if _, err := db.SyncFolder(ctx, folderID, "/photos/band-shoot", []store.FileEntry{
		{Name: "a.arw", Size: 1, MtimeNs: 1},
		{Name: "b.arw", Size: 1, MtimeNs: 1},
	}); err != nil {
		t.Fatalf("SyncFolder: %v", err)
	}
	photos, err := db.ListPhotos(ctx, folderID)
	if err != nil {
		t.Fatalf("ListPhotos: %v", err)
	}

	link, err := NewGuestLink(folderID, "/photos/band-shoot", "band-shoot", GuestCaps{Cull: true}, 0, nil, ReachPublic)
	if err != nil {
		t.Fatalf("NewGuestLink: %v", err)
	}
	tokens := NewAuthTokens("launch", "pairing")
	tokens.SetGuests([]GuestLink{link})
	l := &Library{deps: &Deps{DB: db, Tokens: tokens}}

	owner := aprot.WithTestConnectionUser(ctx, 1, ConnLocal)
	if err := l.SetFocus(owner, folderID, photos[0].ID); err != nil {
		t.Fatalf("SetFocus(owner) = %v", err)
	}
	if got := l.deps.focusPhotoID.Load(); got != photos[0].ID {
		t.Fatalf("focus after the owner set it = %d, want %d", got, photos[0].ID)
	}

	// The guest's own photo, in the folder it was shared: accepted, and
	// ignored. Not an error — the page is doing nothing wrong.
	guest := aprot.WithTestConnectionUser(ctx, 2, ConnGuestPrefix+link.ID)
	if err := l.SetFocus(guest, folderID, photos[1].ID); err != nil {
		t.Errorf("SetFocus(guest, own photo) = %v, want nil", err)
	}
	if got := l.deps.focusPhotoID.Load(); got != photos[0].ID {
		t.Errorf("a guest moved the owner's render focus to %d", got)
	}

	// A folder the link was not minted for is still refused outright.
	if err := l.SetFocus(guest, folderID+1, photos[1].ID); err == nil {
		t.Error("SetFocus(guest, another folder) = nil, want refusal")
	}
}

func TestGuestLinkExpired(t *testing.T) {
	now := time.Now()
	if (GuestLink{}).Expired(now) {
		t.Error("a link with no expiry must never expire")
	}
	if !(GuestLink{ExpiresAt: now.Add(-time.Second).UnixMilli()}).Expired(now) {
		t.Error("a past expiry must read as expired")
	}
	if (GuestLink{ExpiresAt: now.Add(time.Hour).UnixMilli()}).Expired(now) {
		t.Error("a future expiry must not read as expired")
	}
}

// Links minted before the reach choice existed carry no value at all, and
// every one of them was funnelled. Reading those as tailnet-only would take
// the tunnel down under a link that is out in the world working.
func TestLinksWithoutReachReadAsPublic(t *testing.T) {
	if got := NormalizeReach(""); got != ReachPublic {
		t.Errorf(`NormalizeReach("") = %q, want %q`, got, ReachPublic)
	}
	if got := NormalizeReach("nonsense"); got != ReachPublic {
		t.Errorf(`NormalizeReach("nonsense") = %q, want %q`, got, ReachPublic)
	}
	links := []GuestLink{
		{ID: "minted-before-the-choice"},
		{ID: "my-devices", Reach: ReachTailnet},
		{ID: "anyone", Reach: ReachPublic},
	}
	// Two, not three: the tailnet-only share never raised the funnel and must
	// not be what keeps it up.
	if got := PublicLinkCount(links); got != 2 {
		t.Errorf("PublicLinkCount = %d, want 2", got)
	}
}

// A tailnet-only share has no fallbacks. The LAN tier a public link drops to
// would hand out a URL answering to a room the owner did not choose — and the
// owner would have no way to tell, because the link they copied still works
// from their own desk.
func TestTailnetShareDoesNotFallBackToTheLAN(t *testing.T) {
	ctx := context.Background()
	// A daemon bound wide, with no Tailscale at all.
	s := &Share{deps: &Deps{ListenAddr: "0.0.0.0:8482"}}
	if base := s.shareBase(ctx, ReachTailnet); base != "" {
		t.Errorf("tailnet share fell back to %q, want no base at all", base)
	}
	// The same daemon minting a public link may use the LAN, which is the
	// whole point of the fallback. Only assertable where this machine has a
	// routable address — on a loopback-only box there is nothing to fall back
	// to either, which the next test covers.
	if addrs := discovery.ReachableAddresses(8482); len(addrs) > 0 {
		if got, want := s.shareBase(ctx, ReachPublic), "http://"+addrs[0]; got != want {
			t.Errorf("public share base = %q, want %q", got, want)
		}
	}
}

// The URL is never built from the bind address. "127.0.0.1:8483" opens
// perfectly in the owner's own browser, and "0.0.0.0:8482" opens nowhere —
// both would be discovered by the person the link was sent to.
func TestLoopbackOnlyDaemonHasNoShareBase(t *testing.T) {
	s := &Share{deps: &Deps{ListenAddr: "127.0.0.1:8483", LoopbackOnly: true}}
	for _, reach := range ShareReachValues() {
		if base := s.shareBase(context.Background(), reach); base != "" {
			t.Errorf("%s share base = %q, want none", reach, base)
		}
	}
}
