import { describe, expect, it } from 'vitest';

import { compareVersions, entriesSince, parseChangelog } from '@/lib/changelog';

// A stand-in changelog: a stable release, the betas that led to the next one,
// and the stable that closed them out — the shape the release skill produces.
const RAW = `# Changelog

Prose that is not a header or a bullet is ignored.

## 0.10.0 - 2026-08-20

- Library: reopens the folder you had open
- Fixed: the rail could hide its own toggle

## 0.10.0-beta.2 - 2026-08-12

- Library: second cut at reopening the folder

## 0.10.0-beta.1 - 2026-08-09

- Library: first cut at reopening the folder

## 0.9.0 - 2026-08-03

- Masks: spatial effects
`;

const entries = parseChangelog(RAW);
const versions = (list: { version: string }[]) => list.map((e) => e.version);

describe('parseChangelog', () => {
  it('reads stable and prerelease headers, newest first', () => {
    expect(versions(entries)).toEqual(['0.10.0', '0.10.0-beta.2', '0.10.0-beta.1', '0.9.0']);
  });

  it('keeps the date out of the version and the prose out of the bullets', () => {
    expect(entries[1]).toEqual({
      version: '0.10.0-beta.2',
      date: '2026-08-12',
      items: ['Library: second cut at reopening the folder'],
    });
  });
});

describe('compareVersions', () => {
  it('orders by the numeric core first', () => {
    expect(compareVersions('0.10.0', '0.9.0')).toBeGreaterThan(0);
    expect(compareVersions('0.9.0', '0.10.0')).toBeLessThan(0);
    expect(compareVersions('0.10.0', '0.10.0')).toBe(0);
  });

  it('sorts a prerelease below its own stable release', () => {
    expect(compareVersions('0.10.0-beta.13', '0.10.0')).toBeLessThan(0);
    expect(compareVersions('0.10.0', '0.10.0-beta.13')).toBeGreaterThan(0);
    // ...and still above the release before it.
    expect(compareVersions('0.10.0-beta.1', '0.9.0')).toBeGreaterThan(0);
  });

  it('compares numeric identifiers as numbers, not text', () => {
    expect(compareVersions('0.10.0-beta.9', '0.10.0-beta.13')).toBeLessThan(0);
    expect(compareVersions('0.10.0-beta.2', '0.10.0-alpha.7')).toBeGreaterThan(0);
    // A longer run of identifiers wins a tie on the shared prefix.
    expect(compareVersions('0.10.0-beta', '0.10.0-beta.1')).toBeLessThan(0);
  });

  it('ignores build metadata', () => {
    expect(compareVersions('0.10.0+abc123', '0.10.0')).toBe(0);
  });
});

describe('entriesSince', () => {
  it('shows nothing on a fresh install', () => {
    expect(entriesSince('', '0.10.0', entries)).toEqual([]);
  });

  it('shows every release the machine skipped, newest first', () => {
    expect(versions(entriesSince('0.9.0', '0.10.0', entries))).toEqual([
      '0.10.0',
      '0.10.0-beta.2',
      '0.10.0-beta.1',
    ]);
  });

  it('shows only the new beta between two betas', () => {
    expect(versions(entriesSince('0.10.0-beta.1', '0.10.0-beta.2', entries))).toEqual([
      '0.10.0-beta.2',
    ]);
  });

  it('shows the stable release when a beta tester moves onto it', () => {
    expect(versions(entriesSince('0.10.0-beta.2', '0.10.0', entries))).toEqual(['0.10.0']);
  });

  it('shows nothing when the version has not moved, or moved backwards', () => {
    expect(entriesSince('0.10.0', '0.10.0', entries)).toEqual([]);
    expect(entriesSince('0.10.0', '0.9.0', entries)).toEqual([]);
  });
});
