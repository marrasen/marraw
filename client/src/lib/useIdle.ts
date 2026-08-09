import { useCallback, useEffect, useMemo, useState } from 'react';

/**
 * useIdle reports whether the user has been inactive for `ms`. Cinema-mode
 * chrome fades out when idle so the photograph reads edge-to-edge; any
 * pointer, wheel, or key activity brings it back.
 */
export function useIdle(ms = 2800): boolean {
  return useIdleControls(ms).idle;
}

/**
 * useIdleControls is useIdle plus `goIdle`, which counts the user as idle
 * *now* rather than waiting out the timer. For gestures that have already said
 * "leave the photograph alone on screen" (hold-to-compare): the keystrokes
 * driving such a gesture look like activity to the timer, so on release the
 * chrome would swim back into view against the user's stated intent. Calling
 * goIdle instead leaves it hidden until real activity — a pointer move, a
 * keypress — asks for it back.
 */
export function useIdleControls(ms = 2800): { idle: boolean; goIdle: () => void } {
  const [idle, setIdle] = useState(false);
  const goIdle = useCallback(() => setIdle(true), []);
  useEffect(() => {
    let t = 0;
    const reset = () => {
      setIdle(false);
      window.clearTimeout(t);
      t = window.setTimeout(() => setIdle(true), ms);
    };
    reset();
    const events = ['pointermove', 'pointerdown', 'keydown', 'wheel'] as const;
    for (const ev of events) window.addEventListener(ev, reset, { passive: true });
    return () => {
      window.clearTimeout(t);
      for (const ev of events) window.removeEventListener(ev, reset);
    };
  }, [ms]);
  return useMemo(() => ({ idle, goIdle }), [idle, goIdle]);
}

/**
 * useHoverKeep: chrome under the cursor must never fade away — a pointer
 * resting on a control emits no events, so the idle timer alone would hide
 * the very thing the user is about to click. Spread `bind` on the hoverable
 * chrome and gate its `hidden` with `hovered` (hidden && !hovered).
 */
export function useHoverKeep(): {
  hovered: boolean;
  bind: { onPointerEnter: () => void; onPointerLeave: () => void };
} {
  const [hovered, setHovered] = useState(false);
  const bind = useMemo(
    () => ({
      onPointerEnter: () => setHovered(true),
      onPointerLeave: () => setHovered(false),
    }),
    [],
  );
  return { hovered, bind };
}
