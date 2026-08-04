import { useCallback, useRef, useState } from 'react';

// Touch handling for the loupe. The desktop loupe zooms on the wheel (a
// trackpad pinch arrives as ctrl+wheel) and pans with a single pointer, which
// is why the share page has its own: on a phone the same gestures are two
// fingers and a drag, and none of that exists in the app.
//
// One pointer does double duty. Zoomed in it pans; at fit it is a swipe —
// horizontally to change photo, downwards to close — and the image follows the
// finger so the gesture is visibly doing something before it commits.

export interface Transform {
  scale: number;
  x: number;
  y: number;
}

const IDENTITY: Transform = { scale: 1, x: 0, y: 0 };

// Past this much movement a horizontal drag becomes a photo change. Roughly a
// thumb's width: far enough not to fire while tapping a star, close enough
// that flicking through a shoot does not feel like work.
const SWIPE_PX = 60;
// A downward drag this far closes the loupe, the way a photo viewer should.
const DISMISS_PX = 110;
const MAX_SCALE = 4;
// Two taps closer together than this, and near enough in space, toggle zoom.
const DOUBLE_TAP_MS = 300;
const DOUBLE_TAP_PX = 30;

interface Options {
  onNext: () => void;
  onPrev: () => void;
  onClose: () => void;
}

interface Pointer {
  x: number;
  y: number;
}

export function useGestures({ onNext, onPrev, onClose }: Options) {
  const [transform, setTransform] = useState<Transform>(IDENTITY);
  // Live drag offset while a swipe is in progress, so the photo tracks the
  // finger. Separate from transform: it springs back rather than persisting.
  const [drag, setDrag] = useState<{ x: number; y: number } | null>(null);

  const pointers = useRef(new Map<number, Pointer>());
  const start = useRef<Pointer | null>(null);
  const startTransform = useRef<Transform>(IDENTITY);
  const pinchStart = useRef<{ dist: number; scale: number } | null>(null);
  const lastTap = useRef<{ t: number; x: number; y: number } | null>(null);
  // Set once a gesture has moved far enough to be a drag: a pointerup that
  // never got here is a tap, and taps toggle the chrome.
  const moved = useRef(false);

  const reset = useCallback(() => {
    setTransform(IDENTITY);
    setDrag(null);
  }, []);

  const onPointerDown = useCallback((e: React.PointerEvent) => {
    // A press that starts on a control is a click, not a gesture: capturing it
    // steals the click from the button and toggles the chrome under it.
    if ((e.target as HTMLElement).closest('button')) return;
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    if (pointers.current.size === 1) {
      start.current = { x: e.clientX, y: e.clientY };
      moved.current = false;
      setTransform((t) => {
        startTransform.current = t;
        return t;
      });
    } else if (pointers.current.size === 2) {
      const [a, b] = [...pointers.current.values()];
      pinchStart.current = { dist: Math.hypot(a.x - b.x, a.y - b.y), scale: startTransform.current.scale };
      // A pinch that began as a swipe must not also change photo.
      setDrag(null);
      start.current = null;
    }
  }, []);

  const onPointerMove = useCallback((e: React.PointerEvent) => {
    if (!pointers.current.has(e.pointerId)) return;
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });

    if (pointers.current.size >= 2 && pinchStart.current) {
      const [a, b] = [...pointers.current.values()];
      const dist = Math.hypot(a.x - b.x, a.y - b.y);
      const next = Math.min(MAX_SCALE, Math.max(1, (pinchStart.current.scale * dist) / pinchStart.current.dist));
      setTransform((t) => ({ ...t, scale: next, ...(next === 1 ? { x: 0, y: 0 } : {}) }));
      moved.current = true;
      return;
    }

    if (!start.current) return;
    const dx = e.clientX - start.current.x;
    const dy = e.clientY - start.current.y;
    if (Math.abs(dx) > 6 || Math.abs(dy) > 6) moved.current = true;

    if (startTransform.current.scale > 1) {
      // Zoomed: pan, bounded so the photo cannot be dragged off screen. The
      // bound is derived from the scale rather than measured — at these
      // magnifications half the overflow is close enough, and measuring the
      // rendered image every frame is not.
      const el = e.currentTarget as HTMLElement;
      const maxX = (el.clientWidth * (startTransform.current.scale - 1)) / 2;
      const maxY = (el.clientHeight * (startTransform.current.scale - 1)) / 2;
      setTransform({
        scale: startTransform.current.scale,
        x: Math.max(-maxX, Math.min(maxX, startTransform.current.x + dx)),
        y: Math.max(-maxY, Math.min(maxY, startTransform.current.y + dy)),
      });
      return;
    }
    setDrag({ x: dx, y: dy });
  }, []);

  const onPointerUp = useCallback(
    (e: React.PointerEvent) => {
      // Not ours: the press started on a control, so it never became a gesture.
      if (!pointers.current.has(e.pointerId)) return;
      pointers.current.delete(e.pointerId);
      if (pointers.current.size < 2) pinchStart.current = null;
      if (pointers.current.size > 0) return;

      const d = drag;
      setDrag(null);
      start.current = null;

      if (!moved.current) {
        // A tap. Two in quick succession toggle zoom; the loupe treats a
        // single one as "show me the controls" (handled by the caller).
        const now = Date.now();
        const prev = lastTap.current;
        if (
          prev &&
          now - prev.t < DOUBLE_TAP_MS &&
          Math.hypot(e.clientX - prev.x, e.clientY - prev.y) < DOUBLE_TAP_PX
        ) {
          // Suppress the browser's own double-tap handling — selecting the
          // image (and tinting it blue) and page zoom both ride on this.
          e.preventDefault();
          lastTap.current = null;
          setTransform((t) => (t.scale > 1 ? IDENTITY : { scale: 2.5, x: 0, y: 0 }));
          return 'zoom' as const;
        }
        lastTap.current = { t: now, x: e.clientX, y: e.clientY };
        return 'tap' as const;
      }

      if (d) {
        // Horizontal wins ties: flicking through photos is the common gesture,
        // and a slightly diagonal flick is still meant as a flick.
        if (Math.abs(d.x) > SWIPE_PX && Math.abs(d.x) > Math.abs(d.y)) {
          if (d.x < 0) onNext();
          else onPrev();
        } else if (d.y > DISMISS_PX) {
          onClose();
        }
      }
      return 'drag' as const;
    },
    [drag, onNext, onPrev, onClose],
  );

  const onPointerCancel = useCallback((e: React.PointerEvent) => {
    if (!pointers.current.has(e.pointerId)) return;
    pointers.current.delete(e.pointerId);
    pinchStart.current = null;
    start.current = null;
    setDrag(null);
  }, []);

  return {
    transform,
    drag,
    zoomed: transform.scale > 1,
    reset,
    handlers: { onPointerDown, onPointerMove, onPointerUp, onPointerCancel },
  };
}
