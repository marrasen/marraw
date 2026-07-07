# Handoff: marraw — Library, Culling & Develop surfaces

## Overview
marraw is a fast, local **RAW photo culling + develop desktop app** for Windows (Electron shell wrapping a Go daemon that serves previews + an RPC API). This handoff covers a consolidated redesign built around a single **zero-chrome, edge-to-edge canvas** with four modes — **Library · Cull · Develop · Export** — reachable from a constant top segmented control plus a `⌘K` command palette. It adds the surfaces the original app was missing or that needed rethinking: the curated Library (folder management), the Add-folder picker, folder organize menus, empty/scanning states, the Loupe + crop/straighten, the WB eyedropper, batch multi-select, and a unified background-task tray.

The photograph is the content; the UI chrome must stay quiet and recede so images read true. Dense, precise, "instrument-like," keyboard-first. Not playful or marketing-y.

## About the Design Files
The files in this bundle are **design references created in HTML** — prototypes showing the intended look and behavior. They are **not production code to copy directly**. The task is to **recreate these designs in marraw's existing codebase** using its established stack and patterns:

- React 19 + TypeScript + Vite + **Tailwind CSS v4** + **shadcn/ui** (built on `@base-ui/react`)
- State via **Zustand**; virtualized grid via `@tanstack/react-virtual`
- Icons: **lucide-react**; toasts: **sonner**
- No router — one window; the center view switches between **Grid** and **Loupe** via state; panels are always-present asides; dialogs are modal overlays

The HTML uses a small custom "DC" runtime (`support.js`) with `<x-dc>`, `<sc-for>`, `<sc-if>` and `{{ }}` bindings, plus inline styles — this is a prototyping harness only. Do not port the runtime; read the markup for structure, measurements, colors, and copy, and rebuild each surface with shadcn/Tailwind components.

`original-brief.md` is the product brief the design was built against (component inventory, interaction model, tokens).

## Fidelity
**High-fidelity (hifi).** Final colors, typography, spacing, and interaction detail are specified. Recreate pixel-accurately using the codebase's existing components. Where the prototype fakes data (photo thumbnails are CSS gradients, histogram is a generated polygon), substitute the real preview/RAW data pipeline.

## Layout model (global)
Two layout archetypes are used depending on mode:

1. **Structured window** (Library, Batch, Empty, Scanning) — a full-height flex column:
   - Top bar `48px`: logo mark + shoot name (left), centered segmented control (Library·Cull·Develop·Export), `⌘K` affordance (right).
   - Body flex row: left **folder rail** `214px` (`border-r`) · center column (filter bar `47px` → grid → status bar `26px`) · right **Develop/Edit panel** `300px` (`border-l`).
2. **Cinema canvas** (Cull, Develop, Loupe, Rendering, Crop, Eyedropper) — the photo fills the window edge-to-edge; every control is **floating glass** that auto-hides when idle and never pushes the image. Top HUD = three glass clusters (status left, segmented center, `⌘K`/context right). Bottom = glass confirm bar / zoom bar / filmstrip.

Prototype canvases are drawn at **1480 × 864**; treat as a resizable desktop window ~1280–1600px wide. Minimum comfortable window 1280px.

## Screens / Views

### 1. Library (Grid) — the home base
- **Purpose:** Browse, filter, and triage the whole shoot. Every session starts here. `Enter` opens the focused photo in the Cull loupe.
- **Layout:** Structured window (see above).
- **Folder rail (`214px`, `bg #0e0f12`):**
  - "Filter folders" search input (`30px`, `bg rgba(255,255,255,.05)`, border `rgba(255,255,255,.08)`, radius `8px`).
  - Section header row: "IN YOUR LIBRARY" (uppercase, `10px`, `#6f747c`, letter-spacing `.07em`) + count (`Geist Mono 10.5px #5f636b`).
  - **Curated folder tree — this is the key model:** groups are the **parent folder name on disk** (NOT auto date-detection). Group header row shows the parent folder name (bold `12.5px #e6e7ea`) + a caret (`9px`, rotate 90° when open) and a muted middle-elided path line below it (`Geist Mono 10px #5f636b`, e.g. `C:\Users\Marcus\Photos`). Under each group, the **added shoot folders** are leaf rows (indent `padding-left:24px`, `30px` tall) with a folder icon + name (ellipsis-truncated) + photo count. Selected leaf: `bg rgba(124,131,255,.16)`, icon `#c3c7ff`, name `#e6e7ea`, weight 600.
  - **"+ Add folder"** dashed button (`32px`, border `1px dashed rgba(124,131,255,.5)`, `bg rgba(124,131,255,.08)`, text `#c3c7ff`).
  - Footer "Settings" row with gear icon.
