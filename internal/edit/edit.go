// Package edit defines the non-destructive edit state of a photo and its
// mapping onto LibRaw processing parameters.
package edit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"

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

// MaskType tags the geometry variant of a local adjustment mask.
type MaskType string

const (
	MaskLinear MaskType = "linear"
	MaskRadial MaskType = "radial"
	MaskBrush  MaskType = "brush"
	MaskAI     MaskType = "ai"
)

// AIKind selects which model-generated map an AI mask samples.
type AIKind string

const (
	// AISubject is the salient-subject matte (continuous 0..255 coverage).
	AISubject AIKind = "subject"
	// AIClass is the semantic category map (pixel = photographer-category ID,
	// see pyramid's category table; 0 = uncategorized).
	AIClass AIKind = "class"
	// AIDepth is the normalized relative depth map (255 = nearest).
	AIDepth AIKind = "depth"
)

func AIKindValues() []AIKind { return []AIKind{AISubject, AIClass, AIDepth} }

// MaskAdjust is the adjustment a mask applies inside its weighted region:
// the tone and color basics, all with zero neutral. Kept slice-free so the
// == neutrality check stays valid even as Mask itself grows slice fields.
type MaskAdjust struct {
	ExpEV          float64 `json:"expEV,omitempty"`          // -4..4 EV
	Contrast       float64 `json:"contrast,omitempty"`       // ±1
	ToneHighlights float64 `json:"toneHighlights,omitempty"` // ±1
	ToneShadows    float64 `json:"toneShadows,omitempty"`    // ±1
	Whites         float64 `json:"whites,omitempty"`         // ±1
	Blacks         float64 `json:"blacks,omitempty"`         // ±1
	Temp           float64 `json:"temp,omitempty"`           // ±1, warm/cool
	Tint           float64 `json:"tint,omitempty"`           // ±1, green/magenta
	Saturation     float64 `json:"saturation,omitempty"`     // ±1
}

// IsNeutral reports whether the mask's adjustment changes nothing.
func (a *MaskAdjust) IsNeutral() bool { return *a == MaskAdjust{} }

// Stroke is one brush stroke: a polyline of feathered circular stamps.
// Coordinates are fractions of the oriented frame (like the crop rectangle);
// Radius is a fraction of the frame's long edge so strokes are resolution
// independent.
type Stroke struct {
	Erase   bool      `json:"erase,omitempty"`
	Radius  float64   `json:"radius"`
	Feather float64   `json:"feather,omitempty"` // 0..1 of Radius
	Flow    float64   `json:"flow,omitempty"`    // 0 means full (1.0)
	Pts     []float64 `json:"pts"`               // flattened x0,y0,x1,y1,…
}

