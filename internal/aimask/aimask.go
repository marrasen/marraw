// Package aimask generates the model-derived maps AI masks sample: the
// salient-subject matte and the relative depth map (the semantic class map
// joins them once a license-clean ADE20K model is hosted — see
// design/ml-roadmap.md). It bridges the generic inference runtime
// (internal/infer) and the map store (pyramid.AIMapStore): input is a
// neutral, base-orientation render of the photo; output is a grayscale map
// at mapLongEdge in the same frame.
package aimask

import (
	"context"
	"fmt"
	"image"

	xdraw "golang.org/x/image/draw"

	"github.com/marrasen/marraw/internal/edit"
	"github.com/marrasen/marraw/internal/infer"
	ort "github.com/yalue/onnxruntime_go"
)

// mapLongEdge is the stored map resolution — the brush-plane philosophy: a
// fixed, modest raster every render samples identically.
const mapLongEdge = 1024

// Model pins. Weights are hash-verified at download; bump Version when
// swapping weights so existing edits' MapVer references stay distinct.
var (
	// subjectModel is ISNet (DIS) general-use, the matte rembg ships.
	// Code Apache-2.0; weights redistributed under the same reading by the
	// MIT-licensed rembg project for years.
	subjectModel = infer.ModelSpec{
		ID: "isnet", Version: "1",
		URL:     "https://github.com/danielgatis/rembg/releases/download/v0.0.0/isnet-general-use.onnx",
		SHA256:  "60920e99c45464f2ba57bee2ad08c919a52bbf852739e96947fbb4358c0d964a",
		Bytes:   178648008,
		License: "Apache-2.0",
	}
	// depthModel is Depth Anything V2 Small (fp32 export). Only the Small
	// variant is Apache-2.0 — Base/Large are CC-BY-NC, never swap up.
	depthModel = infer.ModelSpec{
		ID: "depthany2s", Version: "1",
		URL:     "https://huggingface.co/onnx-community/depth-anything-v2-small/resolve/main/onnx/model.onnx",
		SHA256:  "afb6a5c28f3b6bf1618c6e43f02073ef9dfdc70e937502d51603e57b0a1df10c",
		Bytes:   99060839,
		License: "Apache-2.0",
	}
)

// SpecFor returns the pinned model serving an AI-mask kind. ok=false means
// the kind has no available model (semantic class maps, pending a
// license-clean host).
func SpecFor(kind edit.AIKind) (infer.ModelSpec, bool) {
	switch kind {
	case edit.AISubject:
		return subjectModel, true
	case edit.AIDepth:
		return depthModel, true
	}
	return infer.ModelSpec{}, false
}

// MapVerFor is the version tag stamped into mask params and map file names:
// model identity + weights version, so a model upgrade regenerates maps and
// re-renders the edits that reference them.
func MapVerFor(kind edit.AIKind) (string, bool) {
	spec, ok := SpecFor(kind)
	if !ok {
		return "", false
	}
	return string(spec.ID) + "-" + spec.Version, true
}

// Generate runs the kind's model over a neutral render of the photo and
// returns the map to store. src must be the base-orientation frame (no user
// rotate/flip/crop); progress reports the model download when one happens.
func Generate(ctx context.Context, mgr *infer.Manager, kind edit.AIKind, src *image.RGBA, progress infer.Progress) (*image.Gray, error) {
	spec, ok := SpecFor(kind)
	if !ok {
		return nil, fmt.Errorf("aimask: no model available for kind %q", kind)
	}
	sess, err := mgr.Session(ctx, spec, progress)
	if err != nil {
		return nil, err
	}
	switch kind {
	case edit.AISubject:
		return generateSubject(ctx, sess, src)
	case edit.AIDepth:
		return generateDepth(ctx, sess, src)
	}
	return nil, fmt.Errorf("aimask: no generator for kind %q", kind)
}

