// Watermark model helpers and the preview renderer — the TypeScript twin of
// internal/watermark (Go). The placement math must stay in lockstep: sizes
// and margins are percentages of the output's short edge, text is placed by
// its ink bounding box (measureText actualBoundingBox* here, font.BoundString
// there), and both sides round with Math.round semantics.
import type { Watermark, WatermarkElement, WatermarkFrame } from '@/api/settings';
import { watermarkFontFamily } from '@/lib/watermarkFonts';

// Mirror of the Go-side bounds in internal/api/watermarks.go.
export const WATERMARK_LIMITS = {
  sizeMin: 0.5,
  sizeMax: 50,
  sizeDefault: 4,
  marginMax: 25,
  marginDefault: 3,
  textMax: 200,
  rectDimMin: 1,
  rectDimMax: 100,
  rectWidthDefault: 100,
  rectHeightDefault: 14,
  frameWidthMin: 0.5,
  frameWidthMax: 15,
  frameWidthDefault: 3,
  frameBottomMax: 30,
} as const;

const HEX_COLOR = /^#[0-9a-f]{6}$/;
const ASSET_NAME = /^[0-9a-f]{16}\.(png|jpg)$/;

// Mirrors the server's normalizeWatermarkElement so an optimistic local
// write and the subscription echo agree.
const sanitizeHexColor = (c: unknown): string => {
  const s = typeof c === 'string' ? c.trim().toLowerCase() : '';
  return HEX_COLOR.test(s) ? s : '#ffffff';
};

