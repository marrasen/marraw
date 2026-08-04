package imghttp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marrasen/marraw/internal/store"
)

func TestJpegName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"DSC01234.ARW", "DSC01234.jpg"},
		{"band shoot 01.arw", "band shoot 01.jpg"},
		{"no-extension", "no-extension.jpg"},
		{"two.dots.cr3", "two.dots.jpg"},
	} {
		if got := jpegName(photoNamed(tc.in)); got != tc.want {
			t.Errorf("jpegName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A file name that is not plain ASCII must survive the header without
// producing something a browser will reject or truncate.
func TestContentDisposition(t *testing.T) {
	got := contentDisposition("Bandfoto Ödegård.jpg")
	if !strings.Contains(got, `filename="Bandfoto _deg_rd.jpg"`) {
		t.Errorf("contentDisposition = %q, want an ASCII fallback name", got)
	}
	if !strings.Contains(got, "filename*=UTF-8''Bandfoto%20%C3%96deg%C3%A5rd.jpg") {
		t.Errorf("contentDisposition = %q, want a percent-encoded UTF-8 name", got)
	}
	if strings.Count(got, `"`) != 2 {
		t.Errorf("contentDisposition = %q, want exactly one quoted value", got)
	}
}

// The capability is what separates a link that can view from one that can take
// copies away, so it is checked before any rendering is queued.
func TestDownloadsRequireTheCapability(t *testing.T) {
	cases := []struct {
		name  string
		acc   Access
		allow bool
	}{
		{"owner is unconfined", Access{}, true},
		{"share with downloads", Access{FolderID: 7, Downloads: true}, true},
		{"share without downloads", Access{FolderID: 7}, false},
	}
	for _, tc := range cases {
		h := &Downloads{Authorize: func(string) (Access, bool) { return tc.acc, true }}
		w := httptest.NewRecorder()
		if _, ok := h.allowed(w, httptest.NewRequest("GET", "/dl/1?t=x", nil)); ok != tc.allow {
			t.Errorf("%s: allowed = %v, want %v", tc.name, ok, tc.allow)
		}
	}

	// An unknown credential is refused outright.
	h := &Downloads{Authorize: func(string) (Access, bool) { return Access{}, false }}
	if _, ok := h.allowed(httptest.NewRecorder(), httptest.NewRequest("GET", "/dl/1", nil)); ok {
		t.Error("allowed = true for an invalid token, want false")
	}
}

// photoNamed is a store.Photo with only the field jpegName reads.
func photoNamed(name string) store.Photo { return store.Photo{FileName: name} }

// The share's export settings must reach the renderer untouched: they are the
// difference between a friend getting a 2560px web JPEG and a 60 MB original.
func TestDownloadSpecReachesRender(t *testing.T) {
	want := DownloadSpec{
		LongEdge: 2560, JpegQuality: 88, ColorSpace: "adobergb",
		SharpenTarget: "screen", SharpenAmount: "high",
		ExifMode: "copyright", RemoveLocation: true, WatermarkID: "wm1",
	}
	var got DownloadSpec
	h := &Downloads{
		Authorize: func(string) (Access, bool) {
			return Access{FolderID: 7, Downloads: true, Download: want}, true
		},
		Render: func(_ context.Context, _ int64, spec DownloadSpec) ([]byte, error) {
			got = spec
			return []byte{0xff, 0xd8}, nil
		},
	}
	acc, ok := h.allowed(httptest.NewRecorder(), httptest.NewRequest("GET", "/dl/1?t=x", nil))
	if !ok {
		t.Fatal("allowed = false for a link with downloads enabled")
	}
	if _, err := h.Render(context.Background(), 1, acc.Download); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != want {
		t.Errorf("Render got %+v, want %+v", got, want)
	}
}

// A link minted without a preset carries the zero spec, which is what tells
// the renderer to use its own defaults rather than quality 0.
func TestNoPresetYieldsZeroSpec(t *testing.T) {
	h := &Downloads{Authorize: func(string) (Access, bool) {
		return Access{FolderID: 7, Downloads: true}, true
	}}
	acc, ok := h.allowed(httptest.NewRecorder(), httptest.NewRequest("GET", "/dl/1?t=x", nil))
	if !ok {
		t.Fatal("allowed = false")
	}
	if (acc.Download != DownloadSpec{}) {
		t.Errorf("Download = %+v, want the zero spec", acc.Download)
	}
}
