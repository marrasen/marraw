import { useMemo, useState } from 'react';
import {
  ArrowDown,
  ArrowUp,
  Check,
  Copy,
  ExternalLink,
  Folder,
  FolderPen,
  Info,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Search,
  Settings,
  Trash2,
} from 'lucide-react';
import { toast } from 'sonner';
import { openFolder, renameFolderOnDisk, type LibraryRoot } from '@/api/library';
import { useApiClient } from '@/api/client';
import { useMyTasks } from '@/api/tasks';
import { ChipSpinner } from '@/components/ui/task-chip';
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from '@/components/ui/context-menu';
import { cn } from '@/lib/utils';
import {
  baseName,
  groupRoots,
  openRoot,
  rootName,
  samePath,
  saveRoots,
  useLibraryRoots,
  type RootGroup,
} from '@/lib/library';
import { useUIStore } from '@/stores/uiStore';

// Group display aliases are a pure client display preference.
const groupAliasKey = (parent: string) => `marraw:groupAlias:${parent.toLowerCase()}`;
const groupOpenKey = (parent: string) => `marraw:railGroup:${parent.toLowerCase()}`;

function groupName(g: RootGroup) {
  return localStorage.getItem(groupAliasKey(g.parentPath)) || g.parentName;
}

/**
 * The curated library rail (214px): shoot folders the user added, grouped by
 * their parent folder on disk, with organize context menus. Replaces the old
 * whole-filesystem tree — browsing now lives in the Add-folder picker.
 */
export function LibraryRail() {
  const { roots } = useLibraryRoots();
  const [filter, setFilter] = useState('');
  const [renaming, setRenaming] = useState<string | null>(null); // root path or "group:<parent>"
  const [, bump] = useState(0); // group alias/open state lives in localStorage
  const setAddFolderOpen = useUIStore((s) => s.setAddFolderOpen);
  const setSettingsOpen = useUIStore((s) => s.setSettingsOpen);

  const groups = useMemo(() => groupRoots(roots), [roots]);
  const q = filter.trim().toLowerCase();
  const visibleGroups = q
    ? groups
        .map((g) => ({
          ...g,
          roots: g.roots.filter((r) => rootName(r).toLowerCase().includes(q)),
        }))
        .filter((g) => g.roots.length > 0 || groupName(g).toLowerCase().includes(q))
    : groups;

  return (
    <div className="flex h-full flex-col bg-sidebar text-[12.5px]">
      <div className="px-3 pt-3.5 pb-2.5">
        <label className="flex h-[30px] items-center gap-2 rounded-lg border border-border bg-secondary px-2.5 text-xs text-muted-foreground focus-within:border-ring dark:bg-white/5">
          <Search className="size-3.5 shrink-0" />
          <input
            className="w-full bg-transparent text-foreground outline-none placeholder:text-muted-foreground"
            placeholder="Filter folders"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </label>
      </div>

      {roots.length === 0 ? (
        <div className="flex flex-1 items-center justify-center px-5 text-center text-faint">
          No folders yet
        </div>
      ) : (
        <>
          <div className="flex items-center justify-between px-[18px] pb-2">
            <span className="text-[10px] tracking-[.07em] text-faint uppercase">
              In your library
            </span>
            <span className="font-mono text-[10.5px] text-faint">
              {roots.length} folder{roots.length === 1 ? '' : 's'}
            </span>
          </div>
          <div className="flex flex-1 flex-col gap-px overflow-y-auto px-2">
            {visibleGroups.map((g, gi) => (
              <Group
                key={g.parentPath.toLowerCase()}
                group={g}
                groupIndex={gi}
                groups={groups}
                roots={roots}
                renaming={renaming}
                setRenaming={setRenaming}
                forceOpen={q !== ''}
                onChanged={() => bump((n) => n + 1)}
              />
            ))}
          </div>
        </>
      )}

      <div className="p-2">
        <button
          className="flex h-8 w-full items-center justify-center gap-1.5 rounded-lg border border-dashed border-primary/50 bg-primary/10 text-accent-text hover:bg-primary/15"
          onClick={() => setAddFolderOpen(true)}
        >
          <Plus className="size-3.5" />
          Add folder
        </button>
      </div>
      <button
        className="flex items-center gap-2 border-t px-3 py-2.5 text-xs text-muted-foreground hover:text-foreground"
        onClick={() => setSettingsOpen(true)}
      >
        <Settings className="size-3.5" />
        Settings
      </button>
    </div>
  );
}

