package api

import (
	"testing"

	"github.com/marrasen/marraw/internal/watermark"
)

func TestNormalizeWatermarkElementRect(t *testing.T) {
	// Zero values (an older blob, or a fresh element) get rect defaults.
	e := normalizeWatermarkElement(WatermarkElement{ID: "a", Type: WatermarkRect})
	if e.Fill != WatermarkFillSolid {
		t.Errorf("fill = %q, want solid default", e.Fill)
	}
	if e.GradientDir != WatermarkGradientDown {
		t.Errorf("gradientDir = %q, want down default", e.GradientDir)
	}
	if e.Color2 != "#ffffff" {
		t.Errorf("color2 = %q, want white fallback", e.Color2)
	}
	if e.Opacity2 != 0 {
		t.Errorf("opacity2 = %v, want 0 preserved (fade end)", e.Opacity2)
	}
	if e.WidthPct != watermarkRectWidthDefault || e.HeightPct != watermarkRectHeightDefault {
		t.Errorf("dims = %v x %v, want defaults", e.WidthPct, e.HeightPct)
	}

	// Out-of-range values clamp; valid ones survive.
	e = normalizeWatermarkElement(WatermarkElement{
		ID: "a", Type: WatermarkRect,
		Fill: WatermarkFillGradient, GradientDir: WatermarkGradientLeft,
		Color2: "#A1B2C3", Opacity2: 1.5, WidthPct: 250, HeightPct: 0.2,
	})
	if e.Fill != WatermarkFillGradient || e.GradientDir != WatermarkGradientLeft {
		t.Errorf("valid fill/dir did not survive: %q %q", e.Fill, e.GradientDir)
	}
	if e.Color2 != "#a1b2c3" {
		t.Errorf("color2 = %q, want lowercased", e.Color2)
	}
	if e.Opacity2 != 1 {
		t.Errorf("opacity2 = %v, want clamped to 1", e.Opacity2)
	}
	if e.WidthPct != watermarkRectDimMax {
		t.Errorf("widthPct = %v, want clamped to %v", e.WidthPct, watermarkRectDimMax)
	}
	if e.HeightPct != watermarkRectDimMin {
		t.Errorf("heightPct = %v, want clamped to %v", e.HeightPct, watermarkRectDimMin)
	}
}

func TestNormalizeWatermarkFrame(t *testing.T) {
	f := normalizeWatermarkFrame(WatermarkFrame{})
	want := WatermarkFrame{WidthPct: watermarkFrameWidthDefault, Color: "#ffffff"}
	if f != want {
		t.Errorf("zero frame = %+v, want %+v", f, want)
	}
	f = normalizeWatermarkFrame(WatermarkFrame{Enabled: true, WidthPct: 99, BottomPct: 99, Color: "#FAFAF0"})
	if f.WidthPct != watermarkFrameWidthMax || f.BottomPct != watermarkFrameBottomMax {
		t.Errorf("clamps: %+v", f)
	}
	if f.Color != "#fafaf0" || !f.Enabled {
		t.Errorf("color/enabled: %+v", f)
	}
}

func TestToWatermarkSpecRectAndFrame(t *testing.T) {
	wm := Watermark{
		ID: "w", Name: "n",
		Elements: []WatermarkElement{{
			ID: "e", Type: WatermarkRect, Fill: WatermarkFillGradient,
			Color: "#102030", Color2: "#405060", Opacity: 0.5, Opacity2: 0,
			GradientDir: WatermarkGradientUp, WidthPct: 100, HeightPct: 20,
			Anchor: WatermarkBottom,
		}},
		Frame: WatermarkFrame{Enabled: true, WidthPct: 4, BottomPct: 12, Color: "#fafaf0"},
	}
	spec := toWatermarkSpec(wm, t.TempDir())
	if spec == nil {
		t.Fatal("nil spec")
	}
	if len(spec.Elements) != 1 || spec.Elements[0].Kind != watermark.KindRect {
		t.Fatalf("elements: %+v", spec.Elements)
	}
	el := spec.Elements[0]
	if !el.Gradient || el.GradientDir != watermark.GradientUp {
		t.Errorf("gradient mapping: %+v", el)
	}
	if el.Color.R != 0x10 || el.Color2.R != 0x40 {
		t.Errorf("color mapping: %v %v", el.Color, el.Color2)
	}
	if el.WidthPct != 100 || el.HeightPct != 20 || el.Opacity2 != 0 {
		t.Errorf("geometry mapping: %+v", el)
	}
	if spec.Frame == nil || spec.Frame.WidthPct != 4 || spec.Frame.BottomPct != 12 {
		t.Fatalf("frame mapping: %+v", spec.Frame)
	}
	if spec.Frame.Color.R != 0xfa || spec.Frame.Color.B != 0xf0 {
		t.Errorf("frame color: %v", spec.Frame.Color)
	}
}

// TestToWatermarkSpecFrameOnly: a watermark with no elements but an enabled
// frame must still export (the emptiness rule includes the frame).
func TestToWatermarkSpecFrameOnly(t *testing.T) {
	wm := Watermark{ID: "w", Name: "n", Frame: WatermarkFrame{Enabled: true, WidthPct: 3, Color: "#ffffff"}}
	if spec := toWatermarkSpec(wm, t.TempDir()); spec == nil || spec.Frame == nil {
		t.Fatal("frame-only watermark must yield a spec with a frame")
	}
	// And with the frame disabled it stays nil like before.
	wm.Frame.Enabled = false
	if spec := toWatermarkSpec(wm, t.TempDir()); spec != nil {
		t.Fatal("empty watermark must stay nil")
	}
}
