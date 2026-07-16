import { create } from 'zustand';
import type { FlagType, Photo, PhotoPatch } from '@/api/library';
import type { ExportOptions, FolderView, UISettings, UserPreset, Watermark } from '@/api/settings';
import type { Params } from '@/api/edit';
import { sanitizeDialKeys, type DialKey } from '@/lib/dials';
import { sanitizeAutoPresets, type AutoPreset } from '@/lib/autoPresets';
import { sanitizeWatermarks } from '@/lib/watermarks';

export type Theme = 'dark' | 'light' | 'system';
// How thumbnails are framed in the grids. Mirrors the Go ThumbFit enum.
export type ThumbFit = 'crop' | 'fit' | 'natural';
// Photo ordering in the grids and filmstrips. Mirrors the Go LibrarySort enum.
export type LibrarySort = 'captureAsc' | 'captureDesc' | 'nameAsc' | 'nameDesc';
// Folder ordering in the library rail. Mirrors the Go ShootSort enum.
export type ShootSort = 'nameAsc' | 'nameDesc' | 'dateAsc' | 'dateDesc';
// Time bucketing of rail folders. Mirrors the Go ShootGroup enum.
export type ShootGroup = 'none' | 'year' | 'month' | 'day';

export type View = 'grid' | 'loupe';
export type FlagFilter = 'all' | 'pick' | 'not-excluded' | 'exclude';
// Top-level app mode (the top-bar segmented control). Library is the
// structured window; Cull and Develop are cinema canvases. Export lives in
// a dialog, not a mode.
export type Mode = 'library' | 'cull' | 'develop';
// Tabs of the develop drawer / library edit aside.
export type DevelopTab = 'develop' | 'masks' | 'presets' | 'info';
// Per-photo flag + rating as currently displayed (server rows merged with
// optimistic overrides), for readers outside React.
export interface PhotoMeta {
  flag: FlagType;
  rating: number;
}

export const DEFAULT_EXPORT_OPTIONS: ExportOptions = {
  format: 'jpeg',
  jpegQuality: 90,
  resizeMode: 'full',
  edgePx: 2160,
  colorSpace: 'srgb',
  sharpenTarget: 'off',
  sharpenAmount: 'standard',
  fileNameTemplate: '',
  exifMode: 'all',
  removeLocation: false,
  artist: '',
  copyright: '',
  watermarkId: '',
};

// Library rail width bounds — mirror the server's SetRailWidth validation.
export const RAIL_WIDTH_DEFAULT = 214;
export const RAIL_WIDTH_MIN = 180;
export const RAIL_WIDTH_MAX = 440;

export function clampRailWidth(px: number): number {
  if (!Number.isFinite(px)) return RAIL_WIDTH_DEFAULT;
  return Math.min(RAIL_WIDTH_MAX, Math.max(RAIL_WIDTH_MIN, Math.round(px)));
}

// A thumbFit from an older/newer server blob falls back to fit.
function sanitizeThumbFit(v: string | undefined): ThumbFit {
  return v === 'crop' || v === 'natural' ? v : 'fit';
}

// A librarySort from an older/newer server blob falls back to capture order.
function sanitizeLibrarySort(v: string | undefined): LibrarySort {
  return v === 'captureDesc' || v === 'nameAsc' || v === 'nameDesc' ? v : 'captureAsc';
}

// A shootSort from an older/newer server blob falls back to name order.
function sanitizeShootSort(v: string | undefined): ShootSort {
  return v === 'nameDesc' || v === 'dateAsc' || v === 'dateDesc' ? v : 'nameAsc';
}

// A shootGroup from an older/newer server blob falls back to no grouping.
function sanitizeShootGroup(v: string | undefined): ShootGroup {
  return v === 'year' || v === 'month' || v === 'day' ? v : 'none';
}

