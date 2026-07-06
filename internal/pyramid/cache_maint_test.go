package pyramid

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCacheFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCacheStatAndClear(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeCacheFile(t, filepath.Join(dir, "ab", "f1.jpg"), "hello") // 5
	writeCacheFile(t, filepath.Join(dir, "cd", "f2.jpg"), "worldd") // 6

	if b, n := c.Stat(); b != 11 || n != 2 {
		t.Fatalf("Stat = %d bytes / %d files, want 11/2", b, n)
	}
	if err := c.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if b, n := c.Stat(); b != 0 || n != 0 {
		t.Fatalf("after Clear Stat = %d/%d, want 0/0", b, n)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("cache dir should survive Clear: %v", err)
	}
}

func TestCacheRelocateWipesOld(t *testing.T) {
	base := t.TempDir()
	old := filepath.Join(base, "old")
	c, err := New(old, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeCacheFile(t, filepath.Join(old, "ab", "f.jpg"), "x")

	newDir := filepath.Join(base, "new")
	if err := c.Relocate(newDir); err != nil {
		t.Fatalf("Relocate: %v", err)
	}
	if c.Dir() != filepath.Clean(newDir) {
		t.Fatalf("Dir = %q, want %q", c.Dir(), filepath.Clean(newDir))
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old cache should be wiped after relocate (err=%v)", err)
	}
	writeCacheFile(t, filepath.Join(newDir, "cd", "g.jpg"), "yy")
	if b, _ := c.Stat(); b != 2 {
		t.Fatalf("new cache Stat = %d bytes, want 2", b)
	}
}

// TestCacheRelocateNestedGuard verifies the wipe never deletes an ancestor of
// the new location (which would take the new cache down with it).
func TestCacheRelocateNestedGuard(t *testing.T) {
	base := t.TempDir()
	old := filepath.Join(base, "cache")
	c, err := New(old, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(old, "ab", "f.jpg")
	writeCacheFile(t, keep, "data")

	inner := filepath.Join(old, "marraw-previews") // new dir inside old
	if err := c.Relocate(inner); err != nil {
		t.Fatalf("Relocate: %v", err)
	}
	if c.Dir() != filepath.Clean(inner) {
		t.Fatalf("Dir = %q, want %q", c.Dir(), filepath.Clean(inner))
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("nested relocate must not wipe the ancestor cache: %v", err)
	}
}
