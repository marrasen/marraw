import { useEffect, useState } from 'react';

// useDevicePixelRatio is window.devicePixelRatio, updated when it changes: the
// window moved to a screen of another density, or the zoom factor changed.
// A media query for the current ratio stops matching at the change, which is
// the only notice the browser gives.
export function useDevicePixelRatio(): number {
  const [dpr, setDpr] = useState(() => window.devicePixelRatio);
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return;
    const mq = window.matchMedia(`(resolution: ${dpr}dppx)`);
    const changed = () => setDpr(window.devicePixelRatio);
    mq.addEventListener('change', changed);
    return () => mq.removeEventListener('change', changed);
  }, [dpr]);
  return dpr;
}