- **Filter bar (`47px`, `border-b`):** Rating filter ("Rating ★★★☆☆ & up" — filled amber `#f5b544`, empty `rgba(255,255,255,.22)`); flag segmented **All / Picks / Not excluded / Excluded** (active pill `bg rgba(124,131,255,.24)` text `#c3c7ff`); a **thumbnail-size slider** (grid icon + 104px track + `220px` readout); right-aligned live count `412 shown / 1,284`.
- **Grid:** `display:grid; grid-template-columns:repeat(5,1fr); gap:12px` (columns vary with the size slider, cell target 120–400px, default 220). Each cell `aspect-ratio:3/2`, radius `4px`. Overlays: rating stars bottom-left (amber, on `rgba(0,0,0,.5)` pill, `9px`), flag dot top-right (`9px` square, pick `#34d399` / reject `#f26d6d`), focus ring `2px #7c83ff`.
- **Status bar (`26px`, `Geist Mono 11px #8b8f96`):** path · shown/total · picked count (`#5ee0b0`) · daemon connection dot (`#34d399`).
- **Right Edit/Develop panel (`300px`):** photo header (filename, pick/exclude toggle group [pick green, exclude red], 5-star row, EXIF line), RGB histogram (`52px`, dark `#08090b` inset), collapsible Develop groups (dot marks non-default group), Copy/Paste/Reset footer.

### 2. Add folder — picker modal
- **Purpose:** The old full-filesystem browser, repurposed as a modal picker. Navigate drives/network, tick folders, import them as library roots. Files never move.
- **Layout:** Centered modal `760 × 520` (`bg #141619`, border `rgba(255,255,255,.12)`, radius `14px`) over a dimmed/blurred backdrop.
  - Title bar: "Add folder to library" + subtitle "Browse your drives and pick folders of RAW photos" + close ✕.
  - Left **Quick access** rail `184px` (`bg #0f1013`): This PC, Local Disk (C:), a selected drive, Pictures, Desktop, network share — each `32px` row with a drive/folder glyph.
  - Main: breadcrumb bar `42px` (back chevron button + `Photos (C:) › Marcus › Weddings 2026`, last segment bold) + item count; folder list of `40px` rows — checkbox (`16px`, checked = `#7c83ff` fill + dark check), folder icon, name, RAW count (`Geist Mono 11px`); folders with no RAWs are dimmed (`#5f636b`) and non-selectable.
  - Footer: **Include subfolders** toggle (on) + summary `2 folders · 3,748 RAW files` + Cancel / **Add to library** (primary `#7c83ff`).
- **Include-subfolders semantics (important):** the toggle controls *recursion depth per root*, it does NOT flatten. Off = only RAWs directly in the picked folder. On = every RAW anywhere beneath it, recursively — but the nested folder structure is still shown under the root. Store the flag per-root (editable later via the folder context menu). The footer RAW count must react to the toggle. Skip noise folders (`export/`, `_selects/`, `.thumbnails`, sidecar dirs) either way; follow symlinks once with loop-guarding.

