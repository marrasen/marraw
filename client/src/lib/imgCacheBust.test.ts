// @vitest-environment jsdom
//
// jsdom: lib/backend.ts reads window.location at module scope.
import { describe, expect, it } from 'vitest';

import { imgUrl, type ImgRef } from '@/lib/backend';
import { bumpThumbBust } from '@/lib/imgCacheBust';

describe('thumbnail cache-buster', () => {
  it("asks again for a photo's unedited levels once marraw's render replaced the camera's", () => {
    const base: ImgRef = { id: 7, cacheKey: 'abcdef', editHash: 'base' };
    const edited: ImgRef = { ...base, editHash: 'h1' };
    const before = imgUrl(base, '256');
    const editedBefore = imgUrl(edited, '256');
    bumpThumbBust(7);
    expect(imgUrl(base, '256')).not.toBe(before);
    expect(new URL(imgUrl(base, '256')).searchParams.get('s')).toBe('1');
    // Edited levels never come from the camera: their URLs stay.
    expect(imgUrl(edited, '256')).toBe(editedBefore);
    expect(imgUrl({ ...base, id: 8 }, '256')).not.toContain('s=');
    // Nor do the larger levels: a 2048 already held is not asked for again.
    expect(new URL(imgUrl(base, '2048')).searchParams.has('s')).toBe(false);
  });
});
