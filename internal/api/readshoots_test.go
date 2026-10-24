package api

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/scan"
)

// ReadShoots reads the library's shoots it has not read, and nothing
// outside the library.
func TestReadShootsReadsLibraryShootsOnly(t *testing.T) {
	lib, away := t.TempDir(), t.TempDir()
	l := libraryWith(t, []LibraryRoot{{Path: lib, IsParent: true}})
	l.deps.Scanner = &scan.Scanner{DB: l.deps.DB, Pool: decode.NewPool(1)}
	a, b, c := filepath.Join(lib, "2026", "A"), filepath.Join(lib, "B"), filepath.Join(away, "C")
	for _, d := range []string{a, b, c} {
		touchRaw(t, filepath.Join(d, "IMG_1.ARW"))
	}
	ctx := context.Background()
	if err := l.ReadShoots(ctx, lib, []string{a, b, c}); err != nil {
		t.Fatal(err)
	}
	read, err := l.ScannedFolders(ctx, []string{a, b, c})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, f := range read {
		got[f.Path] = f.PhotoCount
	}
	if got[a] != 1 || got[b] != 1 {
		t.Errorf("read %v, want A and B with a photo each", got)
	}
	if _, ok := got[c]; ok {
		t.Errorf("read %s, outside the library", c)
	}
	// Read already, they are left alone.
	if err := l.ReadShoots(ctx, lib, []string{a, b}); err != nil {
		t.Fatal(err)
	}
}
