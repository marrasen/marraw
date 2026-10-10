package main

import (
	"context"
	"image"
	"time"

	"github.com/marrasen/marraw/internal/marrawclient"
)

// The crop's vocabulary.
type (
	// ToggleCrop starts cropping the photo showing, or ends it, keeping
	// the crop, as R does in marraw; CropDone ends it, as Enter, Escape
	// and the bar's Done do.
	ToggleCrop struct{}
	CropDone   struct{}
	// CropSet makes Rect the crop, as a drag of it ends.
	CropSet struct{ Rect cropRect }
	// CropAngle straightens the photo by Angle degrees, kept as Commit
	// says, as the bar's slider moves and is let go.
	CropAngle struct {
		Angle  float64
		Commit bool
	}
	// CropTurn turns the photo a quarter, clockwise with CW, and CropFlip
	// mirrors it across, or upside down with Vertical.
	CropTurn struct{ CW bool }
	CropFlip struct{ Vertical bool }
	// CropAspect locks the crop's shape to the bar's choice at Index.
	CropAspect struct{ Index int }
	// CropReset takes the crop, the straighten, the turns and the mirror
	// off.
	CropReset struct{}
	// CropAuto crops around the photo's subject, to the shape chosen, as
	// marraw's Auto does; Download allows its model to be fetched.
	CropAuto struct{ Download bool }
)

// CropView is what the cull view shows of a crop under way: the crop of
// the whole frame, its straighten in degrees, the shape it is locked to,
// as an index of aspectChoices and in fractions of the frame, nought for
// free, and the frame's full size, for the crop's size in pixels. Ready
// says the whole frame's pixels have come.
type CropView struct {
	Rect   cropRect
	Angle  float64
	Aspect int
	Ratio  float64
	Frame  image.Point
	Ready  bool
	// Turn is the quarter turns, clockwise, the picture showing is behind
	// the frame, turned since: the view turns it on, while the pixels of
	// the turned frame come.
	Turn int
	// AutoBusy says Auto is looking for the subject.
	AutoBusy bool
	// MirrorH and MirrorV say the picture showing is mirrored behind the
	// frame, across and upside down: the view mirrors it, while the
	// mirrored frame's pixels come.
	MirrorH, MirrorV bool
}

// cropMode is cropping, while it goes on: the shape chosen, and whether
// the whole frame's pixels have come, at frame large.
type cropMode struct {
	on     bool
	aspect int
	ready  bool
	frame  image.Point
	// turn is the quarter turns, clockwise, the picture showing is
	// behind the frame, and mirrorH and mirrorV the mirrors.
	turn             int
	mirrorH, mirrorV bool
	// autoBusy says Auto is looking for the subject.
	autoBusy bool
}

// frameSize is photo p's whole frame, at full size, as the edit's quarter
// turns leave it.
func frameSize(p marrawclient.Photo, params marrawclient.Params) image.Point {
	w, h := p.Width, p.Height
	if p.Orientation == 5 || p.Orientation == 6 {
		w, h = h, w
	}
	if turns(params)%2 == 1 {
		w, h = h, w
	}
	return image.Pt(w, h)
}

// cropView is the crop as the cull view shows it, or nil while not
// cropping.
func (cu *culler) cropView() *CropView {
	if !cu.crop.on {
		return nil
	}
	d := &cu.dev
	v := &CropView{Rect: rectOf(d.params), Angle: d.params.CropAngle, Aspect: cu.crop.aspect, Ready: cu.crop.ready,
		Turn: cu.crop.turn, MirrorH: cu.crop.mirrorH, MirrorV: cu.crop.mirrorV, AutoBusy: cu.crop.autoBusy}
	if i, ok := cu.index[d.id]; ok {
		v.Frame = frameSize(cu.photos[i], d.params)
	}
	v.Ratio = ratioFrac(aspectChoices[cu.crop.aspect].ratio, cu.frameAspect())
	return v
}

