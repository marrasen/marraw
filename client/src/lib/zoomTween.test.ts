import { describe, expect, it } from 'vitest';

import { ZOOM_DURATION_MS, snapReason, zoomAt, type ZoomChange } from '@/lib/zoomTween';

/** A deliberate zoom step on a known frame — the one case that should ease. */
function eased(over: Partial<ZoomChange> = {}): ZoomChange {
  return {
    target: 2,
    shown: 1,
    zoomChanged: true,
    frameChanged: false,
    continuousInput: false,
    haveDims: true,
    ...over,
  };
}

describe('snapReason', () => {
  it('eases a deliberate step on a settled frame', () => {
    expect(snapReason(eased())).toBeNull();
  });

  // The regression the loupe's own comments record: a passive recompute —
  // a window resize, or the metadata pass landing corrected dimensions —
  // moves the target while the user's zoom intent sits still. Easing that
  // reads as the photo spuriously zooming in and springing back.
  it('snaps a target that moved without the user asking', () => {
    expect(snapReason(eased({ zoomChanged: false }))).toBe('not-a-zoom');
  });

  it('snaps between frames rather than animating one into the other', () => {
    expect(snapReason(eased({ frameChanged: true }))).toBe('frame-changed');
  });

  // A wheel is already a stream of positions. Easing lags, and the cursor
  // anchor is computed from the drawn scale, so it wobbles the faster you
  // scroll.
  it('snaps continuous input', () => {
    expect(snapReason(eased({ continuousInput: true }))).toBe('continuous-input');
  });

  it('snaps before the photo has dimensions', () => {
    expect(snapReason(eased({ haveDims: false }))).toBe('dimensions-unknown');
  });

  it('snaps a change too small to see', () => {
    expect(snapReason(eased({ shown: 1, target: 1 }))).toBe('already-there');
    expect(snapReason(eased({ shown: 1, target: 1 + 1e-9 }))).toBe('already-there');
    // Just over the threshold is a real change.
    expect(snapReason(eased({ shown: 1, target: 1.01 }))).toBeNull();
  });

  // Each reason is independent and sufficient: a wheel zoom onto a new photo
  // whose dimensions are unknown snaps for all three reasons, and must not
  // ease because one of them was checked first.
  it('snaps whenever any reason applies, in any combination', () => {
    const flags = ['zoomChanged', 'frameChanged', 'continuousInput', 'haveDims'] as const;
    for (let bits = 0; bits < 1 << flags.length; bits++) {
      const c = eased();
      flags.forEach((f, i) => {
        const on = Boolean(bits & (1 << i));
        // zoomChanged and haveDims snap when FALSE; the others when TRUE.
        c[f] = f === 'zoomChanged' || f === 'haveDims' ? on : !on;
      });
      const shouldEase =
        c.zoomChanged && c.haveDims && !c.frameChanged && !c.continuousInput;
      expect(snapReason(c) === null).toBe(shouldEase);
    }
  });
});

describe('zoomAt', () => {
  it('starts where it was and finishes exactly on the target', () => {
    expect(zoomAt(1, 2, 0)).toBeCloseTo(1, 10);
    expect(zoomAt(1, 2, ZOOM_DURATION_MS)).toBeCloseTo(2, 10);
  });

  // A frame delivered late must land on the target, not sail past it — an
  // overshoot here is a visible bounce at the end of every zoom.
  it('clamps rather than overshooting when a frame arrives late', () => {
    expect(zoomAt(1, 2, ZOOM_DURATION_MS * 5)).toBeCloseTo(2, 10);
    expect(zoomAt(1, 2, -50)).toBeCloseTo(1, 10);
  });

  it('moves toward the target the whole way, never back', () => {
    for (const [from, to] of [
      [1, 4],
      [4, 1], // zooming out
      [0.25, 0.3],
    ]) {
      let prev = from;
      for (let t = 0; t <= ZOOM_DURATION_MS; t += 2) {
        const v = zoomAt(from, to, t);
        // Never past either end...
        expect(v).toBeGreaterThanOrEqual(Math.min(from, to) - 1e-9);
        expect(v).toBeLessThanOrEqual(Math.max(from, to) + 1e-9);
        // ...and never doubling back on itself.
        if (to > from) expect(v).toBeGreaterThanOrEqual(prev - 1e-9);
        else expect(v).toBeLessThanOrEqual(prev + 1e-9);
        prev = v;
      }
    }
  });

  it('eases out — most of the distance is covered early', () => {
    const half = zoomAt(0, 1, ZOOM_DURATION_MS / 2);
    expect(half).toBeGreaterThan(0.5);
  });

  it('has nothing to do when the ends are equal', () => {
    for (const t of [0, 40, ZOOM_DURATION_MS]) expect(zoomAt(3, 3, t)).toBe(3);
  });
});
