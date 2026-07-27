import { useState } from 'react';
import { ChevronLeft, Folder, HardDrive, X } from 'lucide-react';
import { useListDrives, useListDir } from '@/api/library';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import { cn } from '@/lib/utils';
import { samePath } from '@/lib/library';

// Server-side directory picker: browses the DAEMON's filesystem via the
// ListDrives/ListDir RPCs, so it picks the right machine's folders whether the
// daemon is local or remote — a native dialog can only ever see this machine.
// Used for export destinations and the cache folder (AddFolderDialog stays
// its own dialog: import needs RAW counts and import-mode framing).

// Breadcrumb segments with cumulative paths, for both path styles the daemon
// may live on ("C:\Users\Marcus" and "/home/marcus").
function crumbs(path: string): { name: string; path: string }[] {
  const out: { name: string; path: string }[] = [];
  const trimmed = path.replace(/[\\/]+$/, '');
  if (path.startsWith('/')) {
    out.push({ name: '/', path: '/' });
    let acc = '';
    for (const p of trimmed.split('/').filter(Boolean)) {
      acc += `/${p}`;
      out.push({ name: p, path: acc });
    }
    return out;
  }
  let acc = '';
  for (const p of trimmed.split(/[\\/]+/)) {
    acc = acc === '' ? p : `${acc}\\${p}`;
    // A bare drive letter needs its slash back ("C:" opens the CWD, not the root).
    out.push({ name: p, path: acc === p && p.endsWith(':') ? `${p}\\` : acc });
  }
  return out;
}

export function DirPickerDialog({
  title,
  description,
  initialPath,
  onSelect,
  onClose,
}: {
  title: string;
  description?: string;
  /** Starting location; falls back to the daemon's first quick-access root. */
  initialPath?: string;
  onSelect: (path: string) => void;
  onClose: () => void;
}) {
  const { data: drives } = useListDrives();
  const [path, setPath] = useState<string | null>(initialPath ?? null);
  const location = path ?? drives?.[0]?.path ?? null;
  // The editable path field follows navigation but lets the user type a path
  // that doesn't exist yet (export/cache flows create their target). Adjusted
  // during render (not an effect) when navigation moves the location.
  const [typed, setTyped] = useState(initialPath ?? '');
  const [lastLocation, setLastLocation] = useState(location);
  if (location !== lastLocation) {
    setLastLocation(location);
    if (location != null) setTyped(location);
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        showCloseButton={false}
        className="flex h-[480px] w-[640px] max-w-none flex-col gap-0 overflow-hidden rounded-[14px] border-glass-border bg-card p-0 sm:max-w-none"
      >
        <div className="flex items-center justify-between border-b px-[22px] py-[15px]">
          <div className="flex flex-col gap-0.5">
            <span className="text-base font-semibold">{title}</span>
            {description && <span className="text-xs text-muted-foreground">{description}</span>}
          </div>
          <button
            className="flex size-7 items-center justify-center rounded-[7px] border text-muted-foreground hover:text-foreground"
            onClick={onClose}
            aria-label="Close"
          >
            <X className="size-3.5" />
          </button>
        </div>

        <div className="flex min-h-0 flex-1">
          <div className="flex w-[168px] shrink-0 flex-col gap-px overflow-y-auto border-r bg-sidebar p-2.5">
            <span className="px-2 pb-2 text-[10px] tracking-[.07em] text-faint uppercase">
              Quick access
            </span>
            {drives?.map((d) => {
              const Icon = /^[A-Z]:$/i.test(d.name) || d.name === '/' ? HardDrive : Folder;
              return (
                <button
                  key={d.path}
                  className={cn(
                    'flex h-8 items-center gap-2 rounded-[7px] px-2.5 text-[12.5px]',
                    location != null && samePath(location, d.path)
                      ? 'bg-sidebar-accent text-foreground'
                      : 'text-secondary-foreground hover:bg-accent',
                  )}
                  onClick={() => setPath(d.path)}
                >
                  <Icon className="size-3.5 shrink-0 opacity-85" strokeWidth={1.5} />
                  <span className="truncate">{d.name}</span>
                </button>
              );
            })}
          </div>

          {location == null ? (
            <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
              Loading folders…
            </div>
          ) : (
            <BrowseList path={location} onNavigate={setPath} />
          )}
        </div>

        <div className="flex items-center gap-2.5 border-t px-[22px] py-3">
          <input
            className="h-8 min-w-0 flex-1 rounded-lg border border-input bg-secondary px-2.5 font-mono text-xs outline-none focus:border-ring dark:bg-white/5"
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && typed.trim()) setPath(typed.trim());
            }}
            aria-label="Folder path"
            spellCheck={false}
          />
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={!typed.trim()}
            onClick={() => {
              onSelect(typed.trim());
              onClose();
            }}
          >
            Choose this folder
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function BrowseList({ path, onNavigate }: { path: string; onNavigate: (p: string) => void }) {
  const { data: entries, isLoading, error } = useListDir(path);
  const parts = crumbs(path);
  const parent = parts.length > 1 ? parts[parts.length - 2].path : null;

  return (
    <div className="flex min-w-0 flex-1 flex-col">
      <div className="flex h-[42px] shrink-0 items-center gap-2 border-b px-4 text-[12.5px]">
        <button
          className="flex size-[26px] items-center justify-center rounded-[7px] border text-muted-foreground hover:text-foreground disabled:opacity-40"
          disabled={parent == null}
          onClick={() => parent && onNavigate(parent)}
          aria-label="Back"
        >
          <ChevronLeft className="size-3.5" />
        </button>
        <div className="flex min-w-0 items-center gap-2 overflow-hidden">
          {parts.map((c, i) => (
            <span key={c.path} className="flex min-w-0 items-center gap-2">
              {i > 0 && <span className="text-faint">›</span>}
              <button
                className={cn(
                  'truncate',
                  i === parts.length - 1
                    ? 'font-semibold text-foreground'
                    : 'text-muted-foreground hover:text-foreground',
                )}
                onClick={() => onNavigate(c.path)}
              >
                {c.name}
              </button>
            </span>
          ))}
        </div>
      </div>
      <div className="flex flex-1 flex-col gap-px overflow-y-auto p-2.5">
        {isLoading && <div className="p-3 text-xs text-muted-foreground">Reading folder…</div>}
        {error != null && (
          <div className="p-3 text-xs text-danger-text">
            Cannot read this folder — it may not exist yet. You can still choose the typed path
            below.
          </div>
        )}
        {entries?.map((e) => (
          <button
            key={e.path}
            className="flex h-9 shrink-0 cursor-pointer items-center gap-[11px] rounded-lg px-3 text-left transition-colors hover:bg-accent"
            onClick={() => onNavigate(e.path)}
          >
            <Folder className="size-4 shrink-0 text-muted-foreground" strokeWidth={1.5} />
            <span className="flex-1 truncate text-[13px]">{e.name}</span>
            {e.hasSubdirs && <span className="font-mono text-[11px] text-faint">›</span>}
          </button>
        ))}
        {entries != null && entries.length === 0 && (
          <div className="p-3 text-xs text-muted-foreground">No subfolders here.</div>
        )}
      </div>
    </div>
  );
}
