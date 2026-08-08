// @vitest-environment jsdom
//
// jsdom for the same reason as the store's own suite: importing it reaches
// lib/backend.ts, which reads window.location at module scope.
import { beforeEach, describe, expect, it } from 'vitest';

import type { Photo, PhotoPatch, PhotoPatchEvent } from '@/api/library';
import { foldPatch, photoPatchReducer } from '@/lib/usePhotos';
import { useUIStore } from '@/stores/uiStore';

function patch(over: Partial<PhotoPatch> & { id: number }): PhotoPatch {
  return {
    rating: null, flag: null, editHash: null, rotate: null,
    cropW: null, cropH: null, subjectSharpness: null, subjectAnalyzed: null,
    eyesClosed: null, eyesAnalyzed: null,
    ...over,
  };
}

function event(...patches: PhotoPatch[]): PhotoPatchEvent {
  return { patches };
}

function photo(over: Partial<Photo> & { id: number }): Photo {
  return { rating: 0, flag: 'none', editHash: 'base', rotate: 0, ...over } as unknown as Photo;
}

beforeEach(() => {
  useUIStore.setState({ overrides: new Map() });
});

describe('photoPatchReducer', () => {
  const data = [photo({ id: 1 }), photo({ id: 2, rating: 3 })];

  it('merges only the fields a patch speaks to', () => {
    const [a, b] = photoPatchReducer(data, event(patch({ id: 2, flag: 'pick' })));
    expect(a).toBe(data[0]); // untouched photos keep their identity
    expect(b.flag).toBe('pick');
    expect(b.rating).toBe(3); // a null field means unchanged, not cleared
  });

  // Geometry arrives as an explicit value on every edit save, so zero is a
  // reset rather than an absence — the grid's cell aspect follows it live.
  it('applies a zeroed geometry field as a reset', () => {
    const withCrop = [photo({ id: 1, rotate: 1, cropW: 0.5, cropH: 0.5 })];
    const [p] = photoPatchReducer(withCrop, event(patch({ id: 1, rotate: 0, cropW: 0, cropH: 0 })));
    expect(p.rotate).toBe(0);
    expect(p.cropW).toBe(0);
  });

  it('passes anything that is not a patch event straight through', () => {
    expect(photoPatchReducer(data, null)).toBe(data);
    expect(photoPatchReducer(data, {})).toBe(data);
    expect(photoPatchReducer(data, { patches: 'nonsense' })).toBe(data);
  });

  it('ignores a patch for a photo not in this folder', () => {
    const out = photoPatchReducer(data, event(patch({ id: 99, flag: 'pick' })));
    expect(out).toEqual(data);
  });
});

describe('foldPatch', () => {
  // The wiring: one arriving patch has to do both halves of the job. Folding
  // it into the snapshot without retiring the override it supersedes is
  // exactly the state where the cache knows the truth and the screen does not.
  it('updates the snapshot and retires the guess it supersedes', () => {
    useUIStore.getState().applyLocal([1], { flag: 'pick' });

    const out = foldPatch([photo({ id: 1 })], event(patch({ id: 1, flag: 'exclude' })));

    expect(out[0].flag).toBe('exclude');
    expect(useUIStore.getState().overrides.has(1)).toBe(false);
  });

  it('leaves overrides alone for a payload that is not a patch event', () => {
    useUIStore.getState().applyLocal([1], { flag: 'pick' });
    foldPatch([photo({ id: 1 })], null);
    expect(useUIStore.getState().overrides.get(1)).toEqual({ flag: 'pick' });
  });
});