// generateSubject: ISNet consumes a stretched 1024×1024 frame normalized as
// x/255 − 0.5; the first output is a (1,1,1024,1024) matte, min-max
// normalized (the rembg post-processing).
func generateSubject(ctx context.Context, sess *infer.Session, src *image.RGBA) (*image.Gray, error) {
	const side = 1024
	in := stretchRGBA(src, side, side)
	data := infer.NCHWFromRGBA(in, [3]float32{0.5, 0.5, 0.5}, [3]float32{1, 1, 1})
	tensor, err := ort.NewTensor(ort.NewShape(1, 3, side, side), data)
	if err != nil {
		return nil, err
	}
	defer tensor.Destroy()

	outs, err := sess.Run(ctx, tensor)
	if err != nil {
		return nil, err
	}
	defer destroyAll(outs)
	matte, ok := outs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("aimask: unexpected subject output type %T", outs[0])
	}
	vals := matte.GetData()
	if len(vals) < side*side {
		return nil, fmt.Errorf("aimask: subject output has %d values, want %d", len(vals), side*side)
	}
	plane := infer.NormalizePlane(vals[:side*side])
	w, h := mapDims(src)
	return resizeGray(grayFromPlane(plane, side, side), w, h), nil
}

// generateDepth: Depth Anything V2 consumes a keep-aspect frame with both
// dims multiples of 14 (canonical 518), ImageNet-normalized; the output is
// relative inverse depth (large = near), min-max normalized so the stored
// map reads 255 = nearest.
func generateDepth(ctx context.Context, sess *infer.Session, src *image.RGBA) (*image.Gray, error) {
	const long = 518
	b := src.Bounds()
	iw, ih := fitMultipleOf14(b.Dx(), b.Dy(), long)
	in := stretchRGBA(src, iw, ih)
	data := infer.NCHWFromRGBA(in,
		[3]float32{0.485, 0.456, 0.406}, [3]float32{0.229, 0.224, 0.225})
	tensor, err := ort.NewTensor(ort.NewShape(1, 3, int64(ih), int64(iw)), data)
	if err != nil {
		return nil, err
	}
	defer tensor.Destroy()

	outs, err := sess.Run(ctx, tensor)
	if err != nil {
		return nil, err
	}
	defer destroyAll(outs)
	depth, ok := outs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("aimask: unexpected depth output type %T", outs[0])
	}
	vals := depth.GetData()
	if len(vals) != iw*ih {
		return nil, fmt.Errorf("aimask: depth output has %d values, want %d×%d", len(vals), iw, ih)
	}
	plane := infer.NormalizePlane(vals)
	w, h := mapDims(src)
	return resizeGray(grayFromPlane(plane, iw, ih), w, h), nil
}

func destroyAll(vals []ort.Value) {
	for _, v := range vals {
		if v != nil {
			v.Destroy()
		}
	}
}

// mapDims is the stored map size: source aspect at mapLongEdge.
func mapDims(src *image.RGBA) (w, h int) {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw >= sh {
		return mapLongEdge, max(1, sh*mapLongEdge/sw)
	}
	return max(1, sw*mapLongEdge/sh), mapLongEdge
}

// fitMultipleOf14 scales (w,h) to fit the given long edge keeping aspect,
// rounding both dims to multiples of 14 (the DPT patch size).
func fitMultipleOf14(w, h, long int) (int, int) {
	scale := float64(long) / float64(max(w, h))
	round14 := func(v float64) int {
		n := int(v/14 + 0.5)
		if n < 1 {
			n = 1
		}
		return n * 14
	}
	return round14(float64(w) * scale), round14(float64(h) * scale)
}

// stretchRGBA resamples src to exactly w×h. Aspect is deliberately not
// preserved for square-input models — they were trained on stretched frames.
func stretchRGBA(src *image.RGBA, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	return dst
}

func grayFromPlane(p []uint8, w, h int) *image.Gray {
	return &image.Gray{Pix: p, Stride: w, Rect: image.Rect(0, 0, w, h)}
}

func resizeGray(src *image.Gray, w, h int) *image.Gray {
	if src.Rect.Dx() == w && src.Rect.Dy() == h {
		return src
	}
	dst := image.NewGray(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	return dst
}
