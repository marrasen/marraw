import { useState } from 'react';
import { ChevronRight, Clock, Folder, HardDrive, Image, Star } from 'lucide-react';
import {
  useListDrives,
  useListDir,
  useGetFolderPrefs,
  openFolder,
  setFavoriteFolders,
} from '@/api/library';
import { useApiClient, type ApiClient } from '@/api/client';
import { toast } from 'sonner';
import { cn } from '@/lib/utils';
import { useUIStore } from '@/stores/uiStore';

// Last path segment for display; drive roots like "D:\" keep their letter.
function baseName(path: string) {
  const trimmed = path.replace(/[\\/]+$/, '');
  const i = Math.max(trimmed.lastIndexOf('\\'), trimmed.lastIndexOf('/'));
  return i >= 0 && i < trimmed.length - 1 ? trimmed.slice(i + 1) : trimmed || path;
}

function openPath(client: ApiClient, path: string) {
  const setFolder = useUIStore.getState().setFolder;
  openFolder(client, path)
    .then((info) => setFolder(info.folderId, path))
    .catch((err) => toast.error(`Cannot open folder: ${err.message}`));
}

const sameFolder = (a: string, b: string) => a.toLowerCase() === b.toLowerCase();

export function FolderTree() {
  const { data: drives, isLoading } = useListDrives();
  const { data: prefs } = useGetFolderPrefs();
  const client = useApiClient();

  const favorites = prefs?.favorites ?? [];
  const recents = (prefs?.recents ?? []).filter(
    (p) => !favorites.some((f) => sameFolder(f, p)),
  );
  const toggleFavorite = (path: string) => {
    const next = favorites.some((f) => sameFolder(f, path))
      ? favorites.filter((f) => !sameFolder(f, path))
      : [...favorites, path];
    setFavoriteFolders(client, next).catch((err) => toast.error(err.message));
  };

  return (
    <div className="flex h-full flex-col gap-1 overflow-y-auto p-2 text-sm">
      {favorites.length > 0 && (
        <Section icon={Star} title="Favourites">
          {favorites.map((p) => (
            <ShortcutRow key={p} path={p} favorited onToggleFavorite={toggleFavorite} />
          ))}
        </Section>
      )}
      {recents.length > 0 && (
        <Section icon={Clock} title="Recent">
          {recents.map((p) => (
            <ShortcutRow key={p} path={p} onToggleFavorite={toggleFavorite} />
          ))}
        </Section>
      )}
      {(favorites.length > 0 || recents.length > 0) && <div className="my-1 border-t" />}
      {isLoading && <div className="p-2 text-muted-foreground">Loading drives…</div>}
      {drives?.map((d) => (
        <TreeNode
          key={d.path}
          name={d.name}
          path={d.path}
          icon="drive"
          depth={0}
          hasSubdirs
          favorites={favorites}
          onToggleFavorite={toggleFavorite}
        />
      ))}
    </div>
  );
}

function Section({
  icon: SectionIcon,
  title,
  children,
}: {
  icon: typeof Star;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col">
      <div className="flex items-center gap-1.5 px-1 py-0.5 text-xs font-medium text-muted-foreground">
        <SectionIcon className="size-3" />
        {title}
      </div>
      {children}
    </div>
  );
}

// A favourite or recent folder: opens on click, star toggles favourite.
function ShortcutRow({
  path,
  favorited,
  onToggleFavorite,
}: {
  path: string;
  favorited?: boolean;
  onToggleFavorite: (path: string) => void;
}) {
  const client = useApiClient();
  const folderPath = useUIStore((s) => s.folderPath);
  const active = folderPath != null && sameFolder(folderPath, path);
  return (
    <div
      className={cn(
        'group flex cursor-pointer items-center gap-1.5 rounded px-1 py-0.5 pl-5 hover:bg-accent',
        active && 'bg-accent text-accent-foreground',
      )}
    >
      <button
        className="flex min-w-0 flex-1 items-center gap-1.5"
        onClick={() => openPath(client, path)}
        title={path}
      >
        <Folder className="size-4 shrink-0 text-muted-foreground" />
        <span className="truncate">{baseName(path)}</span>
        {active && <Image className="ml-auto size-3.5 shrink-0 text-primary" />}
      </button>
      <FavoriteStar path={path} favorited={!!favorited} onToggle={onToggleFavorite} />
    </div>
  );
}

