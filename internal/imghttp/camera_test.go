package imghttp

import (
	"bytes"
	"image"
	"image/jpeg"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestServeCameraLevel: a level drawn from the camera's JPEG, which
// marraw's render replaces under the same URL, goes out for the browser to
// ask again about (no-cache, with a tag that answers "not modified" while
// the file stays); marraw's own render then goes out immutable.
func TestServeCameraLevel(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	plain := buf.Bytes()
	// image/jpeg writes no JFIF header: the mark goes straight after the
	// start of image, as pyramid's tagCamera puts it there.
	tag := []byte("marraw:camera")
	camera := append([]byte{0xFF, 0xD8, 0xFF, 0xFE, 0, byte(2 + len(tag))}, tag...)
	camera = append(camera, plain[2:]...)
	path := filepath.Join(t.TempDir(), "level.jpg")
	h := &Handler{}
	serve := func(etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/img/1/256", nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		w := httptest.NewRecorder()
		h.serveFile(w, req, path)
		return w
	}

	if err := os.WriteFile(path, camera, 0o644); err != nil {
		t.Fatal(err)
	}
	w := serve("")
	etag := w.Header().Get("ETag")
	if cc := w.Header().Get("Cache-Control"); w.Code != 200 || cc != "no-cache" || etag == "" {
		t.Fatalf("camera level: %d, Cache-Control %q, ETag %q; want 200, no-cache, a tag", w.Code, cc, etag)
	}
	if w := serve(etag); w.Code != 304 {
		t.Errorf("camera level asked again unchanged: %d, want 304", w.Code)
	}

	if err := os.WriteFile(path, plain, 0o644); err != nil {
		t.Fatal(err)
	}
	w = serve(etag)
	if cc := w.Header().Get("Cache-Control"); w.Code != 200 || cc != immutable || !bytes.Equal(w.Body.Bytes(), plain) {
		t.Errorf("rendered level asked again: %d, Cache-Control %q; want 200, immutable, the render", w.Code, cc)
	}
}
