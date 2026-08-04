// Package edit defines the non-destructive edit state of a photo and its
// mapping onto LibRaw processing parameters.
package edit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"
	"sort"

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

// LensMode selects whether the matched lens profile is applied. Empty is
// automatic, which is the default for every photo: a profile is a
// measurement of what the lens did to the frame, so undoing it is the
// neutral starting point, not an effect.
type LensMode string

const (
	LensAuto LensMode = ""
	LensOff  LensMode = "off"
)

func LensModeValues() []LensMode { return []LensMode{LensAuto, LensOff} }

// MaskType tags the geometry variant of a local adjustment mask.
type MaskType string

const (
	MaskLinear MaskType = "linear"
	MaskRadial MaskType = "radial"
	MaskBrush  MaskType = "brush"
	MaskAI     MaskType = "ai"
	// MaskRange selects pixels by their own developed value: a luminance
	// window and/or a color (hue) window, rather than by geometry or a model
	// map. Coverage is computed from the render's own pixels, so it needs no
	// stored plane.
	MaskRange MaskType = "range"
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
	// AIPerson is the person instance map (pixel = instance ID, 0 =
	// background, 1..N ordered left-to-right by centroid).
	AIPerson AIKind = "person"
	// AIBackground is everything the subject matte is NOT. It stores no map of
	// its own — it samples the subject matte (see MapKind) and inverts at
	// evaluation time — so the panel can say "Background" outright instead of
	// showing a Subject mask with the Invert pill lit. Note that a build
	// predating this kind drops such a mask in Normalize (unknown kind), so a
	// sidecar written here loses its background masks on an older build.
	AIBackground AIKind = "background"
)

func AIKindValues() []AIKind {
	return []AIKind{AISubject, AIClass, AIDepth, AIPerson, AIBackground}
}

// MapKind is the kind whose stored map this kind samples: background shares the
// subject matte, every other kind owns its map. Generation, the model lookup,
// the map file name and the render's set key all key on this, so a background
// mask never triggers a second inference pass or a duplicate map file.
func (k AIKind) MapKind() AIKind {
	if k == AIBackground {
		return AISubject
	}
	return k
}

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
	// Spatial FX. Unlike the tone fields above these are not point
	// operations: they gather neighbouring pixels, so the render materializes
	// this mask's weight plane and gathers through it (pyramid.applyMaskFX).
	// All lengths are fractions of the oriented frame's LONG EDGE — the
	// Stroke.Radius contract — so a 1024 draft, a 2048 settle, a 1:1 tile and
	// an export agree. Appended after the tone fields on purpose: json.Marshal
	// emits declaration order, so every pre-FX sidecar and edit hash stays
	// byte-identical.
	Blur       float64 `json:"blur,omitempty"`       // 0..1 defocus, radius to 6% of the long edge
	MotionBlur float64 `json:"motionBlur,omitempty"` // 0..1 directional smear, to 15%
	ZoomBlur   float64 `json:"zoomBlur,omitempty"`   // 0..1 radial smear about the mask's own centre
	Streaks    float64 `json:"streaks,omitempty"`    // 0..1 anamorphic light streaks off the highlights
	Glow       float64 `json:"glow,omitempty"`       // 0..1 isotropic bloom off the highlights
	Mosaic     float64 `json:"mosaic,omitempty"`     // 0..1 pixelate, block to 8% of the long edge
	// Prism splits red and blue radially about the mask's own centre — the
	// lateral chromatic aberration of a cheap lens, as a creative dial.
	// Signed: positive throws red outward, negative throws blue outward.
	Prism float64 `json:"prism,omitempty"` // ±1
	// FXAngle is the smear direction for BOTH MotionBlur and Streaks, in
	// degrees of the oriented frame (0 = horizontal, the anamorphic default).
	// Mod 180 — a smear is symmetric under a half turn, the ellipse Angle
	// precedent — and zeroed by Normalize when neither amount is set, so an
	// inert angle can never make a mask non-neutral or fork its hash.
	FXAngle float64 `json:"fxAngle,omitempty"` // 0..180
}