// frameAspect is the whole frame's width over its height, as cropping
// shows it.
func (cu *culler) frameAspect() float64 {
	d := &cu.dev
	if f := cu.crop.frame; f.X > 0 && f.Y > 0 {
		if cu.crop.turn%2 != 0 {
			return float64(f.Y) / float64(f.X)
		}
		return float64(f.X) / float64(f.Y)
	}
	if i, ok := cu.index[d.id]; ok {
		if f := frameSize(cu.photos[i], d.params); f.X > 0 && f.Y > 0 {
			return float64(f.X) / float64(f.Y)
		}
	}
	return 1.5
}

// toggleCrop starts cropping the photo showing, in the develop panel, or
// ends it.
func (cu *culler) toggleCrop() {
	if cu.crop.on {
		cu.cropDone()
		return
	}
	d := &cu.dev
	if !cu.culling || !d.open || !d.mounted || d.id != cu.photos[cu.at].ID {
		cu.tell("Open the develop panel (D) to crop")
		return
	}
	cu.wbFinish(true)
	cu.disarmPick()
	cu.crop = cropMode{on: true}
	// The whole frame's pixels, with the crop off; the crop shows once
	// they come.
	d.edits++
	cu.stopTiles()
	cu.preview(true)
	cu.showCull()
}

// cropDone ends cropping, keeping the crop: the photo renders cropped.
func (cu *culler) cropDone() {
	if !cu.crop.on {
		return
	}
	cu.crop = cropMode{}
	cu.dev.live = nil
	// The photo's size as cropped, at once, for its tiles to be laid out
	// by before the backend's word on it comes.
	d := &cu.dev
	for _, p := range []*marrawclient.Photo{cu.listedPhoto(d.id, true), cu.listedPhoto(d.id, false)} {
		if p != nil {
			p.Rotate, p.CropW, p.CropH = turns(d.params), d.params.CropW, d.params.CropH
		}
	}
	cu.edited(true)
	cu.showCull()
}

// cropKeep saves the edit as it is, as a step of its history, with no new
// pixels: cropping, the frame shows whole as it was.
func (cu *culler) cropKeep(label string) {
	d := &cu.dev
	d.edits++
	d.committed = d.edits
	if d.saved == nil {
		d.saved = map[int64]bool{}
	}
	d.saved[d.id] = true
	cu.save(d.id, d.params)
	cu.remember(label)
	_ = cu.c.Update("develop", cu.developState())
	cu.showCull()
}

// cropSet takes a crop dragged.
func (cu *culler) cropSet(r cropRect) {
	if !cu.crop.on {
		return
	}
	setRect(&cu.dev.params, r)
	cu.cropKeep("Crop")
}

// cropAngle straightens the photo, shrinking the crop to stay on it.
func (cu *culler) cropAngle(in CropAngle) {
	if !cu.crop.on {
		return
	}
	d := &cu.dev
	d.params.CropAngle = q4(min(15, max(-15, in.Angle)))
	setRect(&d.params, fitToTurn(rectOf(d.params), d.params.CropAngle, cu.frameAspect()))
	if in.Commit {
		cu.cropKeep("Straighten")
		return
	}
	cu.showCull()
}

// cropTurn turns the frame a quarter: the picture showing turns with it
// at once, in the view, while the turned frame's pixels come.
func (cu *culler) cropTurn(in CropTurn) {
	if !cu.crop.on {
		return
	}
	if !cu.crop.ready {
		cu.cropFrame(turnCrop(cu.dev.params, in.CW), "Rotate")
		return
	}
	d := &cu.dev
	d.params = turnCrop(d.params, in.CW)
	cu.crop.turn += map[bool]int{false: -1, true: 1}[in.CW]
	cu.edited(true)
	cu.remember("Rotate")
	_ = cu.c.Update("develop", cu.developState())
	cu.showCull()
}

// cropFlip mirrors the frame: the picture showing mirrors with it at
// once, in the view, while the mirrored frame's pixels come.
func (cu *culler) cropFlip(in CropFlip) {
	if !cu.crop.on {
		return
	}
	if !cu.crop.ready || cu.crop.turn != 0 {
		cu.cropFrame(flipCrop(cu.dev.params, in.Vertical), "Flip")
		return
	}
	d := &cu.dev
	d.params = flipCrop(d.params, in.Vertical)
	if in.Vertical {
		cu.crop.mirrorV = !cu.crop.mirrorV
	} else {
		cu.crop.mirrorH = !cu.crop.mirrorH
	}
	cu.edited(true)
	cu.remember("Flip")
	_ = cu.c.Update("develop", cu.developState())
	cu.showCull()
}

