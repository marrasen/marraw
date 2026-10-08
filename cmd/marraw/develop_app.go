package main

import (
	"context"
	"fmt"
	"image"
	"log"
	"strings"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"

	"github.com/marrasen/marraw/internal/jpegturbo"
	"github.com/marrasen/marraw/internal/marrawclient"
)

// The vocabulary of the develop panel, between the window and the culler.
type (
	// DevelopState is what the panel shows: the edit of the photo with ID,
	// its exposure at rest, and the tone curve's channel being edited.
	DevelopState struct {
		ID        int64
		Params    marrawclient.Params
		BaseExpEV float64
		Channel   int
		// Active is the control the keys act on, or none.
		Active string
		// Info is what the panel says of the photo, Lens its lens profile,
		// and History its edit's steps, HistoryAt the one showing.
		Info      PhotoInfo
		Lens      LensInfo
		History   []string
		HistoryAt int
		// WBPick says the white-balance eyedropper is out.
		WBPick bool
		// Presets are the presets the panel lists.
		Presets []PresetCard
	}
	// DevHist is the histogram of the pixels showing.
	DevHist struct{ Counts [3][256]uint32 }

	// ToggleDevelop opens or closes the panel.
	ToggleDevelop struct{}
	// DevSet sets the adjustment Key to Value; Commit says the gesture
	// ended, for the edit to be saved.
	DevSet struct {
		Key    string
		Value  float64
		Commit bool
	}
	// DevCurve sets a channel's tone curve.
	DevCurve struct {
		Channel int
		Points  []geom.Point
		Commit  bool
	}
	// DevChannel chooses the tone curve's channel: RGB, red, green, blue.
	DevChannel struct{ Channel int }
)

// developer is the culler's side of the develop panel: the edit loaded,
// and the live previews of it.
type developer struct {
	open, mounted bool
	// id is the photo whose edit is loaded, params the edit, gen counts
	// loads so a late one is dropped.
	id      int64
	params  marrawclient.Params
	gen     int
	channel int
	// A preview renders at a time: busy, a full-size one if busyFull,
	// stopped by stop. want asks for another once it is done, full-size if
	// wantFull.
	busy, busyFull, want, wantFull bool
	stop                           context.CancelFunc
	// live is the last preview of the photo showing, and note what it is.
	live *paint.Image
	note string
	// saved are the photos whose own saves are on their way back as
	// patches, not to be fetched again.
	saved map[int64]bool
	// edits counts the changes made, committed what it was at the last
	// save, and confirmed what it was at the last save the backend has
	// confirmed: while they differ, the edit showing is not the saved
	// one, which the full-resolution tiles are of.
	edits, committed, confirmed int
	// history is each photo's edits, step by step, for undo, and saves
	// carries the saves out in order.
	history map[int64]*editHistory
	saves   chan saveJob
	// active is the control the keys act on, and nudge saves a run of
	// + and - presses once they stop.
	active string
	nudge  *time.Timer
	// lens is the lens profile matched for the photo.
	lens LensInfo
	// hover is the edit with a preset laid over it, showing while the
	// pointer is over the preset's card, or nil.
	hover *marrawclient.Params
	// thumbStop stops the presets' small pictures being rendered.
	thumbStop context.CancelFunc
}

// editing reports whether photo id has an edit under way the backend has
// not confirmed: its tiles would show the edit before.
func (cu *culler) editing(id int64) bool {
	d := &cu.dev
	// Cropping, the photo shows its whole frame, which has no tiles: the
	// saved edit's are cropped.
	return d.open && d.id == id && (d.edits != d.confirmed || cu.crop.on)
}

// tilesShowing are the tiles to show of p: of its saved edit, and none
// while an edit of it is under way.
func (cu *culler) tilesShowing(p marrawclient.Photo) map[image.Point]*paint.Image {
	if cu.editing(p.ID) {
		return nil
	}
	return cu.tiles.of(p.ID, p.EditHash)
}

// draftEdge is the long edge of the previews while a slider moves; a
// gesture's end renders the full size.
const draftEdge = 1024

// developState is the panel's state.
func (cu *culler) developState() DevelopState {
	d := &cu.dev
	st := DevelopState{ID: d.id, Params: d.params, Channel: d.channel, Active: d.active, Lens: d.lens, WBPick: cu.wb.on}
	if i, ok := cu.index[d.id]; ok {
		st.BaseExpEV = cu.photos[i].BaseExpEV
		st.Info = photoInfo(cu.photos[i], cu.folderPath)
	}
	st.History, st.HistoryAt = cu.historyOfShowing()
	st.Presets = cu.presetCards()
	return st
}

