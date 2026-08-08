import { describe, expect, it } from 'vitest';

import { formatBytes } from '@/lib/bytes';

describe('formatBytes', () => {
  it('names each unit at its own scale', () => {
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(1024)).toBe('1.0 KB');
    expect(formatBytes(1024 * 1024)).toBe('1.0 MB');
    expect(formatBytes(1024 ** 3)).toBe('1.0 GB');
    expect(formatBytes(1024 ** 4)).toBe('1.0 TB');
    // Past the largest unit it keeps counting rather than wrapping.
    expect(formatBytes(2048 * 1024 ** 4)).toBe('2048 TB');
  });

  it('drops the decimal for whole bytes and past a hundred of a unit', () => {
    expect(formatBytes(999)).toBe('999 B');
    expect(formatBytes(24 * 1024 * 1024)).toBe('24.0 MB');
    expect(formatBytes(512 * 1024 * 1024)).toBe('512 MB');
  });

  // The one thing the two implementations this replaced disagreed about: a
  // photo whose size has not been read is unknown, while a cache holding
  // nothing — or a download sitting at 0 B/s — really is zero.
  it('lets the caller say what zero means', () => {
    expect(formatBytes(0)).toBe('—');
    expect(formatBytes(0, '0 B')).toBe('0 B');
    expect(formatBytes(-1)).toBe('—');
    expect(formatBytes(-1, '0 B')).toBe('0 B');
  });

  it('rounds up into the next unit rather than printing 1024 of the last', () => {
    expect(formatBytes(1024 * 1024 - 1)).toBe('1024 KB');
    expect(formatBytes(1024 * 1024)).toBe('1.0 MB');
  });
});
