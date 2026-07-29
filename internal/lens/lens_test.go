package lens

import (
	"math"
	"testing"
)

func TestLoadEmbeddedDatabase(t *testing.T) {
	db, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(db.Cameras) < 500 {
		t.Errorf("cameras = %d, want a populated database", len(db.Cameras))
	}
	if len(db.Lenses) < 500 {
		t.Errorf("lenses = %d, want a populated database", len(db.Lenses))
	}
	for i := range db.Lenses {
		l := &db.Lenses[i]
		if len(l.Dist) == 0 && len(l.TCA) == 0 && len(l.Vig) == 0 {
			t.Fatalf("lens %q carries no calibration; the generator should have dropped it", l.Model)
		}
	}
}

func TestFindCameraNormalizesEXIFSpellings(t *testing.T) {
	// The maker is shouty, carries a company suffix, and is repeated inside
	// the model — all three happen in real files and none may defeat the
	// lookup.
	for _, tc := range []struct{ maker, model string }{
		{"SONY", "ILCE-7RM2"},
		{"NIKON CORPORATION", "NIKON D850"},
		{"Canon", "Canon EOS 5D Mark IV"},
	} {
		if c := FindCamera(tc.maker, tc.model); c == nil {
			t.Errorf("FindCamera(%q, %q) = nil, want a body", tc.maker, tc.model)
		} else if c.Crop <= 0 {
			t.Errorf("FindCamera(%q, %q).Crop = %v", tc.maker, tc.model, c.Crop)
		}
	}
	if c := FindCamera("Acme", "Pinhole 9000"); c != nil {
		t.Errorf("FindCamera on an unknown body = %v, want nil", c)
	}
}

func TestFindLensMatchesAbbreviatedEXIFNames(t *testing.T) {
	// Left: what the body writes. Right: what the database calls it. The
	// gap between the two — glued mount prefixes, "F2.8" for "f/2.8",
	// missing maker, missing marketing words — is the whole problem.
	for _, tc := range []struct {
		exif, want string
		crop       float64
	}{
		{"FE 24-70mm F2.8 GM", "Sony FE 24-70mm f/2.8 GM", 1.0},
		{"E 50mm F1.8 OSS", "Sony E 50mm f/1.8 OSS", 1.534},
		{"EF24-105mm f/4L IS USM", "Canon Canon EF 24-105mm f/4L IS USM", 1.0},
		{"OLYMPUS M.12-40mm F2.8", "Olympus Olympus M.Zuiko Digital ED 12-40mm f/2.8 Pro", 2.0},
	} {
		l := FindLens(tc.exif, tc.crop)
		if l == nil {
			t.Errorf("FindLens(%q) = nil, want %q", tc.exif, tc.want)
			continue
		}
		if got := l.displayName(); got != tc.want {
			t.Errorf("FindLens(%q) = %q, want %q", tc.exif, got, tc.want)
		}
	}
}

// TestFindLensRejectsTheWrongVariant is the failure this matcher exists to
// prevent. Nikon's 24-70/2.8 comes in two optically different versions, and
// the older one's name is a near-subset of the newer one's: matching on
// "best score wins" picks the G when the file says E VR.
func TestFindLensRejectsTheWrongVariant(t *testing.T) {
	l := FindLens("AF-S NIKKOR 24-70mm f/2.8E ED VR", 1.0)
	if l == nil {
		t.Fatal("FindLens = nil, want the E VR entry")
	}
	if got := l.Model; got == "Nikon AF-S Zoom-Nikkor 24-70mm f/2.8G ED" {
		t.Fatalf("matched the G variant for an E VR file")
	}
}

func TestFindLensRejectsUnknownAndAmbiguousNames(t *testing.T) {
	if l := FindLens("Totally Made Up 99-1000mm F0.5", 1.0); l != nil {
		t.Errorf("FindLens on an invented lens = %q, want nil", l.Model)
	}
	if l := FindLens("", 1.0); l != nil {
		t.Errorf("FindLens on an empty name = %q, want nil", l.Model)
	}
	// A prime's name must not match a zoom that merely spans it.
	if l := FindLens("24mm f/2.8", 1.0); l != nil && l.MinFocal != l.MaxFocal {
		t.Errorf("FindLens(%q) matched the zoom %q", "24mm f/2.8", l.Model)
	}
}

