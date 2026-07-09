package store

import (
	"context"
	"testing"
)

// ListPhotos orders by capture time, not file name: a second body or a counter
// rollover puts file names badly out of step with capture order, and time-gap
// grouping computed from a mis-ordered list is nonsense.
func TestListPhotosOrdersByCaptureTime(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	const folderPath = `/photos`
	folderID, err := db.UpsertFolder(ctx, folderPath)
	if err != nil {
		t.Fatalf("UpsertFolder: %v", err)
	}
	// Two cameras interleaved: name order and capture order disagree.
	files := []FileEntry{
		{Name: "A0001.arw", Size: 1, MtimeNs: 1},
		{Name: "A0002.arw", Size: 1, MtimeNs: 1},
		{Name: "B0001.arw", Size: 1, MtimeNs: 1},
		{Name: "zz-no-exif.arw", Size: 1, MtimeNs: 1},
		{Name: "aa-no-exif.arw", Size: 1, MtimeNs: 1},
	}
	if _, err := db.SyncFolder(ctx, folderID, folderPath, files); err != nil {
		t.Fatalf("SyncFolder: %v", err)
	}

	names := func() []string {
		photos, err := db.ListPhotos(ctx, folderID)
		if err != nil {
			t.Fatalf("ListPhotos: %v", err)
		}
		out := make([]string, len(photos))
		for i, p := range photos {
			out[i] = p.FileName
		}
		return out
	}
	eq := func(label string, got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: got %v, want %v", label, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: got %v, want %v", label, got, want)
			}
		}
	}

	// Before the metadata pass every taken_at is 0, so the folder still sorts
	// by name — a freshly scanned folder must not reshuffle under the user.
	eq("pre-backfill", names(), []string{
		"A0001.arw", "A0002.arw", "B0001.arw", "aa-no-exif.arw", "zz-no-exif.arw",
	})

	// B0001 was shot between the two A frames.
	setTaken := func(name string, takenAt int64) {
		t.Helper()
		p := getByName(t, db, folderID, name)
		if err := db.SetMeta(ctx, p.ID, PhotoMeta{TakenAt: takenAt}); err != nil {
			t.Fatalf("SetMeta(%s): %v", name, err)
		}
	}
	setTaken("A0001.arw", 1000)
	setTaken("A0002.arw", 3000)
	setTaken("B0001.arw", 2000)

	// Timed frames come first in capture order; the two without EXIF dates
	// sort last, among themselves by name.
	eq("post-backfill", names(), []string{
		"A0001.arw", "B0001.arw", "A0002.arw", "aa-no-exif.arw", "zz-no-exif.arw",
	})
}

// Equal capture times (a burst written to the same second, or two bodies in
// sync) must still produce a total, stable order rather than an arbitrary one.
func TestListPhotosTiesBreakOnFileName(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	const folderPath = `/photos`
	folderID, err := db.UpsertFolder(ctx, folderPath)
	if err != nil {
		t.Fatalf("UpsertFolder: %v", err)
	}
	if _, err := db.SyncFolder(ctx, folderID, folderPath, []FileEntry{
		{Name: "c.arw", Size: 1, MtimeNs: 1},
		{Name: "a.arw", Size: 1, MtimeNs: 1},
		{Name: "b.arw", Size: 1, MtimeNs: 1},
	}); err != nil {
		t.Fatalf("SyncFolder: %v", err)
	}
	for _, n := range []string{"a.arw", "b.arw", "c.arw"} {
		p := getByName(t, db, folderID, n)
		if err := db.SetMeta(ctx, p.ID, PhotoMeta{TakenAt: 500}); err != nil {
			t.Fatalf("SetMeta: %v", err)
		}
	}
	photos, err := db.ListPhotos(ctx, folderID)
	if err != nil {
		t.Fatalf("ListPhotos: %v", err)
	}
	for i, want := range []string{"a.arw", "b.arw", "c.arw"} {
		if photos[i].FileName != want {
			t.Fatalf("position %d = %q, want %q", i, photos[i].FileName, want)
		}
	}
}
