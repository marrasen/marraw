// Backend location and image URL helpers. The Electron shell passes the
// daemon's location and auth token via query params: apiPort for the local
// daemon, or apiHost (host:port) + remote=1 for a connection to another
// machine's daemon. Browser dev falls back to the fixed dev port.

import { getImgBust } from './imgCacheBust';

export type Level = '256' | '512' | '1024' | '2048';

// TILE_SIZE must match pyramid.TileSize in the Go backend: full processed
// resolution is served as a grid of square tiles, not one giant JPEG.
export const TILE_SIZE = 1024;

const q = new URLSearchParams(window.location.search);

// A share link is served by the daemon itself at /s/<token>/, so that page's
// own origin IS the backend and its token is in the path. Every other case —
// the Electron shell, browser dev — is told where to go by query params.
const share = /^\/s\/([0-9a-f]{32})\//.exec(window.location.pathname);

const host = share ? window.location.host : (q.get('apiHost') ?? `127.0.0.1:${q.get('apiPort') ?? '8483'}`);
const token = share ? share[1] : (q.get('token') ?? '');

// Tailscale Funnel terminates TLS, so a share page arrives over https — and a
// page on https may not open ws:// or load http:// images. Follow the page's
// own scheme there. Everywhere else the daemon is plain http (the Electron
// renderer runs on file://, where location.protocol says nothing useful).
const secure = !!share && window.location.protocol === 'https:';

export const backend = {
  token,
  // isRemote: this window talks to a daemon on another machine, so anything
  // that touches THIS machine's filesystem (native pickers, reveal) is wrong.
  isRemote: q.get('remote') === '1',
  remoteName: q.get('remoteName') ?? '',
  // isGuest: this is the shared album page, not the app.
  isGuest: !!share,
  http: `${secure ? 'https' : 'http'}://${host}`,
  // Trust rides in the first-message auth frame (see main.tsx), not the URL.
  ws: `${secure ? 'wss' : 'ws'}://${host}/ws`,
};

// canUseHostFs: whether Electron bridges that touch THIS machine's filesystem
// (reveal in Explorer, dropped-folder paths) are meaningful. They aren't in a
// plain browser tab, nor in a remote window — there the library's paths live
// on the daemon's machine, which local dialogs and Explorer can't see.
export const canUseHostFs = (): boolean => !!window.marraw && !backend.isRemote;

export interface ImgRef {
  id: number;
  cacheKey: string;
  editHash: string;
}

// RENDER_VERSION must match pyramid.renderVersion in the Go backend: image
// responses are cached as immutable, so a rendering-pipeline change must
// change the URL or clients keep serving the old pixels forever.
const RENDER_VERSION = 'r13';

// imgUrl builds a content-addressed image URL: cacheKey (v), editHash (e),
// and render version (r) are part of the URL, so the browser cache never
// serves stale pixels. cacheOnly asks the server for the pre-rendered file or
// a 404 — never an on-demand render — so the fit loupe can show what's warm
// without triggering (and blocking on) a full RAW decode while browsing.
export function imgUrl(
  p: ImgRef,
  level: Level,
  opts?: { editHash?: string; cacheOnly?: boolean; stale?: boolean; fast?: boolean },
): string {
  const e = opts?.editHash ?? p.editHash;
  const params = new URLSearchParams({ v: p.cacheKey, r: RENDER_VERSION });
  if (e && e !== 'base') params.set('e', e);
  if (opts?.cacheOnly) params.set('cacheOnly', '1');
  // stale: when the exact rendition is missing, the server answers with the
  // photo's freshest rendition of this level at ANY edit state (no-store)
  // instead of blocking on a decode — right photo now, right pixels soon.
  if (opts?.stale) params.set('stale', '1');
  // fast: never a RAW decode — the cached file, a downscale of an existing
  // 2048, or the camera's embedded JPEG (a base-look provisional, no-store),
  // else 404. What lets a cold cull frame paint in tens of milliseconds.
  if (opts?.fast) params.set('fast', '1');
  // b: per-photo cache-buster (server-ignored). Advances when a restored AI-mask
  // map regenerates pixels under an unchanged edit hash — see imgCacheBust.
  const b = getImgBust(p.id);
  if (b) params.set('b', String(b));
  if (backend.token) params.set('t', backend.token);
  return `${backend.http}/img/${p.id}/${level}?${params}`;
}

// tileUrl builds the content-addressed URL of one full-resolution tile,
// versioned exactly like imgUrl.
export function tileUrl(p: ImgRef, tx: number, ty: number): string {
  const params = new URLSearchParams({ v: p.cacheKey, r: RENDER_VERSION });
  if (p.editHash && p.editHash !== 'base') params.set('e', p.editHash);
  const b = getImgBust(p.id);
  if (b) params.set('b', String(b));
  if (backend.token) params.set('t', backend.token);
  return `${backend.http}/img/${p.id}/tile/${tx}/${ty}?${params}`;
}

// watermarkAssetUrl serves a stored watermark image (content-hash name from
// Settings.AddWatermarkAsset) for the editor preview.
export function watermarkAssetUrl(fileName: string): string {
  const params = new URLSearchParams();
  if (backend.token) params.set('t', backend.token);
  const qs = params.toString();
  return `${backend.http}/wm/${fileName}${qs ? `?${qs}` : ''}`;
}

// downloadUrl is one photo as a finished full-resolution JPEG — a real render
// through the develop pipeline, not a pyramid rendition, so it is slow and
// uncacheable by design (the pixels change as the owner keeps editing).
export function downloadUrl(id: number): string {
  const params = new URLSearchParams();
  if (backend.token) params.set('t', backend.token);
  const qs = params.toString();
  return `${backend.http}/dl/${id}${qs ? `?${qs}` : ''}`;
}

// zipUrl is a selection as one archive, rendered and streamed photo by photo.
export function zipUrl(ids: number[]): string {
  const params = new URLSearchParams({ ids: ids.join(',') });
  if (backend.token) params.set('t', backend.token);
  return `${backend.http}/dl.zip?${params}`;
}

// levelForSize picks the smallest pyramid level that covers cssPx on this
// display, capped (past 2048 the loupe switches to full-resolution tiles).
export function levelForSize(cssPx: number, cap: Level = '2048'): Level {
  const target = cssPx * window.devicePixelRatio;
  for (const l of ['256', '512', '1024', '2048'] as const) {
    if (Number(l) >= target) return l;
    if (l === cap) return cap;
  }
  return cap;
}
