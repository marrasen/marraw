import { describe, expect, it } from 'vitest';

import { renditionPlan, tilesEngaged, type RenditionInput } from '@/lib/rendition';

/** Past pyramid depth on a settled photo, tiles ready — the rich case. */
function deep(over: Partial<RenditionInput> = {}): RenditionInput {
  return {
    preview: null,
    photoId: 7,
    cropping: false,
    level: 'tiles',
    atFit: false,
    uiMode: 'develop',
    ...over,
  };
}

describe('the live preview blob', () => {
  it('is shown when it is this photo and this geometry', () => {
    expect(renditionPlan(deep({ preview: { photoId: 7, flat: false } })).usePreview).toBe(true);
  });

  it('is ignored when it belongs to another photo', () => {
    // A render landing late after a photo switch.
    expect(renditionPlan(deep({ preview: { photoId: 8, flat: false } })).usePreview).toBe(false);
  });

  // The bug: a flat frame kept after leaving crop mode stretches a photo whose
  // crop the committed rendition already has baked in.
  it('is ignored when its geometry does not match the mode', () => {
    expect(
      renditionPlan(deep({ preview: { photoId: 7, flat: true }, cropping: false })).usePreview,
    ).toBe(false);
    expect(
      renditionPlan(deep({ preview: { photoId: 7, flat: false }, cropping: true })).usePreview,
    ).toBe(false);
  });

  it('is shown for a flat frame while cropping', () => {
    expect(
      renditionPlan(deep({ preview: { photoId: 7, flat: true }, cropping: true })).usePreview,
    ).toBe(true);
  });
});

describe('which rendition backs the image', () => {
  it('resolves tile depth to the 2048 underlay', () => {
    expect(renditionPlan(deep({ level: 'tiles' })).srcLevel).toBe('2048');
  });

  it('passes an ordinary level through', () => {
    for (const l of ['256', '512', '1024', '2048'] as const) {
      expect(renditionPlan(deep({ level: l })).srcLevel).toBe(l);
    }
  });
});

describe('tile depth', () => {
  it('engages past pyramid depth on a settled photo', () => {
    const p = renditionPlan(deep());
    expect(p.tileDepth).toBe(true);
    expect(tilesEngaged(p.tileDepth, true)).toBe(true);
  });

  it('stays off below pyramid depth', () => {
    expect(renditionPlan(deep({ level: '1024' })).tileDepth).toBe(false);
  });

  it('stays off while a live preview is showing', () => {
    expect(renditionPlan(deep({ preview: { photoId: 7, flat: false } })).tileDepth).toBe(false);
  });

  it('stays off while cropping', () => {
    expect(renditionPlan(deep({ cropping: true })).tileDepth).toBe(false);
  });

  // Tile depth must never block browsing: a cold photo shows the warm 2048
  // rather than waiting on a full-resolution render.
  it('is reached but not engaged when the tile set is cold', () => {
    const p = renditionPlan(deep());
    expect(p.tileDepth).toBe(true);
    expect(tilesEngaged(p.tileDepth, false)).toBe(false);
    expect(p.srcLevel).toBe('2048'); // something to show meanwhile
  });

  // The gate that was fit-only once, which on a high-DPI display left the old
  // render-on-demand path live at every numeric zoom.
  it('engages at fit as well as at a numeric zoom, once warm', () => {
    expect(tilesEngaged(renditionPlan(deep({ atFit: true })).tileDepth, true)).toBe(true);
    expect(tilesEngaged(renditionPlan(deep({ atFit: false })).tileDepth, true)).toBe(true);
  });
});

describe('prefetching neighbours', () => {
  it('only follows a deliberate zoom', () => {
    expect(renditionPlan(deep({ atFit: false })).prefetchTiles).toBe(true);
    // At high-DPI fit this would be a multi-second decode per neighbour per step.
    expect(renditionPlan(deep({ atFit: true })).prefetchTiles).toBe(false);
  });

  it('needs tile depth at all', () => {
    expect(renditionPlan(deep({ level: '1024', atFit: false })).prefetchTiles).toBe(false);
  });
});

describe('the dwell kick', () => {
  // Culling dwells 1-3 seconds per frame, which sails past any threshold; on a
  // 4K display that fired the most expensive render the daemon has, per photo
  // merely looked at, starving the cheap requests.
  it('is off while culling at fit', () => {
    expect(renditionPlan(deep({ uiMode: 'cull', atFit: true })).allowDwellKick).toBe(false);
  });

  it('is on when someone zooms in to check focus, even while culling', () => {
    expect(renditionPlan(deep({ uiMode: 'cull', atFit: false })).allowDwellKick).toBe(true);
  });

  it('is on outside culling either way', () => {
    expect(renditionPlan(deep({ uiMode: 'develop', atFit: true })).allowDwellKick).toBe(true);
    expect(renditionPlan(deep({ uiMode: 'library', atFit: false })).allowDwellKick).toBe(true);
  });
});
