# Third-party notices

marraw itself is MIT licensed (see [LICENSE](LICENSE)). The distributed
application also contains, or statically links against, the components below.
This file is the notice required by those licenses.

*This document is a good-faith summary, not legal advice.*

---

## LibRaw

- **Version:** 0.22.1
- **Copyright:** © 2008–2025 LibRaw LLC — <https://www.libraw.org>
- **Upstream source:** <https://www.libraw.org/data/LibRaw-0.22.1.tar.gz>
- **License as used by marraw: CDDL-1.0**

LibRaw is dual-licensed and lets the user choose: **LGPL-2.1** *or*
**CDDL-1.0**. **marraw elects the CDDL-1.0 option.**

This choice is deliberate. `marrawd` links LibRaw **statically** (see the
`#cgo LDFLAGS` in `internal/libraw/libraw.go`). Under LGPL-2.1 §6, static
linking obliges the distributor to ship whatever is needed for a user to
relink the application against a modified LibRaw. CDDL-1.0 is a *file-scoped*
copyleft: §3.6 permits distributing the executable under terms of our choosing,
provided the Covered Software (LibRaw's own source files, and any modifications
to them) remains available under the CDDL. Static linking of separate files
does not make marraw's own code Covered Software.

Accordingly, to satisfy CDDL-1.0 §3.1 and §3.5:

- LibRaw's source is the **unmodified** upstream tarball linked above. marraw
  does not vendor a patched copy of any LibRaw source file.
- The **only** change marraw makes to the LibRaw distribution is to its build
  file `Makefile.mingw`: the `-DLIBRAW_NOTHREADS` flag is removed so that
  concurrent `libraw_data_t` handles are safe. That change is applied at build
  time by [`scripts/setup-libraw.ps1`](scripts/setup-libraw.ps1), which is part
  of this repository and is itself the machine-readable form of the
  modification.
- The full CDDL-1.0 text ships as `LICENSE.CDDL` inside the upstream tarball,
  and is also available at
  <https://opensource.org/license/cddl-1-0>.

LibRaw in turn incorporates:

| Component | Copyright | License |
| --- | --- | --- |
| dcraw.c (raw decoder) | 1997–2018 Dave Coffin | as redistributed by LibRaw; LibRaw excludes dcraw's RESTRICTED code |
| DCB demosaic, FBDD denoise | 2010 Jacek Gozdz | BSD 3-clause |
| X3F (Foveon) tools | 2010 Roland Karlsson | BSD-style |
| portions of Adobe DNG SDK 1.4 | 2005 Adobe Systems Inc. | MIT |

marraw does **not** build or link LibRaw's GPL2/GPL3 demosaic packs. Only the
core `src/` tree is compiled, so no GPL obligations attach. The demosaic
algorithms marraw exposes (VNG, PPG, AHD, DHT) are all core LibRaw.

---

## Electron

- **Copyright:** © Electron contributors; © 2013–2020 GitHub Inc.
- **License:** MIT
- <https://github.com/electron/electron/blob/main/LICENSE>

Electron bundles **Chromium** (BSD 3-clause and a number of other permissive
licenses) and **Node.js** (MIT). The complete, per-component license text for
the exact Electron build is shipped inside the installed application as
`LICENSES.chromium.html`, alongside `LICENSE` in the app's resources
directory.

---

## Go modules (`marrawd`)

Direct dependencies:

| Module | License |
| --- | --- |
| `github.com/marrasen/aprot` | see <https://github.com/marrasen/aprot> |
| `golang.org/x/image` | BSD 3-clause |
| `golang.org/x/sync` | BSD 3-clause |
| `modernc.org/sqlite` | BSD 3-clause |

Plus their transitive dependencies as pinned in [`go.mod`](go.mod) /
[`go.sum`](go.sum). The Go standard library is BSD 3-clause. SQLite itself is
in the public domain; `modernc.org/sqlite` is a BSD-3-clause Go translation of
it, so no C SQLite is linked.

---

## Client (npm)

The renderer bundles React (MIT), Zustand (MIT), Tailwind CSS (MIT),
shadcn/ui (MIT), Base UI (MIT), lucide-react (ISC), TanStack Virtual (MIT),
sonner (MIT), next-themes (MIT), and their transitive dependencies as pinned
in `client/package-lock.json`. All are permissive (MIT / ISC / BSD /
Apache-2.0).

Fonts: **Geist** and **Geist Mono** (© Vercel), SIL Open Font License 1.1.

---

## ML runtime and model weights

The AI features (masks, scene detection) use **ONNX Runtime** (© Microsoft,
MIT), whose shared library ships next to `marrawd`
(`scripts/setup-ort.ps1`/`.sh` fetch the upstream binary release).

Model weights are **not** distributed with marraw — the app downloads them on
first use from <https://github.com/marrasen/marraw-models> (or the pinned
upstream), verifying each against a SHA-256 baked into `internal/aimask` and
`internal/eyes`:

| Weights | Purpose | Origin | License |
|---|---|---|---|
| ISNet general-use | Subject masks | [xuebinqin/DIS](https://github.com/xuebinqin/DIS) via [rembg](https://github.com/danielgatis/rembg) | Apache-2.0 |
| Depth Anything V2 **Small** | Depth masks | [onnx-community/depth-anything-v2-small](https://huggingface.co/onnx-community/depth-anything-v2-small) | Apache-2.0 (only the Small variant — larger ones are CC-BY-NC and are not used) |
| DPT-Large / ADE20K | Scene (semantic) masks | exported from [smp-hub/dpt-large-ade20k](https://huggingface.co/smp-hub/dpt-large-ade20k) (segmentation_models.pytorch) | MIT |
| YuNet (2023mar) | Face + eye-landmark detection, for closed-eye culling | [opencv/opencv_zoo](https://github.com/opencv/opencv_zoo/tree/main/models/face_detection_yunet) | MIT (the model README states MIT covers all files in the model directory, weights included) |
| open-closed-eye-0001 | Eye open/closed classification, for closed-eye culling | [OpenVINO Open Model Zoo](https://github.com/openvinotoolkit/open_model_zoo/tree/master/models/public/open-closed-eye-0001) | Apache-2.0 |

---

## Regenerating this file

The tables above list direct dependencies only. To produce an exhaustive,
machine-generated manifest of every transitive package and its license text:

```powershell
go install github.com/google/go-licenses@latest
go-licenses report ./cmd/marrawd

npx --yes license-checker-rseidelsohn --prefix client --summary
```
