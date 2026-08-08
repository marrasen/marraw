# marraw — design handoff

> **Describes the app as of 2026-07-07, and has not been kept up since. Reread
> it against the running app before handing it to anything.** Known to be out
> of date already: it says Windows-only (macOS and Linux ship too), one window
> (there is a pop-out viewer, and remote/multi-window), Grid and Loupe as the
> only views (cull and cinema exist, plus the guest share pages and the library
> rail), and it gives fixed panel widths the responsive toolbar work replaced.

> A single-file brief describing marraw's UI so it can be designed/redesigned in Claude Design.
> Attach this file to a Claude Design project, then tell it which screen(s) you want to work on.
> **Edit the "What I want to work on" section at the bottom before uploading** so the design work is aimed at your goal.

---

## 1. What marraw is

**marraw** is a fast, local **RAW photo culling + develop desktop app** for Windows — a lightweight Lightroom for triaging a shoot (rate/pick/reject thousands of frames quickly) and then developing the keepers non-destructively. Single user, single window, keyboard-first, dark by default. The photograph is the content; the UI chrome must stay quiet and out of the way.

**Audience:** photographers doing a post-shoot cull + edit pass on large folders of RAW files (Sony `.ARW` etc.).

**Feel to preserve:** dense, precise, "instrument-like," fast. Not playful, not spacious/marketing-y. Chrome recedes so images read true.

## 2. Stack & constraints (for realistic designs)

- **Frontend:** React 19 + TypeScript + Vite + **Tailwind CSS v4** + **shadcn/ui** (built on `@base-ui/react`). State via **Zustand**. Virtualized grid via `@tanstack/react-virtual`. Icons: **lucide-react**. Toasts: **sonner**.
- **Shell:** Electron, wrapping a Go daemon that serves previews + an RPC API. Desktop window, not a responsive website — design for a **fixed desktop window** (~1280–1600px wide), resizable.
- **No router.** One window; the center view switches between **Grid** and **Loupe** via state. Panels are always-present asides, dialogs are modal overlays.
- Everything expensive (preview generation, export, cache scans) runs in the background and reports progress.

## 3. Global layout (current)

A full-height flex column. The main row has three columns; a status bar sits under the center column.

```
┌──────────────────────────────────────────────────────────────────────┐
│ (no OS-style title bar of its own — native window chrome)              │
├──────────┬───────────────────────────────────────────┬───────────────┤
│ Folder   │  Filter bar (filters · thumb-size · counts)│  Edit / Develop│
│ tree     ├───────────────────────────────────────────┤  panel         │
│ (w-60,   │                                           │  (w-72,        │
│  ~240px) │   GRID view  ─or─  LOUPE view              │   ~288px)      │
│          │                                           │   • Photo header│
│          │                                           │   • Histogram   │
│          ├───────────────────────────────────────────┤   • Develop     │
│          │  Status bar (path · counts · task · conn) │     groups      │
└──────────┴───────────────────────────────────────────┴───────────────┘
   left aside            center main (flex-1)               right aside
```

- **Left:** `FolderTree` — fixed width `w-60` (240px), `border-r`.
- **Center:** `FilterBar` (top) → `GridView` **or** `LoupeView` → `StatusBar` (bottom). Fills remaining width.
- **Right:** `EditPanel` — fixed width `w-72` (288px), `border-l`.
- **Modals:** `ExportDialog`, `SettingsDialog` (centered overlays).
- **Empty state** (no folder selected): center reads *"Pick a folder with RAW photos on the left to get started."* + a small Settings button.

## 4. Screens / surfaces (design targets)

### 4.1 Library / Grid view (`GridView`)
- Virtualized thumbnail grid; ~4 columns default, **variable via a thumbnail-size slider** in the filter bar (cell target width 120–400px, default 220).
- Each cell shows the photo + its **rating (0–5 stars)** and **flag (pick / exclude / none)**; supports single + range + toggle selection.
- Keyboard-first culling (see §6). Enter opens the focused photo in Loupe.

### 4.2 Loupe view (`LoupeView`)
- Single-photo zoom/pan viewer, loads full-res tiles. Zoom is `fit` or a scale (Z/Space toggles 1:1 ↔ fit; +/- steps).
- Hosts the **crop/straighten overlay** (`CropOverlay`) when crop mode is on.
- Stars/flags are **not** shown over the image (kept clean) — they live in the right panel's photo header.

### 4.3 Filter bar (`FilterBar`, top of center)
- Rating filter (min stars, 0–5), flag filter (**All / Picks / Not-excluded / Excluded**), a **thumbnail-size slider**, and a live **"shown / total"** count.