// Effective time-gap threshold: gaps are computed between neighboring frames,
// so they only mean something while the list runs in capture order (either
// direction). Name-sorted lists get one flat group.
export const selectGapMinutes = (s: {
  gapMinutes: number | null;
  librarySort: LibrarySort;
}): number | null =>
  s.librarySort === 'nameAsc' || s.librarySort === 'nameDesc' ? null : s.gapMinutes;

// One folder's remembered view resolved to effective values. Sort and gap
// fall back to the last-used globals; filters fall back to show-all, so a
// leftover rating filter never makes a fresh folder look empty. Wire values
// are sanitized field by field — a bad field from an older/newer blob
// degrades alone, not the whole entry.
function resolveFolderView(
  s: {
    folderViews: Record<string, FolderView>;
    globalLibrarySort: LibrarySort;
    globalGapMinutes: number | null;
  },
  folderPath: string | null,
): { minRating: number; flagFilter: FlagFilter; librarySort: LibrarySort; gapMinutes: number | null } {
  const v = folderPath ? s.folderViews[folderPath.toLowerCase()] : undefined;
  return {
    minRating:
      typeof v?.minRating === 'number' && v.minRating >= 0 && v.minRating <= 5
        ? Math.round(v.minRating)
        : 0,
    flagFilter:
      v?.flagFilter === 'pick' || v?.flagFilter === 'not-excluded' || v?.flagFilter === 'exclude'
        ? v.flagFilter
        : 'all',
    librarySort:
      v?.librarySort === 'captureAsc' ||
      v?.librarySort === 'captureDesc' ||
      v?.librarySort === 'nameAsc' ||
      v?.librarySort === 'nameDesc'
        ? v.librarySort
        : s.globalLibrarySort,
    // Stored 0 = per-folder "Off" (the same null bridge as the global row).
    gapMinutes:
      typeof v?.gapMinutes === 'number' && v.gapMinutes >= 0
        ? v.gapMinutes === 0
          ? null
          : Math.round(v.gapMinutes)
        : s.globalGapMinutes,
  };
}

// Mirrors the server's normalizeExportOptions: missing or invalid fields
// from older blobs fall back to the dialog defaults.
function sanitizeExportOptions(o: Partial<ExportOptions> | undefined): ExportOptions {
  return {
    // An older blob may still say 'tiff16'; that format is gone, so it falls
    // back to the jpeg default like any other unknown value.
    format:
      o?.format === 'tiff8' || o?.format === 'png' || o?.format === 'rawXmp' ? o.format : 'jpeg',
    jpegQuality:
      typeof o?.jpegQuality === 'number' && o.jpegQuality >= 1 && o.jpegQuality <= 100
        ? Math.round(o.jpegQuality)
        : DEFAULT_EXPORT_OPTIONS.jpegQuality,
    resizeMode: o?.resizeMode === 'edge' ? 'edge' : 'full',
    edgePx:
      typeof o?.edgePx === 'number' && o.edgePx >= 16 && o.edgePx <= 65536
        ? Math.round(o.edgePx)
        : DEFAULT_EXPORT_OPTIONS.edgePx,
    colorSpace:
      o?.colorSpace === 'adobergb' || o?.colorSpace === 'prophoto' ? o.colorSpace : 'srgb',
    sharpenTarget:
      o?.sharpenTarget === 'screen' || o?.sharpenTarget === 'matte' || o?.sharpenTarget === 'glossy'
        ? o.sharpenTarget
        : 'off',
    sharpenAmount:
      o?.sharpenAmount === 'low' || o?.sharpenAmount === 'high' ? o.sharpenAmount : 'standard',
    fileNameTemplate: typeof o?.fileNameTemplate === 'string' ? o.fileNameTemplate.trim() : '',
    exifMode: o?.exifMode === 'copyright' || o?.exifMode === 'none' ? o.exifMode : 'all',
    removeLocation: o?.removeLocation === true,
    artist: typeof o?.artist === 'string' ? o.artist.trim() : '',
    copyright: typeof o?.copyright === 'string' ? o.copyright.trim() : '',
    watermarkId: typeof o?.watermarkId === 'string' ? o.watermarkId : '',
  };
}

