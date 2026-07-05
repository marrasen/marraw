// Package export renders selected photos at full quality and writes them to
// disk, saturating the CPU with one LibRaw handle per worker.
package export

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/tiff"
	"golang.org/x/sync/errgroup"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/libraw"
	"github.com/marrasen/marraw/internal/store"
)

type Request struct {
	PhotoIDs    []int64
	DestDir     string
	Format      string // "jpeg" or "tiff16"
	JpegQuality int    // 0 = 90
	LongEdge    int    // 0 = full resolution
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
	quality := req.JpegQuality
	if quality <= 0 || quality > 100 {
		quality = 90
	}

	// Claim output names up front (sequentially) so concurrent workers
	// can't collide on duplicates.
	names := newNamer(req.DestDir)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(min(runtime.NumCPU(), 8))
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
			err := exportOne(photo, filepath.Join(req.DestDir, outName), req.Format, quality, req.LongEdge)
			onItem(Item{PhotoID: photo.ID, FileName: outName, Err: err})
			return nil
		})
	}
	return g.Wait()
}

func exportOne(photo store.Photo, outPath, format string, quality, longEdge int) error {
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
	lp.UserQual = libraw.DemosaicAHD
	if format == "tiff16" {
		lp.OutputBPS = 16
	}

	img, err := proc.Process(lp)
	if err != nil {
		return err
	}

	tmp := outPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	switch format {
	case "tiff16":
		err = encodeTIFF16(f, img, longEdge)
	default:
		err = encodeJPEG(f, img, quality, longEdge)
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

func encodeJPEG(f *os.File, img *libraw.Image, quality, longEdge int) error {
	if img.Bits != 8 {
		return fmt.Errorf("export: jpeg needs 8-bit output, got %d", img.Bits)
	}
	rgba := image.NewRGBA(image.Rect(0, 0, img.Width, img.Height))
	for i, j := 0, 0; i < len(img.Data); i, j = i+3, j+4 {
		rgba.Pix[j+0] = img.Data[i+0]
		rgba.Pix[j+1] = img.Data[i+1]
		rgba.Pix[j+2] = img.Data[i+2]
		rgba.Pix[j+3] = 0xff
	}
	out := resizeRGBA(rgba, longEdge)
	return jpeg.Encode(f, out, &jpeg.Options{Quality: quality})
}

func encodeTIFF16(f *os.File, img *libraw.Image, longEdge int) error {
	if img.Bits != 16 {
		return fmt.Errorf("export: tiff16 needs 16-bit output, got %d", img.Bits)
	}
	rgba := image.NewRGBA64(image.Rect(0, 0, img.Width, img.Height))
	// LibRaw 16-bit output is host-endian (little on Windows); RGBA64 wants
	// big-endian pixel bytes.
	for i, j := 0, 0; i+5 < len(img.Data); i, j = i+6, j+8 {
		r := binary.LittleEndian.Uint16(img.Data[i:])
		g := binary.LittleEndian.Uint16(img.Data[i+2:])
		b := binary.LittleEndian.Uint16(img.Data[i+4:])
		binary.BigEndian.PutUint16(rgba.Pix[j:], r)
		binary.BigEndian.PutUint16(rgba.Pix[j+2:], g)
		binary.BigEndian.PutUint16(rgba.Pix[j+4:], b)
		binary.BigEndian.PutUint16(rgba.Pix[j+6:], 0xffff)
	}
	var out image.Image = rgba
	if longEdge > 0 && max(img.Width, img.Height) > longEdge {
		w, h := fitLongEdge(img.Width, img.Height, longEdge)
		dst := image.NewRGBA64(image.Rect(0, 0, w, h))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), rgba, rgba.Bounds(), xdraw.Src, nil)
		out = dst
	}
	return tiff.Encode(f, out, &tiff.Options{Compression: tiff.Deflate, Predictor: true})
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
	if format == "tiff16" {
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
