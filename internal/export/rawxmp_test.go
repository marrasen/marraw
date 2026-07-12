package export

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/marraw/internal/store"
	"github.com/marrasen/marraw/internal/xmp"
)

func writeFakeRaw(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("not-really-raw-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExportRawXmpCopies(t *testing.T) {
	srcDir, destDir := t.TempDir(), t.TempDir()
	src := writeFakeRaw(t, srcDir, "DSC0001.ARW")
	mtime := time.Date(2026, 3, 14, 9, 26, 53, 0, time.Local)
	if err := os.Chtimes(src, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	photo := store.Photo{
		FolderPath: srcDir,
		FileName:   "DSC0001.ARW",
		Rating:     4,
		EditParams: sql.NullString{String: `{"contrast":0.25}`, Valid: true},
	}
	out := filepath.Join(destDir, "DSC0001.ARW")
	if err := exportRawXmp(photo, out, false); err != nil {
		t.Fatalf("exportRawXmp: %v", err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("copy missing: %v", err)
	}
	if string(got) != "not-really-raw-bytes" {
		t.Fatalf("copy content mismatch: %q", got)
	}
	if st, _ := os.Stat(out); !st.ModTime().Equal(mtime) {
		t.Errorf("mtime not preserved: got %v want %v", st.ModTime(), mtime)
	}
	sc, err := os.ReadFile(filepath.Join(destDir, "DSC0001.xmp"))
	if err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}
	if !strings.Contains(string(sc), `crs:Contrast2012="+25"`) || !strings.Contains(string(sc), `xmp:Rating="4"`) {
		t.Fatalf("sidecar content: %s", sc)
	}
}

func TestExportRawXmpSameDirWritesSidecarOnly(t *testing.T) {
	dir := t.TempDir()
	src := writeFakeRaw(t, dir, "DSC0002.ARW")

	photo := store.Photo{FolderPath: dir, FileName: "DSC0002.ARW", Rating: 2}
	if err := exportRawXmp(photo, src, true); err != nil {
		t.Fatalf("exportRawXmp: %v", err)
	}

	got, err := os.ReadFile(src)
	if err != nil || string(got) != "not-really-raw-bytes" {
		t.Fatalf("original touched: %q, %v", got, err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		names := make([]string, 0, len(ents))
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("expected exactly RAW + sidecar, got %v", names)
	}
	if _, err := os.Stat(xmp.PathFor(src)); err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}
	// Re-export refreshes the sidecar in place rather than duplicating.
	photo.Rating = 5
	if err := exportRawXmp(photo, src, true); err != nil {
		t.Fatal(err)
	}
	sc, _ := os.ReadFile(xmp.PathFor(src))
	if !strings.Contains(string(sc), `xmp:Rating="5"`) {
		t.Fatalf("sidecar not refreshed: %s", sc)
	}
}

func TestExportRawXmpInvalidEditFallsBackToNeutral(t *testing.T) {
	srcDir, destDir := t.TempDir(), t.TempDir()
	writeFakeRaw(t, srcDir, "DSC0003.ARW")
	photo := store.Photo{
		FolderPath: srcDir,
		FileName:   "DSC0003.ARW",
		EditParams: sql.NullString{String: "{corrupt", Valid: true},
	}
	if err := exportRawXmp(photo, filepath.Join(destDir, "DSC0003.ARW"), false); err != nil {
		t.Fatalf("exportRawXmp: %v", err)
	}
	sc, err := os.ReadFile(filepath.Join(destDir, "DSC0003.xmp"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sc), "crs:") {
		t.Fatalf("corrupt edit must fall back to a metadata-only sidecar: %s", sc)
	}
}

func TestNamerRawXmpKeepsSourceExtension(t *testing.T) {
	dest := t.TempDir()
	n := newNamer(dest, "", 3)
	if got := n.claim("DSC0001.ARW", 0, "rawXmp"); got != "DSC0001.ARW" {
		t.Fatalf("claim = %q, want DSC0001.ARW", got)
	}
	// A pre-existing file at the destination suffixes, never overwrites.
	writeFakeRaw(t, dest, "DSC0002.ARW")
	if got := n.claim("DSC0002.ARW", 0, "rawXmp"); got != "DSC0002-2.ARW" {
		t.Fatalf("claim = %q, want DSC0002-2.ARW", got)
	}
}

func TestNamerRawXmpReservesSidecarName(t *testing.T) {
	n := newNamer(t.TempDir(), "", 2)
	if got := n.claim("IMG1.ARW", 0, "rawXmp"); got != "IMG1.ARW" {
		t.Fatalf("first claim = %q", got)
	}
	// Same basename, different RAW extension: the sidecars would collide on
	// IMG1.xmp, so the second claim must move aside.
	if got := n.claim("IMG1.CR2", 0, "rawXmp"); got != "IMG1-2.CR2" {
		t.Fatalf("second claim = %q, want IMG1-2.CR2", got)
	}
}
