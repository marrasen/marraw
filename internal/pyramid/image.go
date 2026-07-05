package pyramid

import (
	"fmt"
	"image"

	xdraw "golang.org/x/image/draw"

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
	// CatmullRom below ~4x shrink; for bigger jumps ApproxBiLinear first is
	// dramatically faster and visually identical after the final pass.
	if long > longEdge*4 {
		mid := image.NewRGBA(image.Rect(0, 0, nw*2, nh*2))
		xdraw.ApproxBiLinear.Scale(mid, mid.Bounds(), src, b, xdraw.Src, nil)
		src, b = mid, mid.Bounds()
	}
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
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
		for y := range h {
			for x := range w {
				dst.SetRGBA(w-1-x, h-1-y, src.RGBAAt(x, y))
			}
		}
	case 5: // 90 CCW
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
		for y := range h {
			for x := range w {
				dst.SetRGBA(y, w-1-x, src.RGBAAt(x, y))
			}
		}
	case 6: // 90 CW
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
		for y := range h {
			for x := range w {
				dst.SetRGBA(h-1-y, x, src.RGBAAt(x, y))
			}
		}
	default:
		return src
	}
	return dst
}
