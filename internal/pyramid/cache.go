// Package pyramid maintains the on-disk preview cache: a mip-map-style set
// of JPEG renditions per photo at fixed long-edge levels, plus a grid of
// full-resolution tiles for 1:1 viewing, keyed by file identity (cache key)
// and edit state (edit hash).
package pyramid

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/sync/errgroup"

	"github.com/marrasen/marraw/internal/decode"
	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/libraw"
	"github.com/marrasen/marraw/internal/store"
)

// Levels are the pyramid long-edge sizes. Full processed resolution is not a
// level: it is served as a grid of TileSize tiles (see EnsureTile), so the
// client never downloads or decodes a whole-sensor JPEG.
var Levels = []string{"256", "512", "1024", "2048"}

func ValidLevel(level string) bool {
	return slices.Contains(Levels, level)
}

// TileSize is the edge length of one full-resolution tile in pixels.
// Must match TILE_SIZE in client/src/lib/backend.ts.
const TileSize = 1024

// TileGrid is the tile-grid size implied by the photo's scanned metadata
// (orientation-corrected, like the rendered image). Returns zeros when the
// dimensions haven't been scanned yet.
func TileGrid(p store.Photo) (cols, rows int) {
	w, h := p.Width, p.Height
	if p.Orientation == 5 || p.Orientation == 6 {
		w, h = h, w
	}
	return (w + TileSize - 1) / TileSize, (h + TileSize - 1) / TileSize
}

type Cache struct {
	dir  string
	pool *decode.Pool
	db   *store.DB
	// OnPhotoChanged, when set, is called after the cache corrects a photo
	// row (dimension healing) so folder subscribers can refresh.
	OnPhotoChanged func(folderID int64)
}

func New(dir string, pool *decode.Pool, db *store.DB) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Cache{dir: dir, pool: pool, db: db}, nil
}

func (c *Cache) Dir() string { return c.dir }

// renderVersion is baked into cache file names; bump it whenever the
// rendering pipeline changes (tone curve, gamma, …) so stale renditions
// regenerate instead of being served. Orphans age out via the janitor.
// Must match RENDER_VERSION in client/src/lib/backend.ts — image URLs are
// cached as immutable, so the version has to appear in the URL too.
const renderVersion = "r5"

// PathFor is the cache file location for one rendition.
func (c *Cache) PathFor(cacheKey, level, editHash string) string {
	return filepath.Join(c.dir, cacheKey[:2],
		fmt.Sprintf("%s_%s_%s_%s.jpg", cacheKey, level, editHash, renderVersion))
}

