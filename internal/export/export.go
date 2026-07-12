// Package export renders selected photos at full quality and writes them to
// disk, saturating the CPU with one LibRaw handle per worker.
package export

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/sync/errgroup"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/libraw"
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/store"
	"github.com/marrasen/marraw/internal/watermark"
	"github.com/marrasen/marraw/internal/xmp"
)

type Request struct {
	PhotoIDs    []int64
	DestDir     string
	Format      string // "jpeg", "tiff8", "png", or "rawXmp" (copy RAW + .xmp sidecar)
	JpegQuality int    // 0 = 90; jpeg only
	LongEdge    int    // 0 = full resolution
	// ColorSpace selects the output primaries: "srgb" (default),
	// "adobergb", or "prophoto". LibRaw converts during decode; both encoders
	// embed a matching ICC profile (sRGB stays untagged).
	ColorSpace string
	// SharpenTarget/SharpenAmount select output sharpening, applied after the
	// final resize: "off"/"screen"/"matte"/"glossy" and
	// "low"/"standard"/"high" ("" = off / standard).
	SharpenTarget string
	SharpenAmount string
	// FileNameTemplate names the output files (no extension); "" = "{name}".
	// See namer.claim for the tokens.
	FileNameTemplate string
	// ExifMode selects the exported metadata: "all" (or "", the full catalog
	// set), "copyright" (only the credit below), or "none" (no EXIF at all).
	ExifMode string
	// RemoveLocation strips GPS from "all" exports.
	RemoveLocation bool
	// Artist and Copyright are the user's credit, written as EXIF tags
	// 315/33432 when non-empty (modes all and copyright).
	Artist    string
	Copyright string
	// Watermark is composited onto the final pixels after output sharpening;
	// nil = none.
	Watermark *watermark.Spec
}

type Item struct {
	PhotoID  int64
	FileName string // output file name (or source name on failure)
	Err      error
}

// Run exports all requested photos, invoking onItem as each finishes.
// Per-photo failures are reported through onItem and do not abort the batch;
// Run returns an error only for setup problems or cancellation.
func Run(ctx context.Context, db *store.DB, req Request, onItem func(Item)) error {
	if err := os.MkdirAll(req.DestDir, 0o755); err != nil {
		return err
	}
	if req.JpegQuality <= 0 || req.JpegQuality > 100 {
		req.JpegQuality = 90
	}

	// Claim output names up front (sequentially) so concurrent workers
	// can't collide on duplicates, and so {seq} follows the request order.
	names := newNamer(req.DestDir, req.FileNameTemplate, len(req.PhotoIDs))

	g, gctx := errgroup.WithContext(ctx)
	// Export is a deliberate foreground action with its own LibRaw handles
	// (separate from the decode pool), so saturate every thread — the user is
	// waiting on the whole batch.
	g.SetLimit(runtime.NumCPU())
	for _, id := range req.PhotoIDs {
		photo, err := db.GetPhoto(ctx, id)
		if err != nil {
			onItem(Item{PhotoID: id, Err: err})
			continue
		}
		if req.Format == "rawXmp" {
			// Exporting into the photo's own folder writes only the sidecar,
			// next to the original under its own name — and must bypass the
			// namer, whose collision check would see the original RAW itself
			// and "resolve" it to a renamed copy. Case-insensitive compare per
			// the Windows convention elsewhere (junction/subst aliases of the
			// same directory are not detected).
			sameDir := strings.EqualFold(filepath.Clean(req.DestDir), filepath.Clean(photo.FolderPath))
			outName := photo.FileName
			if !sameDir {
				outName = names.claim(photo.FileName, photo.TakenAt, req.Format)
			}
			g.Go(func() error {
				if gctx.Err() != nil {
					return gctx.Err()
				}
				err := exportRawXmp(photo, filepath.Join(req.DestDir, outName), sameDir)
				onItem(Item{PhotoID: photo.ID, FileName: outName, Err: err})
				return nil
			})
			continue
		}
		outName := names.claim(photo.FileName, photo.TakenAt, req.Format)
		g.Go(func() error {
			if gctx.Err() != nil {
				return gctx.Err()
			}
			err := exportOne(gctx, photo, filepath.Join(req.DestDir, outName), req)
			onItem(Item{PhotoID: photo.ID, FileName: outName, Err: err})
			return nil
		})
	}
	return g.Wait()
}

