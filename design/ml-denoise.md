# ML denoise + super resolution — design & measured feasibility

Status: **infrastructure shipped and an export stage built; both features HELD on
a packaging decision** · measured 2026-07-13 (Arc), unlock criteria revised
2026-07-29, measured 2026-07-30 (RTX 3070 + real-photo quality + CPU-overhead
benchmarks)
(The roadmap gates Milestone 3 on a design doc; this is it, with measured
numbers instead of estimates.)

**Headline: the compute problem is solved on paper and the quality is good, but
every fast path needs a runtime the app does not ship.**

- **CUDA runs everything, fast.** SCUNet denoise at **1.7 s/MP** — a 42 MP
  master in ~1.2 min against criterion 1's ≤3.5 min target, which this
  **meets**. Swin2SR at 14.3 s/MP. Both survived 100-tile soaks.
- **DirectML runs Swin2SR but cannot run SCUNet at all** — 4 of 4 runs die with
  a native access violation in an unsupported operator in SCUNet's transformer
  block. The old hypothesis that Arc's failures were an Intel driver bug and
  NVIDIA would "just pass" is disproven: on the DML path the blocker is the
  model, not the vendor.
- **Quality is genuinely good where there is noise to remove** (measured on real
  frames, see below) — and actively harmful where there is not, which is why the
  strength control must be driven by measured noise rather than a constant.
- **The catch is distribution.** The CUDA runtime is ~2.3 GB and NVIDIA-only.
  DirectML is ~20 MB but **is not currently packaged either** (see below), and
  it cannot run the denoiser regardless. So on a stock install today all
  inference is CPU: 15.5 s/MP denoise, 97.9 s/MP SR.

**Nothing here is blocked on compute, tiling, or quality any more. It is blocked
on choosing what to ship.** The options and their real costs are in "What is
left" at the end.

## What exists

- `infer.RunTiled` — tiled image-to-image inference with overlap cross-fade,
  ctx-cancellable between tiles, `Scale` support for SR (tested against both
  pinned models below, including a seam/blend unit test).
- GPU execution providers — `ModelSpec.PreferGPU` appends DirectML (Windows)
  / CoreML (macOS) / CUDA (Windows, when `MARRAW_GPU_EP=cuda`) with silent CPU
  fallback, and the session manager keeps at most ONE resident GPU session (two
  concurrent DirectML sessions crash natively in the driver — reproduced).
  Binding pinned to onnxruntime_go v1.27.0 (ORT API 24) so one binary can load
  the CPU, DirectML or CUDA ONNX Runtime build.
- **Packaging reality check (verified 2026-07-30): the shipped app has no GPU
  provider on Windows or Linux.** CI runs `npm run setup:ort` with no variant
  flag (`release.yml`) and `package.json` ships `third_party/onnxruntime/lib`,
  which is the **CPU-only** ORT build on all three platforms. So `PreferGPU`
  appends DirectML, the CPU-only library rejects it, and `newSession` falls back
  to CPU with only a log line. macOS is the one exception, since the CoreML EP is
  inside the stock macOS build (untested — no hardware). Every GPU number in this
  document therefore describes a runtime that must be *added* to the installer,
  not one that is already there.
- `export.RestoreOptions` — the export-side denoise + SR stage, built and tested
  but not reachable from the UI. See "Current state of the code".
- Verified permissive models (hash-pinned in `.devdata`, not yet in the
  production registry):
  - SCUNet-PSNR (blind real-photo denoise), MIT chain via deepghs, 91 MB —
    `https://huggingface.co/deepghs/image_restoration/resolve/main/SCUNet-PSNR.onnx`
    sha256 b0f8c12f1575bb49e39a85924152f1c6d4b527a4aae0432c9e5c7397123465e3
  - Swin2SR classical x2, 54 MB (**licence gap, see below**) —
    `https://huggingface.co/Xenova/swin2SR-classical-sr-x2-64/resolve/main/onnx/model.onnx`
    sha256 6dde3fe2440543ccae7c40d175609f83c18aeaa3d8456745c4329ef97ae744bd

