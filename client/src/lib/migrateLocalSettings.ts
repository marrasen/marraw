// One-time import of the pre-server-settings localStorage into the daemon's
// settings table. Settings used to live client-side, which meant two app
// instances clobbered each other; the server is now the single source of
// truth and nothing may remain in localStorage. Legacy keys are removed only
// after every push succeeded, so a failed run retries on the next launch
// (the setters are idempotent upserts).
import {
  setAutoPresets,
  setCullDials,
  setDevelopPinned,
  setEditGroupOpen,
  setExportDir,
  setGapMinutes,
  setGroupAlias,
  setQuickDials,
  setRailGroupOpen,
  setTheme,
} from '@/api/settings';
import type { ApiClient } from '@/api/client';
import { sanitizeAutoPresets } from '@/lib/autoPresets';
import { sanitizeDialKeys } from '@/lib/dials';
import { presetToWire } from '@/lib/uiSettings';

const THEMES = ['dark', 'light', 'system'] as const;

export async function migrateLocalSettings(client: ApiClient): Promise<void> {
  const keys = Object.keys(localStorage).filter(
    (k) => k === 'theme' || k.startsWith('marraw:') || k.startsWith('marraw.'),
  );
  if (keys.length === 0) return;

  const pushes: Promise<void>[] = [];
  const push = (p: Promise<void>) => pushes.push(p);

  const theme = localStorage.getItem('theme');
  if (theme && (THEMES as readonly string[]).includes(theme)) {
    push(setTheme(client, theme as (typeof THEMES)[number]));
  }

  const gap = localStorage.getItem('marraw:gapMinutes');
  if (gap === 'off') {
    push(setGapMinutes(client, 0));
  } else if (gap != null) {
    const n = Number(gap);
    if (Number.isFinite(n) && n > 0) push(setGapMinutes(client, Math.round(n)));
  }

  for (const [key, send] of [
    ['marraw:cullDials', setCullDials],
    ['marraw:quickDials', setQuickDials],
  ] as const) {
    const raw = localStorage.getItem(key);
    if (raw == null) continue;
    try {
      const parsed: unknown = JSON.parse(raw);
      if (Array.isArray(parsed)) {
        push(send(client, sanitizeDialKeys(parsed.filter((v) => typeof v === 'string'))));
      }
    } catch {
      // Malformed value: nothing worth preserving.
    }
  }

  const presetsRaw = localStorage.getItem('marraw:autoPresets');
  if (presetsRaw != null) {
    try {
      const presets = sanitizeAutoPresets(JSON.parse(presetsRaw));
      if (presets.length > 0) push(setAutoPresets(client, presets.map(presetToWire)));
    } catch {
      // Malformed value: nothing worth preserving.
    }
  }

  const exportDir = localStorage.getItem('marraw.exportDir');
  if (exportDir) push(setExportDir(client, exportDir));

  if (localStorage.getItem('marraw:developPinned') === '0') {
    push(setDevelopPinned(client, false));
  }

  // Dynamic families. Open/no-alias is the default server-side, so only the
  // non-default entries need migrating.
  for (const key of keys) {
    const value = localStorage.getItem(key);
    if (value == null) continue;
    if (key.startsWith('marraw:editGroup:') && value === '0') {
      push(setEditGroupOpen(client, key.slice('marraw:editGroup:'.length), false));
    } else if (key.startsWith('marraw:groupAlias:') && value !== '') {
      push(setGroupAlias(client, key.slice('marraw:groupAlias:'.length), value));
    } else if (key.startsWith('marraw:railGroup:') && value === '0') {
      push(setRailGroupOpen(client, key.slice('marraw:railGroup:'.length), false));
    }
  }

  const results = await Promise.allSettled(pushes);
  const failed = results.filter((r) => r.status === 'rejected');
  if (failed.length > 0) {
    console.error(`settings migration: ${failed.length} push(es) failed, keeping localStorage for retry`, failed);
    return;
  }
  for (const key of keys) localStorage.removeItem(key);
  console.info(`settings migration: imported ${pushes.length} setting(s) into the catalog, localStorage cleared`);
}
