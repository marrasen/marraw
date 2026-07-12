// Watermark model helpers and the preview renderer — the TypeScript twin of
// internal/watermark (Go). The placement math must stay in lockstep: sizes
// and margins are percentages of the output's short edge, text is placed by
// its ink bounding box (measureText actualBoundingBox* here, font.BoundString
// there), and both sides round with Math.round semantics.
import type { Watermark, WatermarkElement } from '@/api/settings';
import { watermarkFontFamily } from '@/lib/watermarkFonts';

// Mirror of the Go-side bounds in internal/api/watermarks.go.
export const WATERMARK_LIMITS = {
  sizeMin: 0.5,
  sizeMax: 50,
  sizeDefault: 4,
  marginMax: 25,
  marginDefault: 3,
  textMax: 200,
} as const;

const HEX_COLOR = /^#[0-9a-f]{6}$/;
const ASSET_NAME = /^[0-9a-f]{16}\.(png|jpg)$/;

// Mirrors the server's normalizeWatermarkElement so an optimistic local
// write and the subscription echo agree.
export function sanitizeWatermarkElement(e: Partial<WatermarkElement>): WatermarkElement {
  const type = e.type === 'image' ? 'image' : 'text';
  const color = typeof e.color === 'string' ? e.color.trim().toLowerCase() : '';
  const asset = typeof e.asset === 'string' && ASSET_NAME.test(e.asset) ? e.asset : '';
  const sizePct =
    typeof e.sizePct === 'number' && e.sizePct > 0
      ? Math.min(WATERMARK_LIMITS.sizeMax, Math.max(WATERMARK_LIMITS.sizeMin, e.sizePct))
      : WATERMARK_LIMITS.sizeDefault;
  const marginPct =
    typeof e.marginPct === 'number'
      ? Math.min(WATERMARK_LIMITS.marginMax, Math.max(0, e.marginPct))
      : WATERMARK_LIMITS.marginDefault;
  return {
    id: e.id ?? crypto.randomUUID(),
    type,
    text: typeof e.text === 'string' ? e.text.slice(0, WATERMARK_LIMITS.textMax) : '',
    font:
      e.font === 'serif' || e.font === 'mono' || e.font === 'script' ? e.font : 'sans',
    color: HEX_COLOR.test(color) ? color : '#ffffff',
    asset,
    assetWidth: asset && typeof e.assetWidth === 'number' ? e.assetWidth : 0,
    assetHeight: asset && typeof e.assetHeight === 'number' ? e.assetHeight : 0,
    anchor:
      e.anchor === 'topLeft' ||
      e.anchor === 'top' ||
      e.anchor === 'topRight' ||
      e.anchor === 'left' ||
      e.anchor === 'center' ||
      e.anchor === 'right' ||
      e.anchor === 'bottomLeft' ||
      e.anchor === 'bottom'
        ? e.anchor
        : 'bottomRight',
    sizePct,
    marginPct,
    opacity: typeof e.opacity === 'number' && e.opacity > 0 && e.opacity <= 1 ? e.opacity : 1,
  };
}

export function sanitizeWatermarks(list: Watermark[] | undefined | null): Watermark[] {
  return (list ?? [])
    .filter((w) => w.id && w.name)
    .map((w) => ({
      id: w.id,
      name: w.name,
      elements: (w.elements ?? [])
        .filter((e) => e.id && (e.type === 'text' || e.type === 'image'))
        .map(sanitizeWatermarkElement),
    }));
}

export function newTextElement(): WatermarkElement {
  return sanitizeWatermarkElement({ type: 'text', text: '' });
}

export function newImageElement(asset: string, width: number, height: number): WatermarkElement {
  return sanitizeWatermarkElement({
    type: 'image',
    asset,
    assetWidth: width,
    assetHeight: height,
    sizePct: 8,
  });
}

// ---- Placement math (Go twin: internal/watermark/layout.go) ----

// Text below this em size is unreadable; both renderers clamp identically.
const MIN_TEXT_PX = 4;

export const shortEdge = (w: number, h: number) => Math.min(w, h);

// sizePx converts a percent-of-short-edge to pixels (minimum 1).
export const sizePx = (pct: number, short: number) =>
  Math.max(1, Math.round((pct / 100) * short));

export const textPx = (pct: number, short: number) =>
  Math.max(MIN_TEXT_PX, Math.round((pct / 100) * short));

// anchorOrigin returns the top-left corner for a w×h box inside a W×H
// canvas, inset by marginPx on each anchored edge; centered axes ignore the
// margin. Math.trunc matches Go's integer division.
export function anchorOrigin(
  W: number,
  H: number,
  w: number,
  h: number,
  anchor: WatermarkElement['anchor'],
  marginPx: number,
): { x: number; y: number } {
  let x: number;
  if (anchor === 'topLeft' || anchor === 'left' || anchor === 'bottomLeft') x = marginPx;
  else if (anchor === 'topRight' || anchor === 'right' || anchor === 'bottomRight')
    x = W - marginPx - w;
  else x = Math.trunc((W - w) / 2);
  let y: number;
  if (anchor === 'topLeft' || anchor === 'top' || anchor === 'topRight') y = marginPx;
  else if (anchor === 'bottomLeft' || anchor === 'bottom' || anchor === 'bottomRight')
    y = H - marginPx - h;
  else y = Math.trunc((H - h) / 2);
  return { x, y };
}

// ---- Preview renderer ----

// renderWatermark draws every element onto a 2d context sized W×H (backing
// pixels, not CSS px — the caller scales for devicePixelRatio *before* this,
// via ctx.scale, so the math stays in output-image pixel space). Image
// bitmaps come from the caller (keyed by asset name); elements whose bitmap
// has not loaded yet reserve their box via assetWidth/Height but draw
// nothing.
export function renderWatermark(
  ctx: CanvasRenderingContext2D,
  wm: Watermark,
  W: number,
  H: number,
  assets: Map<string, HTMLImageElement>,
) {
  const short = shortEdge(W, H);
  if (short <= 0) return;
  for (const el of wm.elements) {
    ctx.save();
    ctx.globalAlpha = el.opacity;
    if (el.type === 'text') {
      const text = el.text;
      if (text.trim()) {
        ctx.font = `${textPx(el.sizePct, short)}px ${watermarkFontFamily(el.font)}`;
        // Ligatures off and pre spacing: the Go rasterizer shapes glyph by
        // glyph, so the preview must not let the browser get fancier.
        ctx.fontKerning = 'none';
        const m = ctx.measureText(text);
        const w = Math.ceil(m.actualBoundingBoxLeft + m.actualBoundingBoxRight);
        const h = Math.ceil(m.actualBoundingBoxAscent + m.actualBoundingBoxDescent);
        if (w > 0 && h > 0) {
          const o = anchorOrigin(W, H, w, h, el.anchor, sizePx(el.marginPct, short));
          ctx.fillStyle = el.color;
          ctx.fillText(text, o.x + m.actualBoundingBoxLeft, o.y + m.actualBoundingBoxAscent);
        }
      }
    } else if (el.asset) {
      const img = assets.get(el.asset);
      const aw = img?.naturalWidth || el.assetWidth;
      const ah = img?.naturalHeight || el.assetHeight;
      if (aw > 0 && ah > 0) {
        const h = sizePx(el.sizePct, short);
        const w = Math.max(1, Math.round((h * aw) / ah));
        const o = anchorOrigin(W, H, w, h, el.anchor, sizePx(el.marginPct, short));
        if (img?.complete) ctx.drawImage(img, o.x, o.y, w, h);
      }
    }
    ctx.restore();
  }
}
