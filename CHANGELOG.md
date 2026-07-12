# Changelog

This file feeds the "What's new" card on the Welcome page after an update.
Only two constructs are parsed (client/src/lib/changelog.ts): `## X.Y.Z - YYYY-MM-DD`
version headers and `-` bullets; everything else is ignored. Newest release first.
When cutting a release: bump the version in package.json and add a section here.

## 0.1.0 - 2026-07-12

- Watermark editor: text and image overlays composited onto exports
- 1:1 renders: cancel mid-decode, live progress, instant fit renders
- Quick edits settle to full resolution immediately; superseded renders are cancelled
- Crop mode: steps out of Fit so edge handles clear the window border
- Loupe: return-to-fit glides the pan back to center in sync with the zoom
- Export metadata options: All / Copyright only / None, credit line, GPS strip
- RAW + XMP export: copy RAWs with Adobe-compatible sidecars
