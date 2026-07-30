package export

// ML restoration on export: denoise and 2x super resolution, both riding on
// infer.RunTiled. Deliberately scoped to the export path only — it needs no
// denoised-master cache, no LinearInputsHash key and no renderVersion bump,
// because nothing here feeds an interactive render. See design/ml-denoise.md,
// unlock criterion 4.

import (
	"context"
	"fmt"
	"image"
	"log"
	"os"
	"runtime"

	"github.com/marrasen/marraw/internal/infer"
)

// Restoration model IDs. The specs live in the registry once the weights are
// mirrored; see restoreSpec.
const (
	denoiseModel = infer.ModelID("scunet")
	upscaleModel = infer.ModelID("swin2sr")
)

// Tile geometry per model, matching what design/ml-denoise.md measured:
// 256 for the denoiser, 128 for the 2x SR model whose output tile is four
// times its input.
var (
	denoiseTiles = infer.TileConfig{Size: 256, Overlap: 16, Scale: 1}
	upscaleTiles = infer.TileConfig{Size: 128, Overlap: 8, Scale: 2}
)

// defaultDenoiseBudgetMP caps the pixels the denoiser is allowed to touch.
// At the measured 15.5 s/MP on a desktop CPU this is ~3 minutes; the point is
// that an export never silently turns into a ten-minute job.
const defaultDenoiseBudgetMP = 12.0

// downscaleSkipFactor is the export downscale factor at or above which the
// denoiser is skipped entirely. Resampling by 2x averages ~4 source pixels per
// output pixel (and a 1600px export off a 42MP master averages ~25), which
// already cuts noise sigma substantially; what survives is out of distribution
// for a model trained on native-scale sensor noise, so running it costs minutes
// to make the result worse.
const downscaleSkipFactor = 2.0

// RestoreOptions turns on the ML restoration stages. A nil *RestoreOptions on
// Request disables both, which is the same no-op-when-absent contract as
// Request.AIMaps, Lenses and Fills.
type RestoreOptions struct {
	// Models resolves and caches the ONNX sessions. Required; nil disables
	// restoration entirely.
	Models *infer.Manager

	// DenoiseStrength blends the model output against the input: 0 = off,
	// 1 = the model's raw output. It is not a model parameter — SCUNet-PSNR is
	// blind and single-strength, and at moderate ISO its raw output visibly
	// over-smooths fine texture (measured on hair at ISO 1600). Blending is the
	// only strength control available, which is why this is not a bare on/off.
	DenoiseStrength float64

	// DenoiseBudgetMP caps the megapixels denoised; 0 uses
	// defaultDenoiseBudgetMP. A region over budget is skipped rather than
	// partially processed.
	DenoiseBudgetMP float64

	// Upscale runs 2x super resolution before the output resize, so an export
	// requested larger than the source resolves from model detail instead of
	// interpolation.
	Upscale bool

	// PreferGPU asks for a GPU execution provider. Denoise has no GPU path on
	// the DirectML runtime that ships with the app (SCUNet's transformer block
	// faults); SR does. Either way infer falls back to CPU silently.
	PreferGPU bool

	// Progress, when set, is called as tiles complete so a long export can
	// report more than a frozen bar. stage is "denoise" or "upscale".
	Progress func(stage string, done, total int)
}

// active reports whether any stage would run.
func (o *RestoreOptions) active() bool {
	return o != nil && o.Models != nil && (o.DenoiseStrength > 0 || o.Upscale)
}

// denoiseGPUSafe reports whether it is safe to ask for a GPU session for the
// denoise model.
//
// This is a guard against a process kill, not a performance choice. SCUNet's
// transformer block faults inside a DirectML operator with a native access
// violation — measured 4/4 on an RTX 3070 and 3/4 on an Arc 140V — and an
// access violation is not a Go error: it aborts the daemon, taking the user's
// whole export with it. No in-process probe can catch it either, since the
// probe is what crashes. So DirectML must never be handed this model.
//
// On Windows the provider is DirectML unless CUDA is explicitly selected. The
// app currently ships a CPU-only ONNX Runtime, so DirectML is not even
// available in production today (see design/ml-denoise.md) — but this must stay
// correct if that packaging ever changes, which is exactly when the crash would
// otherwise appear.
func denoiseGPUSafe() bool {
	if runtime.GOOS != "windows" {
		// CoreML is unmeasured on this model; it is expected to reject
		// unsupported operators as an error rather than fault, which
		// newSession already handles by falling back to CPU.
		return true
	}
	return os.Getenv("MARRAW_GPU_EP") == "cuda"
}

// restoreSpec builds the model spec. URL and SHA256 are deliberately empty:
// the weights are not yet mirrored on marrasen/marraw-models, and every
// production model resolves there so that no registry URL depends on a third
// party's release hygiene. Until they are published, Manager.ensureModel finds
// a locally staged file (scripts/setup-devmodels.ps1) or fails, which is the
// correct behaviour — better a clear "model unavailable" than a download from
// an artifact whose licence does not cover redistribution.
//
// TODO(ml-denoise): fill URL/SHA256/Bytes/License once the weights land on a
// marraw-models release. SCUNet is MIT and safe to mirror; Swin2SR must be
// re-exported from caidas/swin2SR-classical-sr-x2-64 (Apache-2.0) rather than
// mirrored from Xenova, which declares no licence. See design/ml-denoise.md.
func restoreSpec(id infer.ModelID, preferGPU bool) infer.ModelSpec {
	if s, ok := infer.Lookup(id); ok {
		s.PreferGPU = preferGPU
		return s
	}
	return infer.ModelSpec{ID: id, Version: "1", PreferGPU: preferGPU}
}

