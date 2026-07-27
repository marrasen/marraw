// PersonPickOverlay is the person pick tool's loupe surface: hover a person
// to see their mask, click to add it. The hit-test runs entirely client-side
// against the photo's instance-ID plane (Edits.AIInstancePlane, fetched once
// per entry and decoded to ImageData) so hovering costs zero round trips;
// the highlight itself is the server-rendered red tint of the CANDIDATE mask
// — the draft with that person's mask appended, never committed — through
// the shared tint cache (the MaskHoverTint mechanics). Every instance's tint
// is prefetched on entry, so the first hover paints from cache. Clicking
// commits the mask and exits the tool; Esc (keyboard.ts) and right-click
// exit without adding.
import { useEffect, useRef, useState } from 'react';
import type { ApiClient } from '@/api/client';
import type { Params } from '@/api/edit';
import { aIInstancePlane } from '@/api/edits';
import { aiPersonMask } from '@/lib/controlSpecs';
import { frameFromDisplay } from '@/lib/crop';
import {
  esAddMaskObject,
  esSetPersonHover,
  esSetPersonPick,
  useEditSession,
} from '@/lib/editSession';
import { cachedTint, fetchTint, tintKey } from '@/lib/tintCache';

// candidateFor builds the never-committed params MaskTintPreview evaluates:
// the draft plus the hovered person's mask at the end.
function candidateFor(draft: Params, mask: ReturnType<typeof aiPersonMask>): [Params, number] {
  const masks = [...(draft.masks ?? []), mask];
  return [{ ...draft, masks }, masks.length - 1];
}

export function PersonPickOverlay({
  client,
  draft,
  photoId,
  frameW,
  frameH,
}: {
  client: ApiClient;
  draft: Params;
  photoId: number;
  frameW: number;
  frameH: number;
}) {
  const personPick = useEditSession((s) => s.personPick);
  const personHover = useEditSession((s) => s.personHover);
  const mapVer = personPick?.mapVer;
  const planeRef = useRef<{ data: Uint8ClampedArray; w: number; h: number } | null>(null);
  const [tintUrl, setTintUrl] = useState<string | null>(null);
  const fetchSeq = useRef(0);

  // Fetch + decode the oriented instance plane. rotate/flip are baked in
  // server-side (they change which plane we need); crop and straighten map
  // client-side via frameFromDisplay, so a mid-pick recrop needs no refetch.
  useEffect(() => {
    planeRef.current = null;
    if (!mapVer) return;
    let stale = false;
    aIInstancePlane(client, photoId, draft)
      .then(async (blob) => {
        const bmp = await createImageBitmap(blob);
        const canvas = new OffscreenCanvas(bmp.width, bmp.height);
        const ctx = canvas.getContext('2d')!;
        ctx.drawImage(bmp, 0, 0);
        const img = ctx.getImageData(0, 0, bmp.width, bmp.height);
        if (!stale) planeRef.current = { data: img.data, w: img.width, h: img.height };
      })
      .catch(() => {});
    return () => {
      stale = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- refetch on orientation only, not every draft frame
  }, [client, photoId, mapVer, draft.rotate, draft.flipH]);

  // Prefetch every instance's tint — N is small (≤32, usually a handful) and
  // each is a ~30 KB PNG, so the first hover paints from cache.
  const instances = personPick?.instances;
  useEffect(() => {
    if (!mapVer || !instances || instances.length > 8) return;
    for (const inst of instances) {
      const m = aiPersonMask(inst.id, mapVer);
      const key = tintKey(photoId, m, draft);
      if (cachedTint(key)) continue;
      const [candidate, index] = candidateFor(draft, m);
      void fetchTint(client, photoId, candidate, index, key).catch(() => {});
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- prefetch once per plane, not every draft frame
  }, [client, photoId, mapVer, instances]);

  // Keep the last hovered person mounted through the fade-out (the
  // MaskHoverTint pattern; adjust during render, personHover is a primitive).
  const [shown, setShown] = useState<number | null>(personHover);
  const [prevHover, setPrevHover] = useState(personHover);
  if (personHover !== prevHover) {
    setPrevHover(personHover);
    if (personHover != null) setShown(personHover);
  }

  useEffect(() => {
    if (personHover == null || !mapVer) return;
    const m = aiPersonMask(personHover, mapVer);
    const key = tintKey(photoId, m, draft);
    const cached = cachedTint(key);
    if (cached) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- cache-hit fast path of this async tint fetch
      setTintUrl(cached);
      return;
    }
    const seq = ++fetchSeq.current;
    const [candidate, index] = candidateFor(draft, m);
    fetchTint(client, photoId, candidate, index, key)
      .then((url) => {
        if (fetchSeq.current === seq) setTintUrl(url);
      })
      .catch(() => {});
  }, [client, draft, photoId, personHover, mapVer]);

  // Pointer → displayed-box fraction → oriented-frame fraction → plane
  // sample. Instance ID rides the R channel of the grayscale PNG.
  const hitTest = (e: React.MouseEvent): number => {
    const p = planeRef.current;
    if (!p) return 0;
    const rect = e.currentTarget.getBoundingClientRect();
    const bx = (e.clientX - rect.left) / rect.width;
    const by = (e.clientY - rect.top) / rect.height;
    const [fx, fy] = frameFromDisplay(bx, by, draft, frameW, frameH);
    if (fx < 0 || fx > 1 || fy < 0 || fy > 1) return 0;
    const x = Math.min(p.w - 1, Math.max(0, Math.round(fx * (p.w - 1))));
    const y = Math.min(p.h - 1, Math.max(0, Math.round(fy * (p.h - 1))));
    return p.data[(y * p.w + x) * 4];
  };

  const visible = personHover != null && personHover === shown;

  return (
    <div
      className="absolute inset-0 z-10 cursor-crosshair"
      data-testid="person-pick-overlay"
      onPointerMove={(e) => {
        const id = hitTest(e);
        esSetPersonHover(id > 0 ? id : null);
      }}
      onPointerLeave={() => esSetPersonHover(null)}
      onClick={(e) => {
        const id = hitTest(e);
        if (id > 0 && mapVer) {
          esAddMaskObject(client, aiPersonMask(id, mapVer));
          esSetPersonPick(null);
        }
      }}
      onContextMenu={(e) => {
        e.preventDefault();
        esSetPersonPick(null);
      }}
    >
      <div
        className="pointer-events-none absolute inset-0 transition-opacity duration-300"
        style={{ opacity: visible ? 1 : 0 }}
        data-testid="person-pick-tint"
      >
        {tintUrl && <img src={tintUrl} alt="" className="absolute inset-0 size-full" draggable={false} />}
      </div>
    </div>
  );
}
