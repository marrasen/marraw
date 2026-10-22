//go:build !cgo

package jpegturbo

import (
	"bytes"
	"image"
	"io"
)

// DecodeRGBA decodes the JPEG in data to an *image.RGBA, with image/jpeg:
// this build has no cgo.
func DecodeRGBA(data []byte) (*image.RGBA, error) { return decodeGo(bytes.NewReader(data)) }

// Decode reads a JPEG from r, decoded to an *image.RGBA.
func Decode(r io.Reader) (*image.RGBA, error) { return decodeGo(r) }

// EncodeRGBA writes img to w as a JPEG, with image/jpeg.
func EncodeRGBA(w io.Writer, img *image.RGBA, quality int) error { return encodeGo(w, img, quality) }
