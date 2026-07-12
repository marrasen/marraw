package watermark

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // decode watermark assets
	_ "image/png"
	"math"
	"os"

	xdraw "golang.org/x/image/draw"
)

// drawImage composites one image element, scaled so its height is SizePct of
// the short edge (symmetric with text, whose size is also a height).
func drawImage(dst *image.RGBA, el Element, shortEdge int) error {
	if el.AssetPath == "" {
		return nil
	}
	src, err := decodeAsset(el.AssetPath)
	if err != nil {
		return err
	}
	sb := src.Bounds()
	if sb.Dx() <= 0 || sb.Dy() <= 0 {
		return nil
	}
	h := sizePx(el.SizePct, shortEdge)
	w := max(1, int(math.Round(float64(h)*float64(sb.Dx())/float64(sb.Dy()))))

	// Scale in premultiplied space (interpolating straight alpha would bleed
	// the colors of transparent pixels).
	scaled := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), src, sb, xdraw.Src, nil)

	origin := anchorOrigin(dst.Bounds(), w, h, el.Anchor, sizePx(el.MarginPct, shortEdge))
	rect := image.Rectangle{Min: origin, Max: origin.Add(image.Point{X: w, Y: h})}
	mask := image.NewUniform(color.Alpha{A: alpha8(el.Opacity)})
	draw.DrawMask(dst, rect, scaled, image.Point{}, mask, image.Point{}, draw.Over)
	return nil
}

func decodeAsset(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("watermark: open asset: %w", err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("watermark: decode asset %s: %w", path, err)
	}
	return img, nil
}