### 4.4 Edit / Develop panel (`EditPanel`, right aside — the densest surface)
Scrollable column. Order:
1. **Photo header:** filename (truncates), a **5-star rating** row (filled stars = amber), a **pick/exclude** toggle group (pick=green, exclude=red), and an EXIF line (model · ISO · shutter · f/aperture · focal mm).
2. **Histogram** (`Histogram`) — RGB.
3. **Develop** — collapsible groups (open state persisted; a dot marks a group with non-default values). Each control is a labeled slider with a **tabular-nums value readout** and a reset (↺) affordance that appears only when changed; **slider fill runs from the control's neutral point to the thumb** (bipolar for centered params). Groups & controls:
   - **Crop & straighten:** Crop toggle (opens Loupe overlay), Straighten angle (−15°…+15°).
   - **Tone:** Exposure (−2…+3 EV), Preserve highlights, Brightness, Gamma, Shadow slope, Contrast, Whites, Blacks, Shadows, Highlights.
   - **Presence:** Clarity, Texture, Dehaze.
   - **White balance:** mode toggle **As shot / Auto / Kelvin** + an **eyedropper (pipette)** to pick a neutral gray; Temperature (Kelvin or ±100) and Tint.
   - **Color:** Saturation, Vibrance, Shadow-tint hue + amount, Highlight-tint hue + amount.
   - **Effects:** Vignette.
   - **Detail:** Sharpen, Highlight recovery (Clip/Unclip/Blend/Rebuild button row), Noise reduction, FBDD denoise (Off/Light/Full), Median passes, Demosaic (Auto/VNG/PPG/AHD/DHT), CA red/cyan, CA blue/yellow.
   - Footer: **Copy / Paste / Reset** buttons + a hint line about drag-to-preview and hotkeys.
4. **Relative batch adjustment** (only when >1 photo selected): Exposure / Contrast / Saturation deltas + "Apply to N photos" with a progress bar.

### 4.5 Folder tree (`FolderTree`, left aside)
- Nested folder navigation for the library; selecting a folder loads its photos.

### 4.6 Status bar (`StatusBar`, bottom of center) + Task tray (`TaskTray`)
- Current path, shown/total counts, background-task progress, daemon connection state.

### 4.7 Export dialog (`ExportDialog`, modal)
- Destination, format (**JPEG** / **16-bit TIFF**), quality, with **streaming export progress**.

### 4.8 Settings dialog (`SettingsDialog`, modal)
- Cache directory + size management, **clear cache**, and a **write-sidecar** toggle (portable `.xmp`-style edit sidecars).

## 5. Component inventory (shadcn/ui already in the app)

`button`, `input`, `select`, `dialog`, `dropdown-menu`, `badge`, `progress`, `separator`, `skeleton`, `tooltip`, `slider`, `toggle`, `toggle-group`, `scroll-area`, `sonner` (toaster). Plus app components: `FolderTree`, `FilterBar`, `EditPanel`, `Histogram`, `CropOverlay`, `ExportDialog`, `SettingsDialog`, `StatusBar`, `TaskTray`, `GridView`, `LoupeView`. Prefer designing with these; call out any new component you introduce.

## 6. Interaction model (keyboard-first — central to the app's feel)

- **Navigate:** arrows (↑/↓ move by a grid row in Grid, by one in Loupe); **Shift+arrow** extends selection.
- **Rate:** `1`–`5` set rating, `0` clears. **Flag:** `P` pick · `X` exclude · `U` unflag.
- **View:** `Enter` → Loupe · `Esc` → back to Grid (or cancels crop / eyedropper / active control first).
- **Develop:** press a control's letter to focus it (`E`xposure, `B`rightness, `W`B mode, `T`emp, `I` tint, `K`elvin, `G`amma, `S`hadow, `C`ontrast, s`A`turation, `V`ibrance, vignette `O`, `H`ighlight, `N`oise, `M`edian, `D`emosaic), then `+`/`-` to adjust (Shift = big steps). `R` = crop.
- **Zoom (Loupe):** `+`/`-`, `Z`/`Space` toggle 1:1 ↔ fit.
- **Edits:** `Ctrl+C`/`Ctrl+V` copy/paste settings, `Ctrl+Z`/`Ctrl+Y` undo/redo (per photo), `Ctrl+A` select all, `Ctrl+E` export.

Design should keep this fast, muscle-memory workflow intact — visible affordances shouldn't get in the way of keyboard use.

## 7. Current design tokens (baseline to match or evolve)

- **Theming:** shadcn CSS variables in **OKLCH** (in `client/src/index.css`), **dark by default** (`.dark`), light also defined. `--radius: 0.625rem`.
- **Palette:** neutral / grayscale base; a single blue/violet accent (`--sidebar-primary` in dark). Semantic touches: rating stars **amber**, pick **emerald/green**, exclude **red**, "changed"/primary badges use the accent.
- **Type:** **Geist Variable** (UI); numeric readouts use **tabular-nums**.
- **Density:** compact — 12–14px text, tight spacing, thin borders/hairlines; panels are `bg-card` with `border`.

> (An alternate "Studio" direction — cool charcoal + teal `#2DD4BF` — was explored and set aside; ignore unless asked. Baseline is the neutral-gray tokens above.)

## 8. What I want to work on  ✏️ EDIT THIS BEFORE UPLOADING

_Replace this section with your actual goal so the design work is targeted. Examples:_

- "Redesign the **Develop panel** — it's cramped; improve grouping, hierarchy, and the slider rows while keeping every control and the keyboard hotkeys."
- "Rethink the **whole-app layout** — is the three-pane arrangement right, or should culling and developing be distinct modes?"
- "Design the **Export** and **Settings** dialogs to feel consistent and modern."
- "Propose a cleaner **Grid cell** and **Loupe** chrome (rating/flag overlays, selection state)."

**My goal:** …

**Constraints to respect:** desktop window (~1280–1600px), dark-first, keyboard-first workflow, dense/precise feel, chrome stays quiet so photos dominate, reuse the shadcn components above where possible.