// Mask is one local adjustment: a weighted region plus the adjustment it
// applies there. Geometry is stored in fractional coordinates of the oriented
// frame (after quarter-rotate/FlipH, before straighten and crop — the same
// space as the crop rectangle) so masks stay glued to image content across
// recrop and re-straighten. Masks apply in list order.
type Mask struct {
	Type   MaskType `json:"type"`
	Invert bool     `json:"invert,omitempty"`
	// Linear gradient: weight 1 at A(x0,y0) ramping to 0 at B(x1,y1);
	// the A→B span is the feather.
	X0 float64 `json:"x0,omitempty"`
	Y0 float64 `json:"y0,omitempty"`
	X1 float64 `json:"x1,omitempty"`
	Y1 float64 `json:"y1,omitempty"`
	// Radial ellipse: center and radii as fractions of the frame width and
	// height, rotated by Angle degrees; Feather softens from the edge inward.
	CX      float64 `json:"cx,omitempty"`
	CY      float64 `json:"cy,omitempty"`
	RX      float64 `json:"rx,omitempty"`
	RY      float64 `json:"ry,omitempty"`
	Angle   float64 `json:"angle,omitempty"`
	Feather float64 `json:"feather,omitempty"`
	// Brush: feathered stamps accumulated along stroke polylines.
	Strokes []Stroke `json:"strokes,omitempty"`
	// AI: weight sampled from a model-generated map cached per photo on this
	// machine (pyramid.AIMapStore) — the map is derived data, only its
	// reference lives here so sidecars stay small and portable. AIKind picks
	// the map; MapVer pins the model version the map was generated with (part
	// of the hash, so regenerating with a newer model re-renders). ClassID is
	// the photographer category for "class" kinds; DepthLo/DepthHi bound the
	// kept depth window (0..1, 1 = nearest) for "depth"; Threshold moves the
	// subject matte cutoff (0 = default 0.5). Feather is reused as the edge
	// softness control for all three kinds.
	AIKind    AIKind  `json:"aiKind,omitempty"`
	MapVer    string  `json:"mapVer,omitempty"`
	ClassID   int     `json:"classId,omitempty"`
	DepthLo   float64 `json:"depthLo,omitempty"`
	DepthHi   float64 `json:"depthHi,omitempty"`
	Threshold float64 `json:"threshold,omitempty"`

	Adjust MaskAdjust `json:"adjust"`
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

	// HSL color mixer, also in the look stage: per-band hue shift, chroma
	// scale, and luminance scale, each ±1 with 0 neutral. Bands run red,
	// orange, yellow, green, aqua, blue, purple, magenta (centers at
	// 0/30/60/120/180/240/280/320° — see pyramid.HSLBandCenters). Hue shifts
	// up to ±30°, Sat scales chroma toward 0..2×, Lum darkens or brightens
	// the band's pixels. Normalize clamps out-of-range stored values.
	HSLHue [8]float64 `json:"hslHue"`
	HSLSat [8]float64 `json:"hslSat"`
	HSLLum [8]float64 `json:"hslLum"`

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

	// Masks are the local adjustments, applied in order in the look stage
	// (pyramid.ApplyMasks). Kept last with omitempty so mask-free edits
	// marshal byte-identically to older builds and existing hashes stay
	// stable; the wire validator doesn't dive into the slice, so Normalize
	// clamps mask fields (the HSL-array precedent). The subset hashes
	// (librawInputs/linearInputs) never copy this field, keeping mask drags
	// on the warm decode. NOTE: the slice makes Params non-comparable —
	// IsNeutral uses reflect.DeepEqual, never ==.
	Masks []Mask `json:"masks,omitempty"`
}

// RotateTurns returns the coarse rotation as canonical quarter turns
// clockwise in 0..3 (nil-safe; stored values outside the range wrap).
func (e *Params) RotateTurns() int {
	if e == nil {
		return 0
	}
	return ((e.Rotate % 4) + 4) % 4
}

// HasHSL reports whether any color-mixer band carries an adjustment.
func (e *Params) HasHSL() bool {
	if e == nil {
		return false
	}
	for i := range e.HSLHue {
		if e.HSLHue[i] != 0 || e.HSLSat[i] != 0 || e.HSLLum[i] != 0 {
			return true
		}
	}
	return false
}

// HasMasks reports whether any local adjustment mask is present.
func (e *Params) HasMasks() bool {
	return e != nil && len(e.Masks) > 0
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
	// Mixer bands are unvalidated arrays (the wire validator doesn't dive
	// into them), so clamp here instead.
	for i := range e.HSLHue {
		e.HSLHue[i] = clamp(e.HSLHue[i], -1, 1)
		e.HSLSat[i] = clamp(e.HSLSat[i], -1, 1)
		e.HSLLum[i] = clamp(e.HSLLum[i], -1, 1)
	}
	e.normalizeMasks()
}

