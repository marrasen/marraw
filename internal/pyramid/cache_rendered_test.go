package pyramid

import (
	"bytes"
	"image"
	"os"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

// An unedited RAW render writes over the levels the camera's embedded
// JPEG gave, so the grid's thumbnail shows marraw's render; an edited
// render's files are its own, and one there already is left as it is.
func TestRenderWritesOverThumbLevels(t *testing.T) {
	c, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	src := image.NewRGBA(image.Rect(0, 0, 600, 400))
	stub := []byte("embedded")
	for _, hash := range []string{edit.BaseHash, "abc123"} {
		writeCacheFile(t, c.PathFor("key", "256", hash), string(stub))
	}
	if err := c.writeRendered(src, "key", edit.BaseHash, true); err != nil {
		t.Fatal(err)
	}
	if err := c.writeRendered(src, "key", "abc123", false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(c.PathFor("key", "256", edit.BaseHash)); bytes.Equal(b, stub) {
		t.Error("the unedited render left the embedded JPEG's 256 in place")
	}
	if b, _ := os.ReadFile(c.PathFor("key", "256", "abc123")); !bytes.Equal(b, stub) {
		t.Error("the edited render wrote over a file there already")
	}
	if _, err := os.Stat(c.PathFor("key", "2048", edit.BaseHash)); err != nil {
		t.Errorf("no 2048 written: %v", err)
	}
}
