import { create } from 'zustand';
import type { Photo, PhotoPatch } from '@/api/library';
import type { Params } from '@/api/edits';

export type View = 'grid' | 'loupe';
export type FlagFilter = 'all' | 'pick' | 'not-excluded' | 'exclude';

interface UIState {
  folderId: number | null;
  folderPath: string | null;
  view: View;

  focusId: number | null;
  anchorId: number | null;
  selection: Set<number>;

  minRating: number;
  flagFilter: FlagFilter;

  // Server-truth patches applied on top of the subscribed photo list, so a
  // rating keystroke is O(changed photos), not a full list refresh.
  overrides: Map<number, Partial<Photo>>;

  // Grid geometry + currently visible list, for keyboard navigation.
  gridCols: number;
  visibleIds: number[];

  clipboard: Params | null;
  // Unsaved preview state of the focused photo (slider being dragged).
  previewHash: string | null;
  exportOpen: boolean;

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
  setPreviewHash: (h: string | null) => void;
  setExportOpen: (open: boolean) => void;
}

export const useUIStore = create<UIState>((set, get) => ({
  folderId: null,
  folderPath: null,
  view: 'grid',
  focusId: null,
  anchorId: null,
  selection: new Set<number>(),
  minRating: 0,
  flagFilter: 'all',
  overrides: new Map(),
  gridCols: 4,
  visibleIds: [],
  clipboard: null,
  previewHash: null,
  exportOpen: false,

  setFolder: (id, path) =>
    set({
      folderId: id,
      folderPath: path,
      view: 'grid',
      focusId: null,
      anchorId: null,
      selection: new Set(),
      overrides: new Map(),
      previewHash: null,
    }),

  setView: (v) => set({ view: v, previewHash: null }),

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
        set({ focusId: id, selection: new Set(visibleIds.slice(lo, hi + 1)), previewHash: null });
        return;
      }
    }
    if (opts?.toggle) {
      const next = new Set(selection);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      set({ focusId: id, anchorId: id, selection: next, previewHash: null });
      return;
    }
    set({ focusId: id, anchorId: id, selection: new Set([id]), previewHash: null });
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
  setVisibleIds: (ids) => set({ visibleIds: ids }),
  setClipboard: (p) => set({ clipboard: p }),
  setPreviewHash: (h) => set({ previewHash: h }),
  setExportOpen: (open) => set({ exportOpen: open }),
}));

// selectionOrFocus returns the ids an action should apply to.
export function selectionOrFocus(): number[] {
  const { selection, focusId } = useUIStore.getState();
  if (selection.size > 0) return [...selection];
  return focusId != null ? [focusId] : [];
}
