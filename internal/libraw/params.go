package libraw

/*
#include <libraw/libraw.h>
*/
import "C"

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
	for i, m := range p.UserMul {
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

func cbool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

func clamp(v, lo, hi float64) float64 {
	return min(max(v, lo), hi)
}