// toggleDevelop opens the panel on the photo showing, or closes it.
func (cu *culler) toggleDevelop() {
	if !cu.culling {
		return
	}
	d := &cu.dev
	d.open = !d.open
	if d.open {
		cu.loadEdit(cu.at)
	} else {
		cu.closeDevelop()
	}
	cu.showCull()
}

// closeDevelop takes the panel away, and stops its preview.
func (cu *culler) closeDevelop() {
	cu.wbFinish(true)
	cu.cropDone()
	d := &cu.dev
	if d.thumbStop != nil {
		d.thumbStop()
		d.thumbStop = nil
	}
	d.hover = nil
	if d.stop != nil {
		d.stop()
	}
	d.live, d.want, d.active = nil, false, ""
	if d.mounted {
		d.mounted = false
		_ = cu.c.Unmount("develop")
	}
}

// loadEdit loads photo i's edit for the panel, and mounts the panel with
// it the first time.
func (cu *culler) loadEdit(i int) {
	cu.dev.hover = nil
	d := &cu.dev
	d.gen++
	gen, id := d.gen, cu.photos[i].ID
	if d.stop != nil {
		d.stop()
	}
	d.live, d.want = nil, false
	go func() {
		p, err := cu.api.Edits.GetEditParams(cu.ctx, id)
		select {
		case cu.do <- func() {
			if err != nil {
				log.Printf("develop: %v", err)
				return
			}
			if gen != d.gen || !d.open || !cu.culling {
				return
			}
			// No edit stored, and no exposure measured yet: all neutral.
			// A new photo, with no edit of it under way.
			if id != d.id {
				d.edits, d.committed, d.confirmed = 0, 0, 0
				d.lens = LensInfo{}
				cu.loadLens(id)
			}
			d.id, d.params = id, marrawclient.Params{}
			if p != nil {
				d.params = *p
			}
			// The edit as first loaded is the history's original.
			cu.historyOf(id, d.params)
			if !d.mounted {
				d.mounted = true
				_ = cu.c.Mount("cull", "develop", "develop", cu.developState(), "develop")
			} else {
				_ = cu.c.Update("develop", cu.developState())
			}
			cu.histogramShowing()
			cu.loadPresetThumbs()
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// histogramShowing shows the histogram of the best pixels showing.
func (cu *culler) histogramShowing() {
	if !cu.dev.mounted {
		return
	}
	img := cu.dev.live
	if img == nil {
		if e, ok := cu.cache.get(cu.photos[cu.at].ID); ok {
			img = e.img
		}
	}
	if img == nil {
		return
	}
	w, h := img.Size()
	_ = cu.c.Patch("develop", DevHist{Counts: histogram(img.Pix(), w, h)})
}

// devSet takes an adjustment from the panel.
func (cu *culler) devSet(in DevSet) {
	d := &cu.dev
	if !d.open || d.id != cu.photos[cu.at].ID {
		return
	}
	sp, ok := devSpecs[in.Key]
	if !ok {
		return
	}
	sp.set(&d.params, in.Value)
	// The control dragged is the one the keys act on now.
	if in.Key != d.active && !strings.Contains(in.Key, ":") {
		cu.setActive(in.Key)
	}
	cu.edited(in.Commit)
	if in.Commit {
		cu.remember(stepLabel(in.Key, in.Value))
	}
}

// devCurve takes a tone curve from the panel.
func (cu *culler) devCurve(in DevCurve) {
	d := &cu.dev
	if !d.open || d.id != cu.photos[cu.at].ID || in.Channel < 0 || in.Channel > 3 {
		return
	}
	var pts []marrawclient.CurvePoint
	if !(len(in.Points) == 2 && in.Points[0] == geom.Pt(0, 0) && in.Points[1] == geom.Pt(1, 1)) {
		for _, p := range in.Points {
			pts = append(pts, marrawclient.CurvePoint{X: float64(p.X), Y: float64(p.Y)})
		}
	}
	*curveOf(&d.params, in.Channel) = pts
	cu.edited(in.Commit)
	if in.Commit {
		cu.remember(curveChannels[in.Channel] + " curve")
	}
}

// curveOf is the place in p of channel ch's curve.
func curveOf(p *marrawclient.Params, ch int) *[]marrawclient.CurvePoint {
	switch ch {
	case 1:
		return &p.ToneCurveR
	case 2:
		return &p.ToneCurveG
	case 3:
		return &p.ToneCurveB
	}
	return &p.ToneCurve
}

// edited previews the edit changed, and saves it once the gesture ends.
// The photo's full-resolution tiles are of the edit before, so they go.
func (cu *culler) edited(commit bool) {
	d := &cu.dev
	d.edits++
	cu.stopTiles()
	cu.tileNote = "tiles: once the edit is saved"
	if commit {
		d.committed = d.edits
		id, params := d.id, d.params
		if d.saved == nil {
			d.saved = map[int64]bool{}
		}
		d.saved[id] = true
		cu.save(id, params)
	}
	cu.preview(commit)
}

// preview renders the edit, a draft at draftEdge while a gesture goes on
// or the full size as it ends. A render at a time: a newer request waits
// for it, and a draft stops a full-size render under way, as the slider
// has moved on.
func (cu *culler) preview(full bool) {
	d := &cu.dev
	d.want, d.wantFull = true, full
	if d.busy {
		if !full && d.busyFull && d.stop != nil {
			d.stop()
		}
		return
	}
	cu.startPreview()
}

func (cu *culler) startPreview() {
	d := &cu.dev
	d.want = false
	full, id, params := d.wantFull, d.id, d.params
	if d.hover != nil {
		// A preset under the pointer shows as a draft, kept or not.
		full, params = false, *d.hover
	}
	// Cropping, the photo shows its whole frame, the crop off and the
	// straighten done here, as marraw shows it.
	flat := cu.crop.on
	if flat {
		params.CropX, params.CropY, params.CropW, params.CropH, params.CropAngle = 0, 0, 0, 0, 0
	}
	edge := draftEdge
	if full {
		edge = 0
	}
	ctx, cancel := context.WithCancel(cu.ctx)
	d.busy, d.busyFull, d.stop = true, full, cancel
	go func() {
		defer cancel()
		start := time.Now()
		var img *paint.Image
		var counts [3][256]uint32
		var size image.Point
		blob, err := cu.api.Edits.PreviewEdit(ctx, id, params, edge)
		took := time.Since(start)
		if err == nil {
			var m *image.RGBA
			if m, err = jpegturbo.DecodeRGBA(blob.Data); err == nil {
				img, size = paint.NewImage(m), m.Rect.Size()
				counts = histogram(m.Pix, size.X, size.Y)
			}
		}
		select {
		case cu.do <- func() {
			d.busy = false
			if err == nil && !flat {
				// Its shape, as the edit's crop and rotation give it.
				cu.learnShape(id, size.X, size.Y)
			}
			// A whole frame turned or mirrored otherwise than the edit is now
			// is let go: the one asked for since comes next.
			stale := flat && (turns(params) != turns(d.params) || params.FlipH != d.params.FlipH)
			if err == nil && d.open && id == d.id && id == cu.photos[cu.at].ID && flat == cu.crop.on && !stale {
				d.live = img
				what := "draft"
				if flat {
					what = "whole frame"
					cu.crop.ready, cu.crop.frame, cu.crop.turn = true, size, 0
				}
				if full && !flat {
					what = "full"
					// The saved edit's pixels, for coming back to the photo.
					cu.cache.put(id, cacheEntry{img: img, rank: rankSharp, note: "edited"})
				}
				d.note = fmt.Sprintf("live %s · %dx%d · %d ms", what, size.X, size.Y, took.Milliseconds())
				cu.showCull()
				_ = cu.c.Patch("develop", DevHist{Counts: counts})
			}
			if d.want && d.open {
				cu.startPreview()
			}
		}:
		case <-cu.ctx.Done():
		}
	}()
}

// histogram counts the red, green and blue of premultiplied RGBA pixels
// w by h, a pixel in every few on a large picture.
func histogram(pix []byte, w, h int) [3][256]uint32 {
	var out [3][256]uint32
	step := 1
	for w*h/(step*step) > 300_000 {
		step++
	}
	for y := 0; y < h; y += step {
		row := pix[y*w*4:]
		for x := 0; x < w; x += step {
			p := row[x*4:]
			out[0][p[0]]++
			out[1][p[1]]++
			out[2][p[2]]++
		}
	}
	return out
}

// devChannel chooses the curve's channel.
func (cu *culler) devChannel(ch int) {
	d := &cu.dev
	if ch < 0 || ch > 3 || !d.mounted {
		return
	}
	d.channel = ch
	_ = cu.c.Update("develop", cu.developState())
}

// developFollows moves the panel to photo i, as the cull view steps.
func (cu *culler) developFollows(i int) {
	if cu.dev.open && cu.culling {
		cu.loadEdit(i)
	}
}
