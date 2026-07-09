import { useMemo, useState } from 'react';
import {
  AppWindow,
  ArrowDown,
  ArrowUp,
  Check,
  Copy,
  ExternalLink,
  EyeOff,
  Folder,
  FolderPen,
  FolderTree,
  Info,
  Maximize2,
  Pencil,
  Play,
  PlugZap,
  Plus,
  RefreshCw,
  Search,
  Settings,
  Trash2,
} from 'lucide-react';
import { toast } from 'sonner';
import {
  openFolder,
  renameFolderOnDisk,
  renderFolderFullres,
  useListShoots,
  type LibraryRoot,
  type Shoot,
} from '@/api/library';
import { useApiClient } from '@/api/client';
import { useSharedTasks } from '@/api/tasks';
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
  openRoot,
  openShoot,
  parentKey,
  railBlocks,
  rootName,
  samePath,
  saveRoots,
  useLibraryRoots,
  useRootOnline,
  type RailBlock,
  type RootGroup,
} from '@/lib/library';
import { updateGroupAlias, updateRailGroupOpen } from '@/lib/uiSettings';
import { useUIStore } from '@/stores/uiStore';

// Display aliases are a pure display preference, persisted server-side
// (uiSettings.groupAliases), keyed by the lowercased settings key.
function aliasFor(key: string, fallback: string, aliases: Record<string, string>) {
  return aliases[key.toLowerCase()] || fallback;
}

/** The roots a block owns, for rewriting the stored order. */
function blockRoots(b: RailBlock): LibraryRoot[] {
  return b.kind === 'parent' ? [b.root] : b.group.roots;
}

function blockId(b: RailBlock): string {
  return b.kind === 'parent'
    ? `p:${b.root.path.toLowerCase()}`
    : `g:${b.group.parentPath.toLowerCase()}`;
}

/** Whether a folder's storage is reachable, threaded down from one subscription. */
type OnlineFn = (path: string) => boolean;

/**
 * The folder's storage is disconnected — an unplugged external drive, or an
 * unreachable share. Nothing is lost; the daemon polls and the folder returns.
 */
function OfflineBadge({ className }: { className?: string }) {
  return (
    <span
      data-testid="offline-badge"
      className={cn(
        'flex shrink-0 items-center gap-1 rounded-full bg-secondary px-1.5 py-px font-mono text-[9.5px] tracking-[.04em] text-muted-foreground uppercase dark:bg-white/8',
        className,
      )}
      title="This folder's drive is not connected. It will come back on its own when you reconnect it."
    >
      <PlugZap className="size-[9px]" strokeWidth={2} />
      Offline
    </span>
  );
}

/**
 * The curated library rail (resizable, 214px default): the shoot folders and
 * library folders the user added, with organize context menus. Hand-added
 * shoots are grouped by their parent directory on disk; a library folder is a
 * stored parent whose child shoots are discovered from disk.
 */