interface UIState {
  mode: Mode;
  folderId: number | null;
  folderPath: string | null;
  view: View;
  addFolderOpen: boolean;
  paletteOpen: boolean;
  shortcutsOpen: boolean;
  // OS fullscreen (F11) — mirrored from the Electron window so Esc can exit.
  fullscreen: boolean;
  // Cull: the contact sheet (G). Per-window, not persisted.
  contactSheet: boolean;
  // Library: show/hide the 300px develop aside. Per-window, not persisted.
  showEditPanel: boolean;

  // ---- Server-persisted settings (settings table, one `uiSettings`
  // subscription). This store is a read mirror: <UISettingsSync/> pushes
  // every server snapshot in via applyUISettings, and writes go through the
  // optimistic helpers in lib/uiSettings.ts — never set these directly.
  // Cull time-gap grouping threshold in minutes (null = off, one flat grid).
  // Effective value for the current folder: the folder's remembered view if
  // it has one, else the global last-used (globalGapMinutes).
  gapMinutes: number | null;
  // Develop dials pinned to the Cull confirm bar / Develop quick dock
  // (Settings → Toolbars). Empty = none, the compact default.
  cullDials: DialKey[];
  quickDials: DialKey[];
  // Creative auto presets (Settings → Auto presets).
  autoPresets: AutoPreset[];
  // Saved develop looks (Presets tab → Save current look).
  userPresets: UserPreset[];
  // Named export overlays (Export dialog → Watermark → Edit…).
  watermarks: Watermark[];
  theme: Theme;
  // Last export destination directory ('' = none yet).
  exportDir: string;
  // Last-used export dialog options; the dialog re-opens with these.
  exportOptions: ExportOptions;
  // Active tab of the develop drawer / library aside (client-only, not
  // persisted server-side — it's an ephemeral view choice).
  developTab: DevelopTab;
  // Pre-render 1:1 full-resolution tiles for opened folders (off by default;
  // large on disk).
  prerenderFullres: boolean;
  // Near-duplicate burst grouping cutoff: max dHash bit distance for two
  // adjacent frames to count as the same burst. Global (no per-folder
  // override); the server re-clusters on change.
  burstHamming: number;
  // Burst time window: max whole-second gap for two adjacent frames to chain
  // into the same burst. Global, server re-clusters on change (like
  // burstHamming).
  burstGapSeconds: number;
  // How thumbnails are framed in the grids (crop 3:2 / fit square / natural
  // justified rows). Default fit — the whole frame is visible.
  thumbFit: ThumbFit;
  // Photo ordering in the grids and filmstrips (default captureAsc). Read
  // the gap threshold through selectGapMinutes — name order has no gaps.
  // Effective value for the current folder, like gapMinutes.
  librarySort: LibrarySort;
  // Last-used global sort/gap — still written on every change; the fallback
  // for folders with no folderViews entry.
  globalLibrarySort: LibrarySort;
  globalGapMinutes: number | null;
  // Per-folder remembered views (filters/sort/gap), keyed by the lowercased
  // folder path. Raw server map — resolveFolderView derives the effective
  // minRating/flagFilter/librarySort/gapMinutes on folder switch and on
  // every snapshot.
  folderViews: Record<string, FolderView>;
  // Folder ordering / time bucketing in the library rail.
  shootSort: ShootSort;
  shootGroup: ShootGroup;
  // Edit-panel group id -> open (absent = open).
  editGroups: Record<string, boolean>;
  // Library-group display aliases / rail collapse state, keyed by the
  // lowercased parent path (absent = no alias / open).
  groupAliases: Record<string, string>;
  railGroups: Record<string, boolean>;
  // Library rail width in px (drag its right edge; RAIL_WIDTH_* bounds).
  railWidth: number;
  // App version whose changelog the Welcome page last showed ('' = never).
  lastSeenVersion: string;
  // True once the first uiSettings snapshot has arrived.
  settingsLoaded: boolean;
  // ---- end server-persisted mirror

