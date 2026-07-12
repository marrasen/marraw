// Backend location and image URL helpers. The Electron shell passes the
// daemon's port and auth token via query params; browser dev falls back to
// the fixed dev port.

export type Level = '256' | '512' | '1024' | '2048';

// TILE_SIZE must match pyramid.TileSize in the Go backend: full processed
// resolution is served as a grid of square tiles, not one giant JPEG.
export const TILE_SIZE = 1024;

const q = new URLSearchParams(window.location.search);
const port = q.get('apiPort') ?? '8483';
const token = q.get('token') ?? '';

export const backend = {
  port,
  token,
  http: `http://127.0.0.1:${port}`,
  ws: `ws://127.0.0.1:${port}/ws${token ? `?t=${encodeURIComponent(token)}` : ''}`,
};

export interface ImgRef {
  id: number;
  cacheKey: string;
  editHash: string;
}

// RENDER_VERSION must match pyramid.renderVersion in the Go backend: image
// responses are cached as immutable, so a rendering-pipeline change must
// change the URL or clients keep serving the old pixels forever.
const RENDER_VERSION = 'r8';

// imgUrl builds a content-addressed image URL: cacheKey (v), editHash (e),
// and render version (r) are part of the URL, so the browser cache never
// serves stale pixels. cacheOnly asks the server for the pre-rendered file or
// a 404 — never an on-demand render — so the fit loupe can show what's warm
// without triggering (and blocking on) a full RAW decode while browsing.
export function imgUrl(
  p: ImgRef,
  level: Level,
  opts?: { editHash?: string; cacheOnly?: boolean },
): string {
  const e = opts?.editHash ?? p.editHash;
  const params = new URLSearchParams({ v: p.cacheKey, r: RENDER_VERSION });
  if (e && e !== 'base') params.set('e', e);
  if (opts?.cacheOnly) params.set('cacheOnly', '1');
  if (backend.token) params.set('t', backend.token);
  return `${backend.http}/img/${p.id}/${level}?${params}`;
}

// tileUrl builds the content-addressed URL of one full-resolution tile,
// versioned exactly like imgUrl.
export function tileUrl(p: ImgRef, tx: number, ty: number): string {
  const params = new URLSearchParams({ v: p.cacheKey, r: RENDER_VERSION });
  if (p.editHash && p.editHash !== 'base') params.set('e', p.editHash);
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
