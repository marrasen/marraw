// Package edit defines the non-destructive edit state of a photo and its
// mapping onto LibRaw processing parameters.
package edit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"

	"github.com/marrasen/marraw/internal/libraw"
)

// BaseHash identifies the un-edited rendition in the pyramid cache.
const BaseHash = "base"

// WBMode selects how white balance is chosen.
type WBMode string

const (
	WBCamera WBMode = "camera"
	WBAuto   WBMode = "auto"
	WBCustom WBMode = "custom"
	WBKelvin WBMode = "kelvin"
)

func WBModeValues() []WBMode { return []WBMode{WBCamera, WBAuto, WBCustom, WBKelvin} }

// Demosaic selects the demosaic algorithm; empty means marraw's default
// (AHD, with the faster PPG substituted on interactive 1:1 renders).
type Demosaic string

const (
	DemosaicDefault Demosaic = ""
	DemosaicVNG     Demosaic = "vng"
	DemosaicPPG     Demosaic = "ppg"
	DemosaicAHD     Demosaic = "ahd"
	DemosaicDHT     Demosaic = "dht"
)

func DemosaicValues() []Demosaic {
	return []Demosaic{DemosaicVNG, DemosaicPPG, DemosaicAHD, DemosaicDHT}
}

// Params is the edit state, stored as JSON in photos.edit_params.
// The zero value is the neutral edit. Every field's zero value must mean
// "default" — IsNeutral and hashing rely on it, and it keeps stored JSON
// from older versions forward-compatible.
type Params struct {
	ExpEV       float64    `json:"expEV" validate:"gte=-2,lte=3"`
	ExpPreserve float64    `json:"expPreserve" validate:"gte=0,lte=1"`
	WBMode      WBMode     `json:"wbMode" validate:"omitempty,oneof=camera auto custom kelvin"`
	WBMul       [4]float64 `json:"wbMul"`
	// WBTemp/WBTint warm/shift the white balance relative to the selected
	// base (as-shot or picked custom multipliers): ±1 ≈ ±1 stop on the
	// R/B (temp) or G (tint) multipliers. Ignored in auto mode.
	WBTemp float64 `json:"wbTemp" validate:"gte=-1,lte=1"`
	WBTint float64 `json:"wbTint" validate:"gte=-1,lte=1"`
	// WBKelvin is the absolute color temperature used when WBMode is
	// "kelvin" (0 = unset). Computed into multipliers via the camera's
	// XYZ matrix; WBTint still applies on top.
	WBKelvin float64 `json:"wbKelvin" validate:"omitempty,gte=1700,lte=25000"`
	Bright   float64 `json:"bright" validate:"gte=0,lte=4"` // 0 = default (1.0)
	// Gamma is the display gamma power (contrast): 0 = default (BT.709,
	// 2.222). Higher lifts midtones (flatter), lower darkens (punchier).
	Gamma float64 `json:"gamma" validate:"gte=0,lte=3.5"`
	// Shadow is the gamma toe slope: 0 = default (4.5). Higher darkens
	// deep shadows, lower lifts them.
	Shadow      float64 `json:"shadow" validate:"gte=0,lte=12"`
	Highlight   int     `json:"highlight" validate:"gte=0,lte=9"`
	NRThreshold float64 `json:"nrThreshold" validate:"gte=0,lte=1000"`
	FBDDNoiseRd int     `json:"fbddNoiseRd" validate:"gte=0,lte=2"`
	MedPasses   int     `json:"medPasses" validate:"gte=0,lte=5"`

	// Tone controls applied in the display-look stage (pyramid.ApplyLook)
	// after LibRaw output, all ±1 with 0 neutral: Contrast steepens the
	// S-curve, Whites/Blacks move the endpoints, ToneShadows/ToneHighlights
	// lift or pull their luminance region.
	Contrast       float64 `json:"contrast" validate:"gte=-1,lte=1"`
	Whites         float64 `json:"whites" validate:"gte=-1,lte=1"`
	Blacks         float64 `json:"blacks" validate:"gte=-1,lte=1"`
	ToneShadows    float64 `json:"toneShadows" validate:"gte=-1,lte=1"`
	ToneHighlights float64 `json:"toneHighlights" validate:"gte=-1,lte=1"`

	// Color controls, also in the look stage. Saturation scales the base
	// look's boost (-1 = grayscale); Vibrance weights the boost toward
	// low-saturation pixels. Split toning tints shadows/highlights toward a
	// hue (degrees) by an amount (0..1).
	Saturation        float64 `json:"saturation" validate:"gte=-1,lte=1"`
	Vibrance          float64 `json:"vibrance" validate:"gte=-1,lte=1"`
	SplitShadowHue    float64 `json:"splitShadowHue" validate:"gte=0,lt=360"`
	SplitShadowAmt    float64 `json:"splitShadowAmt" validate:"gte=0,lte=1"`
	SplitHighlightHue float64 `json:"splitHighlightHue" validate:"gte=0,lt=360"`
	SplitHighlightAmt float64 `json:"splitHighlightAmt" validate:"gte=0,lte=1"`

	// Vignette darkens (>0) or brightens (<0) toward the corners.
	Vignette float64 `json:"vignette" validate:"gte=-1,lte=1"`

	// Detail controls, applied after the look stage (pyramid.ApplyDetail).
	// Texture and Clarity are local-contrast ops at fine vs. midtone-weighted
	// large radius; Dehaze subtracts (or, negative, adds) the estimated
	// atmospheric veil; Sharpen is an unsharp mask at output resolution.
	Texture float64 `json:"texture" validate:"gte=-1,lte=1"`
	Clarity float64 `json:"clarity" validate:"gte=-1,lte=1"`
	Dehaze  float64 `json:"dehaze" validate:"gte=-1,lte=1"`
	Sharpen float64 `json:"sharpen" validate:"gte=0,lte=1"`

	// Raw-pipeline controls. CARed/CABlue are chromatic-aberration channel
	// scales (±1 slider ≈ ±0.2% channel magnification).
	Demosaic Demosaic `json:"demosaic" validate:"omitempty,oneof=vng ppg ahd dht"`
	CARed    float64  `json:"caRed" validate:"gte=-1,lte=1"`
	CABlue   float64  `json:"caBlue" validate:"gte=-1,lte=1"`

	// Crop + straighten, applied as a post-decode geometry stage in display
	// (orientation-corrected) space. Rotate turns the frame in quarter turns
	// clockwise (0..3) and FlipH then mirrors it about the vertical axis,
	// both BEFORE the crop — so the crop rectangle and straighten angle live
	// in the rotated-and-mirrored frame (a vertical flip is FlipH plus two
	// turns). CropW/CropH == 0 means "no crop" (the full frame); when set
	// they are the rectangle size as a fraction of the frame, with
	// CropX/CropY its top-left, all in [0,1]. CropAngle levels the horizon in
	// degrees: the frame is rotated about its center and the axis-aligned
	// crop rectangle is taken from the rotated result.
	Rotate    int     `json:"rotate" validate:"gte=0,lte=3"`
	FlipH     bool    `json:"flipH"`
	CropX     float64 `json:"cropX" validate:"gte=0,lte=1"`
	CropY     float64 `json:"cropY" validate:"gte=0,lte=1"`
	CropW     float64 `json:"cropW" validate:"gte=0,lte=1"`
	CropH     float64 `json:"cropH" validate:"gte=0,lte=1"`
	CropAngle float64 `json:"cropAngle" validate:"gte=-15,lte=15"`
}

