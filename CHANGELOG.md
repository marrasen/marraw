# Changelog

This file feeds the "What's new" dialog the app raises after an update.
Only two constructs are parsed (client/src/lib/changelog.ts): `## X.Y.Z - YYYY-MM-DD`
version headers and `-` bullets; everything else is ignored. The version may carry a
prerelease suffix (`## X.Y.Z-beta.N - YYYY-MM-DD`), which sorts below its own stable
release. Newest release first. When cutting a release: bump the version in
package.json and add a section here — and when a beta cycle closes, fold its beta
sections into the stable one so nobody reads the same news twice.

## 0.10.0-beta.15 - 2026-08-09

- Windows: marraw **opens where you left it**. The library window comes back at the size and position it had when you quit, maximized if it was. If it was last on a screen that isn't there any more — a laptop undocked from the monitor on your desk — it comes back on a screen you do have, rather than somewhere off the edge you could never drag it from
- Windows: the **pop-out photo window comes back too** when it was open at quit, showing whatever the library window has focused. It already remembered its own size and position; now it remembers having been up at all
- Library: the **develop panel remembers being hidden**, the way the library rail already did. The two toggles sit at either end of the same toolbar and now behave the same — hide the panel for a wider grid and it stays hidden next time you open marraw

## 0.10.0-beta.14 - 2026-08-09

- Library: marraw **reopens the folder you had open** when you quit, so a launch lands you back in the shoot you were working on instead of on the welcome page. If that folder has since been deleted, renamed, or its drive unplugged, it says so and leaves you on the library — and it stays remembered, so plugging the drive back in lands you there again next time
- Library: fixed — hiding the library rail and quitting left no way to bring it back. The rail's show/hide button lives in the folder toolbar, so a collapsed rail with no folder open was a dead end: no rail, no button, no way to open a library at all. The rail now comes back for good whenever nothing is open, and with the folder above reopening, a collapsed rail otherwise stays exactly as you left it
- Updates: **what's new arrives as a dialog** — the mark, the version you just moved to, and every release since the one this machine last saw — rather than a card on a welcome page that reopening a folder means you never land on. It waits until you dismiss it, so quitting mid-read brings it back next launch, and the release notes' emphasis now renders instead of showing its asterisks
- Updates: **beta builds carry their release notes in the app**. Betas get their own changelog sections, and a tester moving from the last beta onto the final stable release sees that release's notes as well — until now a beta showed neither

## 0.9.0 - 2026-08-03

- Masks: spatial effects on every mask type — **blur**, **motion blur** (with a direction), **zoom blur** that radiates from the mask's own centre, **glow** that blooms the highlights, **light streaks** that draw long anamorphic strokes out of them, **prism** for the radial colour fringing of a vintage lens, and **mosaic**. They live in an Effects group under each mask's tone sliders, and unlike the tone controls they gather neighbouring pixels: the blur samples only *inside* the mask, so a masked-out subject never smears into the background around it, and everything is averaged in linear light so highlights stay bright instead of going grey
- Masks: a **Background** button next to Subject — one click detects the subject and masks everything else, pre-loaded with a bloom, light streaks and a prism fringe. The row reads "Background", and its Invert button flips it back to the subject. Tune it from there, or drop it into a preset and apply the whole look to a shoot
- Previews of a mask effect are computed at a fixed working size, so what you see on the fit-to-screen preview is exactly what you get at 1:1 and in the export
- Remote: **find computers instead of copying tokens** — the laptop scans for machines on the local network and across a Tailscale network, asks to connect, and someone at the host approves in a dialog. Each approved machine gets a credential of its own, so a single laptop can be revoked without locking out everyone; **Settings → Remote** lists the approved computers, shows the addresses another machine can actually reach you on, and can stop accepting new requests once your machines are set up. Typing an address by hand and the shared pairing token stay as the fallback for blocked multicast and non-default ports
- Remote: saved connections now live where you look for a library — an **Other computers** block at the foot of the library rail, each row re-probed for reachability and version. Clicking one opens that library in its own window, and a remote window gets the same block plus a **This computer** row as the way home. Adding and editing moved into Settings → Remote, and the startup picker is gone
- Remote: a machine that is running but that nothing can find now says so and points at the firewall, instead of showing a cheerful toggle while being invisible
- Windows: the installer adds the firewall rule that hosting a library needs, so being reachable no longer depends on catching the Security Alert the first time the daemon listens. Upgrades replace the rule and uninstalling removes it (this needs the "anyone who uses this computer" install; a per-user install still gets the Windows prompt as before)
- Updates: check, download and install from the UI — Settings gains an **Updates** pane with the current version, Check for updates, download progress and Restart & install, and a waiting update parks a row at the foot of the library rail until you deal with it. Until now an update was a background download announced by a single OS notification that expired on its own: miss it and there was no way to find out, and no way to ask
- Library: returning to the grid from cull keeps your scroll position, landing on the frame you were just looking at instead of at the top of the folder
- Develop: `Tab` reveals the drawer it just switched to, rather than cycling tabs behind a faded-out heads-up adjust, and stays out of the way during crop and white-balance picking
- Local: up/down arrows now walk a mask's Threshold and Edge feather (and a range mask's Min saturation) — the keyboard walk, the focus ring and `+`/`-` stepping all skipped them
- Local: hovering an AI mask row tints its region even while the Scene/People picker is still armed, and switching rows no longer shows the previous mask's tint
- Fixed: the white-balance eyedropper refused almost every spot on photos under narrow-band light (a blue-lit stage) with "picked area is too dark", and the rare pick that got through turned the photo near-black. It now samples the developed frame, pinned as it was when you opened the picker — so two spots can be compared without undoing between them, and re-picking the same spot always gives the same answer
- Fixed: copying the settings off a photo with an AI mask and pasting them onto another one did nothing until you clicked the Local tab. The same went for a preset landing AI masks across a selection, or a sidecar brought from another machine
- Fixed: auto-tone and edit suggestions metered a frame darker than the one on screen for photos pushed past ±3 EV, so a +4.28 EV photo came back +2.65
- Fixed: two renders of the same photo racing to write the same cache file could make one of them fail with "render failed"
- This release re-renders every cached preview once (the pipeline changed), so the first browse after updating is slower than usual

