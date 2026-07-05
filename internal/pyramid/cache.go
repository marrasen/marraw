// Package pyramid maintains the on-disk preview cache: a mip-map-style set
// of JPEG renditions per photo at fixed long-edge levels, keyed by file
// identity (cache key) and edit state (edit hash).
package pyramid

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strconv"

	xdraw "golang.org/x/image/draw"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/libraw"
	"github.com/marrasen/marraw/internal/store"
)

// Levels are the pyramid long-edge sizes; "full" is full processed resolution.
var Levels = []string{"256", "512", "1024", "2048", "full"}

func ValidLevel(level string) bool {
	for _, l := range Levels {
		if l == level {
			return true
		}
	}
	return false
}

type Cache struct {
	dir  string
	pool *decode.Pool
}

func New(dir string, pool *decode.Pool) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Cache{dir: dir, pool: pool}, nil
}

func (c *Cache) Dir() string { return c.dir }

// PathFor is the cache file location for one rendition.
func (c *Cache) PathFor(cacheKey, level, editHash string) string {
	return filepath.Join(c.dir, cacheKey[:2], fmt.Sprintf("%s_%s_%s.jpg", cacheKey, level, editHash))
}

// Ensure guarantees the rendition exists on disk and returns its path.
// editHash must be edit.BaseHash or the photo's current edit hash.
// Generation is deduplicated and prioritized through the decode pool;
// Ensure blocks until the file is ready or ctx is canceled.
func (c *Cache) Ensure(ctx context.Context, photo store.Photo, level, editHash string, prio decode.Priority) (string, error) {
	path := c.PathFor(photo.CacheKey, level, editHash)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	key := photo.CacheKey + "|" + level + "|" + editHash
	err := c.pool.Do(ctx, key, prio, func(proc *libraw.Processor) error {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		return c.generate(proc, photo, level, editHash)
	})
	if err != nil {
		return "", err
	}
	return path, nil
}

// generate renders the requested level and, opportunistically, the smaller
// levels that fall out of the same decode for free.
func (c *Cache) generate(proc *libraw.Processor, photo store.Photo, level, editHash string) error {
	var edits *edit.Params
	if editHash != edit.BaseHash {
		if photo.EditHash != editHash || !photo.EditParams.Valid {
			return fmt.Errorf("pyramid: photo %d has no edit state %s", photo.ID, editHash)
		}
		e, err := edit.Parse(photo.EditParams.String)
		if err != nil {
			return fmt.Errorf("pyramid: photo %d edit params: %w", photo.ID, err)
		}
		edits = e
	}

	if err := proc.Open(photo.Path()); err != nil {
		return err
	}

	// Fast path: unedited small levels from the embedded JPEG preview.
	if edits == nil && level != "full" {
		if n, _ := strconv.Atoi(level); n <= 1024 {
			if ok, err := c.tryThumbRoute(proc, photo, n, editHash); ok {
				return err
			}
		}
	}

	// RAW route: a half-size decode covers every fixed level; only "full"
	// needs the full-resolution pipeline.
	img, err := proc.Process(edits.LibrawParams(level != "full"))
	if err != nil {
		return err
	}
	rgba, err := FromLibraw(img)
	if err != nil {
		return err
	}
	if level == "full" {
		if err := c.writeJPEG(rgba, photo.CacheKey, "full", editHash, 90); err != nil {
			return err
		}
	}
	return c.WriteLevels(rgba, photo.CacheKey, editHash, 2048, 1024, 512, 256)
}

// tryThumbRoute serves grid-size levels from the embedded JPEG preview when
// it is large enough. Returns ok=false to fall back to a RAW decode.
func (c *Cache) tryThumbRoute(proc *libraw.Processor, photo store.Photo, level int, editHash string) (bool, error) {
	data, err := proc.EmbeddedThumb()
	if err != nil {
		return false, nil
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return false, nil
	}
	b := img.Bounds()
	if max(b.Dx(), b.Dy()) < level {
		return false, nil // too small; decode RAW instead
	}
	rgba, ok := img.(*image.RGBA)
	if !ok {
		rgba = image.NewRGBA(b)
		xdraw.Copy(rgba, image.Point{}, img, b, xdraw.Src, nil)
	}
	rgba = rotateFlip(rgba, photo.Orientation)
	// Write every level the thumb can serve, largest first.
	var levels []int
	for _, l := range []int{1024, 512, 256} {
		if max(b.Dx(), b.Dy()) >= l {
			levels = append(levels, l)
		}
	}
	return true, c.WriteLevels(rgba, photo.CacheKey, editHash, levels...)
}

// WritePreview writes the 2048 rendition on the interactive path: bilinear
// scaling instead of CatmullRom — a slider drag needs latency, not the last
// bit of resampling quality. The committed pipeline re-renders smaller
// levels with the quality scaler later.
func (c *Cache) WritePreview(src *image.RGBA, cacheKey, editHash string) error {
	b := src.Bounds()
	long := max(b.Dx(), b.Dy())
	dst := src
	if long > 2048 {
		w, h := b.Dx()*2048/long, b.Dy()*2048/long
		dst = image.NewRGBA(image.Rect(0, 0, max(1, w), max(1, h)))
		xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	}
	return c.writeJPEG(dst, cacheKey, "2048", editHash, 80)
}

// WriteLevels writes a chain of downscaled renditions from src, skipping
// files that already exist. Levels must be sorted descending.
func (c *Cache) WriteLevels(src *image.RGBA, cacheKey, editHash string, levels ...int) error {
	cur := src
	for _, l := range levels {
		cur = scaleToLongEdge(cur, l)
		q := 80
		if l >= 1024 {
			q = 85
		}
		if err := c.writeJPEG(cur, cacheKey, strconv.Itoa(l), editHash, q); err != nil {
			return err
		}
	}
	return nil
}

func (c *Cache) writeJPEG(img *image.RGBA, cacheKey, level, editHash string, quality int) error {
	path := c.PathFor(cacheKey, level, editHash)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: quality}); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