// normalizeMasks clamps and canonicalizes the local adjustment masks so
// equivalent states hash identically: each type zeroes the other types'
// geometry, unknown types are dropped, and brush geometry is quantized so
// pointer-event float noise doesn't churn hashes. Masks with a neutral
// adjustment are kept — a just-created mask must survive a save — and are
// skipped per-mask at render time instead.
func (e *Params) normalizeMasks() {
	if len(e.Masks) == 0 {
		e.Masks = nil
		return
	}
	// Build fresh slices throughout: IsNeutral and Hash normalize a shallow
	// copy of the receiver, so mutating shared backing arrays here would
	// corrupt the caller's masks.
	kept := make([]Mask, 0, len(e.Masks))
	for _, m := range e.Masks {
		switch m.Type {
		case MaskLinear:
			m.CX, m.CY, m.RX, m.RY, m.Angle, m.Feather = 0, 0, 0, 0, 0, 0
			m.Strokes = nil
			m.clearAI()
			m.X0, m.Y0 = clampFrac(m.X0), clampFrac(m.Y0)
			m.X1, m.Y1 = clampFrac(m.X1), clampFrac(m.Y1)
		case MaskRadial:
			m.X0, m.Y0, m.X1, m.Y1 = 0, 0, 0, 0
			m.Strokes = nil
			m.clearAI()
			m.CX, m.CY = clampFrac(m.CX), clampFrac(m.CY)
			m.RX = clamp(m.RX, 0.001, 2)
			m.RY = clamp(m.RY, 0.001, 2)
			// An ellipse is symmetric under a half turn.
			m.Angle = math.Mod(math.Mod(m.Angle, 180)+180, 180)
			m.Feather = clamp(m.Feather, 0, 1)
		case MaskBrush:
			m.X0, m.Y0, m.X1, m.Y1 = 0, 0, 0, 0
			m.CX, m.CY, m.RX, m.RY, m.Angle, m.Feather = 0, 0, 0, 0, 0, 0
			m.clearAI()
			var strokes []Stroke
			for _, s := range m.Strokes {
				n := len(s.Pts) &^ 1 // drop an odd trailing coordinate
				if n < 2 {
					continue
				}
				s.Radius = quant4(clamp(s.Radius, 0.001, 1))
				s.Feather = quant4(clamp(s.Feather, 0, 1))
				s.Flow = quant4(clamp(s.Flow, 0, 1))
				pts := make([]float64, n)
				for i := range n {
					pts[i] = quant4(clampFrac(s.Pts[i]))
				}
				s.Pts = pts
				strokes = append(strokes, s)
			}
			if len(strokes) == 0 {
				strokes = nil
			}
			m.Strokes = strokes
		case MaskAI:
			m.X0, m.Y0, m.X1, m.Y1 = 0, 0, 0, 0
			m.CX, m.CY, m.RX, m.RY, m.Angle = 0, 0, 0, 0, 0
			m.Strokes = nil
			m.Feather = quant4(clamp(m.Feather, 0, 1))
			switch m.AIKind {
			case AISubject:
				m.ClassID, m.DepthLo, m.DepthHi = 0, 0, 0
				m.Threshold = quant4(clamp(m.Threshold, 0, 1))
			case AIClass:
				m.DepthLo, m.DepthHi, m.Threshold = 0, 0, 0
				m.ClassID = int(clamp(float64(m.ClassID), 0, 255))
			case AIDepth:
				m.ClassID, m.Threshold = 0, 0
				m.DepthLo = quant4(clamp(m.DepthLo, 0, 1))
				m.DepthHi = quant4(clamp(m.DepthHi, 0, 1))
				if m.DepthHi < m.DepthLo {
					m.DepthLo, m.DepthHi = m.DepthHi, m.DepthLo
				}
			default:
				continue // unknown kind: drop, like an unknown mask type
			}
		default:
			continue
		}
		m.Adjust.ExpEV = clamp(m.Adjust.ExpEV, -4, 4)
		m.Adjust.Contrast = clamp(m.Adjust.Contrast, -1, 1)
		m.Adjust.ToneHighlights = clamp(m.Adjust.ToneHighlights, -1, 1)
		m.Adjust.ToneShadows = clamp(m.Adjust.ToneShadows, -1, 1)
		m.Adjust.Whites = clamp(m.Adjust.Whites, -1, 1)
		m.Adjust.Blacks = clamp(m.Adjust.Blacks, -1, 1)
		m.Adjust.Temp = clamp(m.Adjust.Temp, -1, 1)
		m.Adjust.Tint = clamp(m.Adjust.Tint, -1, 1)
		m.Adjust.Saturation = clamp(m.Adjust.Saturation, -1, 1)
		kept = append(kept, m)
	}
	if len(kept) == 0 {
		kept = nil
	}
	e.Masks = kept
}

// clearAI zeroes the AI-mask fields on non-AI mask types so equivalent
// states hash identically.
func (m *Mask) clearAI() {
	m.AIKind, m.MapVer = "", ""
	m.ClassID = 0
	m.DepthLo, m.DepthHi, m.Threshold = 0, 0, 0
}

// clampFrac bounds a fractional frame coordinate; masks may hang partly
// off-frame, so allow half a frame of overhang on each side.
func clampFrac(v float64) float64 { return clamp(v, -0.5, 1.5) }

// quant4 rounds to 1e-4 so brush geometry hashes deterministically.
func quant4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

// IsNeutral reports whether the edit changes nothing; neutral edits are
// stored as NULL and rendered as the base look.
func (e *Params) IsNeutral() bool {
	if e == nil {
		return true
	}
	n := *e
	n.Normalize()
	// Masks make Params non-comparable, so == is unavailable; DeepEqual runs
	// per RPC (never per pixel) and the cost is irrelevant there.
	return reflect.DeepEqual(n, Params{})
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
