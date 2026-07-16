// Package eyes detects closed eyes for culling: YuNet finds faces and their
// eye centers in the embedded thumb, a tiny classifier judges each eye open
// or closed, and the photo-level score is the highest closed probability
// seen. It is a soft signal like subject sharpness — sunglasses, profiles,
// and squints misfire, so the badge suggests, never judges.
package eyes

import (
	"context"
	"fmt"
	"image"
	"math"
	"sort"
	"strconv"

	xdraw "golang.org/x/image/draw"

	"github.com/marrasen/marraw/internal/infer"
	ort "github.com/yalue/onnxruntime_go"
)

// Model pins. Both are tiny (KBs, CPU-only) next to the mask models, but the
// same rules apply: hash-verified downloads from the marrasen/marraw-models
// mirror, origins and licenses in THIRD_PARTY_NOTICES.md.
var (
	// detectModel is YuNet (2023mar) from opencv_zoo: face boxes plus five
	// landmarks including both eye centers, so no separate landmark model.
	// MIT — the opencv_zoo model README states MIT covers all files in the
	// model directory, weights included (the adeseg exception class).
	detectModel = infer.ModelSpec{
		ID: "yunet", Version: "2023mar",
		URL:     "https://github.com/marrasen/marraw-models/releases/download/models-v1/yunet-2023mar.onnx",
		SHA256:  "8f2383e4dd3cfbb4553ea8718107fc0423210dc964f9f4280604804ed2552fa4",
		Bytes:   232589,
		License: "MIT",
	}
	// stateModel is open-closed-eye-0001 from the OpenVINO Open Model Zoo: a
	// 32×32 BGR eye-crop in, softmax [open, closed] out. Proven pairing with
	// YuNet (FaceAiSharp ships exactly this combo).
	stateModel = infer.ModelSpec{
		ID: "openclosedeye", Version: "0001",
		URL:     "https://github.com/marrasen/marraw-models/releases/download/models-v1/openclosedeye-0001.onnx",
		SHA256:  "4daa100034482525a26c9afb9297c16580a531189e66e3d2b2ac7d32becfd593",
		Bytes:   46164,
		License: "Apache-2.0",
	}
)

// DetectSpec and StateSpec expose the pins for the Settings model catalog
// and download-consent sizing.
func DetectSpec() infer.ModelSpec { return detectModel }
func StateSpec() infer.ModelSpec  { return stateModel }

// ModelsInstalled reports whether both weights are on disk — the gate the
// background calibrate pass uses so it never downloads anything itself.
func ModelsInstalled(mgr *infer.Manager) bool {
	return mgr.HasModel(detectModel) && mgr.HasModel(stateModel)
}

const (
	// detSide is the face-detection working resolution — the 2023mar export
	// has a fixed 640×640 input. The frame is letterboxed into it (scaled to
	// fit, padded black) so faces keep their aspect; 640 resolves faces down
	// to a few dozen source pixels while keeping the pass a few ms on CPU.
	detSide = 640
	// faceScoreMin keeps only confident detections: a blink verdict on a
	// marginal face box would badge noise. (OpenCV's high-precision default
	// is 0.9; culling prefers recall on real faces.)
	faceScoreMin = 0.7
	// nmsIoU is the standard YuNet overlap cutoff for duplicate boxes.
	nmsIoU = 0.3
	// eyeCropFrac sizes the classifier crop: half-side = frac × interocular
	// distance, so the crop tracks face scale (≈ the eye plus its lids).
	eyeCropFrac = 0.3
	// minCropPx skips eyes whose source crop would be tinier than half the
	// classifier input — upscaling beyond 2× judges pixel soup, not lids.
	minCropPx = 16
	// eyeInputSide is the classifier's fixed input.
	eyeInputSide = 32
)

// Score runs closed-eye detection over one photo's embedded-thumb decode.
// It returns the highest closed-eye probability (0..1) across every eye of
// every confident face; ok=false means no face with judgeable eyes was found
// (store the sentinel, don't re-run). Downloads happen inside Session when
// weights are missing — callers gate on ModelsInstalled/consent first.
func Score(ctx context.Context, mgr *infer.Manager, src image.Image, progress infer.Progress) (float64, bool, error) {
	// The classifier session comes first even though it may go unused (a
	// faceless frame): a consented scan must leave BOTH weights installed,
	// or the calibrate pass's ModelsInstalled gate would stay closed for
	// every photo ingested after a faceless first scan.
	sess, err := mgr.Session(ctx, stateModel, progress)
	if err != nil {
		return 0, false, err
	}
	faces, err := detectFaces(ctx, mgr, src, progress)
	if err != nil {
		return 0, false, err
	}
	if len(faces) == 0 {
		return 0, false, nil
	}
	worst, scored := 0.0, false
	for _, f := range faces {
		iod := math.Hypot(f.eyeL[0]-f.eyeR[0], f.eyeL[1]-f.eyeR[1])
		half := eyeCropFrac * iod
		if half*2 < minCropPx {
			continue
		}
		for _, eye := range [2][2]float64{f.eyeR, f.eyeL} {
			p, ok, err := classifyEye(ctx, sess, src, eye, half)
			if err != nil {
				return 0, false, err
			}
			if ok {
				scored = true
				worst = math.Max(worst, p)
			}
		}
	}
	return worst, scored, nil
}

