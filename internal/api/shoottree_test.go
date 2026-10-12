package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marrasen/marraw/internal/store"
)

// libraryWith is a Library whose catalog holds roots.
func libraryWith(t *testing.T, roots []LibraryRoot) *Library {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	l := &Library{deps: &Deps{DB: db}}
	setRoots(t, l, roots)
	return l
}

func setRoots(t *testing.T, l *Library, roots []LibraryRoot) {
	t.Helper()
	b, _ := json.Marshal(roots)
	if err := l.deps.DB.SetSetting(context.Background(), settingLibraryRoots, string(b)); err != nil {
		t.Fatal(err)
	}
}

func touchRaw(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("raw"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestShootsAtAnyDepth(t *testing.T) {
	lib := t.TempDir()
	for _, p := range []string{
		"loose.ARW",
		"2019/2019-11-08 Wedding/CamA/a.ARW",
		"2019/2019-11-08 Wedding/CamB/b.ARW",
		"2019/Concert/c.ARW",
		"Clients/Acme/Launch/d.ARW",
		"Clients/Acme/Launch/Selects/e.ARW",
		"Exports/x.ARW",
		"Mixed/m.ARW",
		"Mixed/Second/s.ARW",
		"Music video/DCIM/100MSDCF/v1.ARW",
		"Music video/DCIM/100MSDCF/v2.ARW",
	} {
		touchRaw(t, filepath.Join(lib, p))
	}
	if err := os.MkdirAll(filepath.Join(lib, "Empty", "Nothing"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := LibraryRoot{Path: lib, IsParent: true, FolderModes: map[string]string{
		lowerPath(filepath.Join(lib, "Mixed")): FolderSplit,
	}}
	l := libraryWith(t, []LibraryRoot{root})
	ctx := context.Background()

	got := shootsByName(t, l, lib)
	type want struct {
		group             string
		n                 int
		self, split, subs bool
	}
	expect := map[string]want{
		filepath.Base(lib):   {n: 1, self: true},
		"2019-11-08 Wedding": {group: "2019", n: 2, subs: true},
		"Concert":            {group: "2019", n: 1},
		"Launch":             {group: "Clients/Acme", n: 2, subs: true},
		"Mixed":              {n: 1, split: true, subs: true},
		"Second":             {group: "Mixed", n: 1},
		// A card copied whole, its name undated: one shoot.
		"Music video": {n: 2, subs: true},
	}
	if len(got) != len(expect) {
		t.Fatalf("listed %d shoots, want %d: %+v", len(got), len(expect), got)
	}
	for name, w := range expect {
		s, ok := got[name]
		if !ok {
			t.Errorf("%s is not listed", name)
			continue
		}
		if s.Group != w.group || s.PhotoCount != w.n || s.IsSelf != w.self || s.Split != w.split || s.Subfolders != w.subs {
			t.Errorf("%s: group %q, %d photos, self %v, split %v, subfolders %v; want %+v", name, s.Group, s.PhotoCount, s.IsSelf, s.Split, s.Subfolders, w)
		}
	}

	// How deep each is scanned: a shoot takes in its subfolders, a split
	// one does not, and the library folder holds its loose RAWs alone.
	for path, deep := range map[string]bool{
		filepath.Join(lib, "2019", "2019-11-08 Wedding"): true,
		filepath.Join(lib, "Clients", "Acme", "Launch"):  true,
		filepath.Join(lib, "Mixed"):                      false,
		lib:                                              false,
	} {
		if got := l.scanRecursionFor(ctx, path); got != deep {
			t.Errorf("scanRecursionFor(%s) = %v, want %v", path, got, deep)
		}
	}

	// The user makes a client one shoot, and hides a shoot in a year.
	root.FolderModes[lowerPath(filepath.Join(lib, "Clients"))] = FolderOne
	root.ExcludedChildren = []string{lowerPath(filepath.Join(lib, "2019", "Concert"))}
	setRoots(t, l, []LibraryRoot{root})
	got = shootsByName(t, l, lib)
	if c, ok := got["Clients"]; !ok || c.Group != "" || c.PhotoCount != 2 {
		t.Errorf("Clients made one shoot: %+v, %v", c, ok)
	}
	if _, ok := got["Launch"]; ok {
		t.Error("Launch is still listed apart from its client made one shoot")
	}
	if _, ok := got["Concert"]; ok {
		t.Error("a hidden shoot in a year is still listed")
	}
}

func shootsByName(t *testing.T, l *Library, lib string) map[string]Shoot {
	t.Helper()
	shoots, err := l.ListShoots(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Shoot{}
	for _, s := range shoots {
		if _, dup := out[s.Name]; dup {
			t.Fatalf("%s listed twice", s.Name)
		}
		out[s.Name] = s
	}
	return out
}

func lowerPath(p string) string { return strings.ToLower(filepath.Clean(p)) }