// TestFindLensHonorsImageCircle keeps an APS-C calibration off a full-frame
// body: the measurement never covered the corners the bigger sensor sees.
func TestFindLensHonorsImageCircle(t *testing.T) {
	if l := FindLens("E 50mm F1.8 OSS", 1.534); l == nil {
		t.Fatal("APS-C lens on an APS-C body = nil, want a match")
	}
	if l := FindLens("E 50mm F1.8 OSS", 1.0); l != nil {
		t.Errorf("APS-C lens on a full-frame body = %q, want nil", l.Model)
	}
}

// resolveTestLens is a wide zoom at its wide end — where distortion is
// largest and therefore where the math is easiest to check.
func resolveTestLens(t *testing.T) *Correction {
	t.Helper()
	c := Resolve("SONY", "ILCE-7RM2", "FE 24-70mm F2.8 GM", 24, 2.8)
	if c == nil {
		t.Fatal("Resolve = nil, want a correction")
	}
	return c
}

func TestResolveProducesAllThreeCorrections(t *testing.T) {
	c := resolveTestLens(t)
	if !c.HasDist() {
		t.Error("no distortion resolved")
	}
	if !c.HasTCA {
		t.Error("no TCA resolved")
	}
	if !c.HasVig {
		t.Error("no vignetting resolved")
	}
}

func TestResolveNeedsAKnownBody(t *testing.T) {
	// Without the body there is no crop factor, and without a crop factor
	// the coefficients cannot be placed in the frame at all.
	if c := Resolve("Acme", "Pinhole 9000", "FE 24-70mm F2.8 GM", 24, 2.8); c != nil {
		t.Errorf("Resolve with an unknown body = %+v, want nil", c)
	}
}

func TestWarpFixesTheFrameCentre(t *testing.T) {
	c := resolveTestLens(t)
	w := c.Warp(7952, 5304, Strengths{Distortion: 1, Vignetting: 1, TCA: 1})
	if w == nil {
		t.Fatal("Warp = nil")
	}
	// The correction is radial about the optical centre, so the centre
	// pixel is the one point that cannot move.
	rx, ry, gx, gy, bx, by := w.Source(3976, 2652)
	for _, v := range [][2]float64{{rx, ry}, {gx, gy}, {bx, by}} {
		if math.Abs(v[0]-3976) > 1e-6 || math.Abs(v[1]-2652) > 1e-6 {
			t.Errorf("centre mapped to (%v, %v), want (3976, 2652)", v[0], v[1])
		}
	}
	if g := w.VigGain(3976, 2652); math.Abs(g-1) > 1e-9 {
		t.Errorf("centre vignetting gain = %v, want 1", g)
	}
}

// TestWarpKeepsTheFrameFull is the auto-scale's contract: after correcting,
// every output pixel must still find source pixels to read, or the photo
// grows black edges.
func TestWarpKeepsTheFrameFull(t *testing.T) {
	const w, h = 7952, 5304
	c := resolveTestLens(t)
	warp := c.Warp(w, h, Strengths{Distortion: 1, Vignetting: 1, TCA: 1})
	if warp == nil {
		t.Fatal("Warp = nil")
	}
	// Walk the whole output border, not just the eight points the
	// auto-scale solved for.
	check := func(ox, oy float64) {
		t.Helper()
		rx, ry, gx, gy, bx, by := warp.Source(ox, oy)
		for _, p := range [][2]float64{{rx, ry}, {gx, gy}, {bx, by}} {
			if p[0] < 0 || p[0] > w-1 || p[1] < 0 || p[1] > h-1 {
				t.Fatalf("output (%.0f, %.0f) samples (%.1f, %.1f), outside the %dx%d frame",
					ox, oy, p[0], p[1], w, h)
			}
		}
	}
	for i := range 200 {
		x := float64(i) * (w - 1) / 199
		check(x, 0.5)
		check(x, h-0.5)
	}
	for i := range 200 {
		y := float64(i) * (h - 1) / 199
		check(0.5, y)
		check(w-0.5, y)
	}
}