// IsNeutral reports whether the mask's adjustment changes nothing.
func (a *MaskAdjust) IsNeutral() bool { return *a == MaskAdjust{} }

// HasTone reports whether any of the point-operation fields is set — the gate
// that decides whether the render builds this mask's tone LUTs at all, so an
// FX-only mask skips a full-frame identity pass.
func (a *MaskAdjust) HasTone() bool {
	tone := *a
	tone.Blur, tone.MotionBlur, tone.ZoomBlur = 0, 0, 0
	tone.Streaks, tone.Glow, tone.Mosaic, tone.Prism, tone.FXAngle = 0, 0, 0, 0, 0
	return tone != MaskAdjust{}
}

// HasFX reports whether the mask asks for a spatial effect — the gate that
// decides whether ApplyMasks materializes a weight plane and an FX buffer for
// it. FXAngle alone is not an effect (normalizeMasks zeroes it).
func (a *MaskAdjust) HasFX() bool {
	return a.Blur != 0 || a.MotionBlur != 0 || a.ZoomBlur != 0 ||
		a.Streaks != 0 || a.Glow != 0 || a.Mosaic != 0 || a.Prism != 0
}

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
	Type MaskType `json:"type"`
	// Disabled hides the mask from rendering without deleting it — the
	// panel's eye toggle. Zero means visible, the zero-value contract.
	Disabled bool `json:"disabled,omitempty"`
	Invert   bool `json:"invert,omitempty"`
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
	// the photographer category for "class" kinds and doubles as the person
	// instance index (1..N, left-to-right) for "person"; DepthLo/DepthHi bound
	// the kept depth window (0..1, 1 = nearest) for "depth"; Threshold moves
	// the subject matte cutoff (0 = default 0.5). Feather is reused as the
	// edge softness control for all kinds.
	AIKind    AIKind  `json:"aiKind,omitempty"`
	MapVer    string  `json:"mapVer,omitempty"`
	ClassID   int     `json:"classId,omitempty"`
	DepthLo   float64 `json:"depthLo,omitempty"`
	DepthHi   float64 `json:"depthHi,omitempty"`
	Threshold float64 `json:"threshold,omitempty"`
	// Range mask: a soft band-pass over the pixel's own developed value.
	// RangeLumaLo/Hi bound the kept luminance window (0..1 over display luma);
	// RangeHueLo/Hi bound the kept hue window (0..1 around the hue wheel — the
	// window wraps when Hi < Lo, so this pair is NOT reordered); RangeSatMin
	// gates out near-greys below that saturation. Feather (above) softens all
	// three band edges. Each window defaults fully-open (Lo 0, Hi 1, SatMin 0),
	// so an unset dimension contributes a factor of 1. omitempty keeps non-range
	// masks byte-identical to older builds.
	RangeLumaLo float64 `json:"rangeLumaLo,omitempty"`
	RangeLumaHi float64 `json:"rangeLumaHi,omitempty"`
	RangeHueLo  float64 `json:"rangeHueLo,omitempty"`
	RangeHueHi  float64 `json:"rangeHueHi,omitempty"`
	RangeSatMin float64 `json:"rangeSatMin,omitempty"`

	Adjust MaskAdjust `json:"adjust"`

	// Remove inpaints the mask's region from its surround with the ML model
	// (internal/inpaint), the SpotFill treatment applied to a mask: the pixels
	// are not derivable from the params, so renders composite a cached patch
	// (pyramid.FillStore) in the pre-look stage and skip the removal while none
	// exists. It lives here rather than on Adjust because it describes the
	// region, not an adjustment value — Adjust must stay ==-comparable for
	// IsNeutral, and a Remove mask with a neutral Adjust must still render.
	// Adjust still applies on top of the filled pixels, so a mask can remove
	// something and then grade what replaced it. Only types with a binary,
	// param-derivable region may set it (MaskRemoveAllowed); normalizeMasks
	// clears it elsewhere. Appended last on purpose: json.Marshal emits
	// declaration order, so every older sidecar and edit hash stays
	// byte-identical, and a build predating this field renders the mask as
	// tone-only rather than mis-rendering it.
	Remove bool `json:"remove,omitempty"`
}

