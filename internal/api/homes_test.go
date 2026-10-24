package api

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/marraw/internal/scan"
	"github.com/marrasen/marraw/internal/store"
)

// A shoot read through the scanner, as the daemon wires it (Homes), takes
// its photos' rows from the folder an older catalog read them in, and the
// read finishes: the rule runs with the catalog busy, so it must not use
// it.
func TestAShootTakesItsRowsThroughTheScanner(t *testing.T) {
	lib := t.TempDir()
	touchRaw(t, filepath.Join(lib, "Trip", "Day1", "x.ARW"))
	l := libraryWith(t, []LibraryRoot{{Path: lib, IsParent: true}})
	ctx := context.Background()
	trip := filepath.Join(lib, "Trip")
	tid, err := l.deps.DB.UpsertFolder(ctx, trip)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.deps.DB.SyncFolder(ctx, tid, trip, []store.FileEntry{{Name: filepath.Join("Day1", "x.ARW"), Size: 3, MtimeNs: 1}}); err != nil {
		t.Fatal(err)
	}
	old, err := l.deps.DB.ListPhotos(ctx, tid)
	if err != nil || len(old) != 1 {
		t.Fatalf("Trip = %+v, %v", old, err)
	}
	// Trip is a group now: the upgrade would keep it one shoot, so it is
	// marked done to see the row move.
	l.upgraded = map[string]bool{lowerPath(lib): true}

	sc := &scan.Scanner{DB: l.deps.DB, Homes: l.Homes}
	done := make(chan error, 1)
	var id int64
	go func() {
		var err error
		id, _, err = sc.OpenFolder(ctx, filepath.Join(trip, "Day1"), l.scanRecursionFor(ctx, filepath.Join(trip, "Day1")))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reading a shoot hung")
	}
	got, err := l.deps.DB.ListPhotos(ctx, id)
	if err != nil || len(got) != 1 || got[0].ID != old[0].ID {
		t.Fatalf("Day1 = %+v, %v; want photo %d", got, err, old[0].ID)
	}
}