// TestWarpCorrectsBarrelDistortion checks the direction of the correction,
// which is the one thing a sign error would silently invert: this lens
// barrels at 24mm, so the corrected frame must read its corner content from
// further IN than a straight-through mapping would.
func TestWarpCorrectsBarrelDistortion(t *testing.T) {
	const w, h = 7952, 5304
	c := resolveTestLens(t)
	warp := c.Warp(w, h, Strengths{Distortion: 1})
	if warp == nil {
		t.Fatal("Warp = nil")
	}
	_, _, gx, gy, _, _ := warp.Source(w-1, h-1)
	srcR := math.Hypot(gx-w/2, gy-h/2)
	outR := math.Hypot(w/2-1, h/2-1)
	if srcR >= outR {
		t.Errorf("corner sampled at radius %.1f, want less than the output radius %.1f "+
			"(barrel correction pulls content inward)", srcR, outR)
	}
	// A sane amount: a few percent, not a fisheye unwrap.
	if ratio := srcR / outR; ratio < 0.90 {
		t.Errorf("corner radius ratio %.3f, want a plausible correction", ratio)
	}
}

func TestWarpStrengthScalesTheDisplacement(t *testing.T) {
	const w, h = 7952, 5304
	c := resolveTestLens(t)
	full := c.Warp(w, h, Strengths{Distortion: 1})
	half := c.Warp(w, h, Strengths{Distortion: 0.5})
	if full == nil || half == nil {
		t.Fatal("Warp = nil")
	}
	// Compare the raw polynomial rather than Source, whose auto-scale
	// differs between the two strengths by design.
	x, y := 0.4, 0.3
	fx, _ := full.distort(x, y)
	hx, _ := half.distort(x, y)
	wantHalf := x + (fx-x)/2
	if math.Abs(hx-wantHalf) > 1e-12 {
		t.Errorf("half-strength displacement = %v, want %v", hx, wantHalf)
	}
}

func TestWarpZeroStrengthIsNothing(t *testing.T) {
	c := resolveTestLens(t)
	if w := c.Warp(7952, 5304, Strengths{}); w != nil {
		t.Error("Warp with every strength at zero should be nil")
	}
}

// TestVignettingGainBrightensTheCorners checks the other sign that could be
// silently inverted: correcting fall-off must lighten the corners, never
// darken them.
func TestVignettingGainBrightensTheCorners(t *testing.T) {
	const w, h = 7952, 5304
	c := resolveTestLens(t)
	warp := c.Warp(w, h, Strengths{Vignetting: 1})
	if warp == nil {
		t.Fatal("Warp = nil")
	}
	corner := warp.VigGain(0, 0)
	edge := warp.VigGain(0, h/2)
	if corner <= 1 {
		t.Errorf("corner gain = %v, want > 1", corner)
	}
	if corner <= edge {
		t.Errorf("corner gain %v should exceed the mid-edge gain %v", corner, edge)
	}
	if corner > 4 {
		t.Errorf("corner gain = %v, implausibly large", corner)
	}
}

// TestVignettingOnlyWarpIsNotGeometric lets the renderer skip the resample
// for a lens whose only calibration is a fall-off curve.
func TestVignettingOnlyWarpIsNotGeometric(t *testing.T) {
	c := resolveTestLens(t)
	warp := c.Warp(7952, 5304, Strengths{Vignetting: 1})
	if warp == nil {
		t.Fatal("Warp = nil")
	}
	if warp.Geometric() {
		t.Error("a vignetting-only warp reports itself geometric")
	}
	if !warp.HasVignetting() {
		t.Error("a vignetting-only warp reports no vignetting")
	}
}

// TestInterpolationTracksFocalLength checks that a zoom's coefficients
// actually move between calibration points instead of snapping to one.
func TestInterpolationTracksFocalLength(t *testing.T) {
	l := FindLens("FE 24-70mm F2.8 GM", 1.0)
	if l == nil {
		t.Fatal("FindLens = nil")
	}
	var prev [3]float64
	seen := map[[3]float64]bool{}
	for _, f := range []float64{24, 28, 35, 50, 70} {
		c := l.Resolve(1.0, f, 2.8, 1000)
		if c == nil || !c.HasDist() {
			t.Fatalf("no distortion resolved at %vmm", f)
		}
		if seen[c.Dist] {
			t.Errorf("distortion terms at %vmm repeat an earlier focal length: %v", f, c.Dist)
		}
		seen[c.Dist] = true
		prev = c.Dist
	}
	_ = prev
}