// RotateTurns returns the coarse rotation as canonical quarter turns
// clockwise in 0..3 (nil-safe; stored values outside the range wrap).
func (e *Params) RotateTurns() int {
	if e == nil {
		return 0
	}
	return ((e.Rotate % 4) + 4) % 4
}

// HasCrop reports whether a crop rectangle is set (a straighten angle alone
// does not crop — it rotates the full frame). Callers that need to know
// whether the rendered dimensions differ from the sensor use this.
func (e *Params) HasCrop() bool {
	return e != nil && e.CropW > 0 && e.CropH > 0
}

// OutputDims maps the full display-space dimensions (fullW×fullH, already
// orientation-corrected) to the rendered dimensions after the coarse
// rotation and crop. An odd Rotate swaps the axes; the straighten angle
// rotates within the frame and does not change the output size. A nil or
// neutral-geometry edit returns the input unchanged. Both sides of the wire
// compute this identically (mirrored in client/src/lib/crop.ts) so the loupe
// box, tile grid and dimension-healing all agree without a round trip.
func (e *Params) OutputDims(fullW, fullH int) (w, h int) {
	if e.RotateTurns()%2 != 0 {
		fullW, fullH = fullH, fullW
	}
	if !e.HasCrop() {
		return fullW, fullH
	}
	w = int(math.Round(e.CropW * float64(fullW)))
	h = int(math.Round(e.CropH * float64(fullH)))
	return max(1, w), max(1, h)
}