function FavoriteStar({
  path,
  favorited,
  onToggle,
}: {
  path: string;
  favorited: boolean;
  onToggle: (path: string) => void;
}) {
  return (
    <button
      className={cn(
        'shrink-0 text-muted-foreground hover:text-foreground',
        favorited ? 'text-yellow-500 hover:text-yellow-600' : 'opacity-0 group-hover:opacity-100',
      )}
      onClick={(e) => {
        e.stopPropagation();
        onToggle(path);
      }}
      aria-label={favorited ? 'Remove favourite' : 'Add favourite'}
      title={favorited ? 'Remove favourite' : 'Add to favourites'}
    >
      <Star className={cn('size-3.5', favorited && 'fill-current')} />
    </button>
  );
}

function TreeNode({
  name,
  path,
  icon,
  depth,
  hasSubdirs,
  favorites,
  onToggleFavorite,
}: {
  name: string;
  path: string;
  icon: 'drive' | 'folder';
  depth: number;
  hasSubdirs: boolean;
  favorites: string[];
  onToggleFavorite: (path: string) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const client = useApiClient();
  const folderPath = useUIStore((s) => s.folderPath);
  const active = folderPath === path;
  const favorited = favorites.some((f) => sameFolder(f, path));

  const Icon = icon === 'drive' ? HardDrive : Folder;
  return (
    <div>
      <div
        className={cn(
          'group flex cursor-pointer items-center gap-1 rounded px-1 py-0.5 hover:bg-accent',
          active && 'bg-accent text-accent-foreground',
        )}
        style={{ paddingLeft: depth * 12 + 4 }}
      >
        {hasSubdirs ? (
          <button
            className="flex size-4 shrink-0 items-center justify-center text-muted-foreground"
            onClick={() => setExpanded((v) => !v)}
            aria-label={expanded ? 'Collapse' : 'Expand'}
          >
            <ChevronRight className={cn('size-3.5 transition-transform', expanded && 'rotate-90')} />
          </button>
        ) : (
          <span className="size-4 shrink-0" />
        )}
        <button
          className="flex min-w-0 flex-1 items-center gap-1.5"
          onClick={() => openPath(client, path)}
          onDoubleClick={() => hasSubdirs && setExpanded((v) => !v)}
        >
          <Icon className="size-4 shrink-0 text-muted-foreground" />
          <span className="truncate">{name}</span>
          {active && <Image className="ml-auto size-3.5 shrink-0 text-primary" />}
        </button>
        {icon === 'folder' && (
          <FavoriteStar path={path} favorited={favorited} onToggle={onToggleFavorite} />
        )}
      </div>
      {expanded && (
        <TreeChildren
          path={path}
          depth={depth + 1}
          favorites={favorites}
          onToggleFavorite={onToggleFavorite}
        />
      )}
    </div>
  );
}

function TreeChildren({
  path,
  depth,
  favorites,
  onToggleFavorite,
}: {
  path: string;
  depth: number;
  favorites: string[];
  onToggleFavorite: (path: string) => void;
}) {
  const { data, isLoading, error } = useListDir(path);
  if (isLoading) {
    return (
      <div className="py-0.5 text-xs text-muted-foreground" style={{ paddingLeft: depth * 12 + 24 }}>
        …
      </div>
    );
  }
  if (error) {
    return (
      <div className="py-0.5 text-xs text-destructive" style={{ paddingLeft: depth * 12 + 24 }}>
        unreadable
      </div>
    );
  }
  if (!data?.length) {
    return (
      <div className="py-0.5 text-xs text-muted-foreground" style={{ paddingLeft: depth * 12 + 24 }}>
        no subfolders
      </div>
    );
  }
  return (
    <div className="flex flex-col">
      {data.map((d) => (
        <TreeNode
          key={d.path}
          name={d.name}
          path={d.path}
          icon="folder"
          depth={depth}
          hasSubdirs={d.hasSubdirs}
          favorites={favorites}
          onToggleFavorite={onToggleFavorite}
        />
      ))}
    </div>
  );
}
