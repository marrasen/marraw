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

// A crafted id list must not quietly return the part of itself that happened
// to be in scope, but an id that raced with the owner deleting a frame is not
// the visitor's fault and must not fail the whole download.
func TestDownloadScopingRefusesForeignIDsAndSkipsVanishedOnes(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	ids := func(path string, names ...string) []int64 {
		folderID, err := db.UpsertFolder(ctx, path)
		if err != nil {
			t.Fatalf("UpsertFolder: %v", err)
		}
		files := make([]store.FileEntry, len(names))
		for i, n := range names {
			files[i] = store.FileEntry{Name: n, Size: 1, MtimeNs: 1}
		}
		if _, err := db.SyncFolder(ctx, folderID, path, files); err != nil {
			t.Fatalf("SyncFolder: %v", err)
		}
		photos, err := db.ListPhotos(ctx, folderID)
		if err != nil {
			t.Fatalf("ListPhotos: %v", err)
		}
		out := make([]int64, len(photos))
		for i, p := range photos {
			out[i] = p.ID
		}
		return out
	}

	shared := ids("/photos/band-shoot", "a.arw", "b.arw")
	private := ids("/photos/wedding", "secret.arw")
	scope, err := db.UpsertFolder(ctx, "/photos/band-shoot")
	if err != nil {
		t.Fatalf("UpsertFolder: %v", err)
	}
	h := &Downloads{DB: db}
	acc := Access{FolderID: scope, Downloads: true}

	got, err := h.photosFor(ctx, acc, shared)
	if err != nil || len(got) != 2 {
		t.Errorf("photosFor(own folder) = %d photos, %v; want 2, nil", len(got), err)
	}
	// The interesting case: one real id from the shared folder alongside one
	// from outside it. Returning the first and dropping the second would make
	// a crafted list a partially-successful request.
	if _, err := h.photosFor(ctx, acc, []int64{shared[0], private[0]}); err == nil {
		t.Error("photosFor(shared + another folder's photo) = nil error, want refusal")
	}
	if _, err := h.photosFor(ctx, acc, private); err == nil {
		t.Error("photosFor(another folder) = nil error, want refusal")
	}
	// A vanished row is a race, not an attack.
	got, err = h.photosFor(ctx, acc, []int64{shared[0], 999999})
	if err != nil || len(got) != 1 || got[0].ID != shared[0] {
		t.Errorf("photosFor(shared + deleted) = %d photos, %v; want just the live one", len(got), err)
	}
	if _, err := h.photosFor(ctx, acc, []int64{999999}); err == nil {
		t.Error("photosFor(nothing that resolves) = nil error, want refusal")
	}
	// The owner names whatever they like.
	if got, err := h.photosFor(ctx, Access{}, append(append([]int64{}, shared...), private...)); err != nil || len(got) != 3 {
		t.Errorf("photosFor(owner) = %d photos, %v; want 3, nil", len(got), err)
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
