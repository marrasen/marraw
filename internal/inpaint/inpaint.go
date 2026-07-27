// Package inpaint generates ML content-aware fills for retouch spots: the
// window around a fill-mode spot (pyramid.SpotFillWindow), rendered pre-look,
// goes through an inpainting model and comes back with the masked region
// synthesized from its surround. It bridges the generic inference runtime
// (internal/infer) and the patch store (pyramid.FillStore) the way aimask
// bridges it and the AIMapStore; the output is not derivable from the params,
// so callers cache it per (photo, fill key, model version).
package inpaint

import (
	"context"
	"fmt"
	"image"

	"github.com/marrasen/marraw/internal/infer"
	ort "github.com/yalue/onnxruntime_go"
)

// model is MI-GAN-512-Places2 (Picsart AI Research, ICCV 2023), exported by
// the authors as a self-contained ONNX pipeline (migan_pipeline_v2.onnx on
// huggingface.co/andraniksargsyan/migan): uint8 image + mask in at any
// resolution, internal crop-around-mask/resize-to-512/normalize, and the
// inverse plus blending on the way out. MIT covers code AND weights (the
// repo LICENSE, Picsart AI Research 2024) — vetted 2026-07-27 after
// rejecting big-LaMa, whose weights are CC BY-NC-SA regardless of the
// Apache-2.0 code license. Mirrored on marrasen/marraw-models.
var model = infer.ModelSpec{
	ID: "migan", Version: "1",
	URL:     "https://github.com/marrasen/marraw-models/releases/download/models-v1/migan-pipeline-v2.onnx",
	SHA256:  "6f1f3530a1a2324b19752018ce756088b07973cda8d7d890034ace5c8a48c40b",
	Bytes:   28079181,
	License: "MIT",
}

// Spec returns the pinned inpainting model.
func Spec() infer.ModelSpec { return model }

// FillVer is the version tag in fill-patch file names: model identity +
// weights version, the MapVerFor precedent — a model upgrade re-keys every
// cached patch so stale fills regenerate.
func FillVer() string { return string(model.ID) + "-" + model.Version }

// Generate inpaints src where mask is 0 (255 = keep, the pipeline's
// convention) and returns the result at src's resolution. src should be the
// spot's context window rendered pre-look at generation resolution — the
// same pipeline stage the patch later composites into (pyramid.ApplyHeal),
// so generation and render agree by construction. progress reports the model
// download when one happens.
func Generate(ctx context.Context, mgr *infer.Manager, src *image.RGBA, mask *image.Gray, progress infer.Progress) (*image.RGBA, error) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, fmt.Errorf("inpaint: empty source window")
	}
	if mb := mask.Bounds(); mb.Dx() != w || mb.Dy() != h {
		return nil, fmt.Errorf("inpaint: mask %dx%d does not match source %dx%d", mb.Dx(), mb.Dy(), w, h)
	}
	sess, err := mgr.Session(ctx, model, progress)
	if err != nil {
		return nil, err
	}

	// uint8 NCHW planes, the pipeline's native input — no normalization.
	imgData := make([]uint8, 3*h*w)
	rp, gp, bp := imgData[:h*w], imgData[h*w:2*h*w], imgData[2*h*w:]
	for y := 0; y < h; y++ {
		row := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]
		for x := 0; x < w; x++ {
			i := y*w + x
			rp[i] = row[x*4]
			gp[i] = row[x*4+1]
			bp[i] = row[x*4+2]
		}
	}
	imgTensor, err := ort.NewTensor(ort.NewShape(1, 3, int64(h), int64(w)), imgData)
	if err != nil {
		return nil, err
	}
	defer imgTensor.Destroy()

	maskData := make([]uint8, h*w)
	for y := 0; y < h; y++ {
		copy(maskData[y*w:(y+1)*w], mask.Pix[y*mask.Stride:y*mask.Stride+w])
	}
	maskTensor, err := ort.NewTensor(ort.NewShape(1, 1, int64(h), int64(w)), maskData)
	if err != nil {
		return nil, err
	}
	defer maskTensor.Destroy()

	outs, err := sess.Run(ctx, imgTensor, maskTensor)
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, o := range outs {
			o.Destroy()
		}
	}()
	result, ok := outs[0].(*ort.Tensor[uint8])
	if !ok {
		return nil, fmt.Errorf("inpaint: unexpected output type %T", outs[0])
	}
	shape := result.GetShape()
	if len(shape) != 4 || shape[1] != 3 {
		return nil, fmt.Errorf("inpaint: unexpected output shape %v", shape)
	}
	oh, ow := int(shape[2]), int(shape[3])
	data := result.GetData()
	if len(data) < 3*oh*ow {
		return nil, fmt.Errorf("inpaint: output holds %d values, want %d", len(data), 3*oh*ow)
	}
	out := image.NewRGBA(image.Rect(0, 0, ow, oh))
	orp, ogp, obp := data[:oh*ow], data[oh*ow:2*oh*ow], data[2*oh*ow:]
	for y := 0; y < oh; y++ {
		row := out.Pix[y*out.Stride:]
		for x := 0; x < ow; x++ {
			i := y*ow + x
			row[x*4] = orp[i]
			row[x*4+1] = ogp[i]
			row[x*4+2] = obp[i]
			row[x*4+3] = 255
		}
	}
	return out, nil
}