// Normalize canonicalizes equivalent states so hashing is stable: "camera"
// is the implicit WB default, multipliers only matter in custom mode,
// temp/tint only outside auto mode, Kelvin only in kelvin mode (where it
// replaces the relative temp), and split-tone hues only with an amount.
func (e *Params) Normalize() {
	if e == nil {
		return
	}
	if e.WBMode == WBCamera {
		e.WBMode = ""
	}
	if e.WBMode != WBCustom {
		e.WBMul = [4]float64{}
	}
	if e.WBMode == WBAuto {
		e.WBTemp, e.WBTint = 0, 0
	}
	if e.WBMode != WBKelvin {
		e.WBKelvin = 0
	} else {
		e.WBTemp = 0
	}
	if e.SplitShadowAmt == 0 {
		e.SplitShadowHue = 0
	}
	if e.SplitHighlightAmt == 0 {
		e.SplitHighlightHue = 0
	}
	// A degenerate or full-frame crop is no crop: clear the rectangle so it
	// hashes identically to neutral (a bare straighten angle is kept).
	if !e.HasCrop() || (e.CropX == 0 && e.CropY == 0 && e.CropW >= 1 && e.CropH >= 1) {
		e.CropX, e.CropY, e.CropW, e.CropH = 0, 0, 0, 0
	}
	// Full turns are neutral; canonicalize so 4 hashes like 0.
	e.Rotate = e.RotateTurns()
}

// IsNeutral reports whether the edit changes nothing; neutral edits are
// stored as NULL and rendered as the base look.
func (e *Params) IsNeutral() bool {
	if e == nil {
		return true
	}
	n := *e
	n.Normalize()
	return n == Params{}
}

