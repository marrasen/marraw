# Changelog

This file feeds the "What's new" card on the Welcome page after an update.
Only two constructs are parsed (client/src/lib/changelog.ts): `## X.Y.Z - YYYY-MM-DD`
version headers and `-` bullets; everything else is ignored. Newest release first.
When cutting a release: bump the version in package.json and add a section here.

## 0.5.0 - 2026-07-17

- Develop: exposure range widened to −5..+5 EV (was −2..+3), in line with other RAW editors — the extra stops render correctly everywhere, from the live drag preview to the final export
- Export: copy a single image to the clipboard with Ctrl+⇧+C
- Library: Blinks filter — narrow the grid to closed-eye frames so blinks can be reviewed and rejected in one sweep
- Cull: closed-eye detection now runs only when you start a scan — no more silent scoring in the background when a folder opens
- Settings: the burst grouping time window is adjustable (1–30 s); open folders re-cluster live
- Settings: "Beta versions" toggle — opt in to beta updates ahead of stable releases
- Retouch: entering heal mode keeps you on the Local tab
- Library: toolbar buttons collapse to icons on narrow windows

## 0.4.0 - 2026-07-16

- Retouch: spot removal — click or drag a circle over dust and blemishes; heal (tone-matched) and clone modes, with the source patch picked automatically and draggable (Q)
- Retouch: heal brush — paint over any shape to remove it; the fill comes from a matching movable source region
- Retouch: visualize spots (A) — a high-contrast dust view with a sensitivity slider, for hunting sensor spots
- Retouch: with a spot selected, 1–9 and 0 set its opacity
- AI masks: Subject, Depth and Scene selections in the Local tab — models download only after consent, and mask edges refine automatically at high zoom
- AI masks: the depth window is a two-thumb range slider, and hovering a mask row tints its region on the photo
- Develop: the Local tab gathers masks and retouch; the panel drawer stays visible while hovered
- Auto: subject-aware auto-tone, and an auto-crop that frames the detected subject
- Presets: Ctrl+⇧+1–9 apply your saved presets by position
- Cull: closed-eye detection — scan a folder and filter or badge photos where eyes are closed
- Cull: sharpness and subject-focus scores, a "Soft" filter for reject sweeps, and a folder-wide analyze control
- Cull: burst grouping — near-duplicate series collapse to their sharpest frame, with badges, auto-judge, and ⇧P/⇧X best-of-burst keys
- Cull: flags and ratings have undo history — one Ctrl+Z per culling stroke
- Cull: browsing never stalls on a RAW decode, and background rendering works outward from the photo you're on
- Settings: Models section shows downloaded AI weights and lets you delete them
- Grid: thumbnails recover from transient load errors and refresh immediately after AI analysis

## 0.3.0 - 2026-07-13

- Local adjustments: linear, radial and brush masks, in their own Masks tab
- Masks: every slider can be walked with the arrow keys for fine control
- Linux: first Linux release — AppImage (auto-updating) and .deb installers
- macOS: first macOS release — Apple Silicon .dmg; the app is unsigned, so first launch needs right-click → Open (or "Open Anyway" under Privacy & Security on macOS 15+), and auto-update is not available
- Note: the macOS and Linux builds are brand new and untested on real hardware — issue reports are very welcome

## 0.2.0 - 2026-07-12

- HSL color mixer: 8-band hue / saturation / luminance adjustments
- Presets: save any look as a named preset, apply it from the Presets tab
- Geometry: 90-degree rotation and horizontal/vertical mirroring
- Watermark editor: text and image overlays composited onto exports
- Export: RAW + XMP mode copies RAWs with Adobe-compatible sidecars
- Export: metadata options — All / Copyright only / None, credit line, GPS strip
- Export: PNG output format, and exports carry EXIF (camera, exposure, capture time)
- Export: file-name templates with {name}, {seq}, {date} and {time}
- Export: big batches are paced by available memory so the app stops swapping
- Library: remembers filters, sort order, and gap grouping per folder
- Library: sort by capture time or file name, in either direction
- Library: folder rail sorts and time-groups imported folders
- Add folder: tabbed dialog explains each mode and can add the folder you're in
- Thumbnails: framing setting — crop, fit, or natural aspect
- Loupe: return-to-fit glides the pan back to center in sync with the zoom
- 1:1 renders: cancel mid-decode, live progress, instant fit renders
- Quick edits: settle to full resolution immediately; superseded renders are cancelled
- Crop mode: steps out of Fit so edge handles clear the window border
- Welcome: "What's new" card shows the changelog after an update

## 0.1.0 - 2026-07-09

- Initial release: RAW library, develop tools, loupe, and export
