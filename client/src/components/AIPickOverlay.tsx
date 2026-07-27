// AIPickOverlay is the shared loupe surface for the People and Scene pick
// tools: hover a region — a person instance or a scene category — to see its
// mask, then click to add it. Both kinds behave identically; the only
// difference is which label map (Edits.AIMapPlane) the hit-test reads. Hover
// costs zero round trips: the ID plane is fetched once per arm and decoded to
// ImageData, and the highlight itself is the server-rendered red tint of the
// CANDIDATE mask — the draft with that region's mask appended, never committed
// — through the shared tint cache (the MaskHoverTint mechanics). Every
// region's tint is prefetched from the detection results, so panel-chip hover
// paints from cache even before the tool is armed.
//
// The overlay stays mounted through normal Develop so chip hover always tints;
// it only takes pointer events (and shows the crosshair) while armed. Armed,
// it still lets click-drag PAN: pointerdown does not stop propagation (the pan
// starts in LoupeView's container), and a window pointerup adds the mask only
// when the pointer barely moved — a drag past a few pixels is a pan and adds
// nothing. The tool stays armed after an add so several regions can be added
// in a row; Esc (keyboard.ts), right-click, or re-pressing the panel button
// disarm it.
import { useEffect, useMemo, useRef, useState } from 'react';
import type { ApiClient } from '@/api/client';
import type { Mask, Params } from '@/api/edit';
import { aIMapPlane } from '@/api/edits';
import { aiClassMask, aiPersonMask } from '@/lib/controlSpecs';
import { frameFromDisplay } from '@/lib/crop';
import {
  esAddMaskObject,
  esArmAIPick,
  esSetAIHover,
  useEditSession,
  type AIPickKind,
} from '@/lib/editSession';
import { cachedTint, fetchTint, tintKey } from '@/lib/tintCache';

// A pan drag moves the pointer; a pick is a near-stationary click. Anything
// past this many pixels between down and up is treated as a pan (adds nothing).
const PAN_SLOP = 4;
// Swallow the second half of a double-click so a fast double-tap adds one mask.
const ADD_DEBOUNCE_MS = 350;

function maskForKind(kind: AIPickKind, id: number, mapVer: string): Mask {
  return kind === 'person' ? aiPersonMask(id, mapVer) : aiClassMask(id, mapVer);
}

// candidateFor builds the never-committed params MaskTintPreview evaluates:
// the draft plus the hovered region's mask at the end.
function candidateFor(draft: Params, mask: Mask): [Params, number] {
  const masks = [...(draft.masks ?? []), mask];
  return [{ ...draft, masks }, masks.length - 1];
}

