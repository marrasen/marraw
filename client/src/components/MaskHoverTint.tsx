// MaskHoverTint shows a mask's red weight tint over the loupe while its row
// in the Masks panel is hovered, fading out when the pointer leaves. For the
// parametric types it reuses MaskTint (client-side SVG/canvas); AI masks have
// no client-side weight function, so their tint is rendered by the server
// (Edits.MaskTintPreview — the real evaluator over the real map, returned as
// a red transparent PNG in display space) and stretched over the image box.
import { useEffect, useRef, useState } from 'react';
import type { Params } from '@/api/edit';
import { useApiClient } from '@/api/client';
import { MaskTint } from '@/components/MaskOverlay';
import { useEditSession } from '@/lib/editSession';
import { cachedTint, fetchTint, tintKey } from '@/lib/tintCache';

export function MaskHoverTint({
  draft,
  frameW,
  frameH,
  boxW,
  boxH,
}: {
  draft: Params;
  frameW: number;
  frameH: number;
  boxW: number;
  boxH: number;
}) {
  const client = useApiClient();
  const tintMask = useEditSession((s) => s.tintMask);
  const photoId = useEditSession((s) => s.photoId);
  // The last hovered index stays mounted through the fade-out. Seed from the
  // live hover so a mount while a mask row is already hovered (e.g. toggling
  // crop mode without moving the pointer remounts this) shows the tint
  // immediately — the adjust-during-render below only fires on CHANGES.
  const [shown, setShown] = useState<number | null>(tintMask);
  const [aiUrl, setAiUrl] = useState<string | null>(null);
  const fetchSeq = useRef(0);

  const mask = shown != null ? draft.masks?.[shown] : undefined;

  // Track the hovered mask, but keep the last one mounted through the
  // fade-out. Adjust during render (not an effect); tintMask is a primitive.
  const [prevTint, setPrevTint] = useState(tintMask);
  if (tintMask !== prevTint) {
    setPrevTint(tintMask);
    if (tintMask != null) {
      // Moving to a DIFFERENT mask drops the old PNG: it covers another
      // region entirely, and showing it until the new fetch resolves reads as
      // the wrong mask lighting up. (A cache hit repaints immediately.)
      if (tintMask !== shown) setAiUrl(null);
      setShown(tintMask);
    }
  }

  // AI and range masks have no client-side weight function (range coverage
  // needs the developed pixels): fetch the server-rendered tint for the
  // hovered mask.
  useEffect(() => {
    const m = tintMask != null ? draft.masks?.[tintMask] : undefined;
    if (tintMask == null || photoId == null || !m || (m.type !== 'ai' && m.type !== 'range')) return;
    const key = tintKey(photoId, m, draft);
    const cached = cachedTint(key);
    if (cached) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- cache-hit fast path of this async tint fetch
      setAiUrl(cached);
      return;
    }
    const seq = ++fetchSeq.current;
    fetchTint(client, photoId, draft, tintMask, key)
      .then((url) => {
        if (fetchSeq.current === seq) setAiUrl(url);
      })
      .catch(() => {});
  }, [client, draft, photoId, tintMask]);

  if (shown == null || !mask) return null;
  const visible = tintMask === shown;

  return (
    <div
      className="pointer-events-none absolute inset-0 z-[9] transition-opacity duration-300"
      style={{ opacity: visible ? 1 : 0 }}
      data-testid="mask-hover-tint"
    >
      {mask.type === 'ai' || mask.type === 'range' ? (
        aiUrl && <img src={aiUrl} alt="" className="absolute inset-0 size-full" draggable={false} />
      ) : (
        <MaskTint mask={mask} draft={draft} frameW={frameW} frameH={frameH} boxW={boxW} boxH={boxH} k={boxW / ((draft.cropW > 0 ? draft.cropW : 1) * frameW)} />
      )}
    </div>
  );
}