// cropFrame takes an edit with the frame turned or mirrored.
func (cu *culler) cropFrame(p marrawclient.Params, label string) {
	if !cu.crop.on {
		return
	}
	d := &cu.dev
	d.params = p
	cu.crop.ready, cu.crop.frame, cu.crop.turn = false, image.Point{}, 0
	cu.crop.mirrorH, cu.crop.mirrorV = false, false
	cu.edited(true)
	cu.remember(label)
	_ = cu.c.Update("develop", cu.developState())
	cu.showCull()
}

// cropAspect locks the crop to a shape: the largest of it, in the middle.
func (cu *culler) cropAspect(i int) {
	if !cu.crop.on || i < 0 || i >= len(aspectChoices) {
		return
	}
	cu.crop.aspect = i
	if aspectChoices[i].ratio == 0 {
		cu.showCull()
		return
	}
	d := &cu.dev
	asp := cu.frameAspect()
	setRect(&d.params, centredRect(ratioFrac(aspectChoices[i].ratio, asp), d.params.CropAngle, asp))
	cu.cropKeep("Crop " + aspectChoices[i].label)
}

// cropReset takes the crop, the straighten, the turns and the mirror off.
func (cu *culler) cropReset() {
	if !cu.crop.on {
		return
	}
	p := cu.dev.params
	p.Rotate, p.FlipH = 0, false
	p.CropX, p.CropY, p.CropW, p.CropH, p.CropAngle = 0, 0, 0, 0, 0
	cu.crop.aspect = 0
	cu.cropFrame(p, "Reset crop")
}

// cropAuto crops around the photo's subject: the backend finds it, and
// the crop takes it with room round it, in the shape chosen, kept on the
// photo as straightened. One step in the history.
func (cu *culler) cropAuto(download bool) {
	if !cu.crop.on || !cu.crop.ready || cu.crop.autoBusy {
		return
	}
	d := &cu.dev
	id, params, aspect := d.id, d.params, cu.frameAspect()
	rf := ratioFrac(aspectChoices[cu.crop.aspect].ratio, aspect)
	cu.crop.autoBusy = true
	cu.showCull()
	done := func(fn func()) {
		select {
		case cu.do <- func() { cu.crop.autoBusy = false; fn(); cu.showCull() }:
		case <-cu.ctx.Done():
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 3*time.Minute)
		defer cancel()
		if !download {
			if m, err := cu.api.Edits.AIModelStatus(ctx, marrawclient.AIKindSubject); err == nil && m != nil && !m.Downloaded {
				done(func() {})
				cu.askModel("cropModel", "the subject model", m.Bytes)
				return
			}
		}
		res, err := cu.api.Edits.SubjectBounds(ctx, id, params, download)
		done(func() {
			switch {
			case err != nil:
				cu.fail("Auto could not find the subject", err)
			case res == nil || !res.Found:
				cu.tell("No subject found: crop it by hand")
			case cu.crop.on && d.id == id:
				setRect(&d.params, autoCropRect(cropRect{res.X, res.Y, res.W, res.H}, rf, d.params.CropAngle, aspect))
				cu.cropKeep("Auto crop")
			}
		})
	}()
}

// autoCropRect is the crop round subject b, in frame fractions, as
// marraw's Auto makes it: 12% of the subject's size spare on each side,
// grown on its short side to ratio rf where a shape is chosen, no larger
// than the frame, then kept on the photo as straightened by angle.
func autoCropRect(b cropRect, rf, angle, aspect float64) cropRect {
	const m = 0.12
	w, h := b.W*(1+2*m), b.H*(1+2*m)
	cx, cy := b.X+b.W/2, b.Y+b.H/2
	if rf > 0 {
		if w/h < rf {
			w = h * rf
		} else {
			h = w / rf
		}
	}
	s := min(1, 1/w, 1/h)
	w, h = w*s, h*s
	r := cropRect{X: min(max(cx-w/2, 0), 1-w), Y: min(max(cy-h/2, 0), 1-h), W: w, H: h}
	return fitToTurn(r, angle, aspect)
}
