package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A folder moved away from a folder still there is missing, as is one
// whose parent moved; one with nothing above it there is on a drive
// unplugged.
func TestPresenceTellsMovedFromUnplugged(t *testing.T) {
	base := t.TempDir()
	here := filepath.Join(base, "Photos", "2026-10-01 Öland")
	moved := filepath.Join(base, "Photos", "2026-09-12 Wedding")
	twin := filepath.Join(base, "Photos", "2026-08-03 Gotland")
	// A drive not there: nothing above the shoot is, its top folder
	// included, up to where drives are mounted. A drive letter never used
	// stands in for it on Windows.
	unplugged := "/mnt/marraw-no-such-drive/Trip"
	switch runtime.GOOS {
	case "windows":
		unplugged = `Q:\marraw-no-such-drive\Trip`
	case "darwin":
		unplugged = "/Volumes/marraw-no-such-drive/Trip"
	}
	// A year moved, with the shoot added by hand inside it.
	inYear := filepath.Join(base, "Photos", "2025", "Day 1", "Trip")
	for _, d := range []string{here, moved, inYear} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(moved, filepath.Join(base, "2026-09-12 Wedding")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(base, "Photos", "2025"), filepath.Join(base, "2025")); err != nil {
		t.Fatal(err)
	}
	got := presenceAll([]string{here, moved, twin, unplugged, inYear})
	for path, want := range map[string]presence{here: present, moved: missing, twin: missing, unplugged: disconnected, inYear: missing} {
		if got[strings.ToLower(path)] != want {
			t.Errorf("%s: presence %d, want %d", filepath.Base(path), got[strings.ToLower(path)], want)
		}
	}
}

// Away from Windows, a folder missing from an empty one reads as a drive
// unplugged from its mount point, not a folder moved.
func TestPresenceTakesAnEmptyParentForAMountPoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no mount points in folders to mistake")
	}
	mount := filepath.Join(t.TempDir(), "Drive")
	if err := os.Mkdir(mount, 0o755); err != nil {
		t.Fatal(err)
	}
	trip := filepath.Join(mount, "Trip")
	if got := presenceAll([]string{trip})[strings.ToLower(trip)]; got != disconnected {
		t.Errorf("presence %d, want disconnected (%d)", got, disconnected)
	}
}

// A share whose mount went, under GNOME's gvfs or autofs's /net, is not a
// folder moved, though the folder above it is there and holds other
// mounts.
func TestPresenceStopsWhereSharesAreMounted(t *testing.T) {
	for _, dir := range []string{"/run/user/1000/gvfs", "/net", "/net/nas", "/"} {
		if runtime.GOOS != "windows" && !mountHolder(dir) {
			t.Errorf("%s is where shares are mounted", dir)
		}
	}
	if mountHolder("/home/u/Photos") {
		t.Error("a photo folder is no mount holder")
	}
}
