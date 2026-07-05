import { useEffect, useRef } from 'react';
import type { Photo } from '@/api/library';
import { imgUrl } from '@/lib/backend';
import { useEditSession } from '@/lib/editSession';

const BINS = 256;

interface Bins {
  r: Uint32Array;
  g: Uint32Array;
  b: Uint32Array;
}

// Histogram renders the RGB distribution of what is on screen: the live
// preview blob while editing, otherwise the committed 512px rendition
// fetched over HTTP (the /img endpoint is CORS-open for exactly this).
export function Histogram({ photo }: { photo: Photo }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const preview = useEditSession((s) => s.preview);
  const previewBlob = preview && preview.photoId === photo.id ? preview.blob : null;

  useEffect(() => {
    let alive = true;
    // Abort the rendition fetch when the photo changes so the server can
    // cancel a render nobody is waiting for anymore.
    const ac = new AbortController();
    const compute = async (blob: Blob) => {
      const bins = await histogramOf(blob);
      if (alive && canvasRef.current) drawHistogram(canvasRef.current, bins);
    };
    if (previewBlob) {
      compute(previewBlob).catch(() => {});
    } else {
      fetch(imgUrl(photo, '512'), { signal: ac.signal })
        .then((r) => (r.ok ? r.blob() : Promise.reject(new Error(String(r.status)))))
        .then(compute)
        .catch(() => {});
    }
    return () => {
      alive = false;
      ac.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [photo.id, photo.editHash, photo.cacheKey, previewBlob]);

  return (
    <div className="border-b px-4 py-3">
      <canvas ref={canvasRef} data-testid="histogram" width={256} height={72} className="w-full rounded-sm bg-black/50" />
    </div>
  );
}

async function histogramOf(blob: Blob): Promise<Bins> {
  // Decode small: 256px wide is plenty for a 256-bin histogram.
  const bmp = await createImageBitmap(blob, { resizeWidth: 256 });
  const canvas = document.createElement('canvas');
  canvas.width = bmp.width;
  canvas.height = bmp.height;
  const ctx = canvas.getContext('2d', { willReadFrequently: true })!;
  ctx.drawImage(bmp, 0, 0);
  const { data } = ctx.getImageData(0, 0, canvas.width, canvas.height);
  bmp.close();

  const r = new Uint32Array(BINS);
  const g = new Uint32Array(BINS);
  const b = new Uint32Array(BINS);
  for (let i = 0; i < data.length; i += 4) {
    r[data[i]]++;
    g[data[i + 1]]++;
    b[data[i + 2]]++;
  }
  return { r, g, b };
}

function drawHistogram(canvas: HTMLCanvasElement, bins: Bins) {
  const ctx = canvas.getContext('2d')!;
  const { width: w, height: h } = canvas;
  ctx.clearRect(0, 0, w, h);

  // Normalize against the largest interior bin so clipped-black/white
  // spikes don't flatten everything else.
  let max = 1;
  for (let i = 1; i < BINS - 1; i++) {
    max = Math.max(max, bins.r[i], bins.g[i], bins.b[i]);
  }

  ctx.globalCompositeOperation = 'screen';
  const channels: [Uint32Array, string][] = [
    [bins.r, 'rgba(239,68,68,0.85)'],
    [bins.g, 'rgba(34,197,94,0.85)'],
    [bins.b, 'rgba(96,165,250,0.85)'],
  ];
  for (const [bin, color] of channels) {
    ctx.fillStyle = color;
    ctx.beginPath();
    ctx.moveTo(0, h);
    for (let i = 0; i < BINS; i++) {
      const v = Math.min(1, bin[i] / max);
      // sqrt scale keeps midtone detail visible next to big peaks.
      ctx.lineTo((i / (BINS - 1)) * w, h - Math.sqrt(v) * h);
    }
    ctx.lineTo(w, h);
    ctx.closePath();
    ctx.fill();
  }
  ctx.globalCompositeOperation = 'source-over';
}