export function AIPickOverlay({
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
  const armed = useEditSession((s) => s.aiPickArmed);
  const hover = useEditSession((s) => s.aiHover);
  const detect = useEditSession((s) => s.aiDetect);
  const armedVer = armed ? (detect[armed]?.mapVer ?? null) : null;
  const planeRef = useRef<{ data: Uint8ClampedArray; w: number; h: number } | null>(null);
  const [tintUrl, setTintUrl] = useState<string | null>(null);
  const fetchSeq = useRef(0);
  const lastAdd = useRef(0);

  // Scene picks only accept the categories the chips offer (the backend drops
  // sub-1.5% slivers as noise); people accept any non-background instance.
  const classIds = useMemo(
    () => new Set((detect.class?.categories ?? []).map((c) => c.id)),
    [detect.class],
  );

  // Fetch + decode the armed kind's oriented ID plane. rotate/flip are baked
  // in server-side (they change which plane we need); crop and straighten map
  // client-side via frameFromDisplay, so a mid-pick recrop needs no refetch.
  useEffect(() => {
    planeRef.current = null;
    if (!armed || !armedVer) return;
    let stale = false;
    aIMapPlane(client, photoId, armed, draft)
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
  }, [client, photoId, armed, armedVer, draft.rotate, draft.flipH]);

  // Prefetch every detected region's tint (both kinds, ≤8 each) so hovering a
  // chip — even before arming — paints from cache.
  const prefetchTargets = useMemo(() => {
    const out: { kind: AIPickKind; id: number; mapVer: string }[] = [];
    const p = detect.person;
    if (p && p.instances.length <= 8) for (const i of p.instances) out.push({ kind: 'person', id: i.id, mapVer: p.mapVer });
    const c = detect.class;
    if (c && c.categories.length <= 8) for (const cat of c.categories) out.push({ kind: 'class', id: cat.id, mapVer: c.mapVer });
    return out;
  }, [detect]);
  useEffect(() => {
    for (const t of prefetchTargets) {
      const m = maskForKind(t.kind, t.id, t.mapVer);
      const key = tintKey(photoId, m, draft);
      if (cachedTint(key)) continue;
      const [candidate, index] = candidateFor(draft, m);
      void fetchTint(client, photoId, candidate, index, key).catch(() => {});
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- prefetch once per detection set, not every draft frame
  }, [client, photoId, prefetchTargets]);

  // Keep the last hovered region mounted through the fade-out (the
  // MaskHoverTint pattern; adjust during render, hoverKey is a primitive).
  const hoverKey = hover ? `${hover.kind}:${hover.id}` : null;
  const [shownKey, setShownKey] = useState<string | null>(hoverKey);
  const [prevKey, setPrevKey] = useState<string | null>(hoverKey);
  if (hoverKey !== prevKey) {
    setPrevKey(hoverKey);
    if (hoverKey != null) setShownKey(hoverKey);
  }

  useEffect(() => {
    if (!hover) return;
    const ver = detect[hover.kind]?.mapVer;
    if (!ver) return;
    const m = maskForKind(hover.kind, hover.id, ver);
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
  }, [client, draft, photoId, hover, detect]);

  // clientX/Y → displayed-box fraction → oriented-frame fraction → plane
  // sample. Label ID rides the R channel of the grayscale PNG. Rect is read at
  // call time so a mid-pan release still samples the right pixel.
  const hitTestAt = (clientX: number, clientY: number, rect: DOMRect): number => {
    const p = planeRef.current;
    if (!p) return 0;
    const bx = (clientX - rect.left) / rect.width;
    const by = (clientY - rect.top) / rect.height;
    const [fx, fy] = frameFromDisplay(bx, by, draft, frameW, frameH);
    if (fx < 0 || fx > 1 || fy < 0 || fy > 1) return 0;
    const x = Math.min(p.w - 1, Math.max(0, Math.round(fx * (p.w - 1))));
    const y = Math.min(p.h - 1, Math.max(0, Math.round(fy * (p.h - 1))));
    const id = p.data[(y * p.w + x) * 4];
    if (id <= 0) return 0;
    if (armed === 'class' && !classIds.has(id)) return 0;
    return id;
  };

  const visible = hoverKey != null && hoverKey === shownKey;

  return (
    <div
      className={
        armed
          ? 'pointer-events-auto absolute inset-0 z-10 cursor-crosshair'
          : 'pointer-events-none absolute inset-0 z-10'
      }
      data-testid="ai-pick-overlay"
      onPointerMove={(e) => {
        if (!armed) return;
        const id = hitTestAt(e.clientX, e.clientY, e.currentTarget.getBoundingClientRect());
        esSetAIHover(id > 0 ? { kind: armed, id } : null);
      }}
      onPointerLeave={() => {
        if (armed) esSetAIHover(null);
      }}
      onPointerDown={(e) => {
        if (!armed || e.button !== 0) return;
        const el = e.currentTarget;
        const downX = e.clientX;
        const downY = e.clientY;
        // Do NOT stopPropagation: the pan drag starts in LoupeView's container
        // handler. Once it captures the pointer the overlay stops seeing
        // events, so the add is decided from a window pointerup, not our click.
        const stop = () => {
          window.removeEventListener('pointerup', onUp);
          window.removeEventListener('pointercancel', stop);
        };
        const onUp = (ev: PointerEvent) => {
          stop();
          if (Math.hypot(ev.clientX - downX, ev.clientY - downY) > PAN_SLOP) return; // a pan, not a pick
          if (Date.now() - lastAdd.current < ADD_DEBOUNCE_MS) return; // 2nd half of a double-click
          const id = hitTestAt(ev.clientX, ev.clientY, el.getBoundingClientRect());
          const ver = detect[armed]?.mapVer;
          if (id > 0 && ver) {
            lastAdd.current = Date.now();
            esAddMaskObject(client, maskForKind(armed, id, ver)); // stays armed
          }
        };
        window.addEventListener('pointerup', onUp);
        window.addEventListener('pointercancel', stop);
      }}
      onContextMenu={(e) => {
        if (!armed) return;
        e.preventDefault();
        esArmAIPick(null); // leave without adding; chips persist
      }}
    >
      <div
        className="pointer-events-none absolute inset-0 transition-opacity duration-300"
        style={{ opacity: visible ? 1 : 0 }}
        data-testid="ai-pick-tint"
      >
        {tintUrl && <img src={tintUrl} alt="" className="absolute inset-0 size-full" draggable={false} />}
      </div>
    </div>
  );
}
