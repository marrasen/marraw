import { create } from 'zustand';
import type { FlagType, Photo, PhotoPatch } from '@/api/library';
import type { ExportOptions, UISettings } from '@/api/settings';
import type { Params } from '@/api/edits';
import { sanitizeDialKeys, type DialKey } from '@/lib/dials';
import { sanitizeAutoPresets, type AutoPreset } from '@/lib/autoPresets';

export type Theme = 'dark' | 'light' | 'system';

export type View = 'grid' | 'loupe';
export type FlagFilter = 'all' | 'pick' | 'not-excluded' | 'exclude';
// Top-level app mode (the top-bar segmented control). Library is the
// structured window; Cull and Develop are cinema canvases. Export lives in
// a dialog, not a mode.
export type Mode = 'library' | 'cull' | 'develop';
// Tabs of the develop drawer / library edit aside.
export type DevelopTab = 'develop' | 'presets' | 'info';

export const DEFAULT_EXPORT_OPTIONS: ExportOptions = {
  format: 'jpeg',
  jpegQuality: 90,
  resizeMode: 'full',
  edgePx: 2160,
  colorSpace: 'srgb',
  sharpenTarget: 'off',
  sharpenAmount: 'standard',
};

// Library rail width bounds — mirror the server's SetRailWidth validation.
export const RAIL_WIDTH_DEFAULT = 214;
export const RAIL_WIDTH_MIN = 180;
export const RAIL_WIDTH_MAX = 440;

export function clampRailWidth(px: number): number {
  if (!Number.isFinite(px)) return RAIL_WIDTH_DEFAULT;
  return Math.min(RAIL_WIDTH_MAX, Math.max(RAIL_WIDTH_MIN, Math.round(px)));
}

// Mirrors the server's normalizeExportOptions: missing or invalid fields
// from older blobs fall back to the dialog defaults.
function sanitizeExportOptions(o: Partial<ExportOptions> | undefined): ExportOptions {
  return {
    format: o?.format === 'tiff16' ? 'tiff16' : 'jpeg',
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
  gapMinutes: number | null;
  // Develop dials pinned to the Cull confirm bar / Develop quick dock
  // (Settings → Toolbars). Empty = none, the compact default.
  cullDials: DialKey[];
  quickDials: DialKey[];
  // Creative auto presets (Settings → Auto presets).
  autoPresets: AutoPreset[];
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
  // Edit-panel group id -> open (absent = open).
  editGroups: Record<string, boolean>;
  // Library-group display aliases / rail collapse state, keyed by the
  // lowercased parent path (absent = no alias / open).
  groupAliases: Record<string, string>;
  railGroups: Record<string, boolean>;
  // Library rail width in px (drag its right edge; RAIL_WIDTH_* bounds).
  railWidth: number;
  // True once the first uiSettings snapshot has arrived.
  settingsLoaded: boolean;
  // ---- end server-persisted mirror

  focusId: number | null;
  anchorId: number | null;
  selection: Set<number>;

  minRating: number;
  flagFilter: FlagFilter;

  // Optimistic patches applied on top of the subscribed photo list, so a
  // rating keystroke shows before the server round trip settles. Server
  // truth arrives through subscription patches into the query cache.
  overrides: Map<number, Partial<Photo>>;

  // Grid geometry + currently visible list, for keyboard navigation.
  gridCols: number;
  visibleIds: number[];
  // Capture times parallel to visibleIds. Row navigation needs the same
  // time-gap group boundaries the grid draws headers at.
  visibleTakenAt: number[];
  // Current flag per photo (kept fresh by usePhotos) so the P/X keys can
  // toggle instead of blindly setting.
  photoFlags: Map<number, FlagType>;

  clipboard: Params | null;
  exportOpen: boolean;
  settingsOpen: boolean;

  // Grid cell target width (zoom slider in the gallery).
  cellSize: number;
  // Loupe zoom: 'fit' or a scale factor (1 = 100%). Deliberately survives
  // photo navigation so a series can be compared at the same crop.
  loupeZoom: 'fit' | number;
  // Bumped to re-center the loupe pan (clicking Fit while already at fit).
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
  setFilters: (f: { minRating?: number; flagFilter?: FlagFilter }) => void;
  applyPatches: (patches: PhotoPatch[]) => void;
  applyLocal: (ids: number[], patch: Partial<Photo>) => void;
  setGrid: (cols: number) => void;
  setVisibleIds: (ids: number[], takenAt: number[]) => void;
  setPhotoFlags: (flags: Map<number, FlagType>) => void;
  setClipboard: (p: Params | null) => void;
  setExportOpen: (open: boolean) => void;
  setSettingsOpen: (open: boolean) => void;
  setCellSize: (px: number) => void;
  setLoupeZoom: (z: 'fit' | number) => void;
  centerLoupe: () => void;
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
  theme: 'dark',
  exportDir: '',
  exportOptions: DEFAULT_EXPORT_OPTIONS,
  developTab: 'develop',
  prerenderFullres: false,
  editGroups: {},
  groupAliases: {},
  railGroups: {},
  railWidth: RAIL_WIDTH_DEFAULT,
  settingsLoaded: false,
  focusId: null,
  anchorId: null,
  selection: new Set<number>(),
  minRating: 0,
  flagFilter: 'all',
  overrides: new Map(),
  gridCols: 4,
  visibleIds: [],
  visibleTakenAt: [],
  photoFlags: new Map<number, FlagType>(),
  clipboard: null,
  exportOpen: false,
  settingsOpen: false,
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
  // Server snapshot in (wire shapes sanitized to the client types).
  applyUISettings: (s) =>
    set({
      theme: s.theme,
      gapMinutes: s.gapMinutes === 0 ? null : s.gapMinutes,
      cullDials: sanitizeDialKeys(s.cullDials),
      quickDials: sanitizeDialKeys(s.quickDials),
      autoPresets: sanitizeAutoPresets(s.autoPresets),
      exportDir: s.exportDir,
      exportOptions: sanitizeExportOptions(s.exportOptions),
      prerenderFullres: s.prerenderFullres,
      editGroups: s.editGroups,
      groupAliases: s.groupAliases,
      railGroups: s.railGroups,
      railWidth: clampRailWidth(s.railWidth),
      settingsLoaded: true,
    }),

  setFolder: (id, path) =>
    set({
      folderId: id,
      folderPath: path,
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

  setFilters: (f) => set(f),

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

  setGrid: (cols) => set({ gridCols: cols }),

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

  setPhotoFlags: (flags) => set({ photoFlags: flags }),
  setClipboard: (p) => set({ clipboard: p }),
  setExportOpen: (open) => set({ exportOpen: open }),
  setSettingsOpen: (open) => set({ settingsOpen: open }),
  setCellSize: (px) => set({ cellSize: Math.min(400, Math.max(120, px)) }),
  setLoupeZoom: (z) =>
    set({ loupeZoom: z === 'fit' ? z : Math.min(4, Math.max(0.05, z)) }),
  centerLoupe: () => set((s) => ({ loupeCenterTick: s.loupeCenterTick + 1 })),
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