// denoiseSkip reports why the denoiser should not run, or "" when it should.
// Both guards exist because cost is linear in megapixels and quality is not
// monotonic in effort: a heavily downscaled export gets worse, not better.
func denoiseSkip(w, h, longEdge int, budgetMP float64) string {
	if budgetMP <= 0 {
		budgetMP = defaultDenoiseBudgetMP
	}
	long := max(w, h)
	if longEdge > 0 && long > longEdge {
		if f := float64(long) / float64(longEdge); f >= downscaleSkipFactor {
			return fmt.Sprintf("export downscales %.1fx; resampling already removes the noise", f)
		}
	}
	if mp := float64(w) * float64(h) / 1e6; mp > budgetMP {
		return fmt.Sprintf("region is %.1f MP, over the %.0f MP budget", mp, budgetMP)
	}
	return ""
}

// apply runs the enabled restoration stages on img, returning the result.
// longEdge is the requested output long edge (0 = full resolution), used to
// decide whether either stage is worth running at all.
//
// A stage that cannot run is skipped with a log line rather than failing the
// export: a missing model or an unsupported operator must not cost the user
// their whole batch. Cancellation does propagate — that is a deliberate user
// action, and RunTiled checks ctx between tiles.
func (o *RestoreOptions) apply(ctx context.Context, img *image.RGBA, longEdge int) (*image.RGBA, error) {
	if !o.active() {
		return img, nil
	}
	b := img.Bounds()

	if o.DenoiseStrength > 0 {
		if why := denoiseSkip(b.Dx(), b.Dy(), longEdge, o.DenoiseBudgetMP); why != "" {
			log.Printf("export: skipping ML denoise: %s", why)
		} else {
			out, err := o.run(ctx, "denoise", denoiseModel, denoiseTiles, img,
				o.PreferGPU && denoiseGPUSafe())
			if err != nil {
				if ctx.Err() != nil {
					return nil, err
				}
				log.Printf("export: ML denoise unavailable, exporting undenoised: %v", err)
			} else {
				// Blend rather than replace: see DenoiseStrength.
				blend(img, out, min(o.DenoiseStrength, 1))
			}
		}
	}

	if o.Upscale {
		switch long := max(b.Dx(), b.Dy()); {
		case longEdge <= 0:
			// Full-resolution export. SR would mean inference over the entire
			// frame -- minutes even on CUDA for a 33 MP source -- to produce a
			// 4x-area image the export then keeps whole. Full-res SR stays out
			// of reach; see design/ml-denoise.md.
			log.Printf("export: skipping ML upscale: no output size limit set")
		case longEdge <= long:
			// Not asking for more pixels than the source has.
			log.Printf("export: skipping ML upscale: output %d px is within the source's %d px",
				longEdge, long)
		default:
			// Resize to HALF the target first, so the model runs on ~(L/2)^2
			// pixels and its 2x output lands on the requested long edge. The
			// alternative -- upscaling the full source and downscaling the
			// result -- costs inference proportional to the source frame
			// (minutes) and discards most of what it computed.
			half := (longEdge + 1) / 2
			src := img
			if long > half {
				src = resizeRGBA(img, half)
			}
			out, err := o.run(ctx, "upscale", upscaleModel, upscaleTiles, src, o.PreferGPU)
			if err != nil {
				if ctx.Err() != nil {
					return nil, err
				}
				log.Printf("export: ML upscale unavailable, resizing conventionally: %v", err)
			} else {
				img = out
			}
		}
	}
	return img, nil
}

// run loads one model and pushes img through it tiled. preferGPU is passed
// separately rather than read from o, because it is not uniform across models:
// see denoiseGPUSafe.
func (o *RestoreOptions) run(ctx context.Context, stage string, id infer.ModelID,
	cfg infer.TileConfig, img *image.RGBA, preferGPU bool) (*image.RGBA, error) {

	sess, err := o.Models.Session(ctx, restoreSpec(id, preferGPU), nil)
	if err != nil {
		return nil, err
	}
	if o.Progress != nil {
		cfg.Progress = func(done, total int) { o.Progress(stage, done, total) }
	}
	return infer.RunTiled(ctx, sess, img, cfg)
}

// blend mixes src into dst in place at the given weight (1 = all src). Both
// must share bounds; alpha is left alone because both sides are opaque.
func blend(dst, src *image.RGBA, weight float64) {
	if weight >= 1 {
		copy(dst.Pix, src.Pix)
		return
	}
	w := int32(weight * 256)
	for i := 0; i+3 < len(dst.Pix) && i+3 < len(src.Pix); i += 4 {
		for c := 0; c < 3; c++ {
			d := int32(dst.Pix[i+c])
			dst.Pix[i+c] = uint8(d + ((int32(src.Pix[i+c])-d)*w)>>8)
		}
	}
}
