import { toast } from 'sonner';
import {
  openFolder,
  setLibraryRoots,
  useGetLibraryRoots,
  useGetRootStatus,
  type LibraryRoot,
  type Shoot,
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

/**
 * One block in the rail: either a managed library folder (children discovered
 * from disk) or a derived group of hand-added shoot roots that share a parent
 * directory.
 */
export type RailBlock =
  | { kind: 'parent'; root: LibraryRoot }
  | { kind: 'group'; group: RootGroup };

/**
 * Orders the rail's blocks by each block's first appearance in the stored root
 * order — the same rule groupRoots already uses — so reorder, Move up, and Move
 * down keep working by rewriting that one list.
 */
export function railBlocks(roots: LibraryRoot[]): RailBlock[] {
  const groups = groupRoots(roots.filter((r) => !r.isParent));
  const groupAt = new Map<string, RootGroup>();
  for (const g of groups) groupAt.set(g.roots[0].path.toLowerCase(), g);

  const blocks: RailBlock[] = [];
  for (const r of roots) {
    if (r.isParent) {
      blocks.push({ kind: 'parent', root: r });
      continue;
    }
    const g = groupAt.get(r.path.toLowerCase());
    if (g) blocks.push({ kind: 'group', group: g });
  }
  return blocks;
}

/**
 * Settings key for a managed parent's collapse state and display alias.
 *
 * A derived group is keyed by the directory *containing* its roots, while a
 * parent's own path is the natural key for it — for `D:\Photos` those are the
 * same string, and the two blocks would share state. The prefix separates them.
 */
export const parentKey = (path: string) => `parent:${path}`;

export function useLibraryRoots(): { roots: LibraryRoot[]; isLoading: boolean } {
  const { data, isLoading } = useGetLibraryRoots();
  return { roots: data ?? [], isLoading };
}

/**
 * Which roots' storage is reachable. Unknown paths read as online: the first
 * snapshot arrives a moment after mount, and flashing "Offline" on a perfectly
 * healthy library would be worse than a beat of silence.
 */
export function useRootOnline(): (path: string) => boolean {
  const { data } = useGetRootStatus();
  return (path: string) =>
    data?.find((s) => samePath(s.path, path))?.online ?? true;
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

/**
 * Opens a discovered shoot. Unlike openRoot there is no photo-count write-back:
 * a shoot has no stored record to write to, and the server re-lists the parent
 * with the fresh count once the scan lands.
 */
export async function openShoot(client: ApiClient, shoot: Shoot) {
  try {
    const info = await openFolder(client, shoot.path);
    useUIStore.getState().setFolder(info.folderId, shoot.path);
  } catch (err) {
    toast.error(`Cannot open folder: ${(err as Error).message}`);
  }
}