### 3. Organize — folder context menus
- **Purpose:** Right-click actions on rail rows.
- **Shoot-folder menu** (`262px`, `bg rgba(20,22,26,.98)`): target header (name + RAW count) → Open in Cull (`Enter`) · Rename… (`F2`) · **Locate on disk** (opens OS Explorer, accent-highlighted, "Explorer" hint) · Copy path · Include subfolders (check) · Rescan for new photos · separator · **Remove from library** (red `#f28c8c`, sublabel "Files stay on disk").
- **Parent-group menu:** Rename group… · **Locate on disk** · Rescan all shoots · Move up / Move down (reorder; drag also works) · separator · **Remove group** (red, "2 shoots · files stay on disk").
- **Rename is a display alias only** — it never touches the on-disk folder name (shown inline: input with a note that the disk folder stays `Tobias and Elisabeth Wedding`). Offer a separate **"Rename on disk…"** for the real rename.
- Menu row spec: `32px` tall, `padding:0 10px`, gap `10px`, icon `14px`, label `13px`, right hint `Geist Mono 10.5px #5f636b`. Separators `1px rgba(255,255,255,.08)`.

### 4. Empty state (first run)
- Rail collapses to "No folders yet" + the dashed **Add folder** button. Center canvas: a `72px` rounded folder-plus mark (`border rgba(124,131,255,.35)`, `bg rgba(124,131,255,.1)`), heading "Your library is empty" (`23px/600`), body "Add a folder of RAW photos to start culling. marraw reads them where they live — **your files never move or change**.", primary **Add folder** button (`42px`), and "or drop a folder anywhere in this window". Segmented control + `⌘K` are dimmed (`opacity:.5`).

### 5. Scanning / generating previews
- Freshly added folder fills in progressively: first ~9 cells show resolved thumbnails, the rest hold **skeletons** (`linear-gradient(105deg,#141619,#1a1d22 44%,#23272e 50%,#1a1d22 56%,#141619)` — animate a left→right shimmer in the real build). A **task chip** sits in the top bar (see Task tray for the shared chip spec). Rail shows a spinner next to the scanning folder. Filter count reads `342 ready / 1,284`. Status bar: "Scanning… 342 / 1,284" + amber dot "indexing RAW headers". Right panel is a muted "Select a photo to develop once previews are ready" placeholder. Ready frames are rateable immediately — nothing blocks on the full scan.

### 6. Cull (cinema loupe + time-gap scrubber)
- **Purpose:** Triage fast against a full-size confirm loupe. Arrow through the take; rate (1–5) and flag (P/X) while the big preview confirms each call.
- Glass top HUD (status/segmented/group-by-gap control). **Confirm bar** (bottom-center glass): filename + star row, Pick/Reject buttons (green/red), and the quick-triage sliders (Exposure/Contrast/Temp). **Scrubber deck** (bottom glass, full width): thumbnails split into **time-gap groups** (a new group starts when the inter-frame gap exceeds a threshold, default 6 min) with a vertical "+N min gap" divider between groups and a per-group time-range header. Focus ring `#7c83ff`, pick/reject dots.
- **Group-by-gap threshold:** dropdown presets — 1–2 min (bursts) · 5–10 min (a happening, recommended 5) · 30 min (whole scenes) · Custom (any value) · Off (one flat grid).
- **Contact sheet (`G`):** blows the scrubber into a full multi-row grid, one section header per time-gap group (clock icon + range + frame count + "+N min gap before"); `Esc` collapses back to the loupe.

### 7. Loupe (full-res viewer)
- Single-photo zoom/pan. `Z`/`Space` toggle 1:1 ↔ fit; `+`/`-` step. **Navigator inset** bottom-right (`200px` glass card with a mini frame + a highlighted viewport rectangle + zoom %). **Zoom control** bottom-center glass: prev/next frame arrows + `7 / 1,284` counter, Fit/1:1 segmented, zoom slider + % readout. Stars/flags are **never** drawn over the image (kept clean) — they live in the right panel.

### 8. Rendering indicator (1:1 decode in progress)
- When jumping to 1:1, a fast soft preview shows for a beat while the full-res RAW tile decodes. Make this **obvious** so a sharp shot is never mistaken for blurry and rejected: image shown blurred (`filter:blur(9px)`), a **2px top indeterminate progress line** (moving accent gradient segment), a centered glass **badge** ("Rendering full resolution" / "1:1 tile · decoding RAW" + spinner), and a "Rendering 100%" state in the zoom bar. Badge fades the instant the tile lands.