func exportOne(ctx context.Context, photo store.Photo, outPath string, req Request) error {
	proc, err := libraw.New()
	if err != nil {
		return err
	}
	defer proc.Close()
	if err := proc.Open(photo.Path()); err != nil {
		return err
	}

	var params *edit.Params
	if photo.EditParams.Valid {
		if p, err := edit.Parse(photo.EditParams.String); err == nil {
			params = p
		}
	}
	lp := params.LibrawParams(false)
	if params == nil || params.Demosaic == "" {
		lp.UserQual = libraw.DemosaicAHD
	}
	lp.OutputColor = ColorSpaceOutput(req.ColorSpace)

	// The handle is per-file and closed on return, so a cancelled export
	// (recycle-on-cancel) costs nothing extra — it just stops burning a core.
	img, err := proc.Process(ctx, lp)
	if err != nil {
		return err
	}

	gamma := photo.LookGamma
	if gamma == 0 {
		gamma = pyramid.FallbackLookGamma
	}

	rendered, err := renderFinal(img, gamma, params, req)
	if err != nil {
		return err
	}

	tmp := outPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	icc := ICCFor(req.ColorSpace)
	meta := exifFromPhoto(photo, rendered.Bounds().Dx(), rendered.Bounds().Dy(), req.ColorSpace).applyPolicy(req)
	switch req.Format {
	case "tiff8":
		err = encodeTIFF8(f, rendered, icc, meta)
	case "png":
		err = encodePNG(f, rendered, icc, meta)
	default:
		err = encodeJPEG(f, rendered, req.JpegQuality, icc, meta)
	}
	if err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, outPath)
}

