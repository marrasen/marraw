package main

import (
	"math"
	"testing"

	"github.com/marrasen/marraw/internal/marrawclient"
)

func TestFitToTurnKeepsTheCropOnThePhoto(t *testing.T) {
	r := fitToTurn(cropRect{0, 0, 1, 1}, 10, 1.5)
	if !cornersCovered(r, 10, 1.5) {
		t.Fatalf("the fitted crop %v has a corner off the turned photo", r)
	}
	if math.Abs(r.W/r.H-1) > 1e-6 || r.W > 0.9 {
		t.Fatalf("the fitted crop %v lost its shape or did not shrink", r)
	}
	if got := fitToTurn(cropRect{0.2, 0.2, 0.5, 0.5}, 0, 1.5); got != (cropRect{0.2, 0.2, 0.5, 0.5}) {
		t.Fatalf("with no straighten the crop changed to %v", got)
	}
}

func TestTurnAndFlipComeBackRound(t *testing.T) {
	p := marrawclient.Params{CropX: 0.1, CropY: 0.2, CropW: 0.3, CropH: 0.4, CropAngle: 5}
	q := p
	for range 4 {
		q = turnCrop(q, true)
	}
	if q.Rotate != 0 || math.Abs(q.CropX-p.CropX) > 1e-9 || math.Abs(q.CropY-p.CropY) > 1e-9 {
		t.Fatalf("four quarter turns gave %+v", q)
	}
	if q = turnCrop(turnCrop(p, true), false); q.Rotate != 0 || q.CropX != p.CropX || q.CropW != p.CropW {
		t.Fatalf("a turn and its undoing gave %+v", q)
	}
	if q = flipCrop(flipCrop(p, false), false); q.FlipH || math.Abs(q.CropX-p.CropX) > 1e-9 || q.CropAngle != p.CropAngle {
		t.Fatalf("two mirrors gave %+v", q)
	}
	if q = flipCrop(p, true); q.Rotate != 2 || !q.FlipH || math.Abs(q.CropY-0.4) > 1e-9 || q.CropAngle != -5 {
		t.Fatalf("upside down gave %+v", q)
	}
}

func TestDragKeepsTheRatioAndTheSmallest(t *testing.T) {
	start := cropRect{0, 0, 1, 1}
	r := dragRect(start, "se", -0.5, -0.1, 1.0)
	if math.Abs(r.W-r.H) > 1e-9 || math.Abs(r.W-0.5) > 1e-9 {
		t.Fatalf("a corner dragged at 1:1 gave %v", r)
	}
	if r := dragRect(start, "e", -2, 0, 0); r.W < cropMin-1e-9 {
		t.Fatalf("an edge dragged past the other made %v", r)
	}
	if r := dragCovered(start, "move", 0, 0, 0, 10, 1.5); !cornersCovered(r, 10, 1.5) {
		t.Fatalf("a move with a straighten left %v off the photo", r)
	}
}

func TestRelativePresetsAddAndAbsoluteOnesReplace(t *testing.T) {
	draft := marrawclient.Params{Contrast: 0.2, ExpEV: 1, WBMode: "auto"}
	pre := marrawclient.UserPreset{Params: marrawclient.Params{Contrast: 0.3, ExpEV: 0.5}, BaseExpEV: 0.5}
	abs := applyUserPreset(draft, pre, 0.7)
	if abs.Contrast != 0.3 || math.Abs(abs.ExpEV-0.7) > 1e-9 || abs.WBMode != "" {
		t.Fatalf("an absolute preset gave contrast %v, exposure %v, mode %q", abs.Contrast, abs.ExpEV, abs.WBMode)
	}
	pre.Relative = true
	rel := applyUserPreset(draft, pre, 0.7)
	if math.Abs(rel.Contrast-0.5) > 1e-9 || math.Abs(rel.ExpEV-1) > 1e-9 || rel.WBMode != "auto" {
		t.Fatalf("a relative preset gave contrast %v, exposure %v, mode %q", rel.Contrast, rel.ExpEV, rel.WBMode)
	}
	pre.Sections = []string{"color"}
	if got := applyUserPreset(draft, pre, 0.7); got.Contrast != 0.2 {
		t.Fatalf("a colour-only preset moved the contrast to %v", got.Contrast)
	}
	geo := stripToLook(marrawclient.Params{Rotate: 1, CropW: 0.5, CropH: 0.5, CropAngle: 3})
	if geo.Rotate != 0 || geo.CropW != 0 || geo.CropAngle != 0 {
		t.Fatalf("a look kept geometry: %+v", geo)
	}
}

func TestGapGroupsSplitWhereTimePasses(t *testing.T) {
	at := func(ts ...int64) []marrawclient.Photo {
		var out []marrawclient.Photo
		for _, t := range ts {
			out = append(out, marrawclient.Photo{TakenAt: t})
		}
		return out
	}
	gs := gapGroups(at(1000, 1060, 0, 1100, 1700, 1720), 5, "captureAsc")
	if len(gs) != 2 || gs[0].Start != 0 || gs[0].Count != 4 || gs[1].Start != 4 || gs[1].GapBefore != 10 {
		t.Fatalf("groups %+v", gs)
	}
	if gapGroups(at(1, 9999), 5, "nameAsc") != nil || gapGroups(at(1, 9999), 0, "captureAsc") != nil {
		t.Fatal("a name sort or no gap made groups")
	}
	if got := gapLabel(120, false); got != "+2.0 h gap before" {
		t.Fatalf("a two hour gap reads %q", got)
	}
}

func TestBurstsFindTheirSharpest(t *testing.T) {
	g := int64(1)
	f := func(v float64) *float64 { return &v }
	all := []marrawclient.Photo{
		{ID: 1, FileName: "a", GroupID: &g, Sharpness: f(100)},
		{ID: 2, FileName: "b", GroupID: &g, Sharpness: f(300)},
		{ID: 3, FileName: "c", Sharpness: f(5)},
		{ID: 4, FileName: "d", Sharpness: f(200)},
		{ID: 5, FileName: "e", Sharpness: f(250)},
	}
	a := newAids(all, LibView{Sort: "nameAsc"})
	if got := a.of(all[1]); got.Burst != 2 || got.BurstOf != 2 || !got.BurstBest {
		t.Fatalf("frame b reads %+v", got)
	}
	if got := a.of(all[0]); got.BurstBest || got.Burst != 1 {
		t.Fatalf("frame a reads %+v", got)
	}
	if !a.of(all[2]).Soft || a.of(all[3]).Soft {
		t.Fatal("softness misjudged")
	}
	v := LibView{Sort: "nameAsc", Collapse: true}
	if v.shows(all[0], a) || !v.shows(all[1], a) || !v.shows(all[2], a) {
		t.Fatal("collapsed bursts show the wrong frames")
	}
}
