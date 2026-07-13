# ML denoise + super resolution — design & measured feasibility

Status: **infrastructure shipped, user-facing feature HELD** · 2026-07-13
(The roadmap gates Milestone 3 on a design doc; this is it, with measured
numbers instead of estimates.)

## What exists

- `infer.RunTiled` — tiled image-to-image inference with overlap cross-fade,
  ctx-cancellable between tiles, `Scale` support for SR (tested against both
  pinned models below, including a seam/blend unit test).
- GPU execution providers — `ModelSpec.PreferGPU` appends DirectML (Windows)
  / CoreML (macOS) with silent CPU fallback, and the session manager keeps at
  most ONE resident GPU session (two concurrent DirectML sessions crash
  natively in the driver — reproduced). Binding pinned to onnxruntime_go
  v1.27.0 (ORT API 24) so one binary can load either the CPU or the
  DirectML ONNX Runtime build.
- Verified permissive models (hash-pinned in `.devdata`, not yet in the
  production registry):
  - SCUNet-PSNR (blind real-photo denoise), MIT chain via deepghs, 91 MB —
    `https://huggingface.co/deepghs/image_restoration/resolve/main/SCUNet-PSNR.onnx`
    sha256 b0f8c12f1575bb49e39a85924152f1c6d4b527a4aae0432c9e5c7397123465e3
  - Swin2SR classical x2 (Apache-2.0), 54 MB —
    `https://huggingface.co/Xenova/swin2SR-classical-sr-x2-64/resolve/main/onnx/model.onnx`
    sha256 6dde3fe2440543ccae7c40d175609f83c18aeaa3d8456745c4329ef97ae744bd

## Measured throughput (2026-07-13, Core Ultra + Intel Arc 140V, ORT 1.27.1/1.24.4)

| Model | CPU | DirectML (Arc 140V) |
|---|---|---|
| SCUNet denoise | **93 s/MP** | ~50 s/MP (when stable) |
| Swin2SR ×2 | **822 s/MP** | ~148 s/MP (when stable) |

Extrapolated to real work:

- Denoise a 42 MP A7R II frame: **~65 min CPU / ~35 min DML**.
- 2× upscale of a 1600 px export (1.7 MP): **~23 min CPU / ~4 min DML**.

**DirectML stability: FAILED.** SCUNet fp32 on Arc 140V (driver 32.0.101.8724)
crashed with native access violations in 3 of 4 solo runs, and two heavy DML
sessions in one process crash deterministically (hence the one-GPU-session
policy, which stays). CoreML on Apple Silicon is untested (no hardware).

## Decision

Ship nothing user-facing yet. On this hardware class the feature would be a
trap: an hour-long export-blocking operation (CPU) or a driver crash (DML).
This matches the roadmap's warning, now with data. The infrastructure is
merged and tested so the feature is an afternoon of wiring once any unlock
lands.

### Unlock criteria (re-evaluate when any is true)

1. **Stable GPU path** — a newer ORT DirectML + Arc driver combination that
   survives a 100-tile soak (`go test ./internal/infer -run TestRunTiled`
   with `MARRAW_TEST_GPU=1`), or CoreML verified on Apple hardware. Target:
   ≤ 5 s/MP denoise → 42 MP in ≤ 3.5 min.
2. **Lighter model** — an fp16 or distilled blind-denoise UNet at ~5-10× less
   compute with acceptable quality (candidates to watch: NAFNet-tiny
   variants, PMRID-class mobile denoisers). fp16 alone typically buys 2×
   on DML.
3. **Alternative runtime** — ncnn/Vulkan builds (what Real-ESRGAN's shipping
   apps use) sidestep DML entirely; big packaging change, only worth it if
   ORT stays unstable.

### Architecture (unchanged from the roadmap, ready when unlocked)

- Denoise runs on the scene-linear half of the pipeline as a **denoised
  master**: an explicit, cancellable, progress-reporting action (like
  Lightroom Denoise) producing a cached full-res intermediate keyed by
  (photo cacheKey, model ver, strength) under `<dataDir>/denoised/`, with an
  LRU disk cap. Interactive edits then run on top; `LinearInputsHash` gains
  the denoise key so the fold path invalidates correctly. Requires a
  renderVersion bump when wired.
- SR is one extra stage in `export.renderFinal` after the final resize,
  before output sharpening (watermark math already runs on final dims).
- Both surface as shared tasks; GPU capability is probed once per process
  (`Session.OnGPU`) and the UI shows expected duration up front.

## Raw-domain demosaic+denoise (Milestone 5 research notes)

The DeepPRIME-class endgame — model consumes the Bayer mosaic, does
demosaic + denoise jointly. Findings from the model sweep (2026-07-13):

- **No permissively-licensed, hosted ONNX exists** for joint Bayer
  demosaic+denoise today. Research models (PMRID, LED, SID-derived) publish
  PyTorch weights under research-only or unclear licenses; DxO/Adobe are
  proprietary.
- The practical path is **training or fine-tuning in-house** (SID-style
  pairs from one camera body are collectable with a tripod), which is a
  research project, not an integration task.
- Prerequisite plumbing if it ever lands: raw mosaic access from
  `internal/libraw` (libraw exposes the unpacked CFA via rawdata), per-CFA
  pattern handling, and the same denoised-master cache as above — nothing
  else in the pipeline needs to know.

Revisit after the denoise unlock criteria are met; the same runtime and
tiling serve both.
