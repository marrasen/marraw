package libraw

/*
#include <libraw/libraw.h>
*/
import "C"

import "math"

// Demosaic algorithms (libraw user_qual).
const (
	DemosaicLinear = 0
	DemosaicVNG    = 1
	DemosaicPPG    = 2
	DemosaicAHD    = 3
	DemosaicDHT    = 11
)

// Output color spaces (libraw output_color).
const (
	ColorSRGB     = 1
	ColorAdobe    = 2
	ColorWide     = 3
	ColorProPhoto = 4
)

// Params is the subset of libraw_output_params_t that marraw drives.
// Construct via DefaultParams and override — the zero value forces
// linear demosaic and 0° rotation, which is almost never what you want.
type Params struct {
	HalfSize bool // skip demosaic, half resolution: the fast-preview switch

	// Exposure correction (applied before demosaic). ExpShift is a linear
	// multiplier: 0.25 = -2 EV, 8.0 = +3 EV. 0 disables the stage.
	ExpShift    float64
	ExpPreserve float64 // highlight preservation 0..1 when ExpShift > 1

	UseCameraWB bool
	UseAutoWB   bool
	UserMul     [4]float64 // manual WB multipliers; all-zero = unset

	// WBTemp/WBTint adjust white balance relative to the effective base
	// multipliers (UserMul if set, else the camera's as-shot multipliers):
	// temp > 0 warms (R×2^t, B÷2^t), tint > 0 shifts magenta (G÷2^t).
	// Ignored when UseAutoWB is set (auto multipliers are computed inside
	// dcraw_process and cannot be pre-adjusted).
	WBTemp float64
	WBTint float64

	Bright        float64 // linear brightness, 1.0 neutral; 0 = leave default
	Highlight     int     // 0 clip, 1 unclip, 2 blend, 3..9 rebuild
	Gamma         [2]float64 // power, toe slope; zero = library default (BT.709)
	OutputColor   int     // ColorSRGB etc.; 0 = sRGB
	OutputBPS     int     // 8 or 16; 0 = 8
	Threshold     float64 // wavelet denoise threshold, 0 off
	FBDDNoiseRd   int     // 0 off, 1 light, 2 full
	MedPasses     int
	UserQual      int  // demosaic algorithm
	NoAutoBright  bool // true = deterministic output (required for editing)
	AutoBrightThr float64
	UserFlip      int // -1 = use camera orientation
}

// DefaultParams renders a pleasant sRGB 8-bit image honoring camera WB
// and orientation, with auto-brighten on (dcraw default look). Gamma stays
// at LibRaw's BT.709 default — the sRGB toe (12.92 slope) crushes shadows
// visibly against camera JPEGs; contrast shaping happens in the baseline
// look instead.
func DefaultParams() Params {
	return Params{
		UseCameraWB: true,
		UserQual:    DemosaicAHD,
		OutputColor: ColorSRGB,
		OutputBPS:   8,
		UserFlip:    -1,
	}
}

func (p Params) apply(h *C.libraw_data_t) {
	o := &h.params
	o.half_size = cbool(p.HalfSize)
	if p.ExpShift != 0 && p.ExpShift != 1 {
		o.exp_correc = 1
		o.exp_shift = C.float(clamp(p.ExpShift, 0.25, 8))
		o.exp_preser = C.float(clamp(p.ExpPreserve, 0, 1))
	} else {
		o.exp_correc = 0
		o.exp_shift = 1
		o.exp_preser = 0
	}
	o.use_camera_wb = cbool(p.UseCameraWB)
	o.use_auto_wb = cbool(p.UseAutoWB)
	mul := p.UserMul
	if (p.WBTemp != 0 || p.WBTint != 0) && !p.UseAutoWB {
		mul = AdjustWB(baseMul(h, mul), p.WBTemp, p.WBTint)
		o.use_camera_wb = 0
	}
	for i, m := range mul {
		o.user_mul[i] = C.float(m)
	}
	if p.Bright > 0 {
		o.bright = C.float(p.Bright)
	} else {
		o.bright = 1
	}
	o.highlight = C.int(p.Highlight)
	if p.Gamma[0] > 0 {
		o.gamm[0] = C.double(p.Gamma[0])
		o.gamm[1] = C.double(p.Gamma[1])
	} else {
		o.gamm[0] = 1 / 2.222
		o.gamm[1] = 4.5
	}
	if p.OutputColor > 0 {
		o.output_color = C.int(p.OutputColor)
	} else {
		o.output_color = ColorSRGB
	}
	if p.OutputBPS == 16 {
		o.output_bps = 16
	} else {
		o.output_bps = 8
	}
	o.threshold = C.float(p.Threshold)
	o.fbdd_noiserd = C.int(p.FBDDNoiseRd)
	o.med_passes = C.int(p.MedPasses)
	o.user_qual = C.int(p.UserQual)
	o.no_auto_bright = cbool(p.NoAutoBright)
	if p.AutoBrightThr > 0 {
		o.auto_bright_thr = C.float(p.AutoBrightThr)
	}
	o.user_flip = C.int(p.UserFlip)
}

// baseMul returns the multipliers temp/tint adjust: explicit user
// multipliers when set, otherwise the file's as-shot camera multipliers.
func baseMul(h *C.libraw_data_t, userMul [4]float64) [4]float64 {
	if userMul != ([4]float64{}) {
		return userMul
	}
	return camMulOf(h)
}

// camMulOf reads as-shot multipliers with daylight/unity fallbacks.
func camMulOf(h *C.libraw_data_t) [4]float64 {
	var out [4]float64
	for i := range out {
		out[i] = float64(h.color.cam_mul[i])
	}
	if out[0] <= 0 || out[1] <= 0 || out[2] <= 0 {
		for i := range out {
			out[i] = float64(h.color.pre_mul[i])
		}
	}
	if out[0] <= 0 || out[1] <= 0 || out[2] <= 0 {
		return [4]float64{1, 1, 1, 1}
	}
	return out
}

// AdjustWB scales multipliers by the temp/tint offsets: temp warms by
// raising R and lowering B, tint shifts green-magenta on G (and G2).
func AdjustWB(mul [4]float64, temp, tint float64) [4]float64 {
	tf := math.Pow(2, temp)
	gf := math.Pow(2, -tint)
	mul[0] *= tf
	mul[2] /= tf
	mul[1] *= gf
	if mul[3] > 0 {
		mul[3] *= gf
	}
	return mul
}

func cbool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

func clamp(v, lo, hi float64) float64 {
	return min(max(v, lo), hi)
}
