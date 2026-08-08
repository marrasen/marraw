// @vitest-environment jsdom
//
// The store is pure enough, but importing it reaches lib/backend.ts, which
// reads window.location at module scope to work out whether the bundle is
// running as the app or as a share page.
import { beforeEach, describe, expect, it } from 'vitest';

import type { PhotoPatch } from '@/api/library';
import { useUIStore } from '@/stores/uiStore';

// A patch as the server sends one: every field it has no opinion about is null.
function patch(over: Partial<PhotoPatch> & { id: number }): PhotoPatch {
  return {
    rating: null, flag: null, editHash: null, rotate: null,
    cropW: null, cropH: null, subjectSharpness: null, subjectAnalyzed: null,
    eyesClosed: null, eyesAnalyzed: null,
    ...over,
  };
}

const { applyLocal, retireOverrides } = useUIStore.getState();
const overrides = () => useUIStore.getState().overrides;

beforeEach(() => {
  useUIStore.setState({ overrides: new Map() });
});

describe('optimistic overrides', () => {
  it('shows a keystroke before the server has answered', () => {
    applyLocal([7], { flag: 'pick' });
    expect(overrides().get(7)).toEqual({ flag: 'pick' });
  });

  // The bug this exists to prevent: an override outranks the query cache in
  // usePhotos' merge, so one left behind after the server has spoken masks
  // every later change to that field. The photo would sit there wearing a
  // flag nothing could take off it, and the P/X keys — which read the
  // override first — would keep toggling against the stale value.
  it('steps aside once the server has spoken for that field', () => {
    applyLocal([7], { flag: 'pick' });
    retireOverrides([patch({ id: 7, flag: 'pick' })]);
    expect(overrides().has(7)).toBe(false);
  });

  it('steps aside even when the server disagrees — that is the whole point', () => {
    // Another window, or a guest on a share link, rejected the same photo.
    applyLocal([7], { flag: 'pick' });
    retireOverrides([patch({ id: 7, flag: 'exclude' })]);
    expect(overrides().has(7)).toBe(false);
  });

  it('keeps the guesses the patch says nothing about', () => {
    applyLocal([7], { flag: 'pick' });
    applyLocal([7], { rating: 4 });
    expect(overrides().get(7)).toEqual({ flag: 'pick', rating: 4 });

    // A patch carrying only the flag retires only the flag.
    retireOverrides([patch({ id: 7, flag: 'pick' })]);
    expect(overrides().get(7)).toEqual({ rating: 4 });

    retireOverrides([patch({ id: 7, rating: 4 })]);
    expect(overrides().has(7)).toBe(false);
  });

  it('leaves other photos alone', () => {
    applyLocal([7, 8], { flag: 'pick' });
    retireOverrides([patch({ id: 7, flag: 'pick' })]);
    expect(overrides().has(7)).toBe(false);
    expect(overrides().get(8)).toEqual({ flag: 'pick' });
  });

  // rating 0 and flag 'none' are real values, and the wire spells "no opinion"
  // as null. Treating 0 as absent would strand an override on exactly the
  // photos someone had just unrated.
  it('treats a zero rating as something the server said', () => {
    applyLocal([7], { rating: 0 });
    retireOverrides([patch({ id: 7, rating: 0 })]);
    expect(overrides().has(7)).toBe(false);
  });

  it('retires a flag of none', () => {
    applyLocal([7], { flag: 'none' });
    retireOverrides([patch({ id: 7, flag: 'none' })]);
    expect(overrides().has(7)).toBe(false);
  });

  it('ignores a patch for a photo holding no guesses', () => {
    const before = overrides();
    retireOverrides([patch({ id: 99, flag: 'pick' })]);
    expect(overrides()).toBe(before); // same reference: no re-render provoked
  });

  it('does not churn state when nothing is retired', () => {
    applyLocal([7], { flag: 'pick' });
    const before = overrides();
    // A patch about a field this override has no guess for.
    retireOverrides([patch({ id: 7, editHash: 'abc123' })]);
    expect(overrides()).toBe(before);
  });

  it('handles a batch touching many photos at once', () => {
    applyLocal([1, 2, 3], { flag: 'exclude' });
    retireOverrides([patch({ id: 1, flag: 'exclude' }), patch({ id: 3, flag: 'pick' })]);
    expect(overrides().has(1)).toBe(false);
    expect(overrides().get(2)).toEqual({ flag: 'exclude' });
    expect(overrides().has(3)).toBe(false);
  });
});
