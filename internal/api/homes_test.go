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

// A photo's home is the folder the sidebar's rules read it in.
func TestHomesFollowTheShoots(t *testing.T) {
	lib := t.TempDir()
	for _, p := range []string{
		"loose.ARW",
		"2019/Trip/a.ARW",
		"2019/Trip/CamA/b.ARW",
		"2019/Added/Inner/c.ARW",
		"Exports/e.ARW",
		"Hidden/h.ARW",
		"Mixed/m.ARW",
		"Mixed/Deep/Deeper/d.ARW",
		"2020-05-01 Wedding/CamB/w.ARW",
		"Old/Kept/CamA/k.ARW",
		"Exports/Final/f.ARW",
	} {
		touchRaw(t, filepath.Join(lib, p))
	}
	j := func(p ...string) string { return filepath.Join(append([]string{lib}, p...)...) }
	roots := []LibraryRoot{
		{Path: lib, IsParent: true,
			ExcludedChildren: []string{lowerPath(j("Hidden")), lowerPath(j("Old"))},
			FolderModes:      map[string]string{lowerPath(j("Mixed")): FolderSplit}},
		// Added on its own inside a shoot, and inside a group.
		{Path: j("2019", "Trip", "CamA")},
		{Path: j("2019", "Added"), IncludeSubfolders: true},
		// Added on its own under a hidden folder, and a skipped one.
		{Path: j("Old", "Kept"), IncludeSubfolders: true},
		{Path: j("Exports", "Final")},
	}
	l := libraryWith(t, roots)
	home := l.Homes()
	for file, want := range map[string]string{
		j("loose.ARW"):                            lib,
		j("2019", "Trip", "a.ARW"):                j("2019", "Trip"),
		j("2019", "Trip", "CamA", "b.ARW"):        j("2019", "Trip"),
		j("2019", "Added", "Inner", "c.ARW"):      j("2019", "Added"),
		j("Exports", "e.ARW"):                     "",
		j("Hidden", "h.ARW"):                      "",
		j("Mixed", "m.ARW"):                       j("Mixed"),
		j("Mixed", "Deep", "Deeper", "d.ARW"):     j("Mixed", "Deep", "Deeper"),
		j("2020-05-01 Wedding", "CamB", "w.ARW"):  j("2020-05-01 Wedding"),
		filepath.Join(t.TempDir(), "outside.ARW"): "",
		j("Old", "Kept", "CamA", "k.ARW"):         j("Old", "Kept"),
		j("Exports", "Final", "f.ARW"):            j("Exports", "Final"),
	} {
		if got := home(file); got != want {
			t.Errorf("home of %s = %q, want %q", file, got, want)
		}
	}
}