// Hash returns the short content hash identifying this edit state in
// pyramid cache file names. Go's json.Marshal emits struct fields in
// declaration order, so the encoding is canonical.
func (e *Params) Hash() string {
	if e.IsNeutral() {
		return BaseHash
	}
	n := *e
	n.Normalize()
	b, _ := json.Marshal(&n)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// LibrawInputsHash hashes only the fields that change the LibRaw decode
// (exposure, WB, brightness/gamma/shadow, highlight recovery, NR, demosaic,
// CA). The geometry (crop/straighten) and look stages run on top of the
// decoded pixels, so two edits differing only in those share one decode —
// this keys the preview decode cache, letting look/geometry sliders skip the
// ~400 ms demosaic. Always returns a fixed-width hash (never BaseHash), so a
// deterministic edit whose LibRaw subset happens to be neutral still keys
// apart from the auto-brighten base render.
func (e *Params) LibrawInputsHash() string {
	l := e.librawInputs()
	l.Normalize()
	b, _ := json.Marshal(&l)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// LibrawInputsHashNoExp is LibrawInputsHash with exposure (ExpEV/ExpPreserve)
// excluded, so two decodes that differ only in exposure share one hash. The
// transient preview path uses this to reuse a warm decode across an
// exposure-only change and fold the difference in post-decode (pyramid's
// RenderPreview expDeltaEV); the accurate cache render still keys on the full
// LibrawInputsHash and re-demosaics at the exact exposure.
func (e *Params) LibrawInputsHashNoExp() string {
	l := e.librawInputs()
	l.ExpEV, l.ExpPreserve = 0, 0
	l.Normalize()
	b, _ := json.Marshal(&l)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// librawInputs returns the subset of params that change the LibRaw decode.
func (e *Params) librawInputs() Params {
	return Params{
		ExpEV: e.ExpEV, ExpPreserve: e.ExpPreserve,
		WBMode: e.WBMode, WBMul: e.WBMul, WBTemp: e.WBTemp, WBTint: e.WBTint, WBKelvin: e.WBKelvin,
		Bright: e.Bright, Gamma: e.Gamma, Shadow: e.Shadow,
		Highlight: e.Highlight, NRThreshold: e.NRThreshold, FBDDNoiseRd: e.FBDDNoiseRd, MedPasses: e.MedPasses,
		Demosaic: e.Demosaic, CARed: e.CARed, CABlue: e.CABlue,
	}
}

// LinearInputsHash hashes only the fields that change the scene-linear
// reference decode — the genuinely pre-demosaic controls (highlight recovery,
// noise reduction, demosaic algorithm, chromatic aberration). White balance,
// exposure, brightness and gamma are folded post-decode on the interactive
// path (see pyramid.RenderPreviewLinear), so they do NOT invalidate the
// reference and dragging them never re-demosaics. Always a fixed-width hash.
func (e *Params) LinearInputsHash() string {
	l := e.linearInputs()
	l.Normalize()
	b, _ := json.Marshal(&l)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// linearInputs returns the subset of params that change the linear reference
// decode: everything upstream of demosaic that the fold pass cannot reproduce.
func (e *Params) linearInputs() Params {
	if e == nil {
		return Params{}
	}
	return Params{
		Highlight: e.Highlight, NRThreshold: e.NRThreshold,
		FBDDNoiseRd: e.FBDDNoiseRd, MedPasses: e.MedPasses,
		Demosaic: e.Demosaic, CARed: e.CARed, CABlue: e.CABlue,
	}
}

// Parse decodes stored edit-params JSON.
func Parse(paramsJSON string) (*Params, error) {
	var e Params
	if err := json.Unmarshal([]byte(paramsJSON), &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// LibrawParams maps the edit state onto LibRaw processing parameters.
// A nil receiver produces the "base" look (camera WB, auto-brighten).
// Any edit disables auto-brighten so sliders behave deterministically.
func (e *Params) LibrawParams(halfSize bool) libraw.Params {
	p := libraw.DefaultParams()
	p.HalfSize = halfSize
	if e == nil {
		// Base look: auto-bright plus the pyramid baseline LUT. Note that a
		// pre-demosaic exp_shift is pointless here — auto-bright re-normalizes
		// the histogram and cancels it — so tone shaping lives in the LUT.
		return p
	}
	p.NoAutoBright = true
	p.ExpShift = math.Pow(2, e.ExpEV)
	p.ExpPreserve = e.ExpPreserve
	switch e.WBMode {
	case WBAuto:
		p.UseCameraWB = false
		p.UseAutoWB = true
	case WBCustom:
		p.UseCameraWB = false
		p.UserMul = e.WBMul
	case WBKelvin:
		if e.WBKelvin > 0 {
			p.UseCameraWB = false
			p.WBKelvin = e.WBKelvin
		}
	}
	if e.WBMode != WBAuto {
		p.WBTemp = e.WBTemp
		p.WBTint = e.WBTint
	}
	if e.Bright > 0 {
		p.Bright = e.Bright
	}
	if e.Gamma > 0 || e.Shadow > 0 {
		g := e.Gamma
		if g == 0 {
			g = 2.222
		}
		s := e.Shadow
		if s == 0 {
			s = 4.5
		}
		p.Gamma = [2]float64{1 / g, s}
	}
	p.Highlight = e.Highlight
	p.Threshold = e.NRThreshold
	p.FBDDNoiseRd = e.FBDDNoiseRd
	p.MedPasses = e.MedPasses
	if q, ok := demosaicQual[e.Demosaic]; ok {
		p.UserQual = q
	}
	if e.CARed != 0 {
		p.CARed = 1 + e.CARed*caScale
	}
	if e.CABlue != 0 {
		p.CABlue = 1 + e.CABlue*caScale
	}
	return p
}

// LinearRefLibrawParams maps the edit onto LibRaw params for the scene-linear
// reference decode: the pre-demosaic controls (demosaic algorithm, CA, NR,
// highlight recovery) are honored, but white balance, exposure, brightness and
// gamma are neutralized — 16-bit linear output at the camera's as-shot WB —
// because the interactive fold reproduces those afterward as a cheap per-pixel
// pass. Half-size, matching the preview decode. A nil receiver is treated as
// neutral (the reference still decodes deterministically, no auto-brighten).
func (e *Params) LinearRefLibrawParams() libraw.Params {
	p := e.LibrawParams(true) // reuse the pre-demosaic mapping (demosaic, CA, NR, highlight)
	p.OutputBPS = 16
	p.Gamma = [2]float64{1, 1} // linear output, no encoding
	p.ExpShift, p.ExpPreserve = 0, 0
	p.Bright = 0 // apply() reads 0 as the neutral 1.0
	p.UseCameraWB, p.UseAutoWB = true, false
	p.UserMul = [4]float64{}
	p.WBTemp, p.WBTint, p.WBKelvin = 0, 0, 0
	p.NoAutoBright = true
	return p
}

// caScale maps the ±1 CA sliders onto channel magnification: ±0.2% shifts
// the channel by ~8 px at the edge of an 8000 px sensor — beyond any real
// lateral CA.
const caScale = 0.002

var demosaicQual = map[Demosaic]int{
	DemosaicVNG: libraw.DemosaicVNG,
	DemosaicPPG: libraw.DemosaicPPG,
	DemosaicAHD: libraw.DemosaicAHD,
	DemosaicDHT: libraw.DemosaicDHT,
}

// Delta is a relative adjustment applied to many photos at once.
// Nil fields are untouched.
type Delta struct {
	ExpEV          *float64 `json:"expEV"`
	Bright         *float64 `json:"bright"`
	Highlight      *int     `json:"highlight"`
	NRThreshold    *float64 `json:"nrThreshold"`
	FBDDNoiseRd    *int     `json:"fbddNoiseRd"`
	MedPasses      *int     `json:"medPasses"`
	Contrast       *float64 `json:"contrast"`
	Whites         *float64 `json:"whites"`
	Blacks         *float64 `json:"blacks"`
	ToneShadows    *float64 `json:"toneShadows"`
	ToneHighlights *float64 `json:"toneHighlights"`
	Saturation     *float64 `json:"saturation"`
	Vibrance       *float64 `json:"vibrance"`
}

// Apply merges the delta into params, clamping to valid ranges.
func (d Delta) Apply(e *Params) {
	if d.ExpEV != nil {
		e.ExpEV = clamp(e.ExpEV+*d.ExpEV, -2, 3)
	}
	if d.Bright != nil {
		base := e.Bright
		if base == 0 {
			base = 1
		}
		e.Bright = clamp(base+*d.Bright, 0.25, 4)
	}
	if d.Highlight != nil {
		e.Highlight = int(clamp(float64(e.Highlight+*d.Highlight), 0, 9))
	}
	if d.NRThreshold != nil {
		e.NRThreshold = clamp(e.NRThreshold+*d.NRThreshold, 0, 1000)
	}
	if d.FBDDNoiseRd != nil {
		e.FBDDNoiseRd = int(clamp(float64(*d.FBDDNoiseRd), 0, 2))
	}
	if d.MedPasses != nil {
		e.MedPasses = int(clamp(float64(e.MedPasses+*d.MedPasses), 0, 5))
	}
	for _, f := range []struct {
		delta *float64
		field *float64
	}{
		{d.Contrast, &e.Contrast},
		{d.Whites, &e.Whites},
		{d.Blacks, &e.Blacks},
		{d.ToneShadows, &e.ToneShadows},
		{d.ToneHighlights, &e.ToneHighlights},
		{d.Saturation, &e.Saturation},
		{d.Vibrance, &e.Vibrance},
	} {
		if f.delta != nil {
			*f.field = clamp(*f.field+*f.delta, -1, 1)
		}
	}
}

func clamp(v, lo, hi float64) float64 {
	return math.Min(math.Max(v, lo), hi)
}
