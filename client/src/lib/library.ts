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
import { updateGroupAlias, updateRailGroupOpen } from '@/lib/uiSettings';
import { useUIStore } from '@/stores/uiStore';

// Last path segment for display; drive roots like "D:\" keep their letter.
export function baseName(path: string) {
  const trimmed = path.replace(/[\\/]+$/, '');
  const i = Math.max(trimmed.lastIndexOf('\\'), trimmed.lastIndexOf('/'));
  return i >= 0 && i < trimmed.length - 1 ? trimmed.slice(i + 1) : trimmed || path;
}

/**
 * Joins a folder path with a file name (which may itself carry a relative
 * subpath, as recursive scans store it) in the folder's own separator style —
 * the daemon may run on a machine whose paths look nothing like this one's.
 */
export function joinPath(folder: string, name: string) {
  const sep = folder.includes('\\') ? '\\' : '/';
  return `${folder.replace(/[\\/]+$/, '')}${sep}${name.replace(/^[\\/]+/, '')}`;
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

/** Whether `path` is `ancestor` itself or lies beneath it. */
export function isUnder(path: string, ancestor: string) {
  const p = path.replace(/[\\/]+$/, '').toLowerCase();
  const a = ancestor.replace(/[\\/]+$/, '').toLowerCase();
  return p === a || p.startsWith(`${a}\\`) || p.startsWith(`${a}/`);
}

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

/** Whether a root is there, on a drive not connected, or moved or deleted. */
export type RootPresence = 'online' | 'offline' | 'missing';

/**
 * Where each root stands. Unknown paths read as online: the first snapshot
 * arrives a moment after mount, and flashing "Offline" on a perfectly
 * healthy library would be worse than a beat of silence.
 */
export function useRootPresence(): (path: string) => RootPresence {
  const { data } = useGetRootStatus();
  return (path: string) => {
    const st = data?.find((s) => samePath(s.path, path));
    if (!st || st.online) return 'online';
    return st.missing ? 'missing' : 'offline';
  };
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

/** The key k, a lowercased path, after the folder at `from` moved to `to`. */
function movedKey(k: string, from: string, to: string): string {
  const f = from.replace(/[\\/]+$/, '').toLowerCase();
  if (k !== f && !k.startsWith(`${f}\\`) && !k.startsWith(`${f}/`)) return k;
  return (to.replace(/[\\/]+$/, '') + k.slice(f.length)).toLowerCase();
}

/**
 * Points the library's folder `from` at the folder `to`, where it was moved
 * (the rail's "Find folder…"). A folder the library holds there already —
 * a root, or a folder inside a library folder — keeps its place and the old
 * entry goes. Otherwise the entry takes the new path; how a library
 * folder's folders show, which are hidden, its name in the rail and its
 * closed headers are all kept by path, so they move with it. A shoot opens
 * where it is now, its ratings and edits read from its marraw.json.
 */
export async function relocateRoot(client: ApiClient, roots: LibraryRoot[], from: LibraryRoot, to: string) {
  if (samePath(from.path, to)) return;
  // Saved, or told why not: saveRoots reports its own failure and resolves
  // either way, so the rest must not go on as if it had worked.
  const save = async (next: LibraryRoot[]) => {
    try {
      await setLibraryRoots(client, next);
      return true;
    } catch (err) {
      toast.error(`Could not save library: ${(err as Error).message}`);
      return false;
    }
  };
  const lowered = to.toLowerCase();
  const holder = from.isParent ? undefined : roots.find((r) => r.isParent && isUnder(to, r.path) && !samePath(r.path, to));
  if (roots.some((r) => samePath(r.path, to)) || holder) {
    // The library folder that holds it shows it again if it was hidden
    // there. Inside a folder hidden there, showing it would show that
    // whole folder, so the entry stays as it is.
    const hidden = holder?.excludedChildren ?? [];
    const inHidden = hidden.find((k) => k !== lowered && isUnder(lowered, k));
    if (inHidden) {
      toast.error(`${baseName(to)} is in ${baseName(inHidden)}, which is hidden in the library`, {
        description: `Move it out of ${baseName(inHidden)} and find it again, or remove ${rootName(from)} from the library.`,
      });
      return;
    }
    const next = roots
      .filter((r) => !samePath(r.path, from.path))
      .map((r) =>
        r === holder && hidden.includes(lowered)
          ? { ...r, excludedChildren: hidden.filter((k) => k !== lowered) }
          : r,
      );
    if (!(await save(next))) return;
    toast.success(`${baseName(to)} is in the library already`, { description: 'The old entry is gone.' });
    if (from.isParent) return;
    try {
      const info = await openFolder(client, to);
      useUIStore.getState().setFolder(info.folderId, to);
    } catch (err) {
      toast.error(`Cannot open folder: ${(err as Error).message}`);
    }
    return;
  }
  const moved: LibraryRoot = {
    ...from,
    path: to,
    folderModes: from.folderModes
      ? Object.fromEntries(Object.entries(from.folderModes).map(([k, m]) => [movedKey(k, from.path, to), m]))
      : from.folderModes,
    excludedChildren: from.excludedChildren?.map((k) => movedKey(k, from.path, to)),
  };
  // A library folder now shows the shoots right inside its new place, so
  // one added there by hand would show twice, as adding it does.
  const absorbed = (r: LibraryRoot) => from.isParent && !r.isParent && samePath(parentPath(r.path), to);
  const next = roots.filter((r) => !absorbed(r)).map((r) => (samePath(r.path, from.path) ? moved : r));
  if (!(await save(next))) return;
  toast.success(`Found ${rootName(from)} in ${parentPath(to)}`);
  if (!from.isParent) {
    await openRoot(client, next, moved);
    return;
  }
  // The rail's state for a library folder is keyed "parent:<path>", its
  // headers "parent:<path>|…".
  const oldKey = parentKey(from.path).toLowerCase();
  const newKey = parentKey(to).toLowerCase();
  const { railGroups, groupAliases } = useUIStore.getState();
  for (const [k, open] of Object.entries(railGroups)) {
    if (open || (k !== oldKey && !k.startsWith(`${oldKey}|`))) continue;
    updateRailGroupOpen(client, k, true);
    updateRailGroupOpen(client, newKey + k.slice(oldKey.length), false);
  }
  const alias = groupAliases[oldKey];
  if (alias) {
    updateGroupAlias(client, oldKey, '');
    updateGroupAlias(client, newKey, alias);
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
