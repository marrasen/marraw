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

	// Raw-pipeline controls. CARed/CABlue are chromatic-aberration channel
	// scales (±1 slider ≈ ±0.2% channel magnification).
	Demosaic Demosaic `json:"demosaic" validate:"omitempty,oneof=vng ppg ahd dht"`
	CARed    float64  `json:"caRed" validate:"gte=-1,lte=1"`
	CABlue   float64  `json:"caBlue" validate:"gte=-1,lte=1"`
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
