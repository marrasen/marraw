// The bundled watermark faces, loaded from the daemon (GET /fonts/{id}) so
// the preview renders with the byte-identical files the exporter embeds —
// never a lookalike from the OS.
import type { WatermarkFontIDType } from '@/api/settings';
import { backend } from '@/lib/backend';

export const WATERMARK_FONTS: { id: WatermarkFontIDType; label: string; family: string }[] = [
  { id: 'sans', label: 'Inter', family: 'wm-sans' },
  { id: 'serif', label: 'Playfair Display', family: 'wm-serif' },
  { id: 'mono', label: 'JetBrains Mono', family: 'wm-mono' },
  { id: 'script', label: 'Great Vibes', family: 'wm-script' },
];

export function watermarkFontFamily(id: WatermarkFontIDType): string {
  return WATERMARK_FONTS.find((f) => f.id === id)?.family ?? 'wm-sans';
}

export function watermarkFontUrl(id: WatermarkFontIDType): string {
  const params = new URLSearchParams();
  if (backend.token) params.set('t', backend.token);
  const qs = params.toString();
  return `${backend.http}/fonts/${id}${qs ? `?${qs}` : ''}`;
}

let loaded: Promise<void> | null = null;

// ensureWatermarkFonts registers the four FontFaces once and resolves when
// they are usable; callers re-render their canvas afterwards.
export function ensureWatermarkFonts(): Promise<void> {
  loaded ??= Promise.all(
    WATERMARK_FONTS.map(async ({ id, family }) => {
      const face = new FontFace(family, `url(${watermarkFontUrl(id)})`);
      await face.load();
      document.fonts.add(face);
    }),
  ).then(
    () => undefined,
    (err) => {
      // A failed load falls back to system faces — the preview is degraded
      // but usable; the export is unaffected. Allow a later retry.
      console.error('watermark fonts failed to load:', err);
      loaded = null;
    },
  );
  return loaded;
}