function Group({
  group,
  groupIndex,
  groups,
  roots,
  renaming,
  setRenaming,
  forceOpen,
  onChanged,
}: {
  group: RootGroup;
  groupIndex: number;
  groups: RootGroup[];
  roots: LibraryRoot[];
  renaming: string | null;
  setRenaming: (v: string | null) => void;
  forceOpen: boolean;
  onChanged: () => void;
}) {
  const client = useApiClient();
  const [open, setOpen] = useState(() => localStorage.getItem(groupOpenKey(group.parentPath)) !== '0');
  const groupRenameId = `group:${group.parentPath.toLowerCase()}`;
  const showRows = open || forceOpen;

  const toggleOpen = () => {
    setOpen((v) => {
      localStorage.setItem(groupOpenKey(group.parentPath), v ? '0' : '1');
      return !v;
    });
  };

  // Move the whole group block up/down in the stored root order.
  const moveGroup = (dir: -1 | 1) => {
    const target = groups[groupIndex + dir];
    if (!target) return;
    const ordered = groups.slice();
    ordered[groupIndex] = target;
    ordered[groupIndex + dir] = group;
    void saveRoots(client, ordered.flatMap((g) => g.roots));
  };

  // Drag a group header onto another group to reorder ("drag also works").
  const onDropGroup = (e: React.DragEvent) => {
    const from = e.dataTransfer.getData('marraw/group');
    if (!from || samePath(from, group.parentPath)) return;
    e.preventDefault();
    e.stopPropagation();
    const moving = groups.find((g) => samePath(g.parentPath, from));
    if (!moving) return;
    const ordered = groups.filter((g) => !samePath(g.parentPath, from));
    const idx = ordered.findIndex((g) => samePath(g.parentPath, group.parentPath));
    ordered.splice(idx, 0, moving);
    void saveRoots(client, ordered.flatMap((g) => g.roots));
  };

  const rescanAll = () => {
    for (const r of group.roots) void openFolder(client, r.path).catch(() => {});
    toast.success(`Rescanning ${group.roots.length} shoot${group.roots.length === 1 ? '' : 's'}`);
  };

  const removeGroup = () => {
    const keep = roots.filter((r) => !group.roots.some((gr) => samePath(gr.path, r.path)));
    void saveRoots(client, keep);
    const { folderPath } = useUIStore.getState();
    if (folderPath && group.roots.some((gr) => samePath(gr.path, folderPath))) {
      useUIStore.setState({ folderId: null, folderPath: null });
    }
  };

  return (
    <div className="flex flex-col gap-px">
      {renaming === groupRenameId ? (
        <RenameEditor
          initial={groupName(group)}
          diskName={group.parentName}
          onSubmit={(alias) => {
            if (alias && alias !== group.parentName) {
              localStorage.setItem(groupAliasKey(group.parentPath), alias);
            } else {
              localStorage.removeItem(groupAliasKey(group.parentPath));
            }
            setRenaming(null);
            onChanged();
          }}
          onCancel={() => setRenaming(null)}
        />
      ) : (
        <ContextMenu>
          <ContextMenuTrigger
            className="flex cursor-default flex-col gap-px rounded-md px-2 pt-2 pb-1 hover:bg-accent"
            onClick={toggleOpen}
            draggable
            onDragStart={(e: React.DragEvent) =>
              e.dataTransfer.setData('marraw/group', group.parentPath)
            }
            onDragOver={(e: React.DragEvent) => {
              if (e.dataTransfer.types.includes('marraw/group')) e.preventDefault();
            }}
            onDrop={onDropGroup}
          >
            <div className="flex items-center gap-[7px]">
              <span
                className={cn(
                  'text-[9px] text-muted-foreground transition-transform',
                  showRows && 'rotate-90',
                )}
              >
                ▶
              </span>
              <span className="flex-1 truncate font-semibold text-foreground">
                {groupName(group)}
              </span>
            </div>
            <span className="truncate pl-4 font-mono text-[10px] text-faint" title={group.parentPath}>
              {group.parentPath}
            </span>
          </ContextMenuTrigger>
          <ContextMenuContent>
            <div className="flex flex-col gap-px px-2.5 pt-1.5 pb-2">
              <span className="truncate font-semibold text-foreground">{groupName(group)}</span>
              <span className="truncate font-mono text-[10px] text-faint">
                {group.parentPath} · {group.roots.length} shoot{group.roots.length === 1 ? '' : 's'}
              </span>
            </div>
            <ContextMenuSeparator />
            <ContextMenuItem onClick={() => setRenaming(groupRenameId)}>
              <Pencil /> <span className="flex-1">Rename group…</span>
            </ContextMenuItem>
            <ContextMenuItem
              hint="Explorer"
              onClick={() => window.marraw?.revealInExplorer(group.parentPath)}
              disabled={!window.marraw}
            >
              <ExternalLink /> <span className="flex-1">Locate on disk</span>
            </ContextMenuItem>
            <ContextMenuItem onClick={rescanAll}>
              <RefreshCw /> <span className="flex-1">Rescan all shoots</span>
            </ContextMenuItem>
            <ContextMenuItem disabled={groupIndex === 0} onClick={() => moveGroup(-1)}>
              <ArrowUp /> <span className="flex-1">Move up</span>
            </ContextMenuItem>
            <ContextMenuItem disabled={groupIndex === groups.length - 1} onClick={() => moveGroup(1)}>
              <ArrowDown /> <span className="flex-1">Move down</span>
            </ContextMenuItem>
            <ContextMenuSeparator />
            <ContextMenuItem variant="destructive" onClick={removeGroup}>
              <Trash2 />
              <div className="flex flex-col gap-px">
                <span>Remove group</span>
                <span className="text-[11px] text-faint">
                  {group.roots.length} shoot{group.roots.length === 1 ? '' : 's'} · files stay on disk
                </span>
              </div>
            </ContextMenuItem>
          </ContextMenuContent>
        </ContextMenu>
      )}
      {showRows &&
        group.roots.map((r) =>
          renaming === r.path.toLowerCase() ? (
            <RenameEditor
              key={r.path}
              initial={rootName(r)}
              diskName={baseName(r.path)}
              onSubmit={(alias) => {
                const next = roots.map((x) =>
                  samePath(x.path, r.path)
                    ? { ...x, alias: alias === baseName(r.path) ? '' : alias }
                    : x,
                );
                void saveRoots(client, next);
                setRenaming(null);
              }}
              onCancel={() => setRenaming(null)}
            />
          ) : (
            <ShootRow
              key={r.path}
              root={r}
              roots={roots}
              onRename={() => setRenaming(r.path.toLowerCase())}
            />
          ),
        )}
    </div>
  );
}

