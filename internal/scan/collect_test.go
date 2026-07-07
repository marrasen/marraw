package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectEntries(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "IMG1.ARW"))
	touch(t, filepath.Join(root, "notes.txt"))
	touch(t, filepath.Join(root, "ceremony", "IMG2.ARW"))
	touch(t, filepath.Join(root, "ceremony", "deep", "IMG3.ARW"))
	touch(t, filepath.Join(root, "export", "IMG4.ARW"))      // noise dir
	touch(t, filepath.Join(root, ".thumbnails", "IMG5.ARW")) // dot dir

	t.Run("flat", func(t *testing.T) {
		entries, err := CollectEntries(context.Background(), root, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name != "IMG1.ARW" {
			t.Fatalf("flat scan = %+v, want just IMG1.ARW", entries)
		}
	})

	t.Run("recursive", func(t *testing.T) {
		entries, err := CollectEntries(context.Background(), root, true)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, e := range entries {
			got[e.Name] = true
		}
		want := []string{
			"IMG1.ARW",
			filepath.Join("ceremony", "IMG2.ARW"),
			filepath.Join("ceremony", "deep", "IMG3.ARW"),
		}
		if len(got) != len(want) {
			t.Fatalf("recursive scan = %v, want %v", got, want)
		}
		for _, w := range want {
			if !got[w] {
				t.Errorf("missing %s in %v", w, got)
			}
		}
	})

	t.Run("symlink loop", func(t *testing.T) {
		loopRoot := t.TempDir()
		touch(t, filepath.Join(loopRoot, "sub", "IMG1.ARW"))
		// A symlink back to the root inside sub would loop forever without
		// the resolved-path guard. Symlink creation needs privileges on
		// Windows — skip the case (not the whole test) when unavailable.
		if err := os.Symlink(loopRoot, filepath.Join(loopRoot, "sub", "back")); err != nil {
			t.Skipf("cannot create symlink: %v", err)
		}
		entries, err := CollectEntries(context.Background(), loopRoot, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("loop scan = %+v, want 1 entry", entries)
		}
	})
}