### 9. Crop & straighten overlay
- `R` drops the crop grid over the loupe. Everything outside the crop box dims (`rgba(4,6,9,.62)` scrims). Crop box: `1px` white border + rule-of-thirds grid (`rgba(255,255,255,.28)`), 4 corner L-handles + 4 edge bar handles (white), center pill showing ratio + pixel dims (`3:2 · 6000 × 4000`). Bottom glass control bar: aspect presets (Original/3:2/2:3/1:1/4:5/16:9), **Straighten** slider (bipolar from center, `+1.2°` readout), Reset / **Done**.

### 10. White-balance eyedropper
- The WB pipette turns the cursor into a sampler with a **magnified loupe** (`138px` circle, pixel grid, `14px` center target outlined in accent) + a **pipette cursor** icon. A readout tag shows the sampled swatch + `R182 G180 B175` + solved `→ 5450 K · +3`. The **WB panel** flips to **Custom** (segmented As shot / Auto / Custom + an active pipette icon button) and Temperature (gradient blue→amber track) + Tint (green→magenta track) dials jump live to the solved values. Bottom hint bar: "Click a neutral gray to set white balance · Right-click resets · `Esc` cancels".

### 11. Batch / multi-select
- **Selection:** Shift-click extends a range, ⌘-click toggles, `⌘A` selects all. Selected cells get `bg rgba(124,131,255,.14)` + `2px #7c83ff` border + a `16px` check badge top-left; the range **anchor** gets a `2px #fff` ring.
- **Selection bar** replaces the filter row (`bg rgba(124,131,255,.1)`, `border-b rgba(124,131,255,.28)`): "**12** selected" chip + batch Rate (★★★★★) + Pick (green) / Reject (red) + **Paste settings** + **Restore original** + "Esc to clear".
- **Right panel becomes Relative adjustment:** "12 photos selected" + note "Deltas add to each photo's own current value — mixed edits stay intact." Delta sliders Exposure `+0.20 EV` / Contrast `+8` / Saturation `−3` (bipolar). **No Apply button** — edits are live: thumbnails update as you drag (footer: accent dot + "Thumbnails update live as you drag"). An info card points to Develop + Paste settings for absolute edits.

### 12. Export dialog (modal)
- Centered `680px` modal + a background task chip. Fields: Destination (path + Choose…), Format (JPEG / 16-bit TIFF segmented), Quality slider (`90`), Resize (Full res / Long edge + px input `2160`), Color space (sRGB / Adobe RGB / ProPhoto). Footer summary "96 files · JPEG q90 · 2160px · runs in the background" + Cancel / **Export**. Export runs in the background (task chip), doesn't block. **Full light + dark themes provided.**

### 13. Settings dialog (modal)
- Left-nav modal `760 × 480`: General / Cache / Sidecars / Performance. Cache section: directory + Change…, on-disk usage meter (`18.4 GB used · 40 GB limit`) + Clear cache (red). Sidecars: "Write edit sidecars" toggle (portable non-destructive edits beside each RAW). **Full light + dark themes provided.**

### 14. Task tray (background jobs)
- **Collapsed:** the top-bar chip becomes a summary pill — spinner + "3 tasks" + `84px` progress + `41%` + chevron.
- **Expanded tray** (`384px` card, `bg rgba(16,18,22,.98)`): header "BACKGROUND TASKS · 3 running", one row per job (spinner icon + label + count on right + `4px` progress bar + muted sublabel/ETA + **cancel ✕**). Indeterminate jobs use a moving gradient segment. Finished jobs settle to a green-check confirmation ("Imported Weddings 2026 · Done" + "Show in library" + dismiss ✕). Footer: "Runs in the background" + **Cancel all** (red).
- **Every background process is cancellable from its indicator.** The Generating-previews and Exporting chips share ONE style (see Design Tokens → Task chip).

