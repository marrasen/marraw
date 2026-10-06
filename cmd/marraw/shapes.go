package main

import (
	"image"
	"math"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// PhotoAspect is the shape of the photo at Index, learned from its pixels.
type PhotoAspect struct {
	Index  int
	Aspect float32
}

// aspectOf is photo p's width over its height as it shows: as its pixels
// have it once any have come, which take in its orientation, rotation and
// crop; until then from its size, or 3:2 before that is read, as on a
// library that is new.
func (cu *culler) aspectOf(p marrawclient.Photo) float32 {
	if a, ok := cu.aspects[p.ID]; ok {
		return a
	}
	if s := size(p); s.X > 0 && s.Y > 0 {
		return float32(s.X) / float32(s.Y)
	}
	return 1.5
}

// fullOf is photo p's full resolution as it shows, for its tiles: its size
// where that agrees with its shape, or as large in the shape of its
// pixels, or nothing yet, for the window to guess.
func (cu *culler) fullOf(p marrawclient.Photo) image.Point {
	s, a := size(p), cu.aspectOf(p)
	if s.X <= 0 || s.Y <= 0 {
		return image.Point{}
	}
	if math.Abs(float64(float32(s.X)/float32(s.Y)-a)) < 0.02*float64(a) {
		return s
	}
	long := max(s.X, s.Y)
	if a >= 1 {
		return image.Pt(long, int(float32(long)/a+0.5))
	}
	return image.Pt(int(float32(long)*a+0.5), long)
}

// learnShape takes the shape of pixels w by h of photo id, and tells the
// grid and the cull view where it changes what they show.
func (cu *culler) learnShape(id int64, w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	a := float32(w) / float32(h)
	i, ok := cu.index[id]
	if !ok {
		return
	}
	if old := cu.aspectOf(cu.photos[i]); math.Abs(float64(old-a)) < 0.01*float64(a) {
		if _, known := cu.aspects[id]; known {
			return
		}
		cu.aspects[id] = a
		return
	}
	cu.aspects[id] = a
	_ = cu.c.Patch("grid", PhotoAspect{Index: i, Aspect: a})
	if cu.culling && i >= cu.at-stripReach && i <= cu.at+stripReach {
		cu.showCull()
	}
}
