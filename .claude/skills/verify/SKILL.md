---
name: verify
description: Verify a marraw change end-to-end — start the dev servers, drive the backend over the real aprot WebSocket RPC, and screenshot app surfaces via the shot harness.
---

# Verifying marraw changes

## Handles

- Backend only: `npm run dev:server` (marrawd on :8483). Full stack for
  screenshots: `npm run dev` (server + Vite), run in the background and wait
  for :8483 to accept connections (it comes up in ~1s; Vite may bind a port
  other than 5173 — set `MARRAW_VITE_PORT` if needed, and don't trust a
  5173 port probe: `shot.mjs` finds it anyway).
- Kill any user-launched marraw/Electron first (single-instance lock and
  GPU-cache lock will silently break the harness): check
  `Get-Process electron, marrawd`.

## Backend surface (aprot WS RPC)

`scripts/*-verify.mjs` and `scripts/smoke.mjs` are per-feature end-to-end
scripts that speak the real wire (`ws://127.0.0.1:8483/ws`,
`{type:'request', id, method, params}`; task completion arrives as
`TaskStateEvent` pushes). Copy the pattern from `scripts/rawxmp-verify.mjs`
(small) or `scripts/smoke.mjs` (full tour: open folder, edit, export,
subscriptions).

Fixtures: copy ARWs from `D:\Photos\2026-04-18 Velox Valor Trollhättan`
into a disposable folder (e.g. under `D:\Photos\`) — never point a script at
a real shoot; edits fire `.marraw.json` sidecar writes and some flows write
`.xmp`/exports next to the originals.

## GUI surface (screenshots)

`node scripts/shot.mjs <raw-folder> <surface> [out.png]` launches Electron
with `MARRAW_UITEST=scripts/shot.renderer.js`, drives the named surface, and
captures a PNG. Surfaces live in `shot.renderer.js` — adding one is a few
lines using the `window.__marraw` bridge (`mw.useUIStore.getState()` setters)
and `document.querySelectorAll('button')` text-match clicks. Needs
`npm run dev` running.

## Gotchas

- `npm run gen` regenerates `client/src/api/*` from the Go registry; run it
  after any Go wire-type change or the client rejects new enum values.
- Untouched photos carry a seeded camera-mimic `expEV` (base_exp_ev), so
  "no edit" photos still have non-neutral edit params.
- Don't `gofmt -w` across `internal/api` — pre-existing line-ending churn.
