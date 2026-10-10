package main

import (
	"context"
	"fmt"
	"image"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/marrasen/gunim/paint"

	"github.com/marrasen/marraw/internal/jpegturbo"
	"github.com/marrasen/marraw/internal/marrawclient"
)

// images fetches a photo's renditions from the backend's HTTP endpoints, as
// the React loupe does, and decodes them for gunim.
type images struct {
	base, token string
	http        *http.Client
}

func newImages(base, token string) *images {
	return &images{base: base, token: token, http: &http.Client{}}
}

// want is one request for a rendition: its level, and how the server may
// answer it.
type want struct {
	level string
	// cacheOnly answers from the pre-rendered file or 404s, never decoding
	// a RAW. fast also never decodes: the cached file, a downscale of a
	// 2048, or the camera's embedded JPEG. stale takes the freshest
	// rendition of the level at any edit state.
	cacheOnly, fast, stale bool
}

func (w want) String() string {
	s := w.level
	if w.cacheOnly {
		s += " cached"
	}
	if w.fast {
		s += " fast"
	}
	if w.stale {
		s += " stale"
	}
	return s
}

// got is a decoded rendition, and what it took.
type got struct {
	img           *paint.Image
	w, h          int
	fetch, decode time.Duration
	provisional   bool
}

// url is the content-addressed URL of p's rendition, as client/src/lib/backend.ts
// builds it.
func (im *images) url(p marrawclient.Photo, w want) string {
	q := url.Values{"v": {p.CacheKey}, "r": {renderVersion}}
	if p.EditHash != "" && p.EditHash != "base" {
		q.Set("e", p.EditHash)
	}
	if w.cacheOnly {
		q.Set("cacheOnly", "1")
	}
	if w.stale {
		q.Set("stale", "1")
	}
	if w.fast {
		q.Set("fast", "1")
	}
	q.Set("t", im.token)
	return fmt.Sprintf("%s/img/%d/%s?%s", im.base, p.ID, w.level, q.Encode())
}

// errMissing is a cacheOnly or fast request the server had nothing for.
var errMissing = fmt.Errorf("not rendered yet")

// get fetches and decodes one rendition. Cancelling ctx aborts the request,
// which the server takes as a cancel of any render behind it.
func (im *images) get(ctx context.Context, p marrawclient.Photo, w want) (got, error) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, im.url(p, w), nil)
	if err != nil {
		return got{}, err
	}
	resp, err := im.http.Do(req)
	if err != nil {
		return got{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, resp.Body)
		return got{}, errMissing
	}
	if resp.StatusCode != http.StatusOK {
		return got{}, fmt.Errorf("%s: %s", w, resp.Status)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return got{}, err
	}
	fetched := time.Now()
	m, err := jpegturbo.DecodeRGBA(raw)
	if err != nil {
		return got{}, fmt.Errorf("%s: %w", w, err)
	}
	if err := ctx.Err(); err != nil {
		return got{}, err
	}
	b := m.Bounds()
	img := paint.NewImage(m)
	return got{img: img, w: b.Dx(), h: b.Dy(), fetch: fetched.Sub(start), decode: time.Since(fetched),
		provisional: resp.Header.Get("Cache-Control") == "no-store"}, nil
}

// size is the photo's displayed size at full resolution, as the backend
// renders it: turned, and cropped, as its edit says. The full
// resolution's tiles are laid out by it.
func size(p marrawclient.Photo) image.Point {
	w, h := p.Width, p.Height
	if p.Orientation >= 5 {
		w, h = h, w
	}
	if p.Rotate%2 == 1 {
		w, h = h, w
	}
	if p.CropW > 0 && p.CropH > 0 && w > 0 && h > 0 {
		w, h = max(1, int(math.Round(p.CropW*float64(w)))), max(1, int(math.Round(p.CropH*float64(h))))
	}
	return image.Pt(w, h)
}

// rgba fetches and decodes one rendition, for drawing on rather than
// showing.
func (im *images) rgba(ctx context.Context, p marrawclient.Photo, w want) (*image.RGBA, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, im.url(p, w), nil)
	if err != nil {
		return nil, err
	}
	resp, err := im.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", w, resp.Status)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	m, err := jpegturbo.DecodeRGBA(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", w, err)
	}
	return m, nil
}