  // Bumped by "Collapse previous years" in the rail's sort/group menu; each
  // ManagedParent reacts with its own shoot data (per-window, not persisted —
  // the resulting collapse states are, via railGroups).
  collapsePrevYearsTick: number;

  focusId: number | null;
  anchorId: number | null;
  selection: Set<number>;

  // Library filters — effective values for the current folder, persisted
  // per folder via folderViews (write through updateFolderFilters in
  // lib/uiSettings.ts). Folders with no entry show everything.
  minRating: number;
  flagFilter: FlagFilter;
  // Soft-focus culling filter: show only frames the shoot-relative sharpness
  // threshold calls soft. A transient toggle (not persisted per folder) —
  // setFolder clears it so a leftover can't make a fresh folder look empty.
  softOnly: boolean;
  // Collapse near-duplicate bursts to one frame each: hide burst members
  // except the group's sharpest (or its lead frame until scores exist).
  // Transient like softOnly — setFolder clears it.
  collapseBursts: boolean;
  // Blinks culling filter: show only frames closed-eye detection flagged.
  // Transient like softOnly — setFolder clears it.
  eyesClosedOnly: boolean;
  // Whether the folder-wide "analyze subjects & re-score focus" dialog is open.
  // Opened from a grid cell's subject-focus indicator; owned here so the deep
  // cell needn't thread a callback up to the folder-level controller.
  subjectScanOpen: boolean;
  // Whether the folder-wide "detect closed eyes" dialog is open.
  eyeScanOpen: boolean;

  // Optimistic patches applied on top of the subscribed photo list, so a
  // rating keystroke shows before the server round trip settles. Server
  // truth arrives through subscription patches into the query cache.
  overrides: Map<number, Partial<Photo>>;

  // Row model of the mounted grid, for keyboard navigation. navRowStarts is
  // the ascending flat index where each visual row begins ([] = a 1D surface
  // like the loupe/filmstrip, so ↑/↓ falls back to a flat ±1 step). Whichever
  // grid is mounted publishes this and clears it on unmount. navColCenters (if
  // set) is the per-photo normalized x-center, for x-column ↑/↓ in the
  // variable-width natural layout.
  navRowStarts: number[];
  navColCenters: number[] | null;
  visibleIds: number[];
  // Capture times parallel to visibleIds. Row navigation needs the same
  // time-gap group boundaries the grid draws headers at.
  visibleTakenAt: number[];
  // Current flag + rating per photo (kept fresh by usePhotos) so the P/X keys
  // can toggle instead of blindly setting and the flag/rating undo history
  // can capture prior values.
  photoMeta: Map<number, PhotoMeta>;
  // Burst membership per photo: its group's member ids (kept fresh by
  // usePhotos, whole folder) so Shift+P/X can judge the focused photo's
  // whole burst in one stroke.
  burstMembers: Map<number, number[]>;

  clipboard: Params | null;
  exportOpen: boolean;
  settingsOpen: boolean;
  // The watermark editor dialog. Per-window, not persisted.
  watermarkEditorOpen: boolean;

  // Grid cell target width (zoom slider in the gallery).
  cellSize: number;
  // Loupe zoom: 'fit' or a scale factor (1 = 100%). Deliberately survives
  // photo navigation so a series can be compared at the same crop.
  loupeZoom: 'fit' | number;
  // Bumped to re-center the loupe pan (any return to fit).
  loupeCenterTick: number;
  // The loupe's actual fit scale, mirrored out so keyboard zoom steps can
  // start from it while loupeZoom is 'fit'.
  loupeFitScale: number;
  // Keyboard pan (Shift+arrows): cumulative nudge in viewport fractions.
  // Cumulative (not per-press delta) because React batches back-to-back
  // keydowns into one render — the loupe applies the difference it hasn't
  // consumed yet, so no press is lost.
  loupePan: [number, number];

