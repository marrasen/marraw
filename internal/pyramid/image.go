package pyramid

import (
	"fmt"
	"image"
	"runtime"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/sync/errgroup"

	"github.com/marrasen/marraw/internal/libraw"
)

// FromLibraw converts an 8-bit interleaved RGB LibRaw image to *image.RGBA.
func FromLibraw(img *libraw.Image) (*image.RGBA, error) {
	if img.Bits != 8 || img.Channels != 3 {
		return nil, fmt.Errorf("pyramid: unsupported image %d bits %d channels", img.Bits, img.Channels)
	}
	dst := image.NewRGBA(image.Rect(0, 0, img.Width, img.Height))
	src := img.Data
	for i, j := 0, 0; i < len(src); i, j = i+3, j+4 {
		dst.Pix[j+0] = src[i+0]
		dst.Pix[j+1] = src[i+1]
		dst.Pix[j+2] = src[i+2]
		dst.Pix[j+3] = 0xff
	}
	return dst, nil
}

// scaleToLongEdge resizes so the longer side equals longEdge (never upscales).
func scaleToLongEdge(src *image.RGBA, longEdge int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	long := max(w, h)
	if long <= longEdge {
		return src
	}
	var nw, nh int
	if w >= h {
		nw = longEdge
		nh = max(1, h*longEdge/w)
	} else {
		nh = longEdge
		nw = max(1, w*longEdge/h)
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	// CatmullRom reads every source pixel on one core, and its kernel widens
	// with the shrink: 42 MP to 2048 took over a second. Halving first, on
	// every core, leaves it at most a 2× shrink of a quarter of the pixels,
	// and looks the same after the final pass.
	for long > longEdge*2 {
		src = halve(src)
		b = src.Bounds()
		long = max(b.Dx(), b.Dy())
	}
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	return dst
}

// halve shrinks src to half its size, each pixel the rounded mean of a 2×2
// block (an odd last row or column is dropped).
func halve(src *image.RGBA) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx()/2, b.Dy()/2
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	bands(h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			top := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+2*y):]
			bot := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+2*y+1):]
			out := dst.Pix[y*dst.Stride : y*dst.Stride+w*4]
			for i := range out {
				j := i/4*8 + i%4
				out[i] = uint8((uint16(top[j]) + uint16(top[j+4]) + uint16(bot[j]) + uint16(bot[j+4]) + 2) / 4)
			}
		}
	})
	return dst
}

// rotateFlip applies a LibRaw/EXIF flip code: 0 none, 3=180°, 5=90° CCW, 6=90° CW.
func rotateFlip(src *image.RGBA, flip int) *image.RGBA {
	if flip == 0 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	var dst *image.RGBA
	switch flip {
	case 3:
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
		bands(h, func(y0, y1 int) {
			for y := y0; y < y1; y++ {
				for x := range w {
					dst.SetRGBA(w-1-x, h-1-y, src.RGBAAt(x, y))
				}
			}
		})
	case 5: // 90 CCW
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
		bands(h, func(y0, y1 int) {
			for y := y0; y < y1; y++ {
				for x := range w {
					dst.SetRGBA(y, w-1-x, src.RGBAAt(x, y))
				}
			}
		})
	case 6: // 90 CW
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
		bands(h, func(y0, y1 int) {
			for y := y0; y < y1; y++ {
				for x := range w {
					dst.SetRGBA(h-1-y, x, src.RGBAAt(x, y))
				}
			}
		})
	default:
		return src
	}
	return dst
}

// bands runs fn over disjoint bands of rows concurrently (or of columns, for
// a stage whose running sums walk down the image). Every stage that bands
// writes only the rows it was handed and reads nothing another band writes,
// so the result is the same pixels a single pass gives.
func bands(h int, fn func(y0, y1 int)) {
	const band = 64
	if h <= band {
		fn(0, h)
		return
	}
	var g errgroup.Group
	g.SetLimit(runtime.NumCPU())
	for y0 := 0; y0 < h; y0 += band {
		y1 := min(y0+band, h)
		g.Go(func() error { fn(y0, y1); return nil })
	}
	_ = g.Wait()
}

// pixBands is bands for the per-pixel stages: fn gets whole rows of img.Pix,
// and loops over them as it would over all of img.Pix.
func pixBands(img *image.RGBA, fn func(pix []uint8)) {
	bands(img.Bounds().Dy(), func(y0, y1 int) {
		fn(img.Pix[y0*img.Stride : min(y1*img.Stride, len(img.Pix))])
	})
}
