// The non-component half of the edit controls: a display formatter and the
// hook that scrolls a keyboard-focused row into view. Separate file because
// fast refresh only tracks a module that exports components alone.
import { useEffect, useRef } from 'react';

export const pct = (v: number) => (v === 0 ? '0' : `${v > 0 ? '+' : ''}${Math.round(v * 100)}`);

export function useActiveScroll(active?: boolean) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (active) ref.current?.scrollIntoView({ block: 'nearest' });
  }, [active]);
  return ref;
}

// WB dial gradient tracks per the handoff eyedropper plate.
export const TEMP_GRADIENT = 'bg-gradient-to-r from-[#6fa8ff] via-[#e9e3d0] to-[#ffb066]';
export const TINT_GRADIENT = 'bg-gradient-to-r from-[#5cd06e] via-[#d9d9d9] to-[#c86fd0]';
// Full hue wheel left→right (0→1), for the range mask's hue-window track.
export const HUE_GRADIENT = 'bg-[linear-gradient(to_right,#ff0000,#ffff00,#00ff00,#00ffff,#0000ff,#ff00ff,#ff0000)]';
