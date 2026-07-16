package pyramid

import (
	"image"
	"math"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

// fillFlat paints the whole image one gray level (opaque).
func fillFlat(img *image.RGBA, v uint8) {
	b := img.Bounds()
	for y := range b.Dy() {
		for x := range b.Dx() {
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 0xff
		}
	}
}

// fillDisc paints a filled disc (center/radius in pixels) one gray level.
func fillDisc(img *image.RGBA, cx, cy, r float64, v uint8) {
	for y := int(cy - r); y <= int(cy+r); y++ {
		for x := int(cx - r); x <= int(cx+r); x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			if dx*dx+dy*dy > r*r {
				continue
			}
			if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
				continue
			}
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 0xff
		}
	}
}

func lumaAt(img *image.RGBA, x, y int) int {
	i := img.PixOffset(x, y)
	return int(img.Pix[i])
}

func TestApplyHealNeutralNoOp(t *testing.T) {
	img := gradientImage(64, 48)
	before := clonePix(img)
	ApplyHeal(img, nil)
	ApplyHeal(img, &edit.Params{})
	for i := range before {
		if img.Pix[i] != before[i] {
			t.Fatalf("neutral ApplyHeal changed pixel %d: %d -> %d", i, before[i], img.Pix[i])
		}
	}
}

// TestApplyHealClone copies the source verbatim into the destination disc.
func TestApplyHealClone(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	fillFlat(img, 100)
	fillDisc(img, 150, 100, 20, 200) // bright source patch
	e := &edit.Params{Spots: []edit.Spot{{
		Mode: edit.SpotClone,
		CX:   0.25, CY: 0.5, Radius: 0.05, // dest at (50,100), radPx=10
		SX: 0.75, SY: 0.5, // source at (150,100)
		Feather: 0.1,
	}}}
	ApplyHeal(img, e)
	if got := lumaAt(img, 50, 100); got < 190 {
		t.Errorf("clone center should copy the bright source (~200), got %d", got)
	}
	// Far outside the disc is untouched.
	if got := lumaAt(img, 50, 160); got != 100 {
		t.Errorf("clone must not touch pixels outside the disc, got %d", got)
	}
}

// TestApplyHealToneMatches covers a blemished destination healed from a
// differently-lit source: the fill should match the destination surround, not
// the source brightness.
func TestApplyHealToneMatches(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	fillFlat(img, 100)            // destination surround
	fillDisc(img, 150, 100, 22, 200) // bright source patch (constant)
	fillDisc(img, 50, 100, 10, 0)    // black blemish to cover
	before := lumaAt(img, 50, 100)
	if before != 0 {
		t.Fatalf("blemish setup wrong, center=%d", before)
	}
	e := &edit.Params{Spots: []edit.Spot{{
		CX: 0.25, CY: 0.5, Radius: 0.05, // heal mode (default)
		SX: 0.75, SY: 0.5,
		Feather: 0.1,
	}}}
	ApplyHeal(img, e)
	got := lumaAt(img, 50, 100)
	if got < 90 || got > 110 {
		t.Errorf("heal should tone-match the ~100 surround, got %d", got)
	}
}

// TestApplyHealNearEdge must not panic or read out of bounds when a spot sits
// against the frame edge.
func TestApplyHealNearEdge(t *testing.T) {
	img := gradientImage(120, 90)
	e := &edit.Params{Spots: []edit.Spot{
		{CX: 0.01, CY: 0.01, Radius: 0.08, SX: 0.5, SY: 0.5, Feather: 0.4},
		{CX: 0.99, CY: 0.99, Radius: 0.08, SX: 0.5, SY: 0.5, Mode: edit.SpotClone},
	}}
	ApplyHeal(img, e) // just needs to survive
}

func TestSuggestHealSource(t *testing.T) {
	img := smoothImage(200, 160)
	e := &edit.Params{}
	spot := edit.Spot{CX: 0.5, CY: 0.5, Radius: 0.05}
	sx1, sy1 := SuggestHealSource(img, e, spot)
	sx2, sy2 := SuggestHealSource(img, e, spot)
	if sx1 != sx2 || sy1 != sy2 {
		t.Errorf("SuggestHealSource must be deterministic: (%v,%v) vs (%v,%v)", sx1, sy1, sx2, sy2)
	}
	// The suggestion must be in-frame and clear of the destination disc.
	if sx1 < 0 || sx1 > 1 || sy1 < 0 || sy1 > 1 {
		t.Errorf("suggested source off-frame: (%v,%v)", sx1, sy1)
	}
	f := newMaskFrame(200, 160, e)
	long := math.Max(f.frameW, f.frameH)
	scx, scy := f.outputPoint(sx1*f.frameW, sy1*f.frameH)
	dcx, dcy := f.outputPoint(spot.CX*f.frameW, spot.CY*f.frameH)
	if d := math.Hypot(scx-dcx, scy-dcy); d < 2*spot.Radius*long {
		t.Errorf("suggested source overlaps the spot: distance %v < %v", d, 2*spot.Radius*long)
	}
}

// TestApplyHealResolutionStable checks a spot heals to the same tone whether
// rendered large or downscaled — spot geometry is resolution independent.
func TestApplyHealResolutionStable(t *testing.T) {
	mk := func(w, h int) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		fillFlat(img, 120)
		fillDisc(img, float64(w)*0.75, float64(h)*0.5, float64(w)*0.12, 210)
		fillDisc(img, float64(w)*0.25, float64(h)*0.5, float64(w)*0.05, 20)
		return img
	}
	e := &edit.Params{Spots: []edit.Spot{{
		CX: 0.25, CY: 0.5, Radius: 0.05, SX: 0.75, SY: 0.5, Feather: 0.2,
	}}}
	big := mk(400, 300)
	small := mk(200, 150)
	ApplyHeal(big, e)
	ApplyHeal(small, e)
	gb := lumaAt(big, 100, 150)
	gs := lumaAt(small, 50, 75)
	if diff := gb - gs; diff < -12 || diff > 12 {
		t.Errorf("heal tone drifted across resolution: big=%d small=%d", gb, gs)
	}
}