// MaskRemoveAllowed reports whether this mask's type and parameters admit
// inpaint mode. The region handed to the model must be binary, bounded and
// derivable from the params alone, which rules out three groups:
//
//   - Linear and radial gradients are soft by construction and a linear ramp
//     runs to the frame edge; there is no object-shaped region to remove, and
//     brush masks plus fill-mode spots already cover "remove this blob".
//   - Depth windows select a slab of the scene, rarely a thing.
//   - Range masks compute coverage from the render's own developed pixels, so
//     their region moves with the look sliders — no stable fill key could
//     exist for one.
//
// An effectively-inverted mask is refused too: its region is "everything
// except the subject", which cannot be inpainted from a surround it does not
// have. Background is the inverted subject matte, hence the XOR.
func (m *Mask) MaskRemoveAllowed() bool {
	switch m.Type {
	case MaskBrush:
		return !m.Invert && len(m.Strokes) > 0
	case MaskAI:
		if m.Invert != (m.AIKind == AIBackground) {
			return false
		}
		// Keyed on the MAP kind, not the mask kind: the region is the map's own
		// un-inverted coverage, so an inverted Background mask — which selects
		// the subject — qualifies exactly as a Subject mask does.
		switch m.AIKind.MapKind() {
		case AISubject, AIPerson, AIClass:
			return true
		}
		return false
	default:
		return false
	}
}

// HasMaskFills reports whether any enabled mask asks for removal — the gate
// that decides whether the pre-look stage derives mask regions at all.
func (e *Params) HasMaskFills() bool {
	if e == nil {
		return false
	}
	for i := range e.Masks {
		if e.Masks[i].Remove && !e.Masks[i].Disabled {
			return true
		}
	}
	return false
}

// SpotMode selects how a retouch spot fills its destination. "" (the default)
// heals: it copies the source patch's texture but tone-matches it to the
// destination's surroundings, so a differently-lit source blends in. "clone"
// copies the source verbatim (feathered at the edge). "fill" inpaints the
// region with an ML model instead of a source patch — the pixels are not
// derivable from the params alone, so renders composite a cached patch
// (pyramid.FillStore) and skip the spot while none exists.
type SpotMode string

const (
	SpotHeal  SpotMode = ""      // canonical default: tone-matched
	SpotClone SpotMode = "clone" // verbatim copy
	SpotFill  SpotMode = "fill"  // ML content-aware inpaint
)

// Spot is one retouch: a feathered destination region filled from a source
// patch elsewhere in the frame — the dust/blemish tool. Coordinates are
// fractions of the oriented frame (the crop-rectangle space, like Mask), and
// Radius is a fraction of the frame's long edge (the Stroke precedent), so
// spots survive recrop/re-straighten and are resolution independent. Spots
// apply in list order in a pre-look stage (pyramid.ApplyHeal) so healed pixels
// develop identically to their source. Kind discriminates the region shape:
// "" is a circle of Radius at (CX,CY); "stroke" is a painted region — the
// union of Strokes (each with its own radius/feather), with (CX,CY) a
// reference point on the region (its enclosing-circle center) and the fill
// sourced from the region translated by (SX-CX, SY-CY). Mode "fill" uses no
// source: SX/SY are normalized to zero and the region is ML-inpainted.
// Normalize drops kinds and modes it doesn't know, the unknown-mask-type
// precedent, so old builds ignore future spots gracefully.
type Spot struct {
	Kind string `json:"kind,omitempty"`
	// Disabled hides the spot from rendering without deleting it — the
	// panel's eye toggle. Zero means visible, the zero-value contract.
	Disabled bool     `json:"disabled,omitempty"`
	Mode     SpotMode `json:"mode,omitempty"`
	CX      float64  `json:"cx"` // destination reference point, frame fractions
	CY      float64  `json:"cy"`
	Radius  float64  `json:"radius"` // fraction of the frame long edge (circle kind; 0 for strokes)
	SX      float64  `json:"sx"`     // source reference point, frame fractions
	SY      float64  `json:"sy"`
	Feather float64  `json:"feather,omitempty"` // 0..1 of Radius (circle edge softness)
	Opacity float64  `json:"opacity,omitempty"` // 0 = full (1.0), the Flow precedent
	Strokes []Stroke `json:"strokes,omitempty"` // the painted region for Kind "stroke"
}

