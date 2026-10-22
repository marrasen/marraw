// Package jpegturbo decodes and encodes JPEG with libjpeg-turbo, several
// times faster than image/jpeg on the sizes marraw handles, and straight to
// and from the *image.RGBA the pipeline works in.
//
// The library is linked statically, built by scripts/setup-libjpeg-turbo.*
// into third_party/libjpeg-turbo, as LibRaw is. A build without cgo falls
// back to image/jpeg, as the connect-only test build does.
package jpegturbo

import (
	"image"
	"image/draw"
	"image/jpeg"
	"io"
)

// toRGBA is m as an *image.RGBA, at the origin.
func toRGBA(m image.Image) *image.RGBA {
	if rgba, ok := m.(*image.RGBA); ok && rgba.Rect.Min == (image.Point{}) {
		return rgba
	}
	b := m.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Rect, m, b.Min, draw.Src)
	return rgba
}

// decodeGo is DecodeRGBA through image/jpeg, for what libjpeg-turbo will
// not turn into RGBA, as a CMYK JPEG, and for builds without it.
func decodeGo(r io.Reader) (*image.RGBA, error) {
	m, err := jpeg.Decode(r)
	if err != nil {
		return nil, err
	}
	return toRGBA(m), nil
}

// encodeGo is EncodeRGBA through image/jpeg.
func encodeGo(w io.Writer, img *image.RGBA, quality int) error {
	return jpeg.Encode(w, img, &jpeg.Options{Quality: quality})
}
