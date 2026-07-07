import { create } from 'zustand';
import type { Photo, PhotoPatch } from '@/api/library';
import type { Params } from '@/api/edits';

export type View = 'grid' | 'loupe';
export type FlagFilter = 'all' | 'pick' | 'not-excluded' | 'exclude';
// Top-level app mode (the top-bar segmented control). Library is the
// structured window; Cull and Develop are cinema canvases. Export lives in
// a dialog, not a mode.
export type Mode = 'library' | 'cull' | 'develop';

interface UIState {
  mode: Mode;
  folderId: number | null;
  folderPath: string | null;
  view: View;
  addFolderOpen: boolean;
  paletteOpen: boolean;
  // Cull: the contact sheet (G) and the time-gap grouping threshold in
  // minutes (null = off, one flat grid). Persisted across sessions.
  contactSheet: boolean;
  gapMinutes: number | null;

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

  clipboard: Params | null;
  exportOpen: boolean;
  settingsOpen: boolean;

  // Grid cell target width (zoom slider in the gallery).
  cellSize: number;
  // Loupe zoom: 'fit' or a scale factor (1 = 100%). Deliberately survives
  // photo navigation so a series can be compared at the same crop.
  loupeZoom: 'fit' | number;

  setMode: (m: Mode) => void;
  setAddFolderOpen: (open: boolean) => void;
  setPaletteOpen: (open: boolean) => void;
  setContactSheet: (open: boolean) => void;
  setGapMinutes: (min: number | null) => void;
  setFolder: (id: number, path: string) => void;
  setView: (v: View) => void;
  focus: (id: number | null, opts?: { extend?: boolean; toggle?: boolean }) => void;
  selectAll: (ids: number[]) => void;
  clearSelection: () => void;
  setFilters: (f: { minRating?: number; flagFilter?: FlagFilter }) => void;
  applyPatches: (patches: PhotoPatch[]) => void;
  applyLocal: (ids: number[], patch: Partial<Photo>) => void;
  setGrid: (cols: number) => void;
  setVisibleIds: (ids: number[]) => void;
  setClipboard: (p: Params | null) => void;
  setExportOpen: (open: boolean) => void;
  setSettingsOpen: (open: boolean) => void;
  setCellSize: (px: number) => void;
  setLoupeZoom: (z: 'fit' | number) => void;
}

export const useUIStore = create<UIState>((set, get) => ({
  mode: 'library',
  folderId: null,
  folderPath: null,
  view: 'grid',
  addFolderOpen: false,
  paletteOpen: false,
  contactSheet: false,
  gapMinutes: (() => {
    const raw = localStorage.getItem('marraw:gapMinutes');
    if (raw === 'off') return null;
    const n = Number(raw);
    return Number.isFinite(n) && n > 0 ? n : 6;
  })(),
  focusId: null,
  anchorId: null,
  selection: new Set<number>(),
  minRating: 0,
  flagFilter: 'all',
  overrides: new Map(),
  gridCols: 4,
  visibleIds: [],
  clipboard: null,
  exportOpen: false,
  settingsOpen: false,
  cellSize: 220,
  loupeZoom: 'fit',

  setMode: (m) =>
    set(
      m === 'library'
        ? { mode: m, view: 'grid', contactSheet: false }
        : { mode: m, view: 'loupe' },
    ),
  setAddFolderOpen: (open) => set({ addFolderOpen: open }),
  setPaletteOpen: (open) => set({ paletteOpen: open }),
  setContactSheet: (open) => set({ contactSheet: open }),
  setGapMinutes: (min) => {
    localStorage.setItem('marraw:gapMinutes', min == null ? 'off' : String(min));
    set({ gapMinutes: min });
  },

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

  setVisibleIds: (ids) =>
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
          return { visibleIds: ids, focusId: nextId, anchorId: nextId, selection };
        }
      }
      return { visibleIds: ids };
    }),

  setClipboard: (p) => set({ clipboard: p }),
  setExportOpen: (open) => set({ exportOpen: open }),
  setSettingsOpen: (open) => set({ settingsOpen: open }),
  setCellSize: (px) => set({ cellSize: Math.min(400, Math.max(120, px)) }),
  setLoupeZoom: (z) =>
    set({ loupeZoom: z === 'fit' ? z : Math.min(4, Math.max(0.05, z)) }),
}));

// selectionOrFocus returns the ids an action should apply to.
export function selectionOrFocus(): number[] {
  const { selection, focusId } = useUIStore.getState();
  if (selection.size > 0) return [...selection];
  return focusId != null ? [focusId] : [];
}
