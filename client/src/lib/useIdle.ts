import { useEffect, useState } from 'react';

/**
 * useIdle reports whether the user has been inactive for `ms`. Cinema-mode
 * chrome fades out when idle so the photograph reads edge-to-edge; any
 * pointer, wheel, or key activity brings it back.
 */
export function useIdle(ms = 2800): boolean {
  const [idle, setIdle] = useState(false);
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
  return idle;
}