## Measured throughput

Two machines, both via `go test ./internal/infer -run TestRunTiledThroughput`
(100 tiles per case, warmup pass discarded, s/MP is per **input** megapixel).

| Model | A: CPU Lunar Lake | B: CPU i7-13700KF | A: DML Arc 140V | B: DML RTX 3070 | B: CUDA RTX 3070 |
|---|---|---|---|---|---|
| SCUNet denoise | 93 s/MP | 15.5 s/MP | ~50 (unstable) | **crashes 4/4** | **1.7 s/MP** |
| Swin2SR ×2 | 822 s/MP | 97.9 s/MP | ~148 (unstable) | 18.3 s/MP | **14.3 s/MP** |

- **A** = Core Ultra + Intel Arc 140V, ORT 1.27.1 CPU / 1.24.4 DML, 2026-07-13.
- **B** = i7-13700KF (16C) + RTX 3070 8 GB, driver 581.57, 32 GB RAM,
  2026-07-30. ORT 1.27.1 CPU / 1.24.4 DML + DirectML.dll 1.15.4 / 1.27.1
  gpu_cuda12 with CUDA 12.9 + cuDNN 9.24 from NVIDIA's PyPI wheels.

Per-tile steady state on B, which is what extrapolates (the aggregate hides
warmup):

| Model | CPU | DML | CUDA |
|---|---|---|---|
| SCUNet @256² | 754 ms (673-1150) | — | **83 ms** (80-99) |
| Swin2SR @128² | 1199 ms (1070-1636) | 228 ms (225-247) | **177 ms** (172-192) |

All three paths are tight across 100 tiles — no thermal or allocator drift.

Extrapolated to real work on B. Cost is linear in the **source region's**
megapixels, never the output's, which is why a crop is cheap and a full frame is
not:

| Job | region | CPU | DML | CUDA |
|---|---|---|---|---|
| Denoise 42 MP master | 42 MP | ~10.8 min | n/a | **~1.2 min** ✅ |
| Denoise at the 12 MP budget | 12 MP | ~3.1 min | n/a | ~20 s |
| Denoise a 2 MP crop | 2 MP | ~31 s | n/a | ~3 s |
| 2× upscale from a 1 MP crop | 1 MP in | ~98 s | **~18 s** | ~14 s |
| 2× upscale from a 4 MP crop | 4 MP in | ~6.5 min | ~73 s | ~57 s |

A typical downscaled web export appears nowhere in this table on purpose: the
skip rule declines it. "n/a" for DML denoise is the 4/4 crash, not slowness.

The desktop CPU is **6.0×** the laptop on SCUNet and **8.4×** on Swin2SR, so
every CPU figure in the original table was a 15 W-part worst case. Note the
asymmetry the last two rows expose: **denoise is only fast on CUDA, while SR only
needs DirectML** — the two stages want different providers, and the app ships
neither.

### DirectML on NVIDIA: works, except for SCUNet

**Swin2SR: PASS.** 100-tile throughput run plus a separate 100-tile soak, both
green, 18.3 and 18.7 s/MP. VRAM peaked at 2045 MiB total against a 1436 MiB
desktop baseline, i.e. ~600 MiB for the session — the "8 GB is not a
constraint" note holds with a wide margin. GPU utilisation averaged 53% and
peaked at 100%, so there is headroom being lost to CPU-side tile marshalling
(`tileTensorData` and the blend loop are single-threaded Go), not to the model.

**SCUNet: FAIL, 4 of 4 runs, deterministic.** Native access violation
(`0xc0000005`) inside `RunOrtSessionWithOptions` on the *first* forward pass,
with `OnGPU=true` — the provider is accepted and then faults. Tile edges 64,
128, 192 and 256 all crash. At edge 224 the same defect surfaces as a handled
ORT error, which names it:

