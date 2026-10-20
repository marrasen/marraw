package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"log"
	"math"
	"time"

	"github.com/marrasen/gunim/paint"

	"github.com/marrasen/marraw/internal/jpegturbo"
	"github.com/marrasen/marraw/internal/marrawclient"
)

// winState is the sharp window over a zoomed-in edit: the part of the
// photo in view at full resolution (Edits.PreviewWindow), while a slider
// moves and until the edit's tiles are in. A draft of the whole frame is
// too soft to judge an edit by once the view is zoomed in.
type winState struct {
	img *paint.Image
	at  image.Rectangle // in the full resolution's pixels
	id  int64
	// frame is the output frame img was rendered in: the view lays the
	// photo out in its saved frame, so a window of another one would land
	// at the wrong scale.
	frame frameKey
	// key names the edit and view of the window showing or rendering, so
	// the same one is not asked for twice.
	key string
	// busy says a window is rendering, and want that another is wanted
	// after it: the newest edit and view win. gen counts the windows let
	// go: an answer from before one is dropped.
	busy, want bool
	gen        int
	stop       context.CancelFunc
}

// frameKey is the shape of an edit's output: its quarter turns and crop
// size, the parts of the edit size() reads.
type frameKey struct {
	turns  int
	cw, ch float64
}

func frameOf(p marrawclient.Params) frameKey {
	return frameKey{turns(p), cropFrac(p.CropW), cropFrac(p.CropH)}
}

func photoFrame(p marrawclient.Photo) frameKey {
	return frameKey{((p.Rotate % 4) + 4) % 4, cropFrac(p.CropW), cropFrac(p.CropH)}
}

// cropFrac reads a crop size, none being the whole.
func cropFrac(v float64) float64 {
	if v <= 0 || v >= 1 {
		return 1
	}
	return math.Round(v*1e6) / 1e6
}

// windowMaxPixels is the largest view a window renders: past it the view
// is zoomed out far enough for the drafts to do.
const windowMaxPixels = 8 << 20

// shownParams is the edit the photo shows: a preset under the pointer, or
// the panel's.
func (cu *culler) shownParams() marrawclient.Params {
	if cu.dev.hover != nil {
		return *cu.dev.hover
	}
	return cu.dev.params
}

// windowWanted reports whether the view wants a window: zoomed in on the
// photo open in the develop panel, not cropping, not too far out, and the
// edit in the frame the view lays the photo out in.
func (cu *culler) windowWanted() bool {
	d, w := &cu.dev, cu.tileWant
	if !d.open || !cu.culling || cu.crop.on || w.Index != cu.at || d.id != cu.photos[cu.at].ID {
		return false
	}
	v := w.View
	return !v.Empty() && v.Dx()*v.Dy() <= windowMaxPixels && frameOf(cu.shownParams()) == photoFrame(cu.photos[cu.at])
}

// dropWindow lets the window go, and the one rendering.
func (cu *culler) dropWindow() {
	w := &cu.win
	if w.stop != nil {
		w.stop()
	}
	*w = winState{gen: w.gen + 1}
}

// wantWindow asks for a window of the edit and view showing, after the one
// under way if there is one. Its tiles in view already, the photo needs
// none; a view that wants none lets the window go.
func (cu *culler) wantWindow() {
	w := &cu.win
	if !cu.windowWanted() {
		cu.dropWindow()
		return
	}
	p := cu.photos[cu.at]
	if !cu.editing(p.ID) && cu.tilesCover(p, cu.tileWant.Range) {
		return
	}
	if cu.windowKey() == w.key {
		return // showing or rendering already
	}
	w.want = true
	if !w.busy {
		cu.startWindow()
	}
}

// windowKey names the window the edit and view showing want.
func (cu *culler) windowKey() string {
	b, _ := json.Marshal(cu.shownParams())
	return fmt.Sprint(cu.dev.id, cu.tileWant.View, string(b))
}

// tilesCover reports whether p's tiles in r are all held.
func (cu *culler) tilesCover(p marrawclient.Photo, r image.Rectangle) bool {
	held := cu.tiles.of(p.ID, p.EditHash)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if held[image.Pt(x, y)] == nil {
				return false
			}
		}
	}
	return true
}

func (cu *culler) startWindow() {
	w := &cu.win
	w.want = false
	if !cu.windowWanted() {
		return
	}
	id, view, params, key, gen := cu.dev.id, cu.tileWant.View, cu.shownParams(), cu.windowKey(), w.gen
	ctx, cancel := context.WithCancel(cu.ctx)
	w.busy, w.key, w.stop = true, key, cancel
	go func() {
		defer cancel()
		start := time.Now()
		var img *paint.Image
		blob, err := cu.api.Edits.PreviewWindow(ctx, id, params, view.Min.X, view.Min.Y, view.Dx(), view.Dy())
		if err == nil && blob != nil {
			var m *image.RGBA
			if m, err = jpegturbo.DecodeRGBA(blob.Data); err == nil {
				img = paint.NewImage(m)
				// The backend clips the window to the photo.
				view = image.Rectangle{Min: view.Min, Max: view.Min.Add(m.Rect.Size())}
			}
		}
		took := time.Since(start)
		select {
		case cu.do <- func() {
			if gen != w.gen {
				return // let go meanwhile
			}
			w.busy, w.stop = false, nil
			switch {
			case err != nil:
				// A window is a nicety over the drafts: say it in the log.
				if !canceled(err) {
					log.Printf("window of photo %d: %v", id, err)
				}
				w.img, w.key = nil, ""
			case img == nil:
				// No window of this edit (a white balance the kept decode
				// lacks, or effects over the whole frame): the drafts show,
				// not a window of an edit before. It may come later — the
				// backend decodes the photo once the editor holds it — and
				// a null answer is cheap, so the same one may be asked again.
				w.img, w.key = nil, ""
			default:
				w.img, w.at, w.id, w.frame = img, view, id, frameOf(params)
				cu.dev.note = fmt.Sprintf("window · %dx%d · %d ms", view.Dx(), view.Dy(), took.Milliseconds())
			}
			cu.showCull()
			if w.want {
				cu.startWindow()
			}
		}:
		case <-cu.ctx.Done():
		}
	}()
}
