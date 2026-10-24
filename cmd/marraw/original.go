package main

import (
	"fmt"
	"image"
	"math"
	"runtime"
	"sync"

	"github.com/marrasen/gunim/paint"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// ShowOriginal shows the photo as it was before any edit while On, as
// holding Backspace does in marraw.
type ShowOriginal struct{ On bool }

// showOriginal shows the photo showing as it was before any edit, or its
// edit again. The original keeps the edit's crop, turns, mirror and
// straighten, so it lands where the edit does and only the look changes.
// Its pixels are fetched once and kept for the photo and its frame.
func (cu *culler) showOriginal(on bool) {
	if on == cu.original || !cu.culling {
		return
	}
	cu.original, cu.swapNow = on, true
	cu.showCull()
	if !on {
		return
	}
	p := cu.photos[cu.at]
	// The edit on screen, while the panel has it: a crop not yet saved
	// counts. While cropping the whole frame shows, so the original does.
	var params *marrawclient.Params
	if d := &cu.dev; d.open && d.id == p.ID {
		pp := cu.shownParams()
		params = &pp
	}
	if cu.crop.on {
		params = &marrawclient.Params{}
	}
	if params != nil && cu.origImg != nil && cu.origID == p.ID && cu.origGeom == geomKey(*params) {
		return
	}
	// The original is the photo with no edit: its base rendition.
	base := p
	base.EditHash = ""
	go func() {
		g, err := cu.im.get(cu.ctx, base, want{level: target, cacheOnly: true})
		if err != nil {
			g, err = cu.im.get(cu.ctx, base, want{level: target, fast: true})
		}
		if err == nil && params == nil {
			params, err = cu.api.Edits.GetEditParams(cu.ctx, p.ID)
		}
		var key string
		if err == nil {
			if params == nil {
				params = &marrawclient.Params{}
			}
			key = geomKey(*params)
			if m := withGeometry(g.rgba, *params); m != g.rgba {
				g.img = paint.NewImage(m)
			}
		}
		select {
		case cu.do <- func() {
			if err != nil {
				return
			}
			cu.origID, cu.origGeom, cu.origImg = p.ID, key, g.img
			if cu.original && cu.culling && cu.photos[cu.at].ID == p.ID {
				cu.swapNow = true
				cu.showCull()
			}
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// originalShowing is the original's pixels to show for photo id, or nil.
func (cu *culler) originalShowing(id int64) *paint.Image {
	if cu.original && cu.origID == id {
		return cu.origImg
	}
	return nil
}

// geomKey names p's crop, turns, mirror and straighten.
func geomKey(p marrawclient.Params) string {
	r := rectOf(p)
	return fmt.Sprintf("%d|%v|%v|%v|%v|%v|%v", turns(p), p.FlipH, r.X, r.Y, r.W, r.H, p.CropAngle)
}

// withGeometry is src with p's quarter turns, mirror, crop and
// straighten, as the backend's ApplyGeometry lays them on a render; src
// itself when p has none. src is only read.
func withGeometry(src *image.RGBA, p marrawclient.Params) *image.RGBA {
	if src == nil {
		return nil
	}
	origW, origH := src.Bounds().Dx(), src.Bounds().Dy()
	img := turnRGBA(src, turns(p))
	if p.FlipH {
		img = flipRGBA(img)
	}
	hasCrop := p.CropW > 0 && p.CropH > 0
	if !hasCrop && p.CropAngle == 0 {
		return img
	}
	fullW, fullH := img.Bounds().Dx(), img.Bounds().Dy()
	var cx0, cy0 float64
	outW, outH := fullW, fullH
	if hasCrop {
		cx0, cy0 = p.CropX*float64(fullW), p.CropY*float64(fullH)
		w, h := origW, origH
		if turns(p)%2 != 0 {
			w, h = h, w
		}
		outW = max(1, int(math.Round(p.CropW*float64(w))))
		outH = max(1, int(math.Round(p.CropH*float64(h))))
	}
	if p.CropAngle == 0 {
		// A plain crop is a clamped copy of part of the frame.
		x0 := min(max(int(math.Round(cx0)), 0), fullW-1)
		y0 := min(max(int(math.Round(cy0)), 0), fullH-1)
		outW = min(max(x0+outW, x0+1), fullW) - x0
		outH = min(max(y0+outH, y0+1), fullH) - y0
		dst := image.NewRGBA(image.Rect(0, 0, outW, outH))
		for y := range outH {
			so := img.PixOffset(x0, y0+y)
			copy(dst.Pix[y*dst.Stride:y*dst.Stride+outW*4], img.Pix[so:so+outW*4])
		}
		return dst
	}
	// Each output pixel's point in the straightened frame, turned back
	// about the frame's middle onto the frame, and sampled there.
	rad := p.CropAngle * math.Pi / 180
	cos, sin := math.Cos(-rad), math.Sin(-rad)
	fcx, fcy := float64(fullW)/2, float64(fullH)/2
	dst := image.NewRGBA(image.Rect(0, 0, outW, outH))
	rows(outH, func(y0, y1 int) {
		for ly := y0; ly < y1; ly++ {
			fy := cy0 + float64(ly) + 0.5 - fcy
			for lx := range outW {
				fx := cx0 + float64(lx) + 0.5 - fcx
				sx := fcx + fx*cos - fy*sin - 0.5
				sy := fcy + fx*sin + fy*cos - 0.5
				o := dst.PixOffset(lx, ly)
				bilinear(img, sx, sy, dst.Pix[o:o+4])
			}
		}
	})
	return dst
}

// turnRGBA turns src by n quarter turns clockwise.
func turnRGBA(src *image.RGBA, n int) *image.RGBA {
	if n == 0 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if n%2 != 0 {
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	}
	rows(h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := range w {
				c := src.RGBAAt(b.Min.X+x, b.Min.Y+y)
				switch n {
				case 1:
					dst.SetRGBA(h-1-y, x, c)
				case 2:
					dst.SetRGBA(w-1-x, h-1-y, c)
				case 3:
					dst.SetRGBA(y, w-1-x, c)
				}
			}
		}
	})
	return dst
}

// flipRGBA mirrors src left to right.
func flipRGBA(src *image.RGBA) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	rows(h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := range w {
				dst.SetRGBA(w-1-x, y, src.RGBAAt(b.Min.X+x, b.Min.Y+y))
			}
		}
	})
	return dst
}

