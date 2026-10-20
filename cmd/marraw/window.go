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
	// the same one is not asked for twice. full is the photo's size the
	// view laid it out in when asked: the window shows only in that one.
	key  string
	full image.Point
	// busy says a window is rendering, and want that another is wanted
	// after it: the newest edit and view win. gen counts the windows let
	// go: an answer from before one is dropped.
	busy, want bool
	// again asks once more after a null answer: the same window was
	// wanted again while it rendered.
	again bool
	gen   int
	stop  context.CancelFunc
	// paramsKey and run are the edit img shows, as JSON, and the run of
	// edits it was asked in (editClock); clock outlives a drop.
	paramsKey string
	run       int
	clock     editClock
}

// editClock tells drag frames from one-shot changes. A change that is not
// a drag frame — one after an edit that stayed on screen dragFrame or
// longer, or into or out of a hovered preset — starts a new run. A window
// of an earlier edit of the same run shows on over the new edit until
// that one's comes, so a drag keeps a sharp, slightly late view; one of an
// earlier run does not, so a one-shot change (a preset, paste, the
// eyedropper) shows its preview at once rather than the old look over it.
type editClock struct {
	run   int
	key   string
	since time.Time
	hover bool
}

const dragFrame = 400 * time.Millisecond

// noteShown notes the edit the photo shows now.
func (cu *culler) noteShown() {
	c := &cu.win.clock
	key, hover := paramsJSON(cu.shownParams()), cu.dev.hover != nil
	if key == c.key {
		return
	}
	now := time.Now()
	if now.Sub(c.since) >= dragFrame || hover || c.hover {
		c.run++
	}
	c.key, c.since, c.hover = key, now, hover
}

// windowShows reports whether the window held may show over the edit the
// photo shows now: its own edit, or an earlier one of the same run.
func (cu *culler) windowShows() bool {
	w := &cu.win
	return w.paramsKey == paramsJSON(cu.shownParams()) || w.run == w.clock.run
}

func paramsJSON(p marrawclient.Params) string {
	b, _ := json.Marshal(p)
	return string(b)
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
	v, p := w.View, cu.photos[cu.at]
	// The view's size of the photo is the edit's real one only once the
	// shape it learned agrees with the saved edit (fullOf).
	return !v.Empty() && v.Dx()*v.Dy() <= windowMaxPixels &&
		frameOf(cu.shownParams()) == photoFrame(p) && cu.fullOf(p) == size(p)
}

// dropWindow lets the window go, and the one rendering.
func (cu *culler) dropWindow() {
	w := &cu.win
	if w.stop != nil {
		w.stop()
	}
	*w = winState{gen: w.gen + 1, clock: w.clock}
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
	if !cu.editing(p.ID) && (cu.tileWarm[tileSet(p)] || cu.tilesCover(p, cu.tileWant.Range)) {
		// The saved edit's tiles are in, or rendered and a fetch away:
		// cheaper than a window, and they paint over it. Nor one queued.
		w.want, w.again = false, false
		return
	}
	if cu.windowKey() == w.key {
		// Showing or rendering already: back to it, nothing else is.
		// Should the one rendering answer null, it is asked again, as
		// this asks after it (a save's decode may be ready by then).
		w.want, w.again = false, w.busy
		return
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
	id, view, params, key, gen, run := cu.dev.id, cu.tileWant.View, cu.shownParams(), cu.windowKey(), w.gen, w.clock.run
	full := cu.fullOf(cu.photos[cu.at])
	ctx, cancel := context.WithCancel(cu.ctx)
	w.busy, w.key, w.stop, w.again = true, key, cancel, false
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
				w.want = w.want || w.again
			case img == nil:
				// No window of this edit (a white balance the kept decode
				// lacks, or effects over the whole frame): the drafts show,
				// not a window of an edit before. It may come later — the
				// backend decodes the photo once the editor holds it — and
				// a null answer is cheap, so the same one may be asked again.
				w.img, w.key = nil, ""
				w.want = w.want || w.again
			default:
				w.img, w.at, w.id, w.frame, w.full = img, view, id, frameOf(params), full
				w.paramsKey, w.run = paramsJSON(params), run
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