## 0.8.0 - 2026-07-30

- Lens corrections: marraw reads the camera and lens out of the RAW, matches a profile from a built-in database of ~1,500 calibrated lenses, and undoes what the lens did to the frame — barrel and pincushion distortion, lateral chromatic aberration, and corner vignetting. It runs automatically, before every other stage, so the frame you start editing is already the one your camera's own JPEG shows; each correction has its own amount in Develop → Detail → Lens correction (100 % is the profile's measurement), and the whole thing switches off in one click when a lens's own signature is the point. Photos you edited before this release re-render slightly straighter, with brighter corners — set Lens correction to Off to get the previous rendering back
- Curve: a point tone curve on its own tab, with RGB / R / G / B channels — drag a point to move it, click empty space to add one, double-click to remove it. The RGB master shapes overall tone and the per-channel curves grade colour on top of it; the curve is monotone by construction, so no combination of points can invert tones, and it renders identically in previews, 1:1 tiles and exports and travels in presets with the rest of the Tone section
- Masks: a range mask that selects by the photo's own tone and colour — a two-thumb luminance window, a hue centre and range on a rainbow track, a minimum-saturation gate and feather, plus an eyedropper to pick the colour straight off the image. Like AI masks, range masks travel in presets and carry their own local adjustments
- Help overlay: Develop lays out in two columns and its controls in three, and the overlay scrolls on short screens instead of clipping

## 0.7.0 - 2026-07-27

- Remote connections: open a library hosted on another machine — turn on remote access where the photos live, pair a laptop with a token, and it browses and edits the library over the network (e.g. Tailscale) with nothing copied; exports, cache and watermark images all resolve on the host
- Retouch: content-aware fill — a third spot mode that removes a region by ML-inpainting it, from a one-time on-device model download
- Retouch: the heal source is picked by matching texture around the spot, with its tone matched to the surroundings
- Retouch: heal chrome fades when idle and hides the target while you pick a source; hovering a spot's panel row tints its area on the loupe, in or out of healing mode
- Local: per-person AI masks — hover a person in the loupe to mask just them; the People and Scene mask pickers are now one unified picker
- Local: an eye toggle hides masks and retouch spots without deleting them
- Undo: Ctrl+Z outside Develop undoes your most recent action — ratings, flags and other library strokes
- Loupe: right-drag pans the photo in every mode
- Export: save and reuse named presets in the export dialog
- Watermarks: rectangle elements and a polaroid frame; rectangle height follows the short-edge rule so it scales the same at every export size
- Performance: background full-resolution decodes are staged through a per-device I/O gate, so browsing stays smooth while renders catch up

## 0.6.0 - 2026-07-19

- Presets: partial presets — choose which sections of the edit a preset carries when saving it
- Presets: relative mode — a preset applies as an offset on top of each photo's calibrated baseline, with exposure re-anchored per photo instead of stamped absolute
- Presets: adaptive save — save a look as its difference from auto, so it adapts to every photo it lands on
- Presets: hover a preset to preview it on the loupe, and scrub the amount after applying
- Presets: manage your presets — rename, duplicate, overwrite, reorder, and share them as files
- Presets: per-camera defaults are seeded when a folder calibrates
- Presets: AI-mask recipes — a preset's Subject/Depth/Scene masks re-detect on each photo they're applied to
- Presets: applying an AI-mask preset to a multi-photo selection now generates masks for every selected photo, not just the focused one
- Suggestions: scene-aware edit suggestions — 3–5 candidate looks per photo (experimental, off by default)
- Settings: Features tab — switch whole features on or off (burst grouping, soft filter, closed-eye detection, subject focus, edit suggestions) to declutter the UI
- Library: grid cells follow the rendered shape after crop or rotate
- Cull: folder calibration no longer slows down while default presets are being seeded
- UI: the marraw logo replaces the "m" mark in the top bar and the cull/develop overlay

## 0.5.1 - 2026-07-17

- Cull/Develop: the floating folder pill shows just the folder name — a long path collided with the centered mode switch; hover it for the full path
- Info: new Folder row showing the photo's full folder path
- Window: the default window is now 3:2 (1500×1000), matching the photos, so full-bleed frames fill it edge to edge

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
