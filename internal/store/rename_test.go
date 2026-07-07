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

	old := `C:\Shoots\Wedding`
	sub := `C:\Shoots\Wedding\ceremony`
	other := `C:\Shoots\WeddingParty` // shares the string prefix but is a sibling
	for _, p := range []string{old, sub, other} {
		if _, err := db.UpsertFolder(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.RenameFolderPaths(ctx, old, `C:\Shoots\Tobias & Elisabeth`); err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{
		`C:\Shoots\Tobias & Elisabeth`:          true,
		`C:\Shoots\Tobias & Elisabeth\ceremony`: true,
		`C:\Shoots\WeddingParty`:                true,
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
