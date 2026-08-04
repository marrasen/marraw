package api

import (
	"context"
	"testing"
	"time"

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