// renderFinal turns the freshly decoded RAW into exactly the pixels the user
// saw in the loupe: crop and straighten, the look, detail, then the output
// resize and sharpening. Both encoders share it — a JPEG and a TIFF of the
// same photo differ only in how the pixels are written down.
func renderFinal(img *libraw.Image, lookGamma float64, params *edit.Params, req Request) (*image.RGBA, error) {
	if img.Bits != 8 {
		return nil, fmt.Errorf("export: needs 8-bit output, got %d", img.Bits)
	}
	rgba := image.NewRGBA(image.Rect(0, 0, img.Width, img.Height))
	for i, j := 0, 0; i+2 < len(img.Data); i, j = i+3, j+4 {
		rgba.Pix[j+0] = img.Data[i+0]
		rgba.Pix[j+1] = img.Data[i+1]
		rgba.Pix[j+2] = img.Data[i+2]
		rgba.Pix[j+3] = 0xff
	}
	rgba = pyramid.ApplyGeometry(rgba, params)
	pyramid.ApplyLook(rgba, lookGamma, params)
	pyramid.ApplyDetail(rgba, params)
	out := resizeRGBA(rgba, req.LongEdge)
	pyramid.ApplyOutputSharpen(out, req.SharpenTarget, req.SharpenAmount)
	if req.Watermark != nil {
		if err := watermark.Apply(out, *req.Watermark); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func encodeJPEG(f *os.File, img *image.RGBA, quality int, icc []byte, meta exifMeta) error {
	// Encode to memory, then splice the metadata segments in: ICC first so
	// the EXIF APP1 lands directly after SOI, where readers expect it.
	buf := &bytes.Buffer{}
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return err
	}
	out := buf.Bytes()
	if icc != nil {
		out = embedICCJPEG(out, icc)
	}
	_, err := f.Write(embedExifJPEG(out, meta))
	return err
}

func encodePNG(f *os.File, img *image.RGBA, icc []byte, meta exifMeta) error {
	// Encode to memory, then splice the iCCP and eXIf chunks in after IHDR.
	buf := &bytes.Buffer{}
	if err := png.Encode(buf, img); err != nil {
		return err
	}
	out := buf.Bytes()
	if icc != nil {
		out = embedICCPNG(out, icc)
	}
	_, err := f.Write(embedExifPNG(out, meta))
	return err
}

func resizeRGBA(src *image.RGBA, longEdge int) *image.RGBA {
	b := src.Bounds()
	if longEdge <= 0 || max(b.Dx(), b.Dy()) <= longEdge {
		return src
	}
	w, h := fitLongEdge(b.Dx(), b.Dy(), longEdge)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	return dst
}

func fitLongEdge(w, h, longEdge int) (int, int) {
	if w >= h {
		return longEdge, max(1, h*longEdge/w)
	}
	return max(1, w*longEdge/h), longEdge
}

// namer assigns collision-free output file names from the user's template.
type namer struct {
	destDir  string
	template string
	total    int
	seq      int
	used     map[string]bool
}

func newNamer(destDir, template string, total int) *namer {
	if strings.TrimSpace(template) == "" {
		template = "{name}"
	}
	return &namer{destDir: destDir, template: template, total: total, used: map[string]bool{}}
}

// claim expands the template for one photo and de-duplicates the result.
// Tokens: {name} = source file name without extension, {seq} = position in
// the batch (zero-padded to the batch size, at least 3 digits), {date} and
// {time} = capture stamp as YYYYMMDD / HHMMSS (empty when EXIF carries no
// date). Unknown {tokens} stay literal. A template that expands to nothing
// falls back to the source name, so a file always has a real name.
func (n *namer) claim(srcName string, takenAt int64, format string) string {
	ext := ".jpg"
	switch format {
	case "tiff8":
		ext = ".tif"
	case "png":
		ext = ".png"
	case "rawXmp":
		// The copy keeps the RAW's own extension (and its case).
		ext = filepath.Ext(srcName)
	}
	n.seq++
	src := strings.TrimSuffix(srcName, filepath.Ext(srcName))
	base := sanitizeFileName(expandTemplate(n.template, src, takenAt, n.seq, n.total))
	if base == "" {
		base = src
	}
	name := base + ext
	for i := 2; n.collides(name, format); i++ {
		name = fmt.Sprintf("%s-%d%s", base, i, ext)
	}
	n.used[strings.ToLower(name)] = true
	if format == "rawXmp" {
		n.used[strings.ToLower(xmp.PathFor(name))] = true
	}
	return name
}

// collides reports whether name is already claimed in this batch or present
// on disk. A rawXmp claim also covers its derived .xmp sidecar, so two RAWs
// sharing a basename (IMG1.ARW + IMG1.CR2) don't both write IMG1.xmp.
func (n *namer) collides(name, format string) bool {
	if n.used[strings.ToLower(name)] || exists(filepath.Join(n.destDir, name)) {
		return true
	}
	if format != "rawXmp" {
		return false
	}
	sc := xmp.PathFor(name)
	return n.used[strings.ToLower(sc)] || exists(filepath.Join(n.destDir, sc))
}

func expandTemplate(template, name string, takenAt int64, seq, total int) string {
	width := max(3, len(strconv.Itoa(total)))
	var date, clock string
	if takenAt > 0 {
		t := time.Unix(takenAt, 0)
		date = t.Format("20060102")
		clock = t.Format("150405")
	}
	return strings.NewReplacer(
		"{name}", name,
		"{seq}", fmt.Sprintf("%0*d", width, seq),
		"{date}", date,
		"{time}", clock,
	).Replace(template)
}

// sanitizeFileName replaces the characters Windows forbids in file names
// (which also keeps the expansion from escaping destDir) and trims the
// trailing dots/spaces Explorer refuses.
func sanitizeFileName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '-'
		}
		return r
	}, s)
	return strings.TrimRight(s, ". ")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
