package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRenameFolderPaths(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Native separators: RenameFolderPaths matches descendants with the
	// platform separator, so literal `C:\...` strings would silently be
	// prefix-only matches on Linux/macOS CI.
	root := filepath.Join(t.TempDir(), "Shoots")
	old := filepath.Join(root, "Wedding")
	sub := filepath.Join(root, "Wedding", "ceremony")
	other := filepath.Join(root, "WeddingParty") // shares the string prefix but is a sibling
	for _, p := range []string{old, sub, other} {
		if _, err := db.UpsertFolder(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	renamed := filepath.Join(root, "Tobias & Elisabeth")
	if err := db.RenameFolderPaths(ctx, old, renamed); err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{
		renamed:                             true,
		filepath.Join(renamed, "ceremony"):  true,
		filepath.Join(root, "WeddingParty"): true,
	}
	rows, err := db.QueryContext(ctx, `SELECT path FROM folders`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		if !want[p] {
			t.Errorf("unexpected folder path %q", p)
		}
		n++
	}
	if n != len(want) {
		t.Errorf("got %d folder rows, want %d", n, len(want))
	}
}
