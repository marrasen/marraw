package pyramid

import (
	"image"
	"testing"
)

// stepImage is a hard vertical step edge: left half lo, right half hi.
func stepImage(w, h int, lo, hi uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := lo
			if x >= w/2 {
				v = hi
			}
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 0xff
		}
	}
	return img
}

// stepOvershoot measures how far sharpening pushed pixels past the original
// step levels: max pixel above hi on the right plus lo minus min pixel on the
// left, along the middle row.
func stepOvershoot(img *image.RGBA, lo, hi uint8) int {
	b := img.Bounds()
	w := b.Dx()
	y := b.Dy() / 2
	minL, maxR := int(lo), int(hi)
	for x := range w {
		v := int(img.Pix[img.PixOffset(x, y)])
		if x < w/2 && v < minL {
			minL = v
		}
		if x >= w/2 && v > maxR {
			maxR = v
		}
	}
	return (maxR - int(hi)) + (int(lo) - minL)
}

func sharpenedOvershoot(target, amount string) int {
	img := stepImage(64, 32, 64, 192)
	ApplyOutputSharpen(img, target, amount)
	return stepOvershoot(img, 64, 192)
}

func TestApplyOutputSharpenOffNoOp(t *testing.T) {
	for _, target := range []string{"off", "", "bogus"} {
		img := gradientImage(64, 48)
		before := clonePix(img)
		ApplyOutputSharpen(img, target, "standard")
		for i := range before {
			if img.Pix[i] != before[i] {
				t.Fatalf("target %q changed pixel %d: %d -> %d", target, i, before[i], img.Pix[i])
			}
		}
	}
}

func TestApplyOutputSharpenEdgeOvershoot(t *testing.T) {
	for _, target := range []string{"screen", "glossy", "matte"} {
		if got := sharpenedOvershoot(target, "standard"); got <= 0 {
			t.Fatalf("target %q produced no edge overshoot", target)
		}
	}
}

func TestApplyOutputSharpenAmountMonotonic(t *testing.T) {
	for _, target := range []string{"screen", "glossy", "matte"} {
		low := sharpenedOvershoot(target, "low")
		std := sharpenedOvershoot(target, "standard")
		high := sharpenedOvershoot(target, "high")
		if !(low < std && std < high) {
			t.Fatalf("target %q not monotonic: low=%d standard=%d high=%d", target, low, std, high)
		}
	}
	if screen, matte := sharpenedOvershoot("screen", "standard"), sharpenedOvershoot("matte", "standard"); matte <= screen {
		t.Fatalf("matte (%d) should overshoot more than screen (%d)", matte, screen)
	}
}

// TestApplyOutputSharpenUnknownAmountIsStandard: an unrecognized amount must
// behave exactly like "standard" (back-compat with empty wire fields).
func TestApplyOutputSharpenUnknownAmountIsStandard(t *testing.T) {
	std := stepImage(64, 32, 64, 192)
	odd := stepImage(64, 32, 64, 192)
	ApplyOutputSharpen(std, "screen", "standard")
	ApplyOutputSharpen(odd, "screen", "")
	for i := range std.Pix {
		if std.Pix[i] != odd.Pix[i] {
			t.Fatalf("empty amount diverged from standard at pixel %d", i)
		}
	}
}

// TestApplyOutputSharpenThresholdSkipsNoise: a ±1 ripple around flat gray is
// below every target's deadzone and must survive untouched.
func TestApplyOutputSharpenThresholdSkipsNoise(t *testing.T) {
	for _, target := range []string{"screen", "glossy", "matte"} {
		img := image.NewRGBA(image.Rect(0, 0, 64, 64))
		for y := range 64 {
			for x := range 64 {
				v := uint8(128 + (x+y)%2*2 - 1) // 127/129 checker
				i := img.PixOffset(x, y)
				img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 0xff
			}
		}
		before := clonePix(img)
		ApplyOutputSharpen(img, target, "high")
		for i := range before {
			if img.Pix[i] != before[i] {
				t.Fatalf("target %q amplified sub-threshold ripple at pixel %d", target, i)
			}
		}
	}
}

// TestApplyOutputSharpenExtremes: tiny and clipped images must not panic, and
// results stay valid (clamp8 guarantees range; just exercise the paths).
func TestApplyOutputSharpenExtremes(t *testing.T) {
	tiny := stepImage(3, 3, 0, 255)
	before := clonePix(tiny)
	ApplyOutputSharpen(tiny, "matte", "high")
	for i := range before {
		if tiny.Pix[i] != before[i] {
			t.Fatalf("sub-4px image changed at %d", i)
		}
	}
	for _, v := range []uint8{0, 255} {
		img := stepImage(32, 32, v, v)
		ApplyOutputSharpen(img, "matte", "high")
		for i := 0; i < len(img.Pix); i += 4 {
			if img.Pix[i] != v {
				t.Fatalf("flat %d image changed at %d: %d", v, i, img.Pix[i])
			}
		}
	}
}