  setMode: (m: Mode) => void;
  setAddFolderOpen: (open: boolean) => void;
  setPaletteOpen: (open: boolean) => void;
  setShortcutsOpen: (open: boolean) => void;
  setContactSheet: (open: boolean) => void;
  setShowEditPanel: (open: boolean) => void;
  toggleEditPanel: () => void;
  setDevelopTab: (t: DevelopTab) => void;
  applyUISettings: (s: UISettings) => void;
  setFolder: (id: number, path: string) => void;
  setView: (v: View) => void;
  focus: (id: number | null, opts?: { extend?: boolean; toggle?: boolean }) => void;
  selectAll: (ids: number[]) => void;
  clearSelection: () => void;
  applyPatches: (patches: PhotoPatch[]) => void;
  applyLocal: (ids: number[], patch: Partial<Photo>) => void;
  setNavRowModel: (rowStarts: number[], colCenters?: number[] | null) => void;
  setVisibleIds: (ids: number[], takenAt: number[]) => void;
  setPhotoMeta: (meta: Map<number, PhotoMeta>) => void;
  setBurstMembers: (members: Map<number, number[]>) => void;
  setClipboard: (p: Params | null) => void;
  setExportOpen: (open: boolean) => void;
  setSettingsOpen: (open: boolean) => void;
  setWatermarkEditorOpen: (open: boolean) => void;
  toggleSoftOnly: () => void;
  toggleCollapseBursts: () => void;
  toggleEyesClosedOnly: () => void;
  setSubjectScanOpen: (open: boolean) => void;
  setEyeScanOpen: (open: boolean) => void;
  setCellSize: (px: number) => void;
  setLoupeZoom: (z: 'fit' | number) => void;
  setLoupeFitScale: (scale: number) => void;
  nudgeLoupePan: (dx: number, dy: number) => void;
}

