//go:build !nobackend

package main

import (
	"bytes"
	"image"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/marrawclient"
	"github.com/marrasen/marraw/internal/pyramid"
)

// The original lands where the edit does: its crop, turns, mirror and
// straighten are the backend's, pixel for pixel.
func TestOriginalGeometryMatchesTheBackend(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 301, 200))
	for i := range src.Pix {
		src.Pix[i] = uint8(i*7 + i/1203)
	}
	for _, c := range []marrawclient.Params{
		{},
		{Rotate: 1},
		{Rotate: 2, FlipH: true},
		{Rotate: 3, CropX: 0.1, CropY: 0.2, CropW: 0.5, CropH: 0.6},
		{CropX: 0.25, CropY: 0.1, CropW: 0.5, CropH: 0.7},
		{CropAngle: 4.5},
		{Rotate: 1, FlipH: true, CropX: 0.12, CropY: 0.08, CropW: 0.7, CropH: 0.75, CropAngle: -3.2},
	} {
		e := &edit.Params{Rotate: c.Rotate, FlipH: c.FlipH, CropX: c.CropX, CropY: c.CropY, CropW: c.CropW, CropH: c.CropH, CropAngle: c.CropAngle}
		want := pyramid.ApplyGeometry(src, e)
		got := withGeometry(src, c)
		if got.Bounds().Size() != want.Bounds().Size() {
			t.Errorf("%+v: %v, the backend %v", c, got.Bounds().Size(), want.Bounds().Size())
			continue
		}
		for y := range want.Bounds().Dy() {
			w := want.Pix[want.PixOffset(0, y) : want.PixOffset(0, y)+want.Bounds().Dx()*4]
			g := got.Pix[got.PixOffset(0, y) : got.PixOffset(0, y)+got.Bounds().Dx()*4]
			if !bytes.Equal(w, g) {
				t.Errorf("%+v: row %d differs from the backend's", c, y)
				break
			}
		}
	}
}
