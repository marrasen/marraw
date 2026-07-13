package infer

import (
	"fmt"
	"image"
)

// NCHWFromRGBA converts img to 1×3×H×W float32 data (the layout vision ONNX
// models expect), normalizing each channel as (v/255 − mean[c]) / std[c].
// Pass mean {0,0,0} and std {1,1,1} for plain 0..1 scaling.
func NCHWFromRGBA(img *image.RGBA, mean, std [3]float32) []float32 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]float32, 3*h*w)
	rp, gp, bp := out[:h*w], out[h*w:2*h*w], out[2*h*w:]
	for y := 0; y < h; y++ {
		row := img.Pix[img.PixOffset(b.Min.X, b.Min.Y+y):]
		for x := 0; x < w; x++ {
			i := y*w + x
			rp[i] = (float32(row[x*4])/255 - mean[0]) / std[0]
			gp[i] = (float32(row[x*4+1])/255 - mean[1]) / std[1]
			bp[i] = (float32(row[x*4+2])/255 - mean[2]) / std[2]
		}
	}
	return out
}

// ArgmaxPlane collapses C×H×W logits to an H*W plane of class indices —
// the post-processing step that turns segmentation output into a class map.
// classes must fit a uint8 (ADE20K's 150 does).
func ArgmaxPlane(logits []float32, classes, h, w int) ([]uint8, error) {
	if classes < 1 || classes > 256 {
		return nil, fmt.Errorf("infer: argmax classes %d out of uint8 range", classes)
	}
	if len(logits) != classes*h*w {
		return nil, fmt.Errorf("infer: argmax got %d values, want %d×%d×%d", len(logits), classes, h, w)
	}
	plane := h * w
	out := make([]uint8, plane)
	best := make([]float32, plane)
	copy(best, logits[:plane]) // class 0 seeds
	for c := 1; c < classes; c++ {
		ch := logits[c*plane : (c+1)*plane]
		for i, v := range ch {
			if v > best[i] {
				best[i] = v
				out[i] = uint8(c)
			}
		}
	}
	return out, nil
}

// NormalizePlane min-max scales a float plane to 0..255 grayscale — the
// post-processing step for relative-depth output. A constant plane maps to 0.
func NormalizePlane(vals []float32) []uint8 {
	out := make([]uint8, len(vals))
	if len(vals) == 0 {
		return out
	}
	lo, hi := vals[0], vals[0]
	for _, v := range vals {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	if hi <= lo {
		return out
	}
	scale := 255 / (hi - lo)
	for i, v := range vals {
		out[i] = uint8((v-lo)*scale + 0.5)
	}
	return out
}
