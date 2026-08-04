import { useCallback, useEffect, useRef, useState } from 'react';

// iPadOS before 16.4 only ships the webkit-prefixed Fullscreen API, and
// iPhone Safari ships none at all — every entry point feature-detects, and
// callers hide their UI when fullscreenSupported() says no.
type PrefixedDocument = Document & {
  webkitFullscreenEnabled?: boolean;
  webkitFullscreenElement?: Element | null;
  webkitExitFullscreen?: () => void;
};
type PrefixedElement = HTMLElement & {
  webkitRequestFullscreen?: () => void;
};

const doc = document as PrefixedDocument;

export function fullscreenSupported(): boolean {
  return Boolean(doc.fullscreenEnabled || doc.webkitFullscreenEnabled);
}

export function fullscreenElement(): Element | null {
  return doc.fullscreenElement ?? doc.webkitFullscreenElement ?? null;
}

export async function enterFullscreen(): Promise<void> {
  const root = document.documentElement as PrefixedElement;
  try {
    if (root.requestFullscreen) await root.requestFullscreen({ navigationUI: 'hide' });
    else root.webkitRequestFullscreen?.();
  } catch {
    // A rejection (no transient activation, a user setting) just means the
    // page stays as it is.
  }
}

export async function exitFullscreen(): Promise<void> {
  try {
    if (doc.exitFullscreen) await doc.exitFullscreen();
    else doc.webkitExitFullscreen?.();
  } catch {
    // Already out is fine.
  }
}

/**
 * Fullscreen for the loupe: a manual toggle, plus rotate-to-landscape while
 * mounted enters fullscreen and rotating back undoes it — but only when the
 * rotation is what put us there, so a deliberate toggle press is never
 * fought. Unmounting (closing the loupe) also drops auto-entered fullscreen.
 */
export function useFullscreen() {
  const supported = fullscreenSupported();
  const [active, setActive] = useState(() => fullscreenElement() != null);
  const autoEntered = useRef(false);

  useEffect(() => {
    const onChange = () => {
      const fs = fullscreenElement() != null;
      setActive(fs);
      // Leaving by any route (Escape, back gesture, system UI) resets the
      // rotation bookkeeping.
      if (!fs) autoEntered.current = false;
    };
    document.addEventListener('fullscreenchange', onChange);
    document.addEventListener('webkitfullscreenchange', onChange);
    return () => {
      document.removeEventListener('fullscreenchange', onChange);
      document.removeEventListener('webkitfullscreenchange', onChange);
    };
  }, []);

  useEffect(() => {
    // screen.orientation, not an (orientation:) media query: this only fires
    // on a real device rotation, never on a desktop window resize. Chrome
    // allows requestFullscreen without a tap in exactly this case (a
    // user-generated orientation change).
    const orientation = screen.orientation as ScreenOrientation | undefined;
    if (!supported || !orientation) return;
    const onRotate = () => {
      if (orientation.type.startsWith('landscape')) {
        if (!fullscreenElement()) {
          void enterFullscreen().then(() => {
            if (fullscreenElement()) autoEntered.current = true;
          });
        }
      } else if (autoEntered.current) {
        autoEntered.current = false;
        void exitFullscreen();
      }
    };
    orientation.addEventListener('change', onRotate);
    return () => {
      orientation.removeEventListener('change', onRotate);
      if (autoEntered.current) {
        autoEntered.current = false;
        void exitFullscreen();
      }
    };
  }, [supported]);

  const toggle = useCallback(() => {
    autoEntered.current = false;
    if (fullscreenElement()) void exitFullscreen();
    else void enterFullscreen();
  }, []);

  return { supported, active, toggle };
}
