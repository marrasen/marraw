// Package watermark composites text and image overlays onto rendered
// exports. Geometry is resolution-independent: sizes and margins are
// percentages of the output's short edge, so the same watermark reads the
// same at full resolution and at a 1024px web export. The client preview in
// client/src/lib/watermarks.ts is this package's TypeScript twin — the
// placement math must stay in lockstep.
package watermark

import (
	"image"
	"image/color"
	"math"
	"strings"
)

// Kind discriminates the element types.
const (
	KindText  = "text"
	KindImage = "image"
)

// Anchor is one of the nine placement positions.
type Anchor string

const (
	AnchorTopLeft     Anchor = "topLeft"
	AnchorTop         Anchor = "top"
	AnchorTopRight    Anchor = "topRight"
	AnchorLeft        Anchor = "left"
	AnchorCenter      Anchor = "center"
	AnchorRight       Anchor = "right"
	AnchorBottomLeft  Anchor = "bottomLeft"
	AnchorBottom      Anchor = "bottom"
	AnchorBottomRight Anchor = "bottomRight"
)

// Element is one overlay, fully resolved: asset paths absolute, color
// parsed. Zero-content elements (empty text, empty asset path) are skipped.
type Element struct {
	Kind string // KindText or KindImage
	// Text elements.
	Text  string
	Font  FontID
	Color color.NRGBA // alpha ignored; Opacity is the single alpha control
	// Image elements.
	AssetPath string
	// Shared geometry.
	Anchor    Anchor
	SizePct   float64 // % of short edge: text em size / image height
	MarginPct float64 // % of short edge, on anchored edges only
	Opacity   float64 // 0..1
}

// Spec is a watermark ready to composite.
type Spec struct {
	Elements []Element
}

// Apply composites every element onto dst in place, after the final resize
// and output sharpening (the same "property of the final render" rationale
// as sharpening — the encoders that follow see watermarked pixels no matter
// the container). A failed element (missing asset, undecodable image) is
// skipped so one bad logo doesn't lose the export; the first error is
// returned for the task log.
func Apply(dst *image.RGBA, spec Spec) error {
	shortEdge := min(dst.Bounds().Dx(), dst.Bounds().Dy())
	if shortEdge <= 0 {
		return nil
	}
	var firstErr error
	for _, el := range spec.Elements {
		var err error
		switch el.Kind {
		case KindText:
			err = drawText(dst, el, shortEdge)
		case KindImage:
			err = drawImage(dst, el, shortEdge)
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// alpha8 converts the 0..1 opacity to a coverage byte.
func alpha8(opacity float64) uint8 {
	return uint8(math.Round(255 * clamp01(opacity)))
}

func clamp01(v float64) float64 {
	return math.Min(1, math.Max(0, v))
}

// sizePx converts a percent-of-short-edge to pixels (minimum 1).
func sizePx(pct float64, shortEdge int) int {
	return max(1, int(math.Round(pct/100*float64(shortEdge))))
}

func hasText(el Element) bool { return strings.TrimSpace(el.Text) != "" }
