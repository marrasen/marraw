# Changelog

This file feeds the "What's new" card on the Welcome page after an update.
Only two constructs are parsed (client/src/lib/changelog.ts): `## X.Y.Z - YYYY-MM-DD`
version headers and `-` bullets; everything else is ignored. Newest release first.
When cutting a release: bump the version in package.json and add a section here.

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