// Params is the edit state, stored as JSON in photos.edit_params.
// The zero value is the neutral edit. Every field's zero value must mean
// "default" — IsNeutral and hashing rely on it, and it keeps stored JSON
// from older versions forward-compatible.
// Exposure dial range. LibRaw's pre-demosaic exp_shift only spans
// LibrawMinExpEV..LibrawMaxExpEV (exp_shift 0.25..8, a hard LibRaw limit);
// the stops beyond that are folded in post-decode — see BakedExpEV /
// ResidualExpEV and pyramid.ApplyExposureEV.
const (
	MinExpEV = -5
	MaxExpEV = 5

	LibrawMinExpEV = -2
	LibrawMaxExpEV = 3
)

type Params struct {
	ExpEV       float64    `json:"expEV" validate:"gte=-5,lte=5"`
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

	// Lens profile correction (pyramid.ApplyLens), applied to the full frame
	// before the geometry stage. LensMode is "" for automatic — the profile
	// matched from the photo's own camera and lens EXIF is applied whenever
	// one is found — or LensOff to leave the frame as the lens drew it.
	//
	// The three amounts are offsets from the profile's own measurement, on
	// the ±1 scale the rest of the sliders use: 0 is the full correction, -1
	// switches that component off, +1 doubles it. Written that way so the
	// zero value means "correct this photo the way the profile says", which
	// is what an edit made before this feature existed should now do, and so
	// every lens field stays omitempty and leaves old hashes untouched.
	LensMode       LensMode `json:"lensMode,omitempty" validate:"omitempty,oneof=off"`
	LensDistortion float64  `json:"lensDistortion,omitempty" validate:"gte=-1,lte=1"`
	LensVignetting float64  `json:"lensVignetting,omitempty" validate:"gte=-1,lte=1"`
	LensCA         float64  `json:"lensCA,omitempty" validate:"gte=-1,lte=1"`

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

	// Spots are retouch fills (dust/blemish removal), applied in order in the
	// pre-look stage (pyramid.ApplyHeal). Kept after Masks with omitempty for
	// the same reason: spot-free edits marshal byte-identically to older
	// builds so existing hashes stay stable, and the subset hashes
	// (librawInputs/linearInputs) never copy this field, keeping spot edits on
	// the warm decode. Like Masks, this makes Params non-comparable — IsNeutral
	// uses reflect.DeepEqual — and Normalize (not the wire validator) clamps
	// the spot fields.
	Spots []Spot `json:"spots,omitempty"`

	// ToneCurve is a user point curve remapping the developed luminance in the
	// look stage (composed into pyramid.buildLookLUT after the parametric tone,
	// before saturation). Points are (input,output) in 0..1, sorted by X;
	// monotone-cubic interpolated and clamped monotone at render. Empty (or an
	// all-diagonal identity curve, which Normalize folds to nil) is neutral and
	// marshals byte-identically to older builds — the Masks/Spots precedent, so
	// existing hashes stay stable and the subset hashes never copy it. Also
	// makes Params non-comparable: IsNeutral uses reflect.DeepEqual.
	ToneCurve []CurvePoint `json:"toneCurve,omitempty"`

	// ToneCurveR/G/B are the per-channel point curves, applied to each color
	// channel AFTER the master ToneCurve (which shapes overall tone) — so the
	// master is the tonal move and these are the color grade on top, the
	// Lightroom split. Same storage rules as ToneCurve: normalized, identity
	// folds to nil, omitempty so channel-free edits keep their hashes.
	ToneCurveR []CurvePoint `json:"toneCurveR,omitempty"`
	ToneCurveG []CurvePoint `json:"toneCurveG,omitempty"`
	ToneCurveB []CurvePoint `json:"toneCurveB,omitempty"`
}