export function LibraryRail() {
  const { roots } = useLibraryRoots();
  const [filter, setFilter] = useState('');
  const [renaming, setRenaming] = useState<string | null>(null); // root path or "group:<parent>"
  const setAddFolderOpen = useUIStore((s) => s.setAddFolderOpen);
  const setSettingsOpen = useUIStore((s) => s.setSettingsOpen);
  const groupAliases = useUIStore((s) => s.groupAliases);
  // One subscription for the whole rail, not one per row.
  const rootOnline = useRootOnline();

  const blocks = useMemo(() => railBlocks(roots), [roots]);
  const q = filter.trim().toLowerCase();

  // Managed parents filter their own (server-supplied) rows; only the derived
  // groups can be narrowed here.
  const visible = q
    ? blocks.filter((b) => {
        if (b.kind === 'parent') return true;
        const name = aliasFor(b.group.parentPath, b.group.parentName, groupAliases);
        return (
          name.toLowerCase().includes(q) ||
          b.group.roots.some((r) => rootName(r).toLowerCase().includes(q))
        );
      })
    : blocks;

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
            {visible.map((b) =>
              b.kind === 'parent' ? (
                <ManagedParent
                  key={blockId(b)}
                  root={b.root}
                  roots={roots}
                  blocks={blocks}
                  filter={q}
                  renaming={renaming}
                  setRenaming={setRenaming}
                  online={rootOnline(b.root.path)}
                />
              ) : (
                <Group
                  key={blockId(b)}
                  group={b.group}
                  blocks={blocks}
                  roots={roots}
                  renaming={renaming}
                  setRenaming={setRenaming}
                  forceOpen={q !== ''}
                  rootOnline={rootOnline}
                />
              ),
            )}
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

// ---------------------------------------------------------------- reordering

/** Rewrites the stored root order after a block moves. */
function reorder(client: ReturnType<typeof useApiClient>, ordered: RailBlock[]) {
  void saveRoots(client, ordered.flatMap(blockRoots));
}

function moveBlock(
  client: ReturnType<typeof useApiClient>,
  blocks: RailBlock[],
  self: RailBlock,
  dir: -1 | 1,
) {
  const i = blocks.findIndex((b) => blockId(b) === blockId(self));
  const target = blocks[i + dir];
  if (i < 0 || !target) return;
  const ordered = blocks.slice();
  ordered[i] = target;
  ordered[i + dir] = self;
  reorder(client, ordered);
}

function blockIndex(blocks: RailBlock[], self: RailBlock) {
  return blocks.findIndex((b) => blockId(b) === blockId(self));
}

/** Drag a block header onto another to reorder. */
function dropHandlers(
  client: ReturnType<typeof useApiClient>,
  blocks: RailBlock[],
  self: RailBlock,
) {
  return {
    draggable: true,
    onDragStart: (e: React.DragEvent) => e.dataTransfer.setData('marraw/block', blockId(self)),
    onDragOver: (e: React.DragEvent) => {
      if (e.dataTransfer.types.includes('marraw/block')) e.preventDefault();
    },
    onDrop: (e: React.DragEvent) => {
      const from = e.dataTransfer.getData('marraw/block');
      if (!from || from === blockId(self)) return;
      e.preventDefault();
      e.stopPropagation();
      const moving = blocks.find((b) => blockId(b) === from);
      if (!moving) return;
      const ordered = blocks.filter((b) => blockId(b) !== from);
      ordered.splice(blockIndex(ordered, self), 0, moving);
      reorder(client, ordered);
    },
  };
}

// ---------------------------------------------------------------- header

function BlockHeader({
  name,
  subtitle,
  open,
  icon,
  onToggle,
  drag,
  testId,
}: {
  name: string;
  subtitle: string;
  open: boolean;
  icon?: React.ReactNode;
  onToggle: () => void;
  drag: ReturnType<typeof dropHandlers>;
  testId?: string;
}) {
  return (
    <ContextMenuTrigger
      className="flex cursor-default flex-col gap-px rounded-md px-2 pt-2 pb-1 hover:bg-accent"
      onClick={onToggle}
      data-testid={testId}
      {...drag}
    >
      <div className="flex items-center gap-[7px]">
        <span
          className={cn('text-[9px] text-muted-foreground transition-transform', open && 'rotate-90')}
        >
          ▶
        </span>
        <span className="flex min-w-0 flex-1 items-center gap-1.5">
          <span className="truncate font-semibold text-foreground">{name}</span>
          {icon}
        </span>
      </div>
      <span className="truncate pl-4 font-mono text-[10px] text-faint" title={subtitle}>
        {subtitle}
      </span>
    </ContextMenuTrigger>
  );
}

// ---------------------------------------------------------------- managed parent

/**
 * A library folder: a stored parent whose child shoots come from the daemon,
 * re-listed whenever the watcher sees the folder change on disk. The children
 * are never stored, so they carry no alias and cannot be removed — only hidden.
 */
function ManagedParent({
  root,
  roots,
  blocks,
  filter,
  renaming,
  setRenaming,
  online,
}: {
  root: LibraryRoot;
  roots: LibraryRoot[];
  blocks: RailBlock[];
  filter: string;
  renaming: string | null;
  setRenaming: (v: string | null) => void;
  online: boolean;
}) {
  const client = useApiClient();
  const groupAliases = useUIStore((s) => s.groupAliases);
  const key = parentKey(root.path);
  const open = useUIStore((s) => s.railGroups[key.toLowerCase()] !== false);
  const { data: shoots, refetch } = useListShoots(root.path);

  const self: RailBlock = { kind: 'parent', root };
  const name = aliasFor(key, baseName(root.path), groupAliases);
  const rows = useMemo(
    () => (shoots ?? []).filter((s) => !filter || s.name.toLowerCase().includes(filter)),
    [shoots, filter],
  );

  if (filter && rows.length === 0 && !name.toLowerCase().includes(filter)) return null;

  const showRows = open || filter !== '';
  const i = blockIndex(blocks, self);

  const rescanAll = () => {
    for (const s of rows) void openFolder(client, s.path).catch(() => {});
    toast.success(`Rescanning ${rows.length} folder${rows.length === 1 ? '' : 's'}`);
  };

  // Removing a library folder removes exactly one stored root; its children were
  // never stored.
  const remove = () => {
    void saveRoots(client, roots.filter((r) => !samePath(r.path, root.path)));
    const { folderPath } = useUIStore.getState();
    if (folderPath && rows.some((s) => samePath(s.path, folderPath))) {
      useUIStore.setState({ folderId: null, folderPath: null });
    }
  };

  if (renaming === key) {
    return (
      <RenameEditor
        initial={name}
        diskName={baseName(root.path)}
        onSubmit={(alias) => {
          updateGroupAlias(client, key, alias && alias !== baseName(root.path) ? alias : '');
          setRenaming(null);
        }}
        onCancel={() => setRenaming(null)}
      />
    );
  }

  return (
    <div className="flex flex-col gap-px">
      <ContextMenu>
        <BlockHeader
          name={name}
          subtitle={root.path}
          open={showRows}
          icon={
            <>
              <FolderTree
                className={cn(
                  'size-3 shrink-0',
                  online ? 'text-accent-text' : 'text-muted-foreground',
                )}
                strokeWidth={1.5}
                aria-label="Library folder"
              />
              {!online && <OfflineBadge />}
            </>
          }
          onToggle={() => updateRailGroupOpen(client, key, !open)}
          drag={dropHandlers(client, blocks, self)}
          testId="rail-parent"
        />
        <ContextMenuContent>
          <div className="flex flex-col gap-px px-2.5 pt-1.5 pb-2">
            <span className="truncate font-semibold text-foreground">{name}</span>
            <span className="truncate font-mono text-[10px] text-faint">
              {root.path} ·{' '}
              {online
                ? `${rows.length} folder${rows.length === 1 ? '' : 's'} · auto-updating`
                : 'offline'}
            </span>
          </div>
          <ContextMenuSeparator />
          <ContextMenuItem onClick={() => setRenaming(key)}>
            <Pencil /> <span className="flex-1">Rename group…</span>
          </ContextMenuItem>
          <ContextMenuItem
            hint="Explorer"
            onClick={() => window.marraw?.revealInExplorer(root.path)}
            disabled={!window.marraw}
          >
            <ExternalLink /> <span className="flex-1">Locate on disk</span>
          </ContextMenuItem>
          <ContextMenuItem onClick={() => refetch()}>
            <RefreshCw /> <span className="flex-1 text-foreground">Refresh</span>
          </ContextMenuItem>
          <ContextMenuItem disabled={!online} onClick={rescanAll}>
            <RefreshCw /> <span className="flex-1">Rescan all shoots</span>
          </ContextMenuItem>
          <ContextMenuItem disabled={i === 0} onClick={() => moveBlock(client, blocks, self, -1)}>
            <ArrowUp /> <span className="flex-1">Move up</span>
          </ContextMenuItem>
          <ContextMenuItem
            disabled={i === blocks.length - 1}
            onClick={() => moveBlock(client, blocks, self, 1)}
          >
            <ArrowDown /> <span className="flex-1">Move down</span>
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem variant="destructive" onClick={remove}>
            <Trash2 />
            <div className="flex flex-col gap-px">
              <span>Remove library folder</span>
              <span className="text-[11px] text-faint">
                {rows.length} folder{rows.length === 1 ? '' : 's'} · files stay on disk
              </span>
            </div>
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>

      {showRows && !online && (
        <span className="px-6 py-1.5 text-[11px] leading-relaxed text-faint">
          Drive not connected. Its folders reappear when you plug it back in.
        </span>
      )}
      {showRows && online && shoots != null && shoots.length === 0 && (
        <span className="px-6 py-1.5 text-[11px] text-faint">No photo folders yet</span>
      )}
      {showRows &&
        online &&
        rows.map((s) => <ShootRow key={s.path} shoot={s} parent={root} roots={roots} />)}
    </div>
  );
}

/**
 * A discovered shoot. It has no stored record, so there is no display alias to
 * rename and no root to remove — hiding it writes to the parent's exclusion
 * list, because "Remove" would be a lie: the next listing would bring it back.
 */
function ShootRow({
  shoot,
  parent,
  roots,
}: {
  shoot: Shoot;
  parent: LibraryRoot;
  roots: LibraryRoot[];
}) {
  const client = useApiClient();
  const folderPath = useUIStore((s) => s.folderPath);
  const active = folderPath != null && samePath(folderPath, shoot.path);
  const scanning = useFolderBusy(shoot.path);

  const open = () => void openShoot(client, shoot);

  const hide = () => {
    const next = roots.map((r) =>
      samePath(r.path, parent.path)
        ? { ...r, excludedChildren: [...(r.excludedChildren ?? []), shoot.path.toLowerCase()] }
        : r,
    );
    void saveRoots(client, next);
    if (active) useUIStore.setState({ folderId: null, folderPath: null });
    toast.success(`Hid ${shoot.name}`, { description: 'Files stay on disk.' });
  };

  const renameOnDisk = async () => {
    const name = window.prompt('New folder name on disk:', shoot.name);
    if (!name || name === shoot.name) return;
    try {
      const res = await renameFolderOnDisk(client, shoot.path, name);
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
        title={shoot.path}
        data-testid="rail-shoot"
        data-name={shoot.name}
        data-count={shoot.photoCount}
        data-self={shoot.isSelf ? '1' : undefined}
      >
        <Folder
          className={cn('size-[13px] shrink-0', active ? 'text-accent-text' : 'text-muted-foreground')}
          strokeWidth={1.5}
        />
        <span className="flex-1 truncate">
          {shoot.name}
          {shoot.isSelf && <span className="ml-1 text-faint">· loose files</span>}
        </span>
        {scanning ? (
          <ChipSpinner className="size-3" />
        ) : shoot.photoCount > 0 ? (
          <span className="font-mono text-[10.5px] font-normal text-faint">
            {shoot.photoCount.toLocaleString()}
          </span>
        ) : null}
      </ContextMenuTrigger>
      <ContextMenuContent>
        <div className="flex items-start gap-2 px-2.5 pt-1.5 pb-2">
          <Folder className="mt-0.5 size-3.5 shrink-0 text-accent-text" strokeWidth={1.5} />
          <div className="flex min-w-0 flex-col gap-px">
            <span className="truncate font-semibold text-foreground">{shoot.name}</span>
            <span className="font-mono text-[10px] text-faint">
              {shoot.photoCount > 0 ? `${shoot.photoCount.toLocaleString()} RAW` : shoot.path}
            </span>
          </div>
        </div>
        <ContextMenuSeparator />
        <ContextMenuItem
          hint="Enter"
          onClick={() =>
            void openShoot(client, shoot).then(() => useUIStore.getState().setMode('cull'))
          }
        >
          <Play /> <span className="flex-1 text-foreground">Open in Cull</span>
        </ContextMenuItem>
        <ContextMenuItem
          onClick={() => window.win?.openNewWindow(shoot.path)}
          disabled={!window.win}
        >
          <AppWindow /> <span className="flex-1">Open in new window</span>
        </ContextMenuItem>
        {!shoot.isSelf && (
          <ContextMenuItem onClick={renameOnDisk}>
            <FolderPen /> <span className="flex-1">Rename on disk…</span>
          </ContextMenuItem>
        )}
        <ContextMenuItem
          hint="Explorer"
          onClick={() => window.marraw?.revealInExplorer(shoot.path)}
          disabled={!window.marraw}
        >
          <ExternalLink /> <span className="flex-1 text-foreground">Locate on disk</span>
        </ContextMenuItem>
        <ContextMenuItem
          onClick={() => {
            void navigator.clipboard.writeText(shoot.path);
            toast.success('Path copied');
          }}
        >
          <Copy /> <span className="flex-1">Copy path</span>
        </ContextMenuItem>
        <ContextMenuItem onClick={open}>
          <RefreshCw /> <span className="flex-1">Rescan for new photos</span>
        </ContextMenuItem>
        <ContextMenuItem onClick={() => void startFullres(client, shoot.path, shoot.name)}>
          <Maximize2 /> <span className="flex-1">Render 1:1</span>
        </ContextMenuItem>
        {!shoot.isSelf && (
          <>
            <ContextMenuSeparator />
            <ContextMenuItem variant="destructive" onClick={hide}>
              <EyeOff />
              <div className="flex flex-col gap-px">
                <span>Hide from library</span>
                <span className="text-[11px] text-faint">Files stay on disk</span>
              </div>
            </ContextMenuItem>
          </>
        )}
      </ContextMenuContent>
    </ContextMenu>
  );
}

// ---------------------------------------------------------------- derived group

function Group({
  group,
  blocks,
  roots,
  renaming,
  setRenaming,
  forceOpen,
  rootOnline,
}: {
  group: RootGroup;
  blocks: RailBlock[];
  roots: LibraryRoot[];
  renaming: string | null;
  setRenaming: (v: string | null) => void;
  forceOpen: boolean;
  rootOnline: OnlineFn;
}) {
  const client = useApiClient();
  const groupAliases = useUIStore((s) => s.groupAliases);
  const open = useUIStore((s) => s.railGroups[group.parentPath.toLowerCase()] !== false);
  const groupRenameId = `group:${group.parentPath.toLowerCase()}`;
  const showRows = open || forceOpen;

  const self: RailBlock = { kind: 'group', group };
  const i = blockIndex(blocks, self);
  const name = aliasFor(group.parentPath, group.parentName, groupAliases);

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
          initial={name}
          diskName={group.parentName}
          onSubmit={(alias) => {
            updateGroupAlias(
              client,
              group.parentPath,
              alias && alias !== group.parentName ? alias : '',
            );
            setRenaming(null);
          }}
          onCancel={() => setRenaming(null)}
        />
      ) : (
        <ContextMenu>
          <BlockHeader
            name={name}
            subtitle={group.parentPath}
            open={showRows}
            onToggle={() => updateRailGroupOpen(client, group.parentPath, !open)}
            drag={dropHandlers(client, blocks, self)}
          />
          <ContextMenuContent>
            <div className="flex flex-col gap-px px-2.5 pt-1.5 pb-2">
              <span className="truncate font-semibold text-foreground">{name}</span>
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
            <ContextMenuItem disabled={i === 0} onClick={() => moveBlock(client, blocks, self, -1)}>
              <ArrowUp /> <span className="flex-1">Move up</span>
            </ContextMenuItem>
            <ContextMenuItem
              disabled={i === blocks.length - 1}
              onClick={() => moveBlock(client, blocks, self, 1)}
            >
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
            <RootRow
              key={r.path}
              root={r}
              roots={roots}
              online={rootOnline(r.path)}
              onRename={() => setRenaming(r.path.toLowerCase())}
            />
          ),
        )}
    </div>
  );
}