// face is one confident YuNet detection, in source-image coordinates.
type face struct {
	score      float64
	box        [4]float64 // x, y, w, h
	eyeR, eyeL [2]float64 // eye centers
}

// detectFaces runs YuNet over a detLongEdge-fit resample of src and returns
// confident, NMS-deduplicated faces mapped back to source coordinates.
func detectFaces(ctx context.Context, mgr *infer.Manager, src image.Image, progress infer.Progress) ([]face, error) {
	sess, err := mgr.Session(ctx, detectModel, progress)
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw < 2 || sh < 2 {
		return nil, nil
	}
	// Letterbox into the fixed input: scale to fit, keep the aspect, pad the
	// remainder black. One scale factor maps detections back to source.
	fit := math.Min(detSide/float64(sw), detSide/float64(sh))
	fw, fh := max(1, int(float64(sw)*fit+0.5)), max(1, int(float64(sh)*fit+0.5))
	in := image.NewRGBA(image.Rect(0, 0, detSide, detSide))
	xdraw.CatmullRom.Scale(in, image.Rect(0, 0, fw, fh), src, b, xdraw.Src, nil)

	// YuNet consumes raw 0..255 BGR values — no normalization.
	tensor, err := ort.NewTensor(ort.NewShape(1, 3, detSide, detSide), bgrPlanes(in, 0, 1))
	if err != nil {
		return nil, err
	}
	defer tensor.Destroy()
	outs, err := sess.Run(ctx, tensor)
	if err != nil {
		return nil, err
	}
	defer destroyAll(outs)
	byName, err := outputsByName(sess, outs)
	if err != nil {
		return nil, err
	}

	// Anchor-free decode, per stride: score = √(cls·obj); the box center and
	// keypoints are cell-relative offsets, the box size log-scale — all in
	// stride units.
	sx, sy := 1/fit, 1/fit
	var faces []face
	for _, s := range []int{8, 16, 32} {
		n := strconv.Itoa(s)
		cls, obj := byName["cls_"+n], byName["obj_"+n]
		bbox, kps := byName["bbox_"+n], byName["kps_"+n]
		if cls == nil || obj == nil || bbox == nil || kps == nil {
			return nil, fmt.Errorf("eyes: yunet output for stride %d missing", s)
		}
		rows, cols := detSide/s, detSide/s
		if len(cls) < rows*cols || len(obj) < rows*cols || len(bbox) < rows*cols*4 || len(kps) < rows*cols*10 {
			return nil, fmt.Errorf("eyes: yunet stride-%d outputs shorter than the %d×%d grid", s, rows, cols)
		}
		for i := 0; i < rows*cols; i++ {
			score := math.Sqrt(float64(clamp01(cls[i]) * clamp01(obj[i])))
			if score < faceScoreMin {
				continue
			}
			fs := float64(s)
			c, r := float64(i%cols), float64(i/cols)
			w, h := math.Exp(float64(bbox[i*4+2]))*fs, math.Exp(float64(bbox[i*4+3]))*fs
			cx, cy := (c+float64(bbox[i*4]))*fs, (r+float64(bbox[i*4+1]))*fs
			faces = append(faces, face{
				score: score,
				box:   [4]float64{(cx - w/2) * sx, (cy - h/2) * sy, w * sx, h * sy},
				eyeR:  [2]float64{(c + float64(kps[i*10])) * fs * sx, (r + float64(kps[i*10+1])) * fs * sy},
				eyeL:  [2]float64{(c + float64(kps[i*10+2])) * fs * sx, (r + float64(kps[i*10+3])) * fs * sy},
			})
		}
	}
	return nms(faces), nil
}