export const useUIStore = create<UIState>((set, get) => ({
  mode: 'library',
  folderId: null,
  folderPath: null,
  view: 'grid',
  addFolderOpen: false,
  paletteOpen: false,
  shortcutsOpen: false,
  fullscreen: false,
  contactSheet: false,
  showEditPanel: true,
  gapMinutes: 6,
  cullDials: [],
  quickDials: [],
  autoPresets: [],
  userPresets: [],
  watermarks: [],
  theme: 'dark',
  exportDir: '',
  exportOptions: DEFAULT_EXPORT_OPTIONS,
  developTab: 'develop',
  prerenderFullres: false,
  burstHamming: 18,
  burstGapSeconds: 4,
  thumbFit: 'fit',
  librarySort: 'captureAsc',
  globalLibrarySort: 'captureAsc',
  globalGapMinutes: 6,
  folderViews: {},
  shootSort: 'nameAsc',
  shootGroup: 'none',
  collapsePrevYearsTick: 0,
  editGroups: {},
  groupAliases: {},
  railGroups: {},
  railWidth: RAIL_WIDTH_DEFAULT,
  lastSeenVersion: '',
  settingsLoaded: false,
  focusId: null,
  anchorId: null,
  selection: new Set<number>(),
  minRating: 0,
  flagFilter: 'all',
  softOnly: false,
  collapseBursts: false,
  eyesClosedOnly: false,
  subjectScanOpen: false,
  eyeScanOpen: false,
  overrides: new Map(),
  navRowStarts: [],
  navColCenters: null,
  visibleIds: [],
  visibleTakenAt: [],
  photoMeta: new Map<number, PhotoMeta>(),
  burstMembers: new Map<number, number[]>(),
  clipboard: null,
  exportOpen: false,
  settingsOpen: false,
  watermarkEditorOpen: false,
  cellSize: 220,
  loupeZoom: 'fit',
  loupeCenterTick: 0,
  loupeFitScale: 1,
  loupePan: [0, 0],

  setMode: (m) =>
    set(
      m === 'library'
        ? { mode: m, view: 'grid', contactSheet: false }
        : { mode: m, view: 'loupe' },
    ),
  setAddFolderOpen: (open) => set({ addFolderOpen: open }),
  setPaletteOpen: (open) => set({ paletteOpen: open }),
  setShortcutsOpen: (open) => set({ shortcutsOpen: open }),
  setContactSheet: (open) => set({ contactSheet: open }),
  setShowEditPanel: (open) => set({ showEditPanel: open }),
  toggleEditPanel: () => set((s) => ({ showEditPanel: !s.showEditPanel })),
  setDevelopTab: (t) => set({ developTab: t }),
  // Server snapshot in (wire shapes sanitized to the client types). The
  // effective view is resolved against the folder open *now*, so a late
  // echo of folder A's write never touches a window that moved on to B,
  // and two windows on the same folder stay in sync.
  applyUISettings: (s) => {
    const globals = {
      folderViews: s.folderViews ?? {},
      globalLibrarySort: sanitizeLibrarySort(s.librarySort),
      globalGapMinutes: s.gapMinutes === 0 ? null : s.gapMinutes,
    };
    set({
      theme: s.theme,
      ...globals,
      ...resolveFolderView(globals, get().folderPath),
      cullDials: sanitizeDialKeys(s.cullDials),
      quickDials: sanitizeDialKeys(s.quickDials),
      autoPresets: sanitizeAutoPresets(s.autoPresets),
      // The server re-marshals presets through edit.Params, so params arrive
      // complete; only entries missing identity are dropped.
      userPresets: (s.userPresets ?? []).filter((p) => p.id && p.name && p.params),
      watermarks: sanitizeWatermarks(s.watermarks),
      exportDir: s.exportDir,
      exportOptions: sanitizeExportOptions(s.exportOptions),
      prerenderFullres: s.prerenderFullres,
      burstHamming: s.burstHamming,
      burstGapSeconds: s.burstGapSeconds,
      thumbFit: sanitizeThumbFit(s.thumbFit),
      shootSort: sanitizeShootSort(s.shootSort),
      shootGroup: sanitizeShootGroup(s.shootGroup),
      editGroups: s.editGroups,
      groupAliases: s.groupAliases,
      railGroups: s.railGroups,
      railWidth: clampRailWidth(s.railWidth),
      lastSeenVersion: s.lastSeenVersion ?? '',
      settingsLoaded: true,
    });
  },

  setFolder: (id, path) =>
    set({
      folderId: id,
      folderPath: path,
      // The folder's remembered view (or the fallbacks). Before the first
      // snapshot the map is empty and this yields defaults; the snapshot's
      // applyUISettings re-resolve then applies the saved view.
      ...resolveFolderView(get(), path),
      // Transient cull filters, never carried across folders.
      softOnly: false,
      collapseBursts: false,
      eyesClosedOnly: false,
      // Close a folder-scoped scan dialog left open on the old folder.
      subjectScanOpen: false,
      eyeScanOpen: false,
      view: 'grid',
      focusId: null,
      anchorId: null,
      selection: new Set(),
      overrides: new Map(),
    }),

  setView: (v) => set({ view: v }),

  focus: (id, opts) => {
    if (id == null) {
      set({ focusId: null, anchorId: null, selection: new Set() });
      return;
    }
    const { anchorId, selection, visibleIds } = get();
    if (opts?.extend && anchorId != null) {
      const a = visibleIds.indexOf(anchorId);
      const b = visibleIds.indexOf(id);
      if (a >= 0 && b >= 0) {
        const [lo, hi] = a < b ? [a, b] : [b, a];
        set({ focusId: id, selection: new Set(visibleIds.slice(lo, hi + 1)) });
        return;
      }
    }
    if (opts?.toggle) {
      const next = new Set(selection);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      set({ focusId: id, anchorId: id, selection: next });
      return;
    }
    set({ focusId: id, anchorId: id, selection: new Set([id]) });
  },

  selectAll: (ids) => set({ selection: new Set(ids) }),
  clearSelection: () => set({ selection: new Set() }),

  applyPatches: (patches) =>
    set((s) => {
      const overrides = new Map(s.overrides);
      for (const p of patches) {
        const cur = { ...(overrides.get(p.id) ?? {}) };
        if (p.rating != null) cur.rating = p.rating;
        if (p.flag != null) cur.flag = p.flag;
        if (p.editHash != null) cur.editHash = p.editHash;
        overrides.set(p.id, cur);
      }
      return { overrides };
    }),

  applyLocal: (ids, patch) =>
    set((s) => {
      const overrides = new Map(s.overrides);
      for (const id of ids) overrides.set(id, { ...(overrides.get(id) ?? {}), ...patch });
      return { overrides };
    }),

  setNavRowModel: (rowStarts, colCenters) =>
    set({ navRowStarts: rowStarts, navColCenters: colCenters ?? null }),

  setVisibleIds: (ids, takenAt) =>
    set((s) => {
      // Keep the cursor position when the focused photo drops out of the
      // filtered list (e.g. pressing X under the "Unculled" filter): move
      // focus to the photo now occupying the same index instead of letting
      // views fall back to the start of the list.
      if (s.focusId != null && ids.length > 0 && !ids.includes(s.focusId)) {
        const prevIdx = s.visibleIds.indexOf(s.focusId);
        if (prevIdx >= 0) {
          const nextId = ids[Math.min(prevIdx, ids.length - 1)];
          const selection = new Set([...s.selection].filter((id) => ids.includes(id)));
          if (selection.size === 0) selection.add(nextId);
          return { visibleIds: ids, visibleTakenAt: takenAt, focusId: nextId, anchorId: nextId, selection };
        }
      }
      return { visibleIds: ids, visibleTakenAt: takenAt };
    }),

  setPhotoMeta: (meta) => set({ photoMeta: meta }),
  setBurstMembers: (members) => set({ burstMembers: members }),
  setClipboard: (p) => set({ clipboard: p }),
  setExportOpen: (open) => set({ exportOpen: open }),
  setSettingsOpen: (open) => set({ settingsOpen: open }),
  setWatermarkEditorOpen: (open) => set({ watermarkEditorOpen: open }),
  toggleSoftOnly: () => set((s) => ({ softOnly: !s.softOnly })),
  toggleCollapseBursts: () => set((s) => ({ collapseBursts: !s.collapseBursts })),
  toggleEyesClosedOnly: () => set((s) => ({ eyesClosedOnly: !s.eyesClosedOnly })),
  setSubjectScanOpen: (open) => set({ subjectScanOpen: open }),
  setEyeScanOpen: (open) => set({ eyeScanOpen: open }),
  setCellSize: (px) => set({ cellSize: Math.min(400, Math.max(120, px)) }),
  // Entering fit always recenters — a photo panned away at 1:1 must not come
  // back off-center. Bumping the tick here (not at the call sites) covers the
  // Fit button, Space/Z, and double-click alike; re-selecting an already-active
  // Fit still recenters, since the tick moves even when loupeZoom doesn't.
  setLoupeZoom: (z) =>
    set((s) =>
      z === 'fit'
        ? { loupeZoom: 'fit', loupeCenterTick: s.loupeCenterTick + 1 }
        : { loupeZoom: Math.min(4, Math.max(0.05, z)) },
    ),
  setLoupeFitScale: (scale) => set({ loupeFitScale: scale }),
  nudgeLoupePan: (dx, dy) =>
    set((s) => ({ loupePan: [s.loupePan[0] + dx, s.loupePan[1] + dy] })),
}));

// selectionOrFocus returns the ids an action should apply to.
export function selectionOrFocus(): number[] {
  const { selection, focusId } = useUIStore.getState();
  if (selection.size > 0) return [...selection];
  return focusId != null ? [focusId] : [];
}