// bilinear reads src at x, y into out, black where a tap falls outside,
// as the backend leaves a straightened frame's corners.
func bilinear(src *image.RGBA, x, y float64, out []uint8) {
	b := src.Bounds()
	x0f, y0f := math.Floor(x), math.Floor(y)
	x0, y0 := int(x0f), int(y0f)
	fx, fy := x-x0f, y-y0f
	var acc [4]float64
	for _, t := range [4]struct {
		px, py int
		w      float64
	}{
		{x0, y0, (1 - fx) * (1 - fy)},
		{x0 + 1, y0, fx * (1 - fy)},
		{x0, y0 + 1, (1 - fx) * fy},
		{x0 + 1, y0 + 1, fx * fy},
	} {
		if t.px < b.Min.X || t.px >= b.Max.X || t.py < b.Min.Y || t.py >= b.Max.Y {
			continue
		}
		o := src.PixOffset(t.px, t.py)
		for c := range acc {
			acc[c] += float64(src.Pix[o+c]) * t.w
		}
	}
	for c := range out {
		out[c] = uint8(acc[c] + 0.5)
	}
}

// rows runs fn over bands of n rows, on every core.
func rows(n int, fn func(y0, y1 int)) {
	k := min(runtime.GOMAXPROCS(0), max(1, n/32))
	var wg sync.WaitGroup
	for i := range k {
		wg.Go(func() { fn(n*i/k, n*(i+1)/k) })
	}
	wg.Wait()
}
