// The loupe's zoom animation, as two pure functions.
//
// The animation itself is four lines of easing. What has actually gone wrong
// here, repeatedly, is the decision BEFORE it: whether a given scale change
// should ease or land instantly. Every regression the loupe's comments record
// is that predicate answering wrong — most memorably a passive fit recompute
// being eased, which reads as the photo spuriously zooming in and springing
// back while you are looking at it.
//
// So the predicate is what lives here, named and testable. A screenshot cannot
// see a spring-back; a table of cases can.

/** Everything the snap-or-ease decision depends on. */
export interface ZoomChange {
  /** The scale being moved to. */
  target: number;
  /** The scale currently drawn — mid-tween, this is not the last target. */
  shown: number;
  /**
   * Whether the user's zoom intent changed. Every deliberate path — wheel,
   * buttons, Z, double-click — routes through setLoupeZoom, so this is false
   * exactly when the target moved on its own.
   */
  zoomChanged: boolean;
  /**
   * Whether the frame's identity changed: a different photo, or crop mode
   * flipping the geometry.
   */
  frameChanged: boolean;
  /** Whether this change came from a wheel or pinch. */
  continuousInput: boolean;
  /** Whether the photo's dimensions are known yet. */
  haveDims: boolean;
}

/** Why a change snapped, for the test names and for anyone debugging one. */
export type SnapReason =
  | 'continuous-input'
  | 'frame-changed'
  | 'not-a-zoom'
  | 'dimensions-unknown'
  | 'already-there'
  | null;

/**
 * Returns the reason this change must land instantly, or null to ease.
 *
 * The order is not significant — these are independent reasons, any of which
 * is sufficient — but each is here for its own bug:
 *
 *  - continuous-input: a wheel or pinch is already a stream of positions.
 *    Easing one lags visibly, and because the wheel anchors its cursor maths
 *    on the currently drawn scale, easing makes the anchor wobble the faster
 *    you scroll.
 *  - frame-changed: animating a size change between two different photos, or
 *    between the cropped and flat geometries, reads as the photo warping.
 *  - not-a-zoom: the target moved while the user's zoom intent did not — a
 *    window resize, or the background metadata pass landing corrected
 *    dimensions. This is the spring-back case: nobody asked for a zoom, so
 *    showing one is a glitch, not an animation.
 *  - dimensions-unknown: there is no meaningful scale to ease from yet.
 *  - already-there: below a quarter of a percent, an animation is invisible
 *    work.
 */
export function snapReason(c: ZoomChange): SnapReason {
  if (c.continuousInput) return 'continuous-input';
  if (c.frameChanged) return 'frame-changed';
  if (!c.zoomChanged) return 'not-a-zoom';
  if (!c.haveDims) return 'dimensions-unknown';
  if (Math.abs(c.target - c.shown) < ZOOM_EPSILON) return 'already-there';
  return null;
}

/** Below this, a zoom change is not worth animating. */
export const ZOOM_EPSILON = 1e-4;

/** How long an eased zoom takes. */
export const ZOOM_DURATION_MS = 160;

/**
 * The scale to draw `elapsed` ms into an eased change. Ease-out cubic: fast
 * away from the old scale, settling onto the new one. Clamped at both ends, so
 * a frame delivered late lands exactly on the target rather than past it.
 */
export function zoomAt(from: number, to: number, elapsed: number): number {
  const p = Math.min(1, Math.max(0, elapsed / ZOOM_DURATION_MS));
  return from + (to - from) * (1 - Math.pow(1 - p, 3));
}