function ShootRow({
  root,
  roots,
  onRename,
}: {
  root: LibraryRoot;
  roots: LibraryRoot[];
  onRename: () => void;
}) {
  const client = useApiClient();
  const folderPath = useUIStore((s) => s.folderPath);
  const active = folderPath != null && samePath(folderPath, root.path);
  const scanning = useIsScanning(root.path);

  const open = () => void openRoot(client, roots, root);

  const openInCull = () => {
    void openRoot(client, roots, root).then(() => useUIStore.getState().setMode('cull'));
  };

  const toggleSubfolders = () => {
    const next = roots.map((r) =>
      samePath(r.path, root.path) ? { ...r, includeSubfolders: !r.includeSubfolders } : r,
    );
    void saveRoots(client, next).then(() => {
      // Re-scan so the new recursion depth takes effect immediately.
      void openRoot(client, next, { ...root, includeSubfolders: !root.includeSubfolders });
    });
  };

  const remove = () => {
    void saveRoots(client, roots.filter((r) => !samePath(r.path, root.path)));
    if (active) useUIStore.setState({ folderId: null, folderPath: null });
  };

  const renameOnDisk = async () => {
    const name = window.prompt('New folder name on disk:', baseName(root.path));
    if (!name || name === baseName(root.path)) return;
    try {
      const res = await renameFolderOnDisk(client, root.path, name);
      toast.success(`Renamed on disk → ${res.path}`);
      if (active) useUIStore.setState({ folderPath: res.path });
    } catch (err) {
      toast.error(`Rename failed: ${(err as Error).message}`);
    }
  };

  return (
    <ContextMenu>
      <ContextMenuTrigger
        className={cn(
          'flex h-[30px] shrink-0 cursor-pointer items-center gap-[7px] rounded-[7px] pr-2 pl-6',
          active ? 'bg-sidebar-accent font-semibold text-foreground' : 'hover:bg-accent',
        )}
        onClick={open}
        onKeyDown={(e) => {
          if (e.key === 'F2') {
            e.preventDefault();
            onRename();
          }
        }}
        title={root.path}
      >
        <Folder
          className={cn('size-[13px] shrink-0', active ? 'text-accent-text' : 'text-muted-foreground')}
          strokeWidth={1.5}
        />
        <span className="flex-1 truncate">{rootName(root)}</span>
        {scanning ? (
          <ChipSpinner className="size-3" />
        ) : root.photoCount > 0 ? (
          <span className="font-mono text-[10.5px] font-normal text-faint">
            {root.photoCount.toLocaleString()}
          </span>
        ) : null}
      </ContextMenuTrigger>
      <ContextMenuContent>
        <div className="flex items-start gap-2 px-2.5 pt-1.5 pb-2">
          <Folder className="mt-0.5 size-3.5 shrink-0 text-accent-text" strokeWidth={1.5} />
          <div className="flex min-w-0 flex-col gap-px">
            <span className="truncate font-semibold text-foreground">{rootName(root)}</span>
            <span className="font-mono text-[10px] text-faint">
              {root.photoCount > 0 ? `${root.photoCount.toLocaleString()} RAW` : root.path}
            </span>
          </div>
        </div>
        <ContextMenuSeparator />
        <ContextMenuItem hint="Enter" onClick={openInCull}>
          <Play /> <span className="flex-1 text-foreground">Open in Cull</span>
        </ContextMenuItem>
        <ContextMenuItem hint="F2" onClick={onRename}>
          <Pencil /> <span className="flex-1">Rename…</span>
        </ContextMenuItem>
        <ContextMenuItem onClick={renameOnDisk}>
          <FolderPen /> <span className="flex-1">Rename on disk…</span>
        </ContextMenuItem>
        <ContextMenuItem
          hint="Explorer"
          onClick={() => window.marraw?.revealInExplorer(root.path)}
          disabled={!window.marraw}
        >
          <ExternalLink /> <span className="flex-1 text-foreground">Locate on disk</span>
        </ContextMenuItem>
        <ContextMenuItem
          onClick={() => {
            void navigator.clipboard.writeText(root.path);
            toast.success('Path copied');
          }}
        >
          <Copy /> <span className="flex-1">Copy path</span>
        </ContextMenuItem>
        <ContextMenuItem onClick={toggleSubfolders}>
          <Check className={cn(!root.includeSubfolders && 'invisible', 'text-primary!')} />
          <span className="flex-1">Include subfolders</span>
        </ContextMenuItem>
        <ContextMenuItem onClick={open}>
          <RefreshCw /> <span className="flex-1">Rescan for new photos</span>
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem variant="destructive" onClick={remove}>
          <Trash2 />
          <div className="flex flex-col gap-px">
            <span>Remove from library</span>
            <span className="text-[11px] text-faint">Files stay on disk</span>
          </div>
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

// Inline alias rename per the ORGANIZE plate: accent-bordered input plus the
// reassurance that the disk folder name is untouched.
function RenameEditor({
  initial,
  diskName,
  onSubmit,
  onCancel,
}: {
  initial: string;
  diskName: string;
  onSubmit: (value: string) => void;
  onCancel: () => void;
}) {
  const [value, setValue] = useState(initial);
  return (
    <div className="flex flex-col gap-2 rounded-lg border bg-sidebar p-2">
      <div className="flex h-[30px] items-center gap-2 rounded-lg border border-primary bg-primary/10 px-2">
        <Folder className="size-3.5 shrink-0 text-muted-foreground" strokeWidth={1.5} />
        <input
          autoFocus
          className="w-full bg-transparent text-[13px] text-foreground outline-none"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') onSubmit(value.trim() || diskName);
            if (e.key === 'Escape') onCancel();
            e.stopPropagation();
          }}
          onBlur={() => onSubmit(value.trim() || diskName)}
        />
      </div>
      <div className="flex items-start gap-1.5 text-[11px] leading-normal text-muted-foreground">
        <Info className="mt-px size-3 shrink-0" />
        <span>
          Display name only. Folder on disk stays{' '}
          <span className="font-mono text-[10.5px] text-secondary-foreground">{diskName}</span>.
        </span>
      </div>
      <div className="border-t pt-2 text-[11px] leading-normal text-faint">
        Need to rename the folder itself? Use{' '}
        <span className="text-secondary-foreground">Rename on disk…</span> in the menu.
      </div>
    </div>
  );
}

// useIsScanning: a background scan/prerender task whose meta.folder matches
// this root is running.
function useIsScanning(path: string) {
  const tasks = useMyTasks();
  return tasks.some((t) => {
    if (t.status !== 'running' && t.status !== 'created') return false;
    const meta = t.meta as { folder?: string } | undefined;
    return meta?.folder != null && samePath(meta.folder, path);
  });
}
