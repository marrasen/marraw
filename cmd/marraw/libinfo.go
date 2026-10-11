package main

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// libInfoGen is which showing of the info panel's photo is the latest,
// so a histogram that comes late for a photo left is dropped.
var libInfoGen int

// sendLibInfo shows the info panel the photo the grid's keyboard is on:
// its details at once, and its histogram as its pixels come.
func (cu *culler) sendLibInfo() {
	if cu.culling || cu.ui != nil && cu.ui.EditPanelHidden {
		return
	}
	i := cu.cursor
	if i < 0 || i >= len(cu.photos) {
		_ = cu.c.Patch("grid", LibInfo{Folder: cu.folder})
		return
	}
	p := cu.photos[i]
	in := LibInfo{Folder: cu.folder, ID: p.ID, Name: p.FileName, Exif: exifLine(p), Rating: p.Rating, Flag: string(p.Flag),
		Info: photoInfo(p, cu.folderPath)}
	_ = cu.c.Patch("grid", in)
	libInfoGen++
	gen := libInfoGen
	go func() {
		ctx, cancel := context.WithTimeout(cu.ctx, 30*time.Second)
		defer cancel()
		m, err := cu.im.rgba(ctx, p, want{level: "512", stale: true, fast: true})
		if err != nil {
			return
		}
		b := m.Bounds()
		in.Hist, in.HasHist = histogram(m.Pix, b.Dx(), b.Dy()), true
		cu.onDo(func() {
			if libInfoGen == gen {
				_ = cu.c.Patch("grid", in)
			}
		})
	}()
}

// asideToggle puts the info panel away or brings it back, as kept in the
// settings.
func (cu *culler) asideToggle() {
	if cu.ui == nil {
		return
	}
	hidden := !cu.ui.EditPanelHidden
	cu.ui.EditPanelHidden = hidden
	_ = cu.c.Patch("grid", AsideIn{On: !hidden})
	cu.sendLibInfo()
	cu.call("The info panel could not keep that", func(ctx context.Context) error {
		return cu.api.Settings.SetEditPanelHidden(ctx, hidden)
	}, nil)
}

// gapPick groups the photos by a gap of minutes, nought for none.
func (cu *culler) gapPick(minutes int) {
	v := cu.libView
	v.Gap = max(0, minutes)
	cu.setView(v)
	if cu.culling {
		cu.showCull()
	}
}

// askGapCustom asks for a gap of the user's own.
func (cu *culler) askGapCustom(minutes int) {
	val := ""
	if minutes > 0 {
		val = strconv.Itoa(minutes)
	}
	cu.askPrompt(PromptAsk{Kind: "gap", Title: "Group by a gap of your own", Label: "Minutes between groups", Value: val, OK: "Group"})
}

// gapGiven takes the gap typed.
func (cu *culler) gapGiven(s string) {
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "min")))
	if err != nil || n < 0 || n > 24*60 {
		cu.notify("A gap is a number of minutes, as 15")
		return
	}
	cu.gapPick(n)
}