```
Non-zero status code returned while running Add node.
Name:'/m_body/m_body.0/trans_block/Add'
DmlExecutionProvider\src\MLOperatorAuthorImpl.cpp(2818) ... 80070057 (E_INVALIDARG)
```

That is an unsupported/mis-shaped operator in SCUNet's swin-transformer block,
not memory pressure and not a tile-size limit.

**The control that pins the blame.** Running SCUNet on the *same ORT 1.24.4
DirectML build* but with the CPU execution provider passes at 15.0 s/MP,
matching the 15.5 s/MP measured on the 1.27.1 CPU build. So the model file is
good, the 1.24.4 build is good, and the DirectML **execution provider** is the
sole variable. Whoever revisits this should re-run that control before blaming
a driver.

**Session churn: PASS.** swin2sr -> scunet -> swin2sr, where each load after
the first evicts and destroys a live GPU session, completes cleanly. So the
eviction path itself is sound on NVIDIA. This does *not* license running two
concurrent DML sessions — the policy prevents that configuration from ever
arising, so it remains untested here and **stays**.

CoreML on Apple Silicon is still untested (no hardware).

### CUDA on NVIDIA: everything passes

Both models, both 100-tile soaks, and the session-churn test are green on the
CUDA execution provider. SCUNet at 1.7 s/MP is **9× the desktop CPU** and the
first result that satisfies criterion 1. Swin2SR at 14.3 s/MP is 1.3× DirectML,
a modest edge — the denoise difference is categorical (works vs. crashes), the
SR difference is incremental.

Two operational findings:

- **The runtime directory must be on `PATH`.** ORT loads cuDNN with a runtime
  `LoadLibrary` rather than a static import, so it is resolved by the standard
  search order — which does *not* include the directory of an
  `onnxruntime.dll` that was itself loaded by absolute path. Without it the
  failure is the actively misleading `Invalid handle. Cannot load symbol
  cudnnCreate`: the symbol exists and is exported, it is the *library* that
  failed to load. Shipping this needs the provider DLLs beside `marrawd`, or a
  `SetDllDirectory`/`AddDllDirectory` call before `InitializeEnvironment`.
- **VRAM is higher than the DML path but still fits.** Peak 5118 MiB total on an
  8 GB card versus ~2 GB for DirectML — cuDNN's workspaces and precompiled
  engines are large. Fine on this card, but the earlier "8 GB is not a
  constraint, with a wide margin" note applies to DirectML, not CUDA. A 6 GB
  card running a desktop plus browser would want a smaller tile.

GPU utilisation averaged 47%, the same story as DirectML: about half the wall
clock is single-threaded Go between passes, not the model.

### Shipping the CUDA path (the actual open question)

The measured runtime is **2.35 GB across 20 DLLs**, and it does not compress
away:

| DLL | Size |
|---|---|
| `cublasLt64_12.dll` | 638 MB |
| `cudnn_engines_precompiled64_9.dll` | 522 MB |
| `onnxruntime_providers_cuda.dll` | 313 MB |
| `cufft64_11.dll` | 274 MB |
| `cudnn_adv64_9.dll` | 257 MB |
| `cudnn_ops`, `cudnn_graph`, `cublas`, `cudnn_heuristic`, … | ~350 MB |

Trimming is not the answer: `cudart`, `cublas`, `cublasLt`, `cudnn` and `cufft`
are all statically imported by the provider, so the only clearly droppable items
are `cudnn_adv` (257 MB, if no advanced ops are used), `nvblas` and `cufftw`.
That still leaves ~2.1 GB against an installer measured in tens of MB.

So the options are a product decision, not a technical one:

