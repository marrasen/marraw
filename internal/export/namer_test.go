package export

import (
	"fmt"
	"testing"
	"time"
)

func TestNamerDefaultTemplateKeepsSourceName(t *testing.T) {
	n := newNamer(t.TempDir(), "", 3)
	if got := n.claim("DSC09879.ARW", 0, "jpeg"); got != "DSC09879.jpg" {
		t.Fatalf("claim = %q, want DSC09879.jpg", got)
	}
	if got := n.claim("DSC09880.ARW", 0, "tiff8"); got != "DSC09880.tif" {
		t.Fatalf("claim = %q, want DSC09880.tif", got)
	}
	if got := n.claim("DSC09881.ARW", 0, "png"); got != "DSC09881.png" {
		t.Fatalf("claim = %q, want DSC09881.png", got)
	}
}

func TestNamerCollisionSuffix(t *testing.T) {
	n := newNamer(t.TempDir(), "fixed", 2)
	if got := n.claim("a.ARW", 0, "jpeg"); got != "fixed.jpg" {
		t.Fatalf("first claim = %q", got)
	}
	if got := n.claim("b.ARW", 0, "jpeg"); got != "fixed-2.jpg" {
		t.Fatalf("second claim = %q, want fixed-2.jpg", got)
	}
}

func TestNamerSeqPadsToBatchSize(t *testing.T) {
	n := newNamer(t.TempDir(), "{seq}", 5)
	if got := n.claim("a.ARW", 0, "jpeg"); got != "001.jpg" {
		t.Fatalf("claim = %q, want 001.jpg (3-digit floor)", got)
	}
	big := newNamer(t.TempDir(), "{seq}", 1500)
	if got := big.claim("a.ARW", 0, "jpeg"); got != "0001.jpg" {
		t.Fatalf("claim = %q, want 0001.jpg (pads to batch size)", got)
	}
}

func TestNamerDateTimeTokens(t *testing.T) {
	taken := time.Date(2026, 4, 18, 10, 4, 5, 0, time.Local).Unix()
	n := newNamer(t.TempDir(), "{date}_{time}_{name}", 1)
	want := "20260418_100405_DSC09879.jpg"
	if got := n.claim("DSC09879.ARW", taken, "jpeg"); got != want {
		t.Fatalf("claim = %q, want %q", got, want)
	}
}

func TestNamerEmptyExpansionFallsBackToSource(t *testing.T) {
	// {date} on a photo without EXIF date expands to nothing.
	n := newNamer(t.TempDir(), "{date}", 1)
	if got := n.claim("DSC09879.ARW", 0, "jpeg"); got != "DSC09879.jpg" {
		t.Fatalf("claim = %q, want the source-name fallback", got)
	}
}

func TestNamerSanitizesForbiddenCharacters(t *testing.T) {
	n := newNamer(t.TempDir(), `shoot: {name}/final*`, 1)
	if got := n.claim("a.ARW", 0, "jpeg"); got != "shoot- a-final-.jpg" {
		t.Fatalf("claim = %q", got)
	}
}

func TestNamerUnknownTokenStaysLiteral(t *testing.T) {
	n := newNamer(t.TempDir(), "{name}{camera}", 1)
	if got := n.claim("a.ARW", 0, "jpeg"); got != "a{camera}.jpg" {
		t.Fatalf("claim = %q, want a{camera}.jpg", got)
	}
}

func TestNamerSeqFollowsRequestOrder(t *testing.T) {
	n := newNamer(t.TempDir(), "trip-{seq}", 12)
	for i := 1; i <= 3; i++ {
		want := fmt.Sprintf("trip-%03d.jpg", i)
		if got := n.claim("x.ARW", 0, "jpeg"); got != want {
			t.Fatalf("claim %d = %q, want %q", i, got, want)
		}
	}
}