export function sanitizeWatermarkElement(e: Partial<WatermarkElement>): WatermarkElement {
  const type = e.type === 'image' || e.type === 'rect' ? e.type : 'text';
  const asset = typeof e.asset === 'string' && ASSET_NAME.test(e.asset) ? e.asset : '';
  const sizePct =
    typeof e.sizePct === 'number' && e.sizePct > 0
      ? Math.min(WATERMARK_LIMITS.sizeMax, Math.max(WATERMARK_LIMITS.sizeMin, e.sizePct))
      : WATERMARK_LIMITS.sizeDefault;
  const marginPct =
    typeof e.marginPct === 'number'
      ? Math.min(WATERMARK_LIMITS.marginMax, Math.max(0, e.marginPct))
      : WATERMARK_LIMITS.marginDefault;
  const clampDim = (v: unknown, dflt: number) =>
    typeof v === 'number' && v > 0
      ? Math.min(WATERMARK_LIMITS.rectDimMax, Math.max(WATERMARK_LIMITS.rectDimMin, v))
      : dflt;
  return {
    id: e.id ?? crypto.randomUUID(),
    type,
    text: typeof e.text === 'string' ? e.text.slice(0, WATERMARK_LIMITS.textMax) : '',
    font:
      e.font === 'serif' || e.font === 'mono' || e.font === 'script' ? e.font : 'sans',
    color: sanitizeHexColor(e.color),
    asset,
    assetWidth: asset && typeof e.assetWidth === 'number' ? e.assetWidth : 0,
    assetHeight: asset && typeof e.assetHeight === 'number' ? e.assetHeight : 0,
    fill: e.fill === 'gradient' ? 'gradient' : 'solid',
    color2: sanitizeHexColor(e.color2),
    // Unlike opacity, 0 is meaningful here: the fade-to-transparent end.
    opacity2:
      typeof e.opacity2 === 'number' ? Math.min(1, Math.max(0, e.opacity2)) : 0,
    gradientDir:
      e.gradientDir === 'up' || e.gradientDir === 'right' || e.gradientDir === 'left'
        ? e.gradientDir
        : 'down',
    widthPct: clampDim(e.widthPct, WATERMARK_LIMITS.rectWidthDefault),
    heightPct: clampDim(e.heightPct, WATERMARK_LIMITS.rectHeightDefault),
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

// Mirrors the server's normalizeWatermarkFrame; older blobs without a frame
// get the disabled default.
export function sanitizeWatermarkFrame(f: Partial<WatermarkFrame> | undefined | null): WatermarkFrame {
  const widthPct =
    typeof f?.widthPct === 'number' && f.widthPct > 0
      ? Math.min(WATERMARK_LIMITS.frameWidthMax, Math.max(WATERMARK_LIMITS.frameWidthMin, f.widthPct))
      : WATERMARK_LIMITS.frameWidthDefault;
  const bottomPct =
    typeof f?.bottomPct === 'number'
      ? Math.min(WATERMARK_LIMITS.frameBottomMax, Math.max(0, f.bottomPct))
      : 0;
  return { enabled: f?.enabled === true, widthPct, bottomPct, color: sanitizeHexColor(f?.color) };
}

export function sanitizeWatermarks(list: Watermark[] | undefined | null): Watermark[] {
  return (list ?? [])
    .filter((w) => w.id && w.name)
    .map((w) => ({
      id: w.id,
      name: w.name,
      elements: (w.elements ?? [])
        .filter((e) => e.id && (e.type === 'text' || e.type === 'image' || e.type === 'rect'))
        .map(sanitizeWatermarkElement),
      frame: sanitizeWatermarkFrame(w.frame),
    }));
}

export function newTextElement(): WatermarkElement {
  return sanitizeWatermarkElement({ type: 'text', text: '' });
}

// newRectElement defaults to the caption-band use case: a full-width black
// scrim fading upward to transparent, hugging the bottom edge.
export function newRectElement(): WatermarkElement {
  return sanitizeWatermarkElement({
    type: 'rect',
    fill: 'gradient',
    color: '#000000',
    opacity: 0.55,
    color2: '#000000',
    opacity2: 0,
    gradientDir: 'up',
    anchor: 'bottom',
    widthPct: 100,
    heightPct: 14,
    marginPct: 0,
  });
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

// marginPx is sizePx without the 1px floor: margin 0 must mean flush to the
// edge (a full-bleed rect band would otherwise float 1px off it).
export const marginPx = (pct: number, short: number) =>
  Math.max(0, Math.round((pct / 100) * short));

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

// frameInsets returns the photo inset for a framed canvas: the preview box
// IS the framed canvas, so no geometry solve is needed — border and chin are
// fractions of its short edge, same definition as Frame.Layout in Go.
export function frameInsets(
  frame: WatermarkFrame,
  W: number,
  H: number,
): { border: number; chin: number } {
  const short = shortEdge(W, H);
  return {
    border: Math.round((frame.widthPct / 100) * short),
    chin: Math.round((frame.bottomPct / 100) * short),
  };
}

// rgba turns a #rrggbb color and a 0..1 alpha into a canvas color string.
const rgba = (hex: string, alpha: number): string => {
  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
};

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
    if (el.type === 'rect') {
      const w = Math.max(1, Math.round((el.widthPct / 100) * W));
      const h = sizePx(el.heightPct, short);
      const o = anchorOrigin(W, H, w, h, el.anchor, marginPx(el.marginPct, short));
      if (el.fill === 'gradient') {
        // The two stops carry their own alphas (straight, like the Go strips).
        ctx.globalAlpha = 1;
        const ends: Record<string, [number, number, number, number]> = {
          down: [o.x, o.y, o.x, o.y + h],
          up: [o.x, o.y + h, o.x, o.y],
          right: [o.x, o.y, o.x + w, o.y],
          left: [o.x + w, o.y, o.x, o.y],
        };
        const [x0, y0, x1, y1] = ends[el.gradientDir] ?? ends.down;
        const grad = ctx.createLinearGradient(x0, y0, x1, y1);
        grad.addColorStop(0, rgba(el.color, el.opacity));
        grad.addColorStop(1, rgba(el.color2, el.opacity2));
        ctx.fillStyle = grad;
      } else {
        ctx.fillStyle = el.color;
      }
      ctx.fillRect(o.x, o.y, w, h);
    } else if (el.type === 'text') {
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
          const o = anchorOrigin(W, H, w, h, el.anchor, marginPx(el.marginPct, short));
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
        const o = anchorOrigin(W, H, w, h, el.anchor, marginPx(el.marginPct, short));
        if (img?.complete) ctx.drawImage(img, o.x, o.y, w, h);
      }
    }
    ctx.restore();
  }
}