## Interactions & Behavior (keyboard-first — central to the feel)
- **Navigate:** arrows (↑/↓ = one grid row in Grid, one frame in Loupe); **Shift+arrow** extends selection; `⌘A` select all.
- **Rate:** `1`–`5` set, `0` clears. **Flag:** `P` pick · `X` exclude · `U` unflag.
- **View/modes:** `Enter` → Loupe · `Esc` → back to Grid (also cancels crop / eyedropper / active control first); `G` contact sheet; `Z`/`Space` 1:1↔fit.
- **Develop:** press a control's letter to focus (`E`xposure, `C`ontrast, `T`emp, `I` tint, `K`elvin, `G`amma, `S`hadow, s`A`turation, `V`ibrance, vignette `O`, `H`ighlight, `N`oise, `M`edian, `D`emosaic, `B`rightness, `W`B mode), then `+`/`-` to adjust (Shift = big steps). `R` = crop.
- **Edits:** `⌘C`/`⌘V` copy/paste settings, `⌘Z`/`⌘Y` undo/redo per photo, `⌘E` export. `⌘K` opens the command palette (jump to any control/mode by name).
- **Slider fill** runs from the control's neutral point to the thumb (bipolar for centered params); reset (↺) affordance appears only when changed; value readouts use `tabular-nums`.
- Chrome auto-hides when idle in cinema modes; visible affordances must never block keyboard use.

## State Management (Zustand)
- **Library roots:** array of `{ path, displayAlias, parentName, parentPath, includeSubfolders, order }`. Groups are derived by shared parent path. Reorder is a stored preference; alias rename is display-only.
- **Photos:** per-shoot list `{ id, filename, rating(0–5), flag('pick'|'exclude'|'none'), exif, captureTime, previewState('skeleton'|'ready'), edits }`. Time-gap grouping derived from `captureTime` + threshold.
- **View state:** mode (Library/Cull/Develop/Export), center view (Grid/Loupe), focused photo, selection set + range anchor, zoom (fit|scale), crop mode, eyedropper active.
- **Filters:** minRating, flagFilter, thumbSize.
- **Develop:** per-photo edit stack; group open/collapsed state (persisted); "changed" flags per control. Batch = relative deltas applied live over each photo's current values.
- **Background tasks:** list of `{ id, type('previews'|'export'|'cache'|'import'), label, sublabel, progress|indeterminate, status('running'|'done'), cancel() }`. Chip/tray render from this; cancel calls the job's canceller.
- **Grouping threshold:** persisted gap value + on/off.

## Data fetching
- Go daemon over RPC: folder scan (indexes RAW headers → capture time, EXIF), progressive preview generation (thumbnail + 1:1 tile decode), non-destructive edit apply (live thumbnail refresh for batch), export job, cache scan/clear. Everything expensive runs in the background and reports progress into the task list. Virtualize the grid (`@tanstack/react-virtual`).

## Design Tokens

### Colors — dark (default)
- Page `#0d0e0f` · window/canvas `#0c0d0f` · panels/rails `#0e0f12` · cards `#141619` · deep inset `#08090b`
- Hairlines `rgba(255,255,255,.07)`–`rgba(255,255,255,.12)`
- Text primary `#e6e7ea` · secondary `#c4c7cc` · muted `#8b8f96` · faint `#5f636b` / `#6f747c`
- Accent `#7c83ff` · accent-on-surface text `#c3c7ff` / `#aab0ff` · accent bg `rgba(124,131,255,.12)`–`.24)` · accent border `rgba(124,131,255,.32)`–`.5)`
- Rating amber `#f5b544` · pick/success `#34d399` (text `#5ee0b0`) · reject/danger `#f26d6d` (text `#f28c8c`)
- Cinema glass: `rgba(12,14,18,.55)`–`.78)` + `backdrop-filter:blur(16–22px)` + border `rgba(255,255,255,.1)`–`.14)`