// classifyEye crops a square around one eye center, feeds it to the state
// model, and returns the closed probability. ok=false when the crop falls
// outside the frame (an eye landmark on a cropped-off face edge).
func classifyEye(ctx context.Context, sess *infer.Session, src image.Image, center [2]float64, half float64) (float64, bool, error) {
	b := src.Bounds()
	r := image.Rect(
		b.Min.X+int(center[0]-half), b.Min.Y+int(center[1]-half),
		b.Min.X+int(center[0]+half+0.5), b.Min.Y+int(center[1]+half+0.5),
	).Intersect(b)
	if r.Dx() < minCropPx/2 || r.Dy() < minCropPx/2 {
		return 0, false, nil
	}
	in := image.NewRGBA(image.Rect(0, 0, eyeInputSide, eyeInputSide))
	xdraw.CatmullRom.Scale(in, in.Bounds(), src, r, xdraw.Src, nil)

	// open-closed-eye-0001 wants BGR normalized (v−127)/255 (the model.yml
	// mean/scale that OpenVINO folds into its IR conversion).
	tensor, err := ort.NewTensor(ort.NewShape(1, 3, eyeInputSide, eyeInputSide), bgrPlanes(in, 127, 255))
	if err != nil {
		return 0, false, err
	}
	defer tensor.Destroy()
	outs, err := sess.Run(ctx, tensor)
	if err != nil {
		return 0, false, err
	}
	defer destroyAll(outs)
	probs, ok := outs[0].(*ort.Tensor[float32])
	if !ok {
		return 0, false, fmt.Errorf("eyes: unexpected state output type %T", outs[0])
	}
	vals := probs.GetData()
	if len(vals) < 2 {
		return 0, false, fmt.Errorf("eyes: state output has %d values, want 2", len(vals))
	}
	// Softmax over [closed, open] — index 0 is the closed probability. The
	// OMZ README documents the opposite order, but empirically (open-eye
	// portraits land ~[0,1], closed lids ~[1,0]) the raw ONNX follows the
	// MRL training set's alphabetical class order: closed = 0, open = 1.
	return float64(clamp01(vals[0])), true, nil
}

// bgrPlanes converts img to 1×3×H×W float32 data in BGR plane order (the
// OpenCV-lineage layout both models expect), normalizing as (v − mean)/scale.
func bgrPlanes(img *image.RGBA, mean, scale float32) []float32 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]float32, 3*h*w)
	bp, gp, rp := out[:h*w], out[h*w:2*h*w], out[2*h*w:]
	for y := 0; y < h; y++ {
		row := img.Pix[img.PixOffset(b.Min.X, b.Min.Y+y):]
		for x := 0; x < w; x++ {
			i := y*w + x
			rp[i] = (float32(row[x*4]) - mean) / scale
			gp[i] = (float32(row[x*4+1]) - mean) / scale
			bp[i] = (float32(row[x*4+2]) - mean) / scale
		}
	}
	return out
}

// outputsByName maps a session's run outputs to their graph names, so the
// stride decode doesn't depend on the export's output ordering.
func outputsByName(sess *infer.Session, outs []ort.Value) (map[string][]float32, error) {
	if len(outs) != len(sess.Outputs) {
		return nil, fmt.Errorf("eyes: got %d outputs, session declares %d", len(outs), len(sess.Outputs))
	}
	m := make(map[string][]float32, len(outs))
	for i, o := range outs {
		t, ok := o.(*ort.Tensor[float32])
		if !ok {
			return nil, fmt.Errorf("eyes: unexpected output type %T for %q", o, sess.Outputs[i].Name)
		}
		m[sess.Outputs[i].Name] = t.GetData()
	}
	return m, nil
}

// nms keeps the highest-scoring face of every overlapping cluster.
func nms(faces []face) []face {
	sort.Slice(faces, func(i, j int) bool { return faces[i].score > faces[j].score })
	var kept []face
	for _, f := range faces {
		dup := false
		for _, k := range kept {
			if iou(f.box, k.box) > nmsIoU {
				dup = true
				break
			}
		}
		if !dup {
			kept = append(kept, f)
		}
	}
	return kept
}

func iou(a, b [4]float64) float64 {
	ix := math.Min(a[0]+a[2], b[0]+b[2]) - math.Max(a[0], b[0])
	iy := math.Min(a[1]+a[3], b[1]+b[3]) - math.Max(a[1], b[1])
	if ix <= 0 || iy <= 0 {
		return 0
	}
	inter := ix * iy
	return inter / (a[2]*a[3] + b[2]*b[3] - inter)
}

func clamp01(v float32) float32 {
	return float32(math.Max(0, math.Min(1, float64(v))))
}

func destroyAll(vals []ort.Value) {
	for _, v := range vals {
		if v != nil {
			v.Destroy()
		}
	}
}
