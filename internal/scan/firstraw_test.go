package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// FirstRaw stands for a folder not scanned yet: the first RAW by name in
// the folder itself, else in the shallowest subfolder that has one, never
// one in a noise folder such as Exports.
func TestFirstRaw(t *testing.T) {
	root := t.TempDir()
	touch := func(rel string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if got := FirstRaw(ctx, root); got != "" {
		t.Fatalf("an empty folder gave %q", got)
	}
	touch("Exports/A.ARW")
	touch("b/deep/C.ARW")
	touch("b/D.ARW")
	touch("a/notes.txt")
	if got, want := FirstRaw(ctx, root), filepath.Join(root, "b", "D.ARW"); got != want {
		t.Fatalf("with RAWs only in subfolders got %q, want %q", got, want)
	}
	touch("Z.ARW")
	touch("Y.CR3")
	if got, want := FirstRaw(ctx, root), filepath.Join(root, "Y.CR3"); got != want {
		t.Fatalf("with RAWs in the folder itself got %q, want %q", got, want)
	}
}
