package main

import (
	"fmt"
	"image"
	"log"
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
	// busy says a window is rendering, and want that another is wanted
	// after it: the newest edit and view win.
	busy, want bool
}

// windowMaxPixels is the largest view a window renders: past it the view
// is zoomed out far enough for the drafts to do.
const windowMaxPixels = 8 << 20

// windowWanted reports whether the view wants a window: zoomed in on the
// photo open in the develop panel, not cropping, and not too far out.
func (cu *culler) windowWanted() bool {
	d, w := &cu.dev, cu.tileWant
	if !d.open || !cu.culling || cu.crop.on || w.Index != cu.at || d.id != cu.photos[cu.at].ID {
		return false
	}
	v := w.View
	return !v.Empty() && v.Dx()*v.Dy() <= windowMaxPixels
}

// wantWindow asks for a window of the edit and view showing, after the one
// under way if there is one. Its tiles in view already, the photo needs
// none.
func (cu *culler) wantWindow() {
	w := &cu.win
	if !cu.windowWanted() {
		return
	}
	p := cu.photos[cu.at]
	if !cu.editing(p.ID) && cu.tilesCover(p, cu.tileWant.Range) {
		return
	}
	w.want = true
	if !w.busy {
		cu.startWindow()
	}
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
	id, view := cu.dev.id, cu.tileWant.View
	params := cu.dev.params
	if cu.dev.hover != nil {
		params = *cu.dev.hover
	}
	w.busy = true
	go func() {
		start := time.Now()
		var img *paint.Image
		blob, err := cu.api.Edits.PreviewWindow(cu.ctx, id, params, view.Min.X, view.Min.Y, view.Dx(), view.Dy())
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
			w.busy = false
			switch {
			case err != nil:
				// A window is a nicety over the drafts: say it in the log.
				if !canceled(err) {
					log.Printf("window of photo %d: %v", id, err)
				}
				w.img = nil
			case img == nil:
				// No window of this edit (a white balance the kept decode
				// lacks, or effects over the whole frame): the drafts show,
				// not a window of an edit before.
				w.img = nil
			case id == cu.dev.id:
				w.img, w.at, w.id = img, view, id
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
