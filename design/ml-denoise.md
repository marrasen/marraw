# ML denoise + super resolution — design & measured feasibility

Status: **infrastructure shipped, user-facing feature HELD** · measured
2026-07-13, unlock criteria revised 2026-07-29
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
4. **Bounded pixel budget** — the only unlock we can *build* rather than wait
   for. Cost is linear in megapixels, and the 65-minute figure is entirely
   because it denoises the 42 MP master. Denoise the cropped region at native
   scale capped by a budget, then resize: cost becomes `min(crop MP, budget)`,
   which puts a 1600 px web export at ~2.6 min even on the measured CPU path.
   Skip ML entirely when the export downscale factor is ≥ ~2 — averaging 25
   source pixels per output pixel already cuts noise σ by ~5×, and what
   survives is out of distribution for a model trained on native-scale sensor
   noise. Scoped this way it needs no denoised-master cache, no
   `LinearInputsHash` key, and no renderVersion bump — one stage in
   `export.renderFinal`, where the job is already async with progress.
   Caveat: this only helps exports; the loupe still needs criteria 1-3.

### Pending measurement: NVIDIA / Windows (2026-07-29)

Everything above was measured on ONE machine — a Lunar Lake iGPU. The next
data point is a Windows desktop with an **RTX 3070 (8 GB)**, which needs no
code changes: DirectML is vendor-neutral D3D12 and `PreferGPU` already
appends it on Windows. Run there first:

```
set MARRAW_TEST_GPU=1
go test ./internal/infer -run TestRunTiled     # needs the DirectML ORT build
```

A green 100-tile soak satisfies criterion 1 and turns this whole doc from
"held" into "afternoon of wiring".

**Expectations — ESTIMATES, not measurements.** Extrapolated from the Arc
140V DML number by compute (20.3 vs ~3.8 TFLOPS fp32) and bandwidth (448 vs
~135 GB/s), so ~4-5× sustained. Treat as ±2×: it is a cross-architecture
guess from a single data point, and SCUNet's swin-transformer blocks may not
track raw FLOPS. Record what actually happens and replace this table.

| Path | s/MP (est.) | 42 MP master | 1600 px export |
|---|---|---|---|
| CPU (measured, any OS) | 93 | ~65 min | ~2.6 min |
| DirectML fp32 — no code change | ~10-13 | ~7-9 min | ~20 s |
| CUDA EP fp32 | ~6-8 | ~4-6 min | ~13 s |
| CUDA/TensorRT fp16 | ~3-5 | ~2-3.5 min ✅ target | ~7 s |

Swin2SR ×2 scales the same way (~30 s/MP DML fp32 → a 1.7 MP export upscales
in ~50 s). Full-res SR stays out of reach.

Notes for whoever runs this:

- **8 GB VRAM is not a constraint.** `RunTiled` bounds the working set by tile
  edge, not image size — a 512² fp32 tile through SCUNet is roughly 1-3 GB of
  activations, 256² comfortably under 1 GB, weights are 91 MB. Keep the
  one-resident-GPU-session policy; with a desktop and browser already holding
  1-2 GB, two sessions would be tight even without the crash reason.
- **The Arc failures were probably an Intel driver bug**, not an ORT-DML
  architecture flaw — native access violations inside the driver. NVIDIA's
  D3D12 path is far more heavily exercised by shipping DML apps, so there is a
  decent chance this just passes.
- **Linux gets nothing today** — `newSession` appends no provider there (see
  `infer.go`), so a 3070 on Linux falls back to 93 s/MP CPU. The pinned
  onnxruntime_go v1.27.0 does expose `AppendExecutionProviderCUDA` and
  `...TensorRT`, so the Go side is a small change; the cost is shipping the
  CUDA/cuDNN provider libs, which is why it was deferred.
- If one machine class passes and another hard-crashes, gate at runtime with a
  probe plus a short self-test — not a hand-maintained driver allowlist.

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
