// Server-persisted UI settings: the daemon's settings table is the source
// of truth, served through the `uiSettings` subscription so every window
// stays in sync automatically. <UISettingsSync/> mirrors each snapshot into
// the uiStore; the update helpers below write optimistically (instant local
// feedback) and fire the server setter — the subscription echo confirms and
// propagates to other windows. Nothing touches localStorage.
import { useEffect } from 'react';
import {
  setAutoPresets,
  setBurstGapSeconds,
  setBurstHamming,
  setCullDials,
  setEditGroupOpen,
  setExportDir,
  setExportOptions,
  setFeature,
  setFolderView,
  setGapMinutes,
  setGroupAlias,
  setLastSeenVersion,
  setLibrarySort,
  setPrerenderFullres,
  setQuickDials,
  setRailGroupOpen,
  setRailWidth,
  setShootGroup,
  setShootSort,
  setTheme,
  setThumbFit,
  setDefaultPresets,
  setUserPresets,
  setWatermarks,
  useGetUISettings,
  type AutoPreset as WireAutoPreset,
  type ExportOptions,
  type FolderView,
  type UserPreset,
  type Watermark,
} from '@/api/settings';
import type { ApiClient } from '@/api/client';
import type { AutoPreset } from '@/lib/autoPresets';
import type { FeatureId } from '@/lib/features';
import type { DialKey } from '@/lib/dials';
import {
  clampRailWidth,
  useUIStore,
  type FlagFilter,
  type LibrarySort,
  type ShootGroup,
  type ShootSort,
  type Theme,
  type ThumbFit,
} from '@/stores/uiStore';

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

// Remembers a per-folder view patch for the folder open in this window:
// optimistic merge into the local folderViews map (lowercased path key,
// same convention as group aliases), then the server merge — nil fields
// stay untouched there, so concurrent windows never clobber each other.
// No folder open = nothing to remember (globals still get their write).
function patchFolderView(client: ApiClient, patch: FolderView) {
  const { folderPath, folderViews } = useUIStore.getState();
  if (!folderPath) return;
  const key = folderPath.toLowerCase();
  useUIStore.setState({ folderViews: { ...folderViews, [key]: { ...folderViews[key], ...patch } } });
  setFolderView(client, key, patch).catch(swallow);
}

// Library rating/flag filters: effective values in the store plus the
// current folder's remembered view.
export function updateFolderFilters(
  client: ApiClient,
  f: { minRating?: number; flagFilter?: FlagFilter },
) {
  useUIStore.setState(f);
  const patch: FolderView = {};
  if (f.minRating !== undefined) patch.minRating = f.minRating;
  if (f.flagFilter !== undefined) patch.flagFilter = f.flagFilter;
  patchFolderView(client, patch);
}

export function updateGapMinutes(client: ApiClient, min: number | null) {
  useUIStore.setState({ gapMinutes: min, globalGapMinutes: min });
  setGapMinutes(client, min ?? 0).catch(swallow);
  patchFolderView(client, { gapMinutes: min ?? 0 });
}

// Near-duplicate burst grouping cutoff (dHash bits). Global — no per-folder
// override; the server re-clusters open folders on write.
export function updateBurstHamming(client: ApiClient, n: number) {
  useUIStore.setState({ burstHamming: n });
  setBurstHamming(client, n).catch(swallow);
}

// Burst time window (seconds). Global like the cutoff above.
export function updateBurstGapSeconds(client: ApiClient, n: number) {
  useUIStore.setState({ burstGapSeconds: n });
  setBurstGapSeconds(client, n).catch(swallow);
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

export function updateUserPresets(client: ApiClient, presets: UserPreset[]) {
  useUIStore.setState({ userPresets: presets });
  setUserPresets(client, presets).catch(swallow);
}

export function updateDefaultPresets(client: ApiClient, defaults: Record<string, string>) {
  useUIStore.setState({ defaultPresets: defaults });
  setDefaultPresets(client, defaults).catch(swallow);
}

export function updateWatermarks(client: ApiClient, watermarks: Watermark[]) {
  useUIStore.setState({ watermarks });
  setWatermarks(client, watermarks).catch(swallow);
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

// Transient state that stops making sense once its feature is off. Cleared
// on disable so e.g. an active Blinks filter can't keep hiding photos after
// its button is gone. (usePhotos also ignores these while disabled, which
// covers a disable arriving from another window.)
const featureTransients: Partial<Record<FeatureId, () => void>> = {
  bursts: () => useUIStore.setState({ collapseBursts: false }),
  softFilter: () => useUIStore.setState({ softOnly: false }),
  eyes: () => useUIStore.setState({ eyesClosedOnly: false, eyeScanOpen: false }),
  subjects: () => useUIStore.setState({ subjectScanOpen: false }),
};

export function updateFeature(client: ApiClient, id: FeatureId, enabled: boolean) {
  useUIStore.setState({ features: { ...useUIStore.getState().features, [id]: enabled } });
  if (!enabled) featureTransients[id]?.();
  setFeature(client, id, enabled).catch(swallow);
}

export function updateThumbFit(client: ApiClient, fit: ThumbFit) {
  useUIStore.setState({ thumbFit: fit });
  setThumbFit(client, fit).catch(swallow);
}

export function updateLibrarySort(client: ApiClient, sort: LibrarySort) {
  useUIStore.setState({ librarySort: sort, globalLibrarySort: sort });
  setLibrarySort(client, sort).catch(swallow);
  patchFolderView(client, { librarySort: sort });
}

export function updateShootSort(client: ApiClient, sort: ShootSort) {
  useUIStore.setState({ shootSort: sort });
  setShootSort(client, sort).catch(swallow);
}

export function updateShootGroup(client: ApiClient, group: ShootGroup) {
  useUIStore.setState({ shootGroup: group });
  setShootGroup(client, group).catch(swallow);
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

export function updateLastSeenVersion(client: ApiClient, version: string) {
  useUIStore.setState({ lastSeenVersion: version });
  setLastSeenVersion(client, version).catch(swallow);
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
