package api

import (
	"image"
	"testing"

	"github.com/marrasen/marraw/internal/edit"
)

// TestApproxDecodeExposureReuse: a decode stored at one exposure is reused for
// the same photo when only exposure differs, reporting the baked-in ExpEV so
// the caller can fold the delta; a white-balance change (or a different photo)
// misses.
func TestApproxDecodeExposureReuse(t *testing.T) {
	e := &Edits{}
	rgba := image.NewRGBA(image.Rect(0, 0, 2, 2))

	stored := &edit.Params{ExpEV: 0.5, WBTemp: 10}
	key, noExpKey, expEV := decodeKeys(stored)
	e.storeDecode(7, key, noExpKey, expEV, rgba)

	// Same LibRaw inputs, a different exposure, plus look-stage offsets an auto
	// preset layers on (contrast/vignette are post-decode) — must still reuse
	// and report the baked 0.5, since the no-exposure key ignores all of it.
	want := &edit.Params{ExpEV: 1.8, WBTemp: 10, Contrast: 0.4, Vignette: 0.3, Saturation: 0.2}
	got, baked, ok := e.approxDecode(7, want)
	if !ok {
		t.Fatal("exposure-only change did not reuse the decode")
	}
	if got != rgba {
		t.Error("reused a different rgba than stored")
	}
	if baked != 0.5 {
		t.Errorf("baked ExpEV = %v, want 0.5", baked)
	}

	// A different photo misses.
	if _, _, ok := e.approxDecode(8, want); ok {
		t.Error("reused a decode across photos")
	}

	// A white-balance change (a real LibRaw input) misses.
	wbChanged := &edit.Params{ExpEV: 1.8, WBTemp: 40}
	if _, _, ok := e.approxDecode(7, wbChanged); ok {
		t.Error("reused a decode across a white-balance change")
	}
}
