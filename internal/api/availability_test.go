package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A folder moved away from a folder still there is missing; one whose
// parent went too is on a drive unplugged.
func TestPresenceTellsMovedFromUnplugged(t *testing.T) {
	base := t.TempDir()
	here := filepath.Join(base, "Photos", "2026-10-01 Öland")
	moved := filepath.Join(base, "Photos", "2026-09-12 Wedding")
	twin := filepath.Join(base, "Photos", "2026-08-03 Gotland")
	unplugged := filepath.Join(base, "Drive", "Trip")
	for _, d := range []string{here, moved} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(moved, filepath.Join(base, "2026-09-12 Wedding")); err != nil {
		t.Fatal(err)
	}
	got := presenceAll([]string{here, moved, twin, unplugged})
	for path, want := range map[string]presence{here: present, moved: missing, twin: missing, unplugged: disconnected} {
		if got[strings.ToLower(path)] != want {
			t.Errorf("%s: presence %d, want %d", filepath.Base(path), got[strings.ToLower(path)], want)
		}
	}
}
