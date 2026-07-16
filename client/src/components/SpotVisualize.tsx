import { useEffect, useRef } from 'react';
import { useEditSession } from '@/lib/editSession';

// SpotVisualizeLayer is the dust-hunting view for the heal tool (A key —
// Lightroom's "Visualize Spots"): the displayed image redrawn as a high-pass
// edge relief, white on black, where sensor dust and blemishes pop out as
// small closed rings that are invisible against busy color. Pure client-side:
// it re-filters whatever rendition the loupe is already showing (the /img
// endpoint is CORS-open; preview blobs are same-origin — the Magnifier
// precedent), so it needs no backend render and heals update it live. The
// analysis is capped at 1024 px long edge — dust is a low-frequency hunt, and
// the canvas stretches over the displayed box.
export function SpotVisualizeLayer({ src, boxW, boxH }: { src: string; boxW: number; boxH: number }) {
  const threshold = useEditSession((s) => s.spotVisualizeThreshold);
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    if (!src) return;
    let gone = false;
    const img = new Image();
    if (!src.startsWith('blob:')) img.crossOrigin = 'anonymous';
    img.src = src;
    img
      .decode()
      .then(() => {
        const canvas = canvasRef.current;
        if (gone || !canvas) return;
        const long = Math.max(img.naturalWidth, img.naturalHeight) || 1;
        const scale = Math.min(1, 1024 / long);
        const w = Math.max(1, Math.round(img.naturalWidth * scale));
        const h = Math.max(1, Math.round(img.naturalHeight * scale));
        const off = document.createElement('canvas');
        off.width = w;
        off.height = h;
        const octx = off.getContext('2d', { willReadFrequently: true });
        if (!octx) return;
        octx.drawImage(img, 0, 0, w, h);
        let data: ImageData;
        try {
          data = octx.getImageData(0, 0, w, h);
        } catch {
          return; // tainted canvas (unexpected cross-origin): skip quietly
        }
        const px = data.data;
        const gray = new Float32Array(w * h);
        for (let i = 0, j = 0; i < gray.length; i++, j += 4) {
          gray[i] = 0.299 * px[j] + 0.587 * px[j + 1] + 0.114 * px[j + 2];
        }
        // 4-neighbor Laplacian magnitude, tone-mapped by the sensitivity
        // slider: higher sensitivity lowers the cutoff and lifts the gain.
        const cut = 20 * (1 - threshold);
        const gain = 2 + 10 * threshold;
        const out = octx.createImageData(w, h);
        const op = out.data;
        for (let y = 1; y < h - 1; y++) {
          for (let x = 1; x < w - 1; x++) {
            const i = y * w + x;
            const lap =
              4 * gray[i] - gray[i - 1] - gray[i + 1] - gray[i - w] - gray[i + w];
            const v = Math.min(255, Math.max(0, (Math.abs(lap) - cut) * gain));
            const j = i * 4;
            op[j] = op[j + 1] = op[j + 2] = v;
            op[j + 3] = 255;
          }
        }
        // Border ring (skipped by the kernel): opaque black.
        for (let x = 0; x < w; x++) {
          for (const y of [0, h - 1]) {
            op[(y * w + x) * 4 + 3] = 255;
          }
        }
        for (let y = 0; y < h; y++) {
          for (const x of [0, w - 1]) {
            op[(y * w + x) * 4 + 3] = 255;
          }
        }
        canvas.width = w;
        canvas.height = h;
        canvas.getContext('2d')?.putImageData(out, 0, 0);
      })
      .catch(() => {});
    return () => {
      gone = true;
    };
  }, [src, threshold]);

  return (
    <canvas
      ref={canvasRef}
      data-testid="spot-visualize"
      className="pointer-events-none absolute inset-0"
      style={{ width: boxW, height: boxH }}
    />
  );
}