// CurvePoint is one control point of a ToneCurve: X is the input level and Y
// the output level, both in 0..1 (0 = black, 1 = white). A point on the
// diagonal (Y==X) is a no-op; the default identity curve is the two endpoints
// (0,0) and (1,1), stored as nil.
type CurvePoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// CurveBends reports whether a curve bends anything — true only when it has
// at least two points and one sits off the diagonal. A nil, single-point, or
// purely diagonal curve is identity, so the render fast-path stays
// byte-identical to a curve-free edit.
func CurveBends(pts []CurvePoint) bool {
	if len(pts) < 2 {
		return false
	}
	for _, p := range pts {
		if p.Y != p.X {
			return true
		}
	}
	return false
}

// HasToneCurve reports whether the MASTER tone curve bends anything. Nil-safe.
func (e *Params) HasToneCurve() bool {
	return e != nil && CurveBends(e.ToneCurve)
}

// HasChannelCurves reports whether any per-channel curve bends anything, i.e.
// whether the look needs three distinct LUTs instead of one. Nil-safe.
func (e *Params) HasChannelCurves() bool {
	if e == nil {
		return false
	}
	return CurveBends(e.ToneCurveR) || CurveBends(e.ToneCurveG) || CurveBends(e.ToneCurveB)
}

// LensCorrects reports whether the lens profile should be applied at all.
// Nil-safe, and true for a nil or zero Params: automatic is the default.
func (e *Params) LensCorrects() bool {
	return e == nil || e.LensMode != LensOff
}