### Colors — light
- Page `#f4f5f6` · panels `#ffffff` · rails `#f7f8f9` · segmented track `#f1f2f4`
- Hairlines `rgba(0,0,0,.07)`–`.12)`
- Text primary `#1a1c1f` · secondary `#3a3d43` · muted `#6b7078` · faint `#9aa0a6` / `#a9adb3`
- Accent `#5b62e6` · active text `#4c53c9` · accent bg `rgba(91,98,230,.12)`–`.16)`
- Success `#1f9d63` · danger `#d1544f` / `#c53d3d` · rating amber `#f5b544` (unchanged)
- Cinema glass (light): `rgba(255,255,255,.7)`–`.94)` + blur + border `rgba(0,0,0,.06)`–`.09)`, dark text
- Histograms stay on a dark inset (`#08090b` / `#111318`) in both themes.

### Task chip (shared — previews & export)
`display:flex; align-items:center; gap:11px; bg rgba(12,14,18,.78); border 1px rgba(255,255,255,.12); radius 11px; padding 10px 10px 10px 14px; box-shadow 0 16px 40px -14px rgba(0,0,0,.7)`. Spinner (`15px` ring, stroke `#aab0ff`) + text column (`Geist Mono 10.5px` label `#c4c7cc` / count `#8b8f96`, `4px` progress bar `#7c83ff` on `rgba(255,255,255,.12)`) + **cancel ✕** button (`24px`, radius `7px`, border `rgba(255,255,255,.12)`, `#8b8f96`). Light variant: `rgba(255,255,255,.85)` bg, `#5b62e6` fill, `#3a3d43`/`#6b7078` text.

### Typography
- UI: **Geist** (Geist Variable in-app), weights 300–700. Numeric readouts & paths: **Geist Mono**, always `tabular-nums`.
- Scale used: screen titles `18px/600`; big headings `23–40px/600`, letter-spacing `-.02` to `-.025em`; body `13.5–15.5px/1.55–1.6`; labels `11.5–13px`; micro/uppercase eyebrows `10px` letter-spacing `.06–.07em`; mono readouts `10–12px`.

### Spacing / radius / shadow
- Radius: controls/inputs `7–9px`, cards/modals `11–14px`, thumbnails `3–4px`, pills/tracks `2px`, `--radius` baseline `0.625rem`.
- Fixed widths: folder rail `214px`, right panel `300px`, picker quick-access `184px`, export modal `680px`, settings modal `760×480`, task tray `384px`.
- Sliders: `3px` track, `10–12px` white circular thumb with `1px rgba(0,0,0,.3)` border + soft shadow.
- Shadows: cinema floating `0 24px 60px -18px rgba(0,0,0,.7)`; modals `0 50px 120px -30px rgba(0,0,0,.9)` (dark) / `-30px rgba(0,0,0,.5)` (light); window frame `0 40px 90px -30px rgba(0,0,0,.8)`.

## Assets
No external image assets. Photo thumbnails and the full-frame previews are **placeholders** (CSS gradients) — replace with the daemon's real preview pipeline. The histogram is a generated RGB polygon overlay — replace with real per-channel histogram data. Icons in the prototype are hand-inlined SVGs matching lucide shapes; **use lucide-react** for the real build (folder, folder-plus, search, settings/sliders, pencil, external-link, copy, refresh-cw, check, trash, chevrons, x, pipette/eyedropper, image, loader spinner, etc.). The logo is a rounded `m` mark on the accent color.

## Files
- `Marraw Layout Explorations.dc.html` — all surfaces, dark-first with a Light-theme section at the end (Library, Cull, Develop in light + the token-swap recipe). Each surface is a labeled "plate" (LIBRARY, ADD FOLDER, ORGANIZE, EMPTY, SCANNING, LOUPE, RENDERING, CROP, WHITE BALANCE, BATCH, TASK TRAY, CULL, DEVELOP, CONTACT SHEET, GROUPING, EXPORT, SETTINGS, KEYBOARD).
- `support.js` — the prototyping runtime (reference only; do not port).
- `original-brief.md` — the source product brief (stack, component inventory, interaction model, baseline tokens).

To view the prototype: open `Marraw Layout Explorations.dc.html` in a browser (it is a free-pan/zoom canvas). Read the inline styles of each plate for exact measurements.