// PathForTile is the cache file location for one full-resolution tile.
func (c *Cache) PathForTile(cacheKey string, tx, ty int, editHash string) string {
	return filepath.Join(c.dir, cacheKey[:2],
		fmt.Sprintf("%s_t%dx%d_%s_%s.jpg", cacheKey, tx, ty, editHash, renderVersion))
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

// EnsureTile guarantees one full-resolution tile exists on disk and returns
// its path. The whole tile set comes from a single decode: a miss renders
// every tile of the photo (deduplicated through the pool under one job key),
// so neighboring tiles are already on disk when the viewer pans. Returns
// fs.ErrNotExist for coordinates outside the image — checked against the
// metadata grid up front so a stray request can't burn a full decode.
func (c *Cache) EnsureTile(ctx context.Context, photo store.Photo, tx, ty int, editHash string, prio decode.Priority) (string, error) {
	path := c.PathForTile(photo.CacheKey, tx, ty, editHash)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if tx < 0 || ty < 0 {
		return "", fs.ErrNotExist
	}
	if cols, rows := TileGrid(photo); cols > 0 && (tx >= cols || ty >= rows) {
		return "", fs.ErrNotExist
	}
	key := photo.CacheKey + "|full|" + editHash
	err := c.pool.Do(ctx, key, prio, func(proc *libraw.Processor) error {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		return c.generate(proc, photo, "full", editHash)
	})
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		// Rendered image is a hair smaller than the metadata grid promised.
		return "", fs.ErrNotExist
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
	// needs the full-resolution pipeline. Interactive full renders use PPG —
	// roughly half AHD's cost, visually equivalent at loupe zoom; export
	// keeps AHD.
	params := edits.LibrawParams(level != "full")
	if level == "full" {
		params.UserQual = libraw.DemosaicPPG
	}
	img, err := proc.Process(params)
	if err != nil {
		return err
	}
	rgba, err := FromLibraw(img)
	if err != nil {
		return err
	}
	gamma := c.lookGammaFor(proc, photo, edits == nil, rgba)
	if level == "full" {
		c.healDimensions(photo, rgba.Bounds().Dx(), rgba.Bounds().Dy())
		ApplyLook(rgba, gamma)
		if err := c.writeTiles(rgba, photo.CacheKey, editHash); err != nil {
			return err
		}
		return c.WriteLevels(rgba, photo.CacheKey, editHash, 2048, 1024, 512, 256)
	}
	// Downscale before applying the look: 4x fewer pixels, same result at
	// these sizes.
	scaled := scaleToLongEdge(rgba, 2048)
	ApplyLook(scaled, gamma)
	return c.WriteLevels(scaled, photo.CacheKey, editHash, 2048, 1024, 512, 256)
}

// healDimensions repairs stored photo dimensions against a full-resolution
// render — the one moment the true size is in hand. Databases written by
// older builds can hold wrong (half-size) dimensions, which mis-size the
// loupe box and the 1:1 tile grid; the correction is pushed to folder
// subscribers so an open client recovers immediately.
func (c *Cache) healDimensions(photo store.Photo, renderedW, renderedH int) {
	w, h := renderedW, renderedH
	if photo.Orientation == 5 || photo.Orientation == 6 {
		w, h = h, w // stored dimensions are pre-flip
	}
	if c.db == nil || (w == photo.Width && h == photo.Height) {
		return
	}
	if err := c.db.SetDimensions(context.Background(), photo.ID, w, h); err != nil {
		return
	}
	if c.OnPhotoChanged != nil {
		c.OnPhotoChanged(photo.FolderID)
	}
}

// lookGammaFor returns the photo's calibrated tone lift, computing and
// persisting it on the first base render (the only moment we hold both the
// camera's rendering and our own of the same scene). Edited renders reuse
// the stored value so sliders behave deterministically.
func (c *Cache) lookGammaFor(proc *libraw.Processor, photo store.Photo, isBase bool, rendered *image.RGBA) float64 {
	if photo.LookGamma > 0 {
		return photo.LookGamma
	}
	if !isBase {
		return FallbackLookGamma
	}
	thumb, err := proc.EmbeddedThumb()
	if err != nil {
		return FallbackLookGamma
	}
	thumbImg, err := jpeg.Decode(bytes.NewReader(thumb))
	if err != nil {
		return FallbackLookGamma
	}
	cameraRGBA, ok := thumbImg.(*image.RGBA)
	if !ok {
		cameraRGBA = image.NewRGBA(thumbImg.Bounds())
		xdraw.Copy(cameraRGBA, image.Point{}, thumbImg, thumbImg.Bounds(), xdraw.Src, nil)
	}
	gamma := ComputeLookGamma(MeanLuma(rendered), MeanLuma(cameraRGBA))
	if c.db != nil {
		if err := c.db.SetLookGamma(context.Background(), photo.ID, gamma); err == nil {
			return gamma
		}
	}
	return gamma
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
	// Orientation from the open file, not the DB row: on-demand requests can
	// arrive before the metadata backfill has written photo.Orientation, and
	// a thumb cached unrotated stays wrong forever.
	rgba = rotateFlip(rgba, proc.Metadata().Orientation)
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
// bit of resampling quality — and the look applied after the downscale.
// Input must be a freshly RAW-decoded image (no look applied).
func (c *Cache) WritePreview(src *image.RGBA, cacheKey, editHash string, lookGamma float64) error {
	b := src.Bounds()
	long := max(b.Dx(), b.Dy())
	dst := src
	if long > 2048 {
		w, h := b.Dx()*2048/long, b.Dy()*2048/long
		dst = image.NewRGBA(image.Rect(0, 0, max(1, w), max(1, h)))
		xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	}
	ApplyLook(dst, lookGamma)
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

// writeTiles cuts the full-resolution render into TileSize tiles and encodes
// them in parallel — Go's JPEG encoder is single-threaded, so tiling also
// makes the full render markedly faster than one monolithic encode was.
func (c *Cache) writeTiles(src *image.RGBA, cacheKey, editHash string) error {
	b := src.Bounds()
	var g errgroup.Group
	g.SetLimit(runtime.NumCPU())
	for ty := 0; ty*TileSize < b.Dy(); ty++ {
		for tx := 0; tx*TileSize < b.Dx(); tx++ {
			r := image.Rect(
				b.Min.X+tx*TileSize, b.Min.Y+ty*TileSize,
				min(b.Min.X+(tx+1)*TileSize, b.Max.X), min(b.Min.Y+(ty+1)*TileSize, b.Max.Y),
			)
			path := c.PathForTile(cacheKey, tx, ty, editHash)
			g.Go(func() error {
				return writeJPEGFile(path, src.SubImage(r), 90)
			})
		}
	}
	return g.Wait()
}

func (c *Cache) writeJPEG(img *image.RGBA, cacheKey, level, editHash string, quality int) error {
	return writeJPEGFile(c.PathFor(cacheKey, level, editHash), img, quality)
}

func writeJPEGFile(path string, img image.Image, quality int) error {
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