// LensAmounts converts the stored ±1 offsets into the strength multipliers
// the renderer wants, where 1 is the profile's own measurement. Returns
// zeros when the correction is switched off entirely.
func (e *Params) LensAmounts() (distortion, vignetting, ca float64) {
	if !e.LensCorrects() {
		return 0, 0, 0
	}
	if e == nil {
		return 1, 1, 1
	}
	return 1 + e.LensDistortion, 1 + e.LensVignetting, 1 + e.LensCA
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

// HasSpots reports whether any retouch spot is present.
func (e *Params) HasSpots() bool {
	return e != nil && len(e.Spots) > 0
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
	// A switched-off lens correction ignores its amounts, so clear them —
	// otherwise the same visible result hashes differently depending on
	// where the sliders happened to be left (the WBMode precedent).
	if e.LensMode == LensOff {
		e.LensDistortion, e.LensVignetting, e.LensCA = 0, 0, 0
	}
	// Mixer bands are unvalidated arrays (the wire validator doesn't dive
	// into them), so clamp here instead.
	for i := range e.HSLHue {
		e.HSLHue[i] = clamp(e.HSLHue[i], -1, 1)
		e.HSLSat[i] = clamp(e.HSLSat[i], -1, 1)
		e.HSLLum[i] = clamp(e.HSLLum[i], -1, 1)
	}
	e.normalizeMasks()
	e.normalizeSpots()
	e.normalizeCurve()
}

// normalizeCurve canonicalizes the master and per-channel tone curves.
func (e *Params) normalizeCurve() {
	e.ToneCurve = normalizeCurvePoints(e.ToneCurve)
	e.ToneCurveR = normalizeCurvePoints(e.ToneCurveR)
	e.ToneCurveG = normalizeCurvePoints(e.ToneCurveG)
	e.ToneCurveB = normalizeCurvePoints(e.ToneCurveB)
}

// normalizeCurvePoints canonicalizes one curve so equivalent states hash
// identically: points are clamped to the unit square, quantized so
// pointer-event float noise doesn't churn hashes, sorted by input, and
// collapsed on duplicate X (last wins). An identity curve — fewer than two
// points, or every point on the diagonal — returns nil so it hashes as
// neutral (the CurveBends fast-path). Built into a fresh slice (never
// mutating the caller's backing array — the normalizeMasks contract): Hash
// and IsNeutral normalize a shallow copy of Params, so aliasing here would
// corrupt the caller's curve.
func normalizeCurvePoints(in []CurvePoint) []CurvePoint {
	if len(in) == 0 {
		return nil
	}
	pts := make([]CurvePoint, len(in))
	for i, p := range in {
		pts[i] = CurvePoint{X: quant4(clamp(p.X, 0, 1)), Y: quant4(clamp(p.Y, 0, 1))}
	}
	sort.SliceStable(pts, func(i, j int) bool { return pts[i].X < pts[j].X })
	kept := pts[:0]
	diagonal := true
	for i, p := range pts {
		if i > 0 && p.X == kept[len(kept)-1].X {
			kept[len(kept)-1] = p // duplicate input: last point wins
			continue
		}
		if p.Y != p.X {
			diagonal = false
		}
		kept = append(kept, p)
	}
	if len(kept) < 2 || diagonal {
		return nil
	}
	return kept
}

// normalizeSpots clamps and canonicalizes retouch spots so equivalent states
// hash identically: unknown Kinds and Modes are dropped, "heal" folds to the
// canonical empty Mode, and geometry is quantized so pointer-event float noise
// doesn't churn hashes. Like masks, spots are built into fresh slices — Hash
// and IsNeutral normalize a shallow copy of the receiver, so mutating a shared
// backing array here would corrupt the caller's spots.
func (e *Params) normalizeSpots() {
	if len(e.Spots) == 0 {
		e.Spots = nil
		return
	}
	kept := make([]Spot, 0, len(e.Spots))
	for _, s := range e.Spots {
		if s.Mode == "heal" {
			s.Mode = SpotHeal // fold the explicit spelling to the canonical ""
		}
		switch s.Mode {
		case SpotHeal, SpotClone:
		case SpotFill:
			// Fill has no source patch; zero the reference so equivalent
			// fills hash identically wherever the source dot happened to be.
			s.SX, s.SY = 0, 0
		default:
			continue // unknown mode: drop, like an unknown mask type
		}
		switch s.Kind {
		case "": // circle
			s.Strokes = nil
			s.Radius = quant4(clamp(s.Radius, 0.0005, 0.5))
			s.Feather = quant4(clamp(s.Feather, 0, 1))
		case "stroke": // painted region; radius/feather live per-stroke
			s.Strokes = normalizeStrokes(s.Strokes)
			if s.Strokes == nil {
				continue // nothing painted: drop, like an empty brush mask
			}
			s.Radius, s.Feather = 0, 0
		default:
			continue // unknown kind: drop, like an unknown mask type
		}
		s.CX, s.CY = quant4(clampFrac(s.CX)), quant4(clampFrac(s.CY))
		s.SX, s.SY = quant4(clampFrac(s.SX)), quant4(clampFrac(s.SY))
		s.Opacity = quant4(clamp(s.Opacity, 0, 1))
		kept = append(kept, s)
	}
	if len(kept) == 0 {
		kept = nil
	}
	e.Spots = kept
}

// normalizeStrokes clamps and quantizes a stroke list into a fresh slice
// (never mutating the caller's backing array — the normalizeMasks contract),
// returning nil when nothing usable is painted. Shared by brush masks and
// stroke-kind retouch spots.
func normalizeStrokes(in []Stroke) []Stroke {
	var strokes []Stroke
	for _, s := range in {
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
	return strokes
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
			m.clearRange()
			m.X0, m.Y0 = clampFrac(m.X0), clampFrac(m.Y0)
			m.X1, m.Y1 = clampFrac(m.X1), clampFrac(m.Y1)
		case MaskRadial:
			m.X0, m.Y0, m.X1, m.Y1 = 0, 0, 0, 0
			m.Strokes = nil
			m.clearAI()
			m.clearRange()
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
			m.clearRange()
			m.Strokes = normalizeStrokes(m.Strokes)
		case MaskRange:
			m.X0, m.Y0, m.X1, m.Y1 = 0, 0, 0, 0
			m.CX, m.CY, m.RX, m.RY, m.Angle = 0, 0, 0, 0, 0
			m.Strokes = nil
			m.clearAI()
			m.Feather = quant4(clamp(m.Feather, 0, 1))
			m.RangeLumaLo = quant4(clamp(m.RangeLumaLo, 0, 1))
			m.RangeLumaHi = quant4(clamp(m.RangeLumaHi, 0, 1))
			// The luma window is linear, so an inverted pair is just reordered.
			if m.RangeLumaHi < m.RangeLumaLo {
				m.RangeLumaLo, m.RangeLumaHi = m.RangeLumaHi, m.RangeLumaLo
			}
			// Hue is circular: Hi < Lo means the window wraps through red, so
			// the pair is clamped/quantized but never reordered.
			m.RangeHueLo = quant4(clamp(m.RangeHueLo, 0, 1))
			m.RangeHueHi = quant4(clamp(m.RangeHueHi, 0, 1))
			m.RangeSatMin = quant4(clamp(m.RangeSatMin, 0, 1))
		case MaskAI:
			m.X0, m.Y0, m.X1, m.Y1 = 0, 0, 0, 0
			m.CX, m.CY, m.RX, m.RY, m.Angle = 0, 0, 0, 0, 0
			m.Strokes = nil
			m.clearRange()
			m.Feather = quant4(clamp(m.Feather, 0, 1))
			switch m.AIKind {
			case AISubject, AIBackground:
				// Background thresholds the same matte — the slider moves the
				// subject/background boundary either way.
				m.ClassID, m.DepthLo, m.DepthHi = 0, 0, 0
				m.Threshold = quant4(clamp(m.Threshold, 0, 1))
			case AIClass:
				m.DepthLo, m.DepthHi, m.Threshold = 0, 0, 0
				m.ClassID = int(clamp(float64(m.ClassID), 0, 255))
			case AIPerson:
				m.DepthLo, m.DepthHi, m.Threshold = 0, 0, 0
				// 0 is background, never a valid instance pick.
				m.ClassID = int(clamp(float64(m.ClassID), 1, 255))
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
		m.Adjust.Blur = clamp(m.Adjust.Blur, 0, 1)
		m.Adjust.MotionBlur = clamp(m.Adjust.MotionBlur, 0, 1)
		m.Adjust.ZoomBlur = clamp(m.Adjust.ZoomBlur, 0, 1)
		m.Adjust.Streaks = clamp(m.Adjust.Streaks, 0, 1)
		m.Adjust.Glow = clamp(m.Adjust.Glow, 0, 1)
		m.Adjust.Mosaic = clamp(m.Adjust.Mosaic, 0, 1)
		m.Adjust.Prism = clamp(m.Adjust.Prism, -1, 1)
		// Clear an inpaint flag the type or parameters don't support, so an
		// equivalent state hashes identically and the render rule can never
		// drift from what the panel offers.
		if m.Remove && !m.MaskRemoveAllowed() {
			m.Remove = false
		}
		if m.Adjust.MotionBlur == 0 && m.Adjust.Streaks == 0 {
			// Inert: nothing reads the angle, so equivalent states must hash
			// identically (and a stray angle drag must not make a neutral mask
			// look adjusted).
			m.Adjust.FXAngle = 0
		} else {
			// A smear is symmetric under a half turn, like the ellipse Angle.
			m.Adjust.FXAngle = quant4(math.Mod(math.Mod(m.Adjust.FXAngle, 180)+180, 180))
		}
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

// clearRange zeroes the range-mask fields on non-range mask types so
// equivalent states hash identically.
func (m *Mask) clearRange() {
	m.RangeLumaLo, m.RangeLumaHi = 0, 0
	m.RangeHueLo, m.RangeHueHi = 0, 0
	m.RangeSatMin = 0
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

// SpotFillKey identifies the pixels an ML fill for s is generated from: the
// spot's region geometry plus everything that shapes the pre-look oriented
// frame it is inpainted against — the LibRaw decode subset (which also covers
// the residual-exposure fold via ExpEV) and the quarter-rotate/mirror. Same
// key ⇒ a cached fill patch is still valid; any input change re-keys so the
// patch regenerates instead of compositing stale pixels. Composite-only
// fields (Mode, Disabled, circle Feather, Opacity, SX/SY) stay out of the
// key — tweaking them must not cost an inference. The model version is
// appended by the store, the MapVer precedent.
func (e *Params) SpotFillKey(s *Spot) string {
	seed := struct {
		Decode string   `json:"decode"`
		Rotate int      `json:"rotate"`
		FlipH  bool     `json:"flipH,omitempty"`
		Kind   string   `json:"kind,omitempty"`
		CX     float64  `json:"cx"`
		CY     float64  `json:"cy"`
		Radius float64  `json:"radius"`
		Str    []Stroke `json:"strokes,omitempty"`
	}{
		Decode: e.LibrawInputsHash(), Rotate: e.RotateTurns(), FlipH: e.FlipH,
		Kind: s.Kind, CX: s.CX, CY: s.CY, Radius: s.Radius, Str: s.Strokes,
	}
	b, _ := json.Marshal(&seed)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// MaskFillKey identifies the pixels an ML removal for m is generated from:
// the mask's region parameters plus everything that shapes the pre-look
// oriented frame it is inpainted against — the SpotFillKey contract, applied
// to a mask. Composite-only fields stay out of the key: Feather softens the
// blend edge (the region handed to the model is dilated past it either way,
// so feathering must never cost an inference), and Adjust, Disabled, crop and
// the look stage all apply after the patch is composited. Range fields are
// absent because a range mask can never be a Remove mask. The seed is
// domain-separated from SpotFillKey so a spot and a mask can never collide on
// one patch. The model version is appended by the store, the MapVer
// precedent.
func (e *Params) MaskFillKey(m *Mask) string {
	seed := struct {
		MaskFill  bool     `json:"maskfill"`
		Decode    string   `json:"decode"`
		Rotate    int      `json:"rotate"`
		FlipH     bool     `json:"flipH,omitempty"`
		Type      MaskType `json:"type"`
		Str       []Stroke `json:"strokes,omitempty"`
		AIKind    AIKind   `json:"aiKind,omitempty"`
		MapVer    string   `json:"mapVer,omitempty"`
		ClassID   int      `json:"classId,omitempty"`
		Threshold float64  `json:"threshold,omitempty"`
	}{
		MaskFill: true,
		Decode:   e.LibrawInputsHash(), Rotate: e.RotateTurns(), FlipH: e.FlipH,
		Type: m.Type, Str: m.Strokes,
		AIKind: m.AIKind, MapVer: m.MapVer, ClassID: m.ClassID, Threshold: m.Threshold,
	}
	b, _ := json.Marshal(&seed)
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

// BakedExpEV is the portion of the exposure move that the LibRaw decode
// carries (via exp_shift): ExpEV clamped to LibRaw's hard exp_shift range.
// Nil-safe (the base look bakes nothing).
func (e *Params) BakedExpEV() float64 {
	if e == nil {
		return 0
	}
	return clamp(e.ExpEV, LibrawMinExpEV, LibrawMaxExpEV)
}

// ResidualExpEV is the remainder of the exposure move beyond what LibRaw's
// exp_shift can bake. Every consumer of a LibrawParams decode must fold this
// in post-decode (pyramid.ApplyExposureEV) or the rendered exposure silently
// caps at LibRaw's range. Zero whenever ExpEV is within it. Nil-safe.
func (e *Params) ResidualExpEV() float64 {
	if e == nil {
		return 0
	}
	return e.ExpEV - e.BakedExpEV()
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
	p.ExpShift = math.Pow(2, e.BakedExpEV())
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
		e.ExpEV = clamp(e.ExpEV+*d.ExpEV, MinExpEV, MaxExpEV)
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
