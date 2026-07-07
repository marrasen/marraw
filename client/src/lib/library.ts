import { toast } from 'sonner';
import {
  openFolder,
  setLibraryRoots,
  useGetLibraryRoots,
  type LibraryRoot,
} from '@/api/library';
import type { ApiClient } from '@/api/client';
import { useUIStore } from '@/stores/uiStore';

// Last path segment for display; drive roots like "D:\" keep their letter.
export function baseName(path: string) {
  const trimmed = path.replace(/[\\/]+$/, '');
  const i = Math.max(trimmed.lastIndexOf('\\'), trimmed.lastIndexOf('/'));
  return i >= 0 && i < trimmed.length - 1 ? trimmed.slice(i + 1) : trimmed || path;
}

export function parentPath(path: string) {
  const trimmed = path.replace(/[\\/]+$/, '');
  const i = Math.max(trimmed.lastIndexOf('\\'), trimmed.lastIndexOf('/'));
  return i > 0 ? trimmed.slice(0, i) : trimmed;
}

/** Display name of a root: the alias if renamed, else the folder name. */
export function rootName(root: LibraryRoot) {
  return root.alias || baseName(root.path);
}

export const samePath = (a: string, b: string) => a.toLowerCase() === b.toLowerCase();

// Groups are the parent folder on disk (NOT auto date-detection); order
// follows the stored root order, grouped by first appearance.
export interface RootGroup {
  parentPath: string;
  parentName: string;
  roots: LibraryRoot[];
}

export function groupRoots(roots: LibraryRoot[]): RootGroup[] {
  const groups: RootGroup[] = [];
  const byParent = new Map<string, RootGroup>();
  for (const r of roots) {
    const parent = parentPath(r.path);
    const key = parent.toLowerCase();
    let g = byParent.get(key);
    if (!g) {
      g = { parentPath: parent, parentName: baseName(parent), roots: [] };
      byParent.set(key, g);
      groups.push(g);
    }
    g.roots.push(r);
  }
  return groups;
}

export function useLibraryRoots(): { roots: LibraryRoot[]; isLoading: boolean } {
  const { data, isLoading } = useGetLibraryRoots();
  return { roots: data ?? [], isLoading };
}

export function saveRoots(client: ApiClient, roots: LibraryRoot[]) {
  return setLibraryRoots(client, roots).catch((err) =>
    toast.error(`Could not save library: ${(err as Error).message}`),
  );
}

/**
 * Opens a library root (or re-scans it): syncs the folder, focuses it in the
 * store, and refreshes the root's remembered photo count when it drifted.
 */
export async function openRoot(client: ApiClient, roots: LibraryRoot[], root: LibraryRoot) {
  try {
    const info = await openFolder(client, root.path);
    useUIStore.getState().setFolder(info.folderId, root.path);
    if (info.photoCount !== root.photoCount) {
      const next = roots.map((r) =>
        samePath(r.path, root.path) ? { ...r, photoCount: info.photoCount } : r,
      );
      void saveRoots(client, next);
    }
  } catch (err) {
    toast.error(`Cannot open folder: ${(err as Error).message}`);
  }
}
