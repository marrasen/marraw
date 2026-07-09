// Package export renders selected photos at full quality and writes them to
// disk, saturating the CPU with one LibRaw handle per worker.
package export

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/sync/errgroup"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/libraw"
	"github.com/marrasen/marraw/internal/pyramid"
	"github.com/marrasen/marraw/internal/store"
)

type Request struct {
	PhotoIDs    []int64
	DestDir     string
	Format      string // "jpeg" or "tiff8"
	JpegQuality int    // 0 = 90; ignored by tiff8
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
	// can't collide on duplicates.
	names := newNamer(req.DestDir)

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
		outName := names.claim(photo.FileName, req.Format)
		g.Go(func() error {
			if gctx.Err() != nil {
				return gctx.Err()
			}
			err := exportOne(photo, filepath.Join(req.DestDir, outName), req)
			onItem(Item{PhotoID: photo.ID, FileName: outName, Err: err})
			return nil
		})
	}
	return g.Wait()
}

func exportOne(photo store.Photo, outPath string, req Request) error {
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

	img, err := proc.Process(lp)
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
	switch req.Format {
	case "tiff8":
		err = encodeTIFF8(f, rendered, icc)
	default:
		err = encodeJPEG(f, rendered, req.JpegQuality, icc)
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
	return out, nil
}

func encodeJPEG(f *os.File, img *image.RGBA, quality int, icc []byte) error {
	if icc == nil {
		return jpeg.Encode(f, img, &jpeg.Options{Quality: quality})
	}
	// Wide gamut: encode to memory, then splice the ICC profile in.
	buf := &bytes.Buffer{}
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return err
	}
	_, err := f.Write(embedICCJPEG(buf.Bytes(), icc))
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

// namer assigns collision-free output file names.
type namer struct {
	destDir string
	used    map[string]bool
}

func newNamer(destDir string) *namer {
	return &namer{destDir: destDir, used: map[string]bool{}}
}

func (n *namer) claim(srcName, format string) string {
	ext := ".jpg"
	if format == "tiff8" {
		ext = ".tif"
	}
	base := strings.TrimSuffix(srcName, filepath.Ext(srcName))
	name := base + ext
	for i := 2; n.used[strings.ToLower(name)] || exists(filepath.Join(n.destDir, name)); i++ {
		name = fmt.Sprintf("%s-%d%s", base, i, ext)
	}
	n.used[strings.ToLower(name)] = true
	return name
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