// TestVignettingMatchesTheCalibrationPoint pins the whole vignetting
// coordinate conversion to a number that can be read straight out of the
// database. The "pa" model puts r = 1 at the image corner, so at an exact
// calibration point the corner gain must be 1/(1 + k1 + k2 + k3) — whatever
// the frame's size or orientation. Anything wrong in the rescale, in
// NormScale, or in the sensor-size arithmetic moves this number.
//
//	<vignetting model="pa" focal="50" aperture="1.8" distance="1000"
//	            k1="-0.8304" k2="0.4363" k3="-0.2541"/>
func TestVignettingMatchesTheCalibrationPoint(t *testing.T) {
	const k1, k2, k3 = -0.8304, 0.4363, -0.2541
	want := 1 / (1 + k1 + k2 + k3)

	l := FindLens("E 50mm F1.8 OSS", 1.534)
	if l == nil {
		t.Fatal("FindLens = nil")
	}
	for _, dim := range [][2]int{{6000, 4000}, {3000, 2000}, {4000, 6000}} {
		c := l.Resolve(1.534, 50, 1.8, 1000)
		if c == nil || !c.HasVig {
			t.Fatalf("%dx%d: no vignetting resolved", dim[0], dim[1])
		}
		w := c.Warp(dim[0], dim[1], Strengths{Vignetting: 1})
		if got := w.VigGain(0, 0); math.Abs(got-want) > 1e-9 {
			t.Errorf("%dx%d corner gain = %.9f, want %.9f", dim[0], dim[1], got, want)
		}
	}
}

// TestDistortionMatchesTheCalibrationPoint does the same for the geometry.
// The poly3 coefficients are in Hugin's system, where r = 1 sits at the
// middle of the long edge; lensfun then divides k1 by d³ (d = 1 - k1) so
// that the transformation preserves the focal length. Converting that into
// normalized coordinates has to leave the displacement at Hugin's r = 1
// unchanged, so evaluating the polynomial there must give exactly
// 1 + k1/d³ — on any frame whose aspect ratio matches the calibration's.
//
// The auto-scale is deliberately not in the loop here: it is a framing
// decision on top of the correction, and TestWarpKeepsTheFrameFull covers
// it. This test is about the coefficient conversion alone.
//
//	<distortion model="poly3" focal="50" k1="0.00032"/>
func TestDistortionMatchesTheCalibrationPoint(t *testing.T) {
	const k1 = 0.00032
	d := 1 - k1
	want := 1 + k1/(d*d*d)

	l := FindLens("E 50mm F1.8 OSS", 1.534)
	if l == nil {
		t.Fatal("FindLens = nil")
	}
	for _, dim := range [][2]int{{6000, 4000}, {3000, 2000}} {
		fw, fh := dim[0], dim[1]
		c := l.Resolve(1.534, 50, 1.8, 1000)
		if c == nil || !c.HasDist() {
			t.Fatalf("%dx%d: no distortion resolved", fw, fh)
		}
		w := c.Warp(fw, fh, Strengths{Distortion: 1})
		// Hugin's r = 1: half the frame height, in normalized units.
		r := float64(fh) / 2 * w.normScale
		_, dy := w.distort(0, r)
		if got := dy / r; math.Abs(got-want) > 1e-9 {
			t.Errorf("%dx%d: displacement at Hugin r=1 = %.12f, want %.12f", fw, fh, got, want)
		}
	}
}

// TestFixedLensCameraResolvesByMount covers compacts and bridge cameras,
// which record no lens string at all — there is only one lens and it never
// comes off. The database ties body to lens through a pseudo-mount, and that
// is the only handle available.
func TestFixedLensCameraResolvesByMount(t *testing.T) {
	c := Resolve("Panasonic", "DC-LX100M2", "", 10.9, 1.7)
	if c == nil {
		t.Fatal("Resolve for a fixed-lens body = nil, want its built-in lens")
	}
	if !c.HasDist() || !c.HasVig {
		t.Errorf("resolved %q with dist=%v vig=%v, want both", c.Name, c.HasDist(), c.HasVig)
	}
}

// TestFixedLensLookupIgnoresRealMounts is the guard that keeps the
// fixed-lens shortcut from firing on interchangeable systems, where "the
// mount's only lens" would be an arbitrary pick out of hundreds.
func TestFixedLensLookupIgnoresRealMounts(t *testing.T) {
	for _, mount := range []string{"Sony E", "Canon EF", "Nikon F AF"} {
		if l := FixedLensFor(mount); l != nil {
			t.Errorf("FixedLensFor(%q) = %q, want nil for an interchangeable mount", mount, l.Model)
		}
	}
	if c := Resolve("SONY", "ILCE-7RM2", "", 50, 2.8); c != nil {
		t.Errorf("Resolve with no lens on an interchangeable body = %q, want nil", c.Name)
	}
}