/** A hand-added shoot root, with its stored alias and include-subfolders flag. */
function RootRow({
  root,
  roots,
  online,
  onRename,
}: {
  root: LibraryRoot;
  roots: LibraryRoot[];
  online: boolean;
  onRename: () => void;
}) {
  const client = useApiClient();
  const folderPath = useUIStore((s) => s.folderPath);
  const active = folderPath != null && samePath(folderPath, root.path);
  const scanning = useFolderBusy(root.path);

  const open = () => {
    if (!online) {
      toast.error(`${rootName(root)} is offline`, {
        description: 'Reconnect the drive — the folder returns on its own.',
      });
      return;
    }
    void openRoot(client, roots, root);
  };

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
        title={online ? root.path : `${root.path} — drive not connected`}
        data-testid="rail-root"
        data-name={rootName(root)}
        data-online={online ? '1' : '0'}
      >
        <Folder
          className={cn('size-[13px] shrink-0', active ? 'text-accent-text' : 'text-muted-foreground')}
          strokeWidth={1.5}
        />
        <span className={cn('flex-1 truncate', !online && 'text-muted-foreground')}>
          {rootName(root)}
        </span>
        {!online ? (
          <OfflineBadge />
        ) : scanning ? (
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
        <ContextMenuItem hint="Enter" disabled={!online} onClick={openInCull}>
          <Play /> <span className="flex-1 text-foreground">Open in Cull</span>
        </ContextMenuItem>
        <ContextMenuItem
          onClick={() => window.win?.openNewWindow(root.path)}
          disabled={!window.win || !online}
        >
          <AppWindow /> <span className="flex-1">Open in new window</span>
        </ContextMenuItem>
        <ContextMenuItem hint="F2" onClick={onRename}>
          <Pencil /> <span className="flex-1">Rename…</span>
        </ContextMenuItem>
        <ContextMenuItem disabled={!online} onClick={renameOnDisk}>
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
        <ContextMenuItem disabled={!online} onClick={toggleSubfolders}>
          <Check className={cn(!root.includeSubfolders && 'invisible', 'text-primary!')} />
          <span className="flex-1">Include subfolders</span>
        </ContextMenuItem>
        <ContextMenuItem disabled={!online} onClick={open}>
          <RefreshCw /> <span className="flex-1">Rescan for new photos</span>
        </ContextMenuItem>
        <ContextMenuItem
          disabled={!online}
          onClick={() => void startFullres(client, root.path, rootName(root))}
        >
          <Maximize2 /> <span className="flex-1">Render 1:1</span>
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

// Kicks off the 1:1 full-resolution pre-render; progress shows in the task
// tray, so this just starts it and reports failures.
function startFullres(client: ReturnType<typeof useApiClient>, path: string, name: string) {
  return renderFolderFullres(client, path)
    .then(() => toast.success(`Rendering 1:1 previews for ${name}…`))
    .catch((err) => toast.error(`Render 1:1 failed: ${(err as Error).message}`));
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

// useFolderBusy: any background task tied to this album — scanning,
// calibrating, pre-rendering, 1:1 rendering, or exporting from it — is
// running. Shared (not owner-only) so work started in another window or
// surviving a reconnect still lights up the folder.
function useFolderBusy(path: string) {
  const tasks = useSharedTasks();
  return tasks.some((t) => {
    if (t.status !== 'running' && t.status !== 'created') return false;
    const meta = t.meta as { folderPath?: string } | undefined;
    return meta?.folderPath != null && samePath(meta.folderPath, path);
  });
}
