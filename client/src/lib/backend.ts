// Backend location and image URL helpers. The Electron shell passes the
// daemon's port and auth token via query params; browser dev falls back to
// the fixed dev port.

export type Level = '256' | '512' | '1024' | '2048' | 'full';

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

// imgUrl builds a content-addressed image URL: cacheKey (v) and editHash (e)
// are part of the URL, so the browser cache never serves stale pixels.
export function imgUrl(p: ImgRef, level: Level, editHashOverride?: string): string {
  const e = editHashOverride ?? p.editHash;
  const params = new URLSearchParams({ v: p.cacheKey });
  if (e && e !== 'base') params.set('e', e);
  if (backend.token) params.set('t', backend.token);
  return `${backend.http}/img/${p.id}/${level}?${params}`;
}

// levelForSize picks the smallest pyramid level that covers cssPx on this
// display, capped (the loupe only goes to "full" at 1:1 zoom).
export function levelForSize(cssPx: number, cap: Level = '2048'): Level {
  const target = cssPx * window.devicePixelRatio;
  for (const l of ['256', '512', '1024', '2048'] as const) {
    if (Number(l) >= target) return l;
    if (l === cap) return cap;
  }
  return cap;
}
