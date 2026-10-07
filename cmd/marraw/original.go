package main

import "github.com/marrasen/gunim/paint"

// ShowOriginal shows the photo as it was before any edit while On, as
// holding Backspace does in marraw.
type ShowOriginal struct{ On bool }

// showOriginal shows the photo showing as it was before any edit, or its
// edit again: the original's pixels are fetched once and kept for the
// photo.
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
	if cu.origImg != nil && cu.origID == p.ID {
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
		select {
		case cu.do <- func() {
			if err != nil {
				return
			}
			cu.origID, cu.origImg = p.ID, g.img
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