1. **Optional GPU pack, downloaded on demand** — same consent-gated,
   hash-verified, progress-reporting mechanism the model registry already uses,
   just two orders of magnitude larger. Honest UX ("Fast denoise needs a 2 GB
   NVIDIA component"), NVIDIA-only, and it makes the model registry's download
   path carry something far bigger than it was designed for.
2. **Ship nothing extra; denoise on CPU with a bounded pixel budget**
   (criterion 4) — works on every machine and every vendor, no download at all.
   With the budget set to ~2-4 MP this is a **~31-60 s** per-image operation that
   fires on crops and near-native exports and skips everything else. Note it does
   *not* deliver "~26 s on a 1600 px export": that figure appears elsewhere in
   older revisions and contradicts the downscale-skip rule, which correctly
   skips such an export entirely. Loses the 42 MP master case.
3. **A DirectML-compatible denoise model** (criterion 2) — the only route to fast
   *vendor-neutral* denoise, since DirectML itself works fine for SR. Unbounded
   search cost, but `tools/cmpcrop` and the throughput harness make evaluating
   candidates quick.

No option was chosen: as of 2026-07-30 the findings are documented and shipping
is deferred. (2) remains the cheapest thing that could ship without new
dependencies, and (3) is the highest-value research if vendor-neutral GPU denoise
is wanted.

Note that a CUDA dependency also gets Linux GPU support nearly for free —
`newSession` appends no provider there today, but the EP is the same code path.

## Decision

**Decision 2026-07-30: document the findings and ship nothing yet.** Compute,
tiling and quality are all settled; what is not settled is which runtime the
installer should carry, and that judgement was deliberately deferred rather than
forced. The stage is built and tested behind a nil-means-disabled option, so no
user-visible behaviour changed.

**Denoise.** CUDA does a 42 MP master in ~1.2 min — the feature working as
intended — but it is NVIDIA-only and needs ~2.3 GB of provider libraries the app
does not ship. On a stock install the only available path is CPU at 15.5 s/MP:
~31 s for a 2 MP crop, ~3.1 min at the 12 MP budget, ~10.8 min for a full frame.
That is defensible as a deliberate per-image rescue and not as a batch feature.
The denoised-master cache (the L-sized work touching `LinearInputsHash`, the fold
path and `renderVersion`) should wait for a fast path, because it only earns its
risk once full-res denoise is quick.

**Super resolution.** Swin2SR is stable on DirectML at 18.3 s/MP and 14.3 s/MP on
CUDA, but three things stand between that and shipping: DirectML is not in the
installer, `LongEdge` has no upscaling semantics (export never enlarges), and CPU
SR is poor value at 97.9 s/MP. It is also inherently narrow — SR only helps when
the output exceeds the source, which on a 42 MP body is rare. Its real audience
is heavy crops and smaller sensors.

### Unlock criteria (re-evaluate when any is true)

1. **Stable GPU path for a denoise model** — **MET on NVIDIA via CUDA**
   (1.7 s/MP vs a ≤ 5 s/MP target, 100-tile soak green). Reproduce with
   `go test ./internal/infer -run TestRunTiledSoak` under `MARRAW_TEST_GPU=1`
   and `MARRAW_GPU_EP=cuda`. What is left is not compute but distribution — see
   "Shipping the CUDA path". Still open for non-NVIDIA GPUs and for CoreML on
   Apple hardware.
   **Correction to the earlier wording:** this could never have been satisfied
   by waiting for "a newer ORT DirectML build". Microsoft publishes no DirectML
   asset on the ONNX Runtime releases page at all, and the NuGet package
   `Microsoft.ML.OnnxRuntime.DirectML` stops at **1.24.4** — a ceiling, not a
   lagging pin. On the DML path the only levers are the driver and the model.
2. **A denoise model whose ops DirectML supports** — still the only route to
   *vendor-neutral* GPU denoise, and the requirement is narrower than
   "lighter": it must avoid whatever shape SCUNet's `trans_block` Add feeds the
   DML operator. Convolutional denoisers (NAFNet-tiny variants, PMRID-class
   mobile denoisers) are the natural candidates precisely because they skip
   transformer blocks. Note Swin2SR *is* a transformer and runs fine on DML, so
   "transformer" is not itself disqualifying — test with the throughput harness,
   do not assume. Lower priority now that CUDA covers the NVIDIA majority, but
   this is what would serve AMD and Intel GPUs.
3. **Alternative runtime** — ncnn/Vulkan builds (what Real-ESRGAN's shipping
   apps use) sidestep DML entirely. **Largely superseded:** CUDA already
   provides a working, fast denoise path, so a whole new runtime would only buy
   vendor-neutrality, which criterion 2 buys far more cheaply. Worth revisiting
   only if AMD/Intel GPU denoise becomes a priority and no DML-compatible model
   turns up.
4. **Bounded pixel budget** — the only unlock we can *build* rather than wait
   for. Cost is linear in megapixels, and the 11-minute figure is entirely
   because it denoises the 42 MP master. Denoise the cropped region at native
   scale capped by a budget, then resize: cost becomes `min(crop MP, budget)`.
   Needs no denoised-master cache, no `LinearInputsHash` key and no
   renderVersion bump — one stage in `export.renderFinal`, where the job is
   already async with progress. Only helps exports; the loupe still needs
   criteria 1-3.

   **Correction (2026-07-30): the original wording was self-contradictory.** It
   claimed "~26 s for a 1600 px web export" *and* "skip ML entirely when the
   export downscale factor is ≥ ~2". Those cannot both apply to one export: a
   1600 px export from a 33-42 MP body downscales by 4-5×, so the skip rule
   forbids precisely the export the 26 s figure advertised. The 26 s was the
   cost of denoising ~1.7 MP — i.e. denoising at *output* scale, which is what
   the skip rule exists to prevent, because post-downscale noise is out of
   distribution for a model trained on native-scale sensor noise.

   Reconciled, the stage behaves as:

   - **downscale factor ≥ ~2** → skip ML. Averaging 25 source pixels per output
     pixel already cuts noise σ by ~5×. This is the common web-export case, so
     the honest expectation is that **denoise does nothing for most exports**.
   - **factor < 2 (near-native output, or a heavy crop)** → denoise the region
     at native scale, cost `min(region MP, budget)`. At 15.5 s/MP a 12 MP budget
     is ~3 min of CPU. So the realistic promise is "minutes on a full-resolution
     export", not "26 s on any export".
   - **region MP > budget** → skip and say so, rather than silently spending
     ten minutes.

   This narrows the feature considerably and is worth weighing against just
   shipping SR first.

### Measured denoise quality (2026-07-30, two real frames)

Numbers only say noise went down; they do not say whether the result is good.
Judged on real frames at 1:1, SCUNet via CUDA. **The answer depends entirely on
how noisy the input actually is, and that is the design constraint.**

**Frame A — ILCE-7M4, ISO 1600, flash-lit. Denoise is HARMFUL here.**
Mean gradient 0.848 → 0.535 at full strength (a 37% drop). At fit-to-screen the
two are indistinguishable; at 1:1, skin texture flattens toward waxy and hair
strands merge into broader clumps. There was little noise to remove, so the
model spent its budget destroying detail. An earlier 1304² crop of the same
frame measured local variance 7.42 → 2.08 (72%) with the same verdict.

**Frame B — 42 MP A7R-class, stage-lit festival set, heavily noisy. Denoise is a
clear WIN.** Mean gradient 2.667 (3.1× frame A's noise floor) → 0.177 at full
strength. At 1:1:

- Dense chroma speckle across the projected backdrop is *completely* gone, with
  smooth gradients and no blotching.
- On the instrument headstock, the logo script, tuner posts and wood edge come
  out **more legible than the noisy original**, not less.
- In hair over a dark forehead, strands resolve as distinct strokes where the
  original could not be separated from grain; the eyelid and lash line are
  crisper.

So the earlier "PSNR models over-smooth" verdict was drawn from too little
noise. With real noise present, SCUNet is genuinely good.

**The problem is that SCUNet is blind: it has no noise-level input**, so it
applies roughly the same aggression regardless. That is exactly why the strength
blend exists, and why a single fixed default is wrong — 100% is right for frame
B and clearly wrong for frame A.

**Recommendation: derive the default strength from measured noise, not from a
constant.** Sample local variance over a few hundred blocks of the frame (the
same cheap metric used in the tests, microseconds against minutes of inference)
and map it to strength, so a clean ISO 200 frame gets ~0 and a stage shot gets
~1. ISO from EXIF is a reasonable secondary signal but is strictly worse:
measured variance also catches pushed exposures and shadow lifts, which is where
noise actually lives. Keep the manual override.

Also: never apply the model where the skip rule says the downscale has already
done the job.

### Prerequisite for shipping either stage: mirror the weights

Every production model resolves to `marrasen/marraw-models` releases (see
`internal/inpaint/inpaint.go`), deliberately, so that no registry URL depends on
a third party's release hygiene. SCUNet and Swin2SR are currently pinned to
third-party HuggingFace repos (deepghs, Xenova) and are staged only into
`.devdata` by `scripts/setup-devmodels.ps1`. **Both must be published to a
`marraw-models` release before either export stage can ship**; the registry
entries are then a few lines each.

**Hosting cost is negligible** (measured 2026-07-30):

| File | Bytes | Size |
|---|---|---|
| `scunet-1.onnx` | 91,264,256 | 87.0 MiB |
| `swin2sr-1.onnx` | 54,428,699 | 51.9 MiB |
| total | 145,692,955 | 138.9 MiB |

For scale, the release already carries `adeseg-1.onnx` at 1.31 GiB and ~1.74 GiB
across all seven models, so this is roughly an 8% increase and far under
GitHub's 2 GB per-file limit.

**Licences — one is clean, one is not:**

- **SCUNet: MIT, safe to mirror.** `deepghs/image_restoration` declares MIT in
  both its HF tags and card metadata. More permissive than the Apache-2.0 floor
  in the roadmap's cross-cutting rules.
- **Swin2SR: the Apache-2.0 claim does not cover the file we pinned.**
  Apache-2.0 is declared by the *upstream* `caidas/swin2SR-classical-sr-x2-64`.
  The repo we actually download from, `Xenova/swin2SR-classical-sr-x2-64`,
  declares **no licence at all** — empty tags, empty card data. Absence of a
  licence is not a grant, so mirroring that specific artifact is the one real
  legal risk here.
  **Fix, following the precedent already set for the segmentation model** (whose
  gap was closed by re-exporting from smp-hub's MIT checkpoint): re-export
  Swin2SR ×2 from `caidas` under Apache-2.0 rather than mirroring Xenova's file,
  and record the recipe alongside the others in the marraw-models repo. This
  changes the SHA-256, so the pin above and `scripts/setup-devmodels.ps1` must be
  updated together.

Both models also need entries in `THIRD_PARTY_NOTICES.md` when they ship.

### Reproducing these measurements

Nothing here needs production code changes. Full env-var reference in
[DEVELOPER.md](../DEVELOPER.md#ml-inference-and-gpu-throughput).

```powershell
npm run setup:devmodels          # hash-pinned SCUNet + Swin2SR -> .devdata/models
npm run setup:ort -- -DirectML   # ORT 1.24.4 DML -> third_party/onnxruntime-directml
$env:MARRAW_ORT_LIB="$PWD\third_party\onnxruntime-directml\lib\onnxruntime.dll"
$env:MARRAW_TEST_GPU="1"; $env:MARRAW_TEST_TILES="100"
go test ./internal/infer -run 'TestRunTiled(Throughput|Soak)|TestGPUSessionChurn' -v -count=1 -timeout 60m
```

For the CUDA path instead (note the `PATH` prepend — without it cuDNN fails to
load with a misleading "cannot load symbol" error):

```powershell
npm run setup:ort -- -CUDA       # ORT gpu_cuda12 + CUDA runtime/cuDNN, ~1.4 GB download
$env:PATH="$PWD\third_party\onnxruntime-cuda\lib;$env:PATH"
$env:MARRAW_ORT_LIB="$PWD\third_party\onnxruntime-cuda\lib\onnxruntime.dll"
$env:MARRAW_TEST_GPU="1"; $env:MARRAW_GPU_EP="cuda"
```

Read the `RESULT` lines; `-v` is required. The `ep=` field reports the provider
that actually ran (`cpu`/`dml`/`cuda`/`coreml`). **Always confirm `OnGPU=true`** — a
CPU-only ORT build rejects the DML provider and falls back with only a log line,
which is the easy way to record a CPU number as a GPU one. `MARRAW_TEST_TILE_SIZE`
overrides the tile edge; that is how the SCUNet failure was shown to be
size-independent. To repeat the control that isolates the EP, point
`MARRAW_ORT_LIB` at the DirectML build but leave `MARRAW_TEST_GPU` unset.

### Notes and open experiments

- **8 GB VRAM is not a constraint — confirmed for DirectML, adequate for CUDA.**
  Swin2SR at 128² on DML peaked at ~600 MiB of session VRAM (2045 MiB total
  against a 1436 MiB desktop baseline); the CUDA path peaked at 5118 MiB total.
  `RunTiled` bounds the working set by tile edge, not image size, so a smaller
  tile is the lever on a smaller card. Keep the one-resident-GPU-session policy:
  sequential load-and-evict is proven fine on NVIDIA under both providers, two
  *concurrent* sessions remain untested and crashed on Arc.
- **The Arc-driver-bug hypothesis was wrong.** Both GPUs fail on SCUNet with the
  same `0xc0000005`, and on NVIDIA it is 4/4 rather than 3/4 — a
  vendor-independent operator gap, not a flaky driver. Correspondingly, a
  runtime probe plus self-test is still the right gating mechanism, but what it
  must probe is **the model**, not the driver: run one small tile through the
  actual model at session-open and fall back to CPU on error. Cheap, since the
  failure is deterministic and immediate rather than intermittent.
- **There is no CPU-side overhead worth optimising — measured, and it refutes an
  earlier claim in this document.** An earlier revision read GPU utilisation of
  53% (DML) / 47% (CUDA) as "roughly half the wall clock is CPU-side" and
  proposed overlapping tile preparation with inference for a ~1.5-2× win. That
  was wrong. Those averages spanned model loading, warmup, JPEG decode and the
  gaps between subtests, not the tile loop.

  Benchmarked directly (`tile_bench_test.go`, i7-13700KF), per 256² tile:
  `tileTensorData` 0.155 ms, `blendTile` 0.188 ms — **0.34 ms against 83 ms of
  CUDA inference, i.e. ~0.4%**. The final acc→RGBA compose costs 16.3 ms once
  per 5 MP image, another ~0.2% of a 100-tile run. And 100 CUDA tiles took 8.4 s
  wall against an 83 ms/tile median, so the loop is ~99.6% inference with no
  hidden overhead.

  **Do not spend time pipelining or parallelising the tile marshalling**; the
  ceiling is half a percent. The only levers that matter are the execution
  provider, a cheaper model, and reduced precision. Keep the benchmarks so this
  does not get re-proposed.
- **CUDA is measured, not pending** — see the CUDA section above. The Go side
  turned out to be one case in `gpuSessionOptions` plus `MARRAW_GPU_EP`; the
  cost is entirely in the ~2.3 GB of provider libraries.
- **TensorRT / fp16 is untested and no longer urgent.** The old estimate table
  put fp16 at 3-5 s/MP as the target to reach; CUDA fp32 already beats it at
  1.7 s/MP, so TensorRT's per-shape engine builds and extra SDK are hard to
  justify for denoise. Revisit only for full-resolution SR.
- **Linux gets nothing today** — `newSession` appends no provider there (see
  `infer.go`), so a 3070 on Linux falls back to the CPU path. The CUDA EP would
  cover it with the same code path, if the packaging question is answered.

## Current state of the code (2026-07-30)

An export-side restoration stage **exists, is tested, and is deliberately not
reachable from the UI.** Anyone resuming this should read `restore.go` first
rather than starting over.

- `internal/export/restore.go` — `RestoreOptions` on `Request` (nil = disabled,
  the same contract as `AIMaps`/`Lenses`/`Fills`), wired into `renderFinal`
  between `ApplyGeometry` and `ApplyFinish`. That position is load-bearing: the
  denoiser sees cropped native-scale pixels and runs *before* `ApplyDetail`'s
  sharpening, and SR lands before the output resize.
- **Denoise strength is a blend**, not a model parameter — SCUNet has no
  strength input.
- **Skip rules are enforced**: downscale ≥2× skips, over-budget skips, both with
  a logged reason. A missing or faulting model degrades to exporting without the
  stage rather than failing the batch; cancellation still propagates.
- **SR resizes to half the target before inference**, so cost tracks the target
  rather than the source frame. An earlier revision inferred over the full frame
  and discarded most of the result.
- `denoiseGPUSafe()` **refuses DirectML for the denoise model.** This guards a
  process kill, not performance: the fault is a native access violation, which
  aborts the daemon and cannot be caught by an in-process probe. It matters only
  if packaging ever gains the DirectML build — which is exactly when it would
  otherwise bite.
- Registry entries in `restoreSpec` are **stubbed with a TODO** (no URL/SHA256).
  With no URL, `ensureModel` uses a locally staged file or fails cleanly, which
  is correct while the weights are unmirrored and one of them has no licence
  grant.
- Tests: `restore_test.go` (skip table incl. the exact-2× boundary, blend
  arithmetic, nil/inactive no-ops, real-model monotonic strength, SR sizing) and
  `restore_e2e_test.go` (real RAW through the full JPEG pipeline; the fast case
  asserts a web export is byte-identical with denoise at full strength, proving
  the skip guard). `tile_bench_test.go` records the CPU-overhead measurements.
- Tooling: `scripts/setup-devmodels.ps1` (hash-verified model staging),
  `setup-ort.ps1 -DirectML` / `-CUDA`, `tools/cmpcrop` (1:1 A/B crop viewer for
  judging model quality — the tool to reach for when evaluating criterion 2
  candidates).

## What is left

Not blocked on compute or quality. In rough order of who has to act:

1. **Decide what to ship** (you). Each fast path needs a runtime the installer
   does not carry:
   - *Fast denoise* → CUDA only → ~2.3 GB optional pack, NVIDIA only.
   - *Fast SR* → DirectML → only ~20 MB, but it drops Windows ORT from 1.27.1 to
     1.24.4, so all seven existing models (masks, inpaint, eye-detect, depth…)
     need a re-verification pass. That test time is the real cost, not the bytes.
   - *Ship on CPU as-is* → a 2 MP crop denoises in ~31 s, the 12 MP budget in
     ~3.1 min, a full 42 MP frame in ~10.8 min. Viable framed as a deliberate
     per-image rescue; not viable as a batch feature. SR on CPU is poor value at
     97.9 s/MP.
   - *A DirectML-compatible denoise model* (criterion 2) → the only route to fast
     vendor-neutral denoise. Unbounded search, but `cmpcrop` plus the throughput
     harness make candidate evaluation quick.
2. **Mirror the weights** (you) — 139 MiB total; SCUNet is MIT and safe, Swin2SR
   must be re-exported from `caidas` (Apache-2.0).
3. **Decide SR's `LongEdge` semantics** (you) — three options in the roadmap.
   Export currently never enlarges, so SR has no natural home until this lands.
4. **Auto-strength from measured noise** (code) — required for correctness, not
   polish; a constant default is wrong for one of the two measured frames
   whichever value is chosen.
5. **Daemon wiring, progress, aprot + UI** (code) — `RestoreOptions.Progress`
   currently connects to nothing, which matters because CPU denoise is minutes.

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
