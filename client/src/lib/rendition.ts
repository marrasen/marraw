// What the loupe should actually display, as one decision.
//
// The inputs are a live preview blob, a pyramid level, and a few modes; the
// outputs decide whether the blob is shown, which rendition backs it, whether
// full-resolution tiles engage, and whether a cold photo may kick a render.
// Spread across a component these read as five independent conditions, and the
// bugs here have all been one of them disagreeing with another:
//
//   * a flat (crop-mode) blob left on screen after Done, stretching a frame
//     whose crop the committed rendition already has baked in;
//   * tile depth gating on fit only, which on a high-DPI display left the
//     old render-on-demand path live at every numeric zoom;
//   * the dwell kick firing while culling, where 1-3 seconds per frame sails
//     past any dwell threshold — on 4K that meant the daemon's most expensive
//     render per photo merely looked at, starving the cheap requests.
//
// None of those is visible in a screenshot of a working state. All of them are
// a table.
import type { Level } from '@/lib/backend';

export interface RenditionInput {
  /** The live edit-preview blob, if one is in hand. */
  preview: { photoId: number; flat: boolean } | null;
  /** The photo the loupe is showing. */
  photoId: number;
  /** Whether the crop overlay is up — the flat geometry. */
  cropping: boolean;
  /** The pyramid level the TARGET scale calls for; 'tiles' means past depth. */
  level: Level | 'tiles';
  /** Whether the loupe is at fit rather than a deliberate zoom. */
  atFit: boolean;
  /** Which surface is mounted. */
  uiMode: string;
}

export interface RenditionPlan {
  /** Show the live blob rather than a cached rendition. */
  usePreview: boolean;
  /** The level backing the image; 'tiles' resolves to the 2048 underlay. */
  srcLevel: Level;
  /** Past pyramid depth, with a tile layer possible. */
  tileDepth: boolean;
  /** Prefetch neighbours' tile sets. */
  prefetchTiles: boolean;
  /** Allow a cold photo to kick one background full render after a dwell. */
  allowDwellKick: boolean;
}

export function renditionPlan(i: RenditionInput): RenditionPlan {
  // A blob belongs to one photo AND one geometry: flat frames are crop mode's,
  // cropped renders are normal viewing's. A mismatch falls back to the
  // committed rendition rather than stretching the wrong shape.
  const usePreview = i.preview != null && i.preview.photoId === i.photoId && i.preview.flat === i.cropping;

  const tileDepth = !usePreview && i.level === 'tiles' && !i.cropping;

  return {
    usePreview,
    srcLevel: i.level === 'tiles' ? '2048' : i.level,
    tileDepth,
    // Only when the user deliberately zoomed in. At high-DPI fit, prefetching
    // neighbours' tile sets is a multi-second full decode per neighbour per
    // step.
    prefetchTiles: tileDepth && !i.atFit,
    // Off entirely while culling at fit; a deliberate zoom still kicks, since
    // that is someone checking focus.
    allowDwellKick: i.uiMode !== 'cull' || !i.atFit,
  };
}

/**
 * Whether the tile layer engages now. Separate from the plan because warmth is
 * only asked about once tile depth is known — the query is driven by tileDepth
 * — so it cannot also be an input to it.
 *
 * Tiles engage only when the set is already rendered. A cold photo shows the
 * warm 2048 immediately rather than blocking on a full-resolution render, at
 * any zoom: tile depth must never stop someone browsing.
 */
export function tilesEngaged(tileDepth: boolean, tilesWarm: boolean): boolean {
  return tileDepth && tilesWarm;
}
