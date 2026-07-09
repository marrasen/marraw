// Server-persisted UI settings: the daemon's settings table is the source
// of truth, served through the `uiSettings` subscription so every window
// stays in sync automatically. <UISettingsSync/> mirrors each snapshot into
// the uiStore; the update helpers below write optimistically (instant local
// feedback) and fire the server setter — the subscription echo confirms and
// propagates to other windows. Nothing touches localStorage.
import { useEffect } from 'react';
import {
  setAutoPresets,
  setCullDials,
  setEditGroupOpen,
  setExportDir,
  setExportOptions,
  setGapMinutes,
  setGroupAlias,
  setPrerenderFullres,
  setQuickDials,
  setRailGroupOpen,
  setRailWidth,
  setTheme,
  useGetUISettings,
  type AutoPreset as WireAutoPreset,
  type ExportOptions,
} from '@/api/settings';
import type { ApiClient } from '@/api/client';
import type { AutoPreset } from '@/lib/autoPresets';
import type { DialKey } from '@/lib/dials';
import { clampRailWidth, useUIStore, type Theme } from '@/stores/uiStore';

// Mirrors every uiSettings snapshot into the store. Render once, above
// everything that reads settings.
export function UISettingsSync() {
  const { data } = useGetUISettings();
  useEffect(() => {
    if (data) useUIStore.getState().applyUISettings(data);
  }, [data]);
  return null;
}

const swallow = (err: unknown) => console.error('uiSettings write failed:', err);

export function updateTheme(client: ApiClient, theme: Theme) {
  useUIStore.setState({ theme });
  setTheme(client, theme).catch(swallow);
}

export function updateGapMinutes(client: ApiClient, min: number | null) {
  useUIStore.setState({ gapMinutes: min });
  setGapMinutes(client, min ?? 0).catch(swallow);
}

export function updateCullDials(client: ApiClient, dials: DialKey[]) {
  useUIStore.setState({ cullDials: dials });
  setCullDials(client, dials).catch(swallow);
}

export function updateQuickDials(client: ApiClient, dials: DialKey[]) {
  useUIStore.setState({ quickDials: dials });
  setQuickDials(client, dials).catch(swallow);
}

export function updateAutoPresets(client: ApiClient, presets: AutoPreset[]) {
  useUIStore.setState({ autoPresets: presets });
  setAutoPresets(client, presets.map(presetToWire)).catch(swallow);
}

export function updateExportDir(client: ApiClient, dir: string) {
  useUIStore.setState({ exportDir: dir });
  setExportDir(client, dir).catch(swallow);
}

export function updateExportOptions(client: ApiClient, opts: ExportOptions) {
  useUIStore.setState({ exportOptions: opts });
  setExportOptions(client, opts).catch(swallow);
}

export function updatePrerenderFullres(client: ApiClient, enabled: boolean) {
  useUIStore.setState({ prerenderFullres: enabled });
  setPrerenderFullres(client, enabled).catch(swallow);
}

export function updateEditGroupOpen(client: ApiClient, id: string, open: boolean) {
  const next = { ...useUIStore.getState().editGroups };
  if (open) delete next[id];
  else next[id] = false;
  useUIStore.setState({ editGroups: next });
  setEditGroupOpen(client, id, open).catch(swallow);
}

// Group alias/collapse keys are the lowercased parent path (folder grouping
// is case-insensitive on Windows).
export function updateGroupAlias(client: ApiClient, parentPath: string, alias: string) {
  const key = parentPath.toLowerCase();
  const next = { ...useUIStore.getState().groupAliases };
  if (alias) next[key] = alias;
  else delete next[key];
  useUIStore.setState({ groupAliases: next });
  setGroupAlias(client, key, alias).catch(swallow);
}

export function updateRailGroupOpen(client: ApiClient, parentPath: string, open: boolean) {
  const key = parentPath.toLowerCase();
  const next = { ...useUIStore.getState().railGroups };
  if (open) delete next[key];
  else next[key] = false;
  useUIStore.setState({ railGroups: next });
  setRailGroupOpen(client, key, open).catch(swallow);
}

export function updateRailWidth(client: ApiClient, px: number) {
  const width = clampRailWidth(px);
  useUIStore.setState({ railWidth: width });
  setRailWidth(client, width).catch(swallow);
}

// The client preset type narrows sections/offset keys; the wire type is the
// open string form. Drop undefined offset slots (Partial) for the wire map.
export function presetToWire(p: AutoPreset): WireAutoPreset {
  const offsets: Record<string, number> = {};
  for (const [k, v] of Object.entries(p.offsets)) {
    if (typeof v === 'number') offsets[k] = v;
  }
  return { id: p.id, name: p.name, sections: [...p.sections], offsets };
}
