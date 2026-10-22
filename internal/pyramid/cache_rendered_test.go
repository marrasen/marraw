package pyramid

import (
	"bytes"
	"image"
	"os"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/jpegturbo"
	"github.com/marrasen/marraw/internal/store"
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
	if _, err := c.writeRendered(src, "key", edit.BaseHash, true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.writeRendered(src, "key", "abc123", false); err != nil {
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

// A level drawn from the camera's JPEG says so, still decodes, and the
// render that writes over it reports it replaced one; marraw's own levels
// carry no mark, and writing over them reports nothing.
func TestCameraLevelsAreMarkedAndReplaced(t *testing.T) {
	c, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	thumb := image.NewRGBA(image.Rect(0, 0, 1200, 800))
	if err := c.writeCameraLevels(thumb, "key", edit.BaseHash, 1024, 512, 256); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"1024", "512", "256"} {
		path := c.PathFor("key", l, edit.BaseHash)
		if !isCameraFile(path) {
			t.Errorf("%s: no camera mark", l)
		}
		b, _ := os.ReadFile(path)
		if m, err := jpegturbo.DecodeRGBA(b); err != nil || m.Rect.Dx() != map[string]int{"1024": 1024, "512": 512, "256": 256}[l] {
			t.Errorf("%s: a marked level does not decode: %v", l, err)
		}
	}
	src := image.NewRGBA(image.Rect(0, 0, 3000, 2000))
	replaced, err := c.writeRendered(src, "key", edit.BaseHash, true)
	if err != nil || !replaced {
		t.Fatalf("writing over the camera's levels: replaced=%v, %v", replaced, err)
	}
	for _, l := range []string{"2048", "1024", "512", "256"} {
		if isCameraFile(c.PathFor("key", l, edit.BaseHash)) {
			t.Errorf("%s: still marked after the render", l)
		}
	}
	if replaced, _ := c.writeRendered(src, "key", edit.BaseHash, true); replaced {
		t.Error("writing over marraw's own levels reported a camera level replaced")
	}
}

// The mark is found after a JFIF header or straight after the start of
// image, and nowhere else.
func TestIsCameraJPEG(t *testing.T) {
	var buf bytes.Buffer
	if err := jpegturbo.EncodeRGBA(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), 80); err != nil {
		t.Fatal(err)
	}
	plain := buf.Bytes()
	if IsCameraJPEG(plain[:min(len(plain), CameraHeadLen)]) {
		t.Error("an unmarked JPEG reads as marked")
	}
	tagged := tagCamera(plain)
	if !IsCameraJPEG(tagged[:CameraHeadLen]) {
		t.Error("a marked JPEG reads as unmarked")
	}
	bare := tagCamera([]byte{0xFF, 0xD8, 0xFF, 0xDB, 0, 4, 1, 2})
	if !IsCameraJPEG(bare) {
		t.Error("a mark straight after the start of image is missed")
	}
	for _, b := range [][]byte{nil, {0xFF}, []byte("marraw:camera"), {0xFF, 0xD8, 0xFF, 0xE0, 0xFF}} {
		if IsCameraJPEG(b) {
			t.Errorf("%x reads as marked", b)
		}
	}
}

// A camera write never replaces a file, a render's own; any other write
// replaces a camera-drawn one, over or not, and says so.
func TestCameraFileGivesWay(t *testing.T) {
	path := t.TempDir() + "/x.jpg"
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	if _, err := writeJPEGFileTagged(path, img, 80, false, false); err != nil {
		t.Fatal(err)
	}
	if r, err := writeJPEGFileTagged(path, img, 80, false, true); err != nil || r || isCameraFile(path) {
		t.Fatalf("a camera write over a render: replaced=%v, %v; camera now %v", r, err, isCameraFile(path))
	}
	os.Remove(path)
	if _, err := writeJPEGFileTagged(path, img, 80, false, true); err != nil || !isCameraFile(path) {
		t.Fatalf("camera write: %v", err)
	}
	if r, err := writeJPEGFileTagged(path, img, 80, false, true); err != nil || r || !isCameraFile(path) {
		t.Fatalf("a camera write over a camera file: replaced=%v, %v", r, err)
	}
	if r, err := writeJPEGFileTagged(path, img, 80, false, false); err != nil || !r || isCameraFile(path) {
		t.Fatalf("a render over a camera file: replaced=%v, %v; camera now %v", r, err, isCameraFile(path))
	}
	if r, _ := writeJPEGFileTagged(path, img, 80, false, false); r {
		t.Error("a render over a render reported a camera file replaced")
	}
}

// The unedited look settling (neutral params) draws the photo's 2048, and
// with it nothing else renders the photo: its camera-drawn small levels
// give way to that render then, and clients hear of it.
func TestBasePreviewReplacesCameraLevels(t *testing.T) {
	c, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var told []int64
	c.OnBaseRendered = func(id int64) { told = append(told, id) }
	photo := store.Photo{ID: 9, CacheKey: "key"}
	if err := c.writeCameraLevels(image.NewRGBA(image.Rect(0, 0, 1200, 800)), "key", edit.BaseHash, 1024, 512, 256); err != nil {
		t.Fatal(err)
	}
	src := image.NewRGBA(image.Rect(0, 0, 3000, 2000))
	if err := c.WritePreview(src, photo, edit.BaseHash, 1, &edit.Params{}, 0); err != nil {
		t.Fatal(err)
	}
	if c.cameraLevels("key") {
		t.Error("camera-drawn levels left after the unedited look settled")
	}
	if len(told) != 1 || told[0] != 9 {
		t.Errorf("OnBaseRendered calls = %v, want [9]", told)
	}
	if err := c.WritePreview(src, photo, edit.BaseHash, 1, &edit.Params{}, 0); err != nil || len(told) != 1 {
		t.Errorf("settling again: %v, calls %v; want no new call", err, told)
	}
}
