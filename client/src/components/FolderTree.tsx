import { useState } from 'react';
import { ChevronRight, Folder, HardDrive, Image } from 'lucide-react';
import { useListDrives, useListDir, openFolder } from '@/api/library';
import { useApiClient } from '@/api/client';
import { toast } from 'sonner';
import { cn } from '@/lib/utils';
import { useUIStore } from '@/stores/uiStore';

export function FolderTree() {
  const { data: drives, isLoading } = useListDrives();
  return (
    <div className="flex h-full flex-col gap-1 overflow-y-auto p-2 text-sm">
      {isLoading && <div className="p-2 text-muted-foreground">Loading drives…</div>}
      {drives?.map((d) => (
        <TreeNode key={d.path} name={d.name} path={d.path} icon="drive" depth={0} />
      ))}
    </div>
  );
}

function TreeNode({
  name,
  path,
  icon,
  depth,
}: {
  name: string;
  path: string;
  icon: 'drive' | 'folder';
  depth: number;
}) {
  const [expanded, setExpanded] = useState(false);
  const client = useApiClient();
  const folderPath = useUIStore((s) => s.folderPath);
  const setFolder = useUIStore((s) => s.setFolder);
  const active = folderPath === path;

  const open = () => {
    openFolder(client, path)
      .then((info) => setFolder(info.folderId, path))
      .catch((err) => toast.error(`Cannot open folder: ${err.message}`));
  };

  const Icon = icon === 'drive' ? HardDrive : Folder;
  return (
    <div>
      <div
        className={cn(
          'flex cursor-pointer items-center gap-1 rounded px-1 py-0.5 hover:bg-accent',
          active && 'bg-accent text-accent-foreground',
        )}
        style={{ paddingLeft: depth * 12 + 4 }}
      >
        <button
          className="flex size-4 shrink-0 items-center justify-center text-muted-foreground"
          onClick={() => setExpanded((v) => !v)}
          aria-label={expanded ? 'Collapse' : 'Expand'}
        >
          <ChevronRight className={cn('size-3.5 transition-transform', expanded && 'rotate-90')} />
        </button>
        <button className="flex min-w-0 flex-1 items-center gap-1.5" onClick={open} onDoubleClick={() => setExpanded((v) => !v)}>
          <Icon className="size-4 shrink-0 text-muted-foreground" />
          <span className="truncate">{name}</span>
          {active && <Image className="ml-auto size-3.5 shrink-0 text-primary" />}
        </button>
      </div>
      {expanded && <TreeChildren path={path} depth={depth + 1} />}
    </div>
  );
}

function TreeChildren({ path, depth }: { path: string; depth: number }) {
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
        <TreeNode key={d.path} name={d.name} path={d.path} icon="folder" depth={depth} />
      ))}
    </div>
  );
}
