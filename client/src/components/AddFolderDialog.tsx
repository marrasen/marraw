import { useEffect, useMemo, useRef, useState } from 'react';
import { ChevronLeft, Folder, HardDrive, X } from 'lucide-react';
import { toast } from 'sonner';
import {
  countRaws,
  useListDrives,
  useListDirRaws,
  type LibraryRoot,
} from '@/api/library';
import { useApiClient } from '@/api/client';
import { Checkbox } from '@/components/ui/checkbox';
import { Switch } from '@/components/ui/switch';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import { cn } from '@/lib/utils';
import { openRoot, samePath, saveRoots, useLibraryRoots } from '@/lib/library';
import { useUIStore } from '@/stores/uiStore';

// Breadcrumb segments with cumulative paths ("C:\Users\Marcus" → C: · Users · Marcus).
function crumbs(path: string): { name: string; path: string }[] {
  const parts = path.replace(/[\\/]+$/, '').split(/[\\/]+/);
  const out: { name: string; path: string }[] = [];
  let acc = '';
  for (const p of parts) {
    acc = acc === '' ? p : `${acc}\\${p}`;
    // A bare drive letter needs its slash back ("C:" opens the CWD, not the root).
    out.push({ name: p, path: acc === p && p.endsWith(':') ? `${p}\\` : acc });
  }
  return out;
}

/**
 * Add-folder picker (handoff plate "ADD FOLDER"): the old filesystem browser
 * repurposed as a modal. Navigate drives, tick folders, import them as
 * library roots — files never move.
 */
export function AddFolderDialog() {
  const open = useUIStore((s) => s.addFolderOpen);
  const setOpen = useUIStore((s) => s.setAddFolderOpen);
  if (!open) return null;
  return <PickerBody onClose={() => setOpen(false)} />;
}

function PickerBody({ onClose }: { onClose: () => void }) {
  const client = useApiClient();
  const { roots } = useLibraryRoots();
  const { data: drives } = useListDrives();
  const [path, setPath] = useState<string | null>(null);
  const [checked, setChecked] = useState<Map<string, { path: string; direct: number }>>(new Map());
  const [subfolders, setSubfolders] = useState(true);

  const location = path ?? drives?.[0]?.path ?? null;

  const toggle = (p: string, direct: number) => {
    setChecked((prev) => {
      const next = new Map(prev);
      const key = p.toLowerCase();
      if (next.has(key)) next.delete(key);
      else next.set(key, { path: p, direct });
      return next;
    });
  };

  const selected = [...checked.values()];

  const add = async () => {
    const fresh = selected.filter((s) => !roots.some((r) => samePath(r.path, s.path)));
    if (fresh.length === 0) {
      onClose();
      return;
    }
    const newRoots: LibraryRoot[] = fresh.map((s) => ({
      path: s.path,
      alias: '',
      includeSubfolders: subfolders,
      photoCount: subfolders ? 0 : s.direct,
    }));
    const all = [...roots, ...newRoots];
    await saveRoots(client, all);
    onClose();
    toast.success(
      `Added ${fresh.length} folder${fresh.length === 1 ? '' : 's'} to the library`,
    );
    void openRoot(client, all, newRoots[0]);
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        showCloseButton={false}
        className="flex h-[520px] w-[760px] max-w-none flex-col gap-0 overflow-hidden rounded-[14px] border-glass-border bg-card p-0 sm:max-w-none"
      >
        <div className="flex items-center border-b px-[22px] py-[15px]">
          <div className="flex flex-col gap-0.5">
            <span className="text-base font-semibold">Add folder to library</span>
            <span className="text-xs text-muted-foreground">
              Browse your drives and pick folders of RAW photos
            </span>
          </div>
          <button
            className="ml-auto flex size-7 items-center justify-center rounded-[7px] border text-muted-foreground hover:text-foreground"
            onClick={onClose}
            aria-label="Close"
          >
            <X className="size-3.5" />
          </button>
        </div>

        <div className="flex min-h-0 flex-1">
          <div className="flex w-[184px] shrink-0 flex-col gap-px overflow-y-auto border-r bg-sidebar p-2.5">
            <span className="px-2 pb-2 text-[10px] tracking-[.07em] text-faint uppercase">
              Quick access
            </span>
            {drives?.map((d) => {
              const Icon = /^[A-Z]:$/i.test(d.name) ? HardDrive : Folder;
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
              Loading drives…
            </div>
          ) : (
            <FolderList
              path={location}
              checked={checked}
              onNavigate={setPath}
              onToggle={toggle}
              subfolders={subfolders}
            />
          )}
        </div>

        <div className="flex items-center gap-4 border-t px-[22px] py-3.5">
          <label className="flex cursor-pointer items-center gap-2.5">
            <Switch checked={subfolders} onCheckedChange={setSubfolders} />
            <span className="text-[12.5px] text-secondary-foreground">Include subfolders</span>
          </label>
          <div className="flex-1" />
          <FooterCount selected={selected} subfolders={subfolders} />
          <Button variant="outline" size="lg" onClick={onClose}>
            Cancel
          </Button>
          <Button size="lg" disabled={selected.length === 0} onClick={() => void add()}>
            Add to library
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function FolderList({
  path,
  checked,
  onNavigate,
  onToggle,
  subfolders,
}: {
  path: string;
  checked: Map<string, { path: string; direct: number }>;
  onNavigate: (p: string) => void;
  onToggle: (p: string, direct: number) => void;
  subfolders: boolean;
}) {
  const { data: entries, isLoading, error } = useListDirRaws(path);
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
        <div className="flex-1" />
        <span className="shrink-0 font-mono text-[11px] text-muted-foreground">
          {entries ? `${entries.length} item${entries.length === 1 ? '' : 's'}` : '…'}
        </span>
      </div>
      <div className="flex flex-1 flex-col gap-px overflow-y-auto p-2.5">
        {isLoading && <div className="p-3 text-xs text-muted-foreground">Reading folder…</div>}
        {error != null && (
          <div className="p-3 text-xs text-danger-text">Cannot read this folder.</div>
        )}
        {entries?.map((e) => {
          const isChecked = checked.has(e.path.toLowerCase());
          // No direct RAWs → dimmed; still selectable when subfolder
          // recursion could pull nested shoots in.
          const selectable = e.rawCount > 0 || (subfolders && e.hasSubdirs);
          return (
            <div
              key={e.path}
              className={cn(
                'flex h-10 shrink-0 cursor-pointer items-center gap-[11px] rounded-lg px-3',
                isChecked ? 'bg-sidebar-accent' : 'hover:bg-accent',
              )}
              onClick={() => e.hasSubdirs && onNavigate(e.path)}
              onDoubleClick={() => onNavigate(e.path)}
            >
              {/* The hit-area wrapper isolates the checkbox from the row: the
                  row navigates on click AND double-click, and two quick ticks
                  on the bare checkbox bubbled up as a dblclick that opened
                  the folder. The negative-margin padding also widens the
                  16px target so near-misses toggle instead of navigating. */}
              <span
                className="-m-2 flex items-center p-2"
                onClick={(ev) => ev.stopPropagation()}
                onDoubleClick={(ev) => ev.stopPropagation()}
              >
                <Checkbox
                  checked={isChecked}
                  disabled={!selectable}
                  onCheckedChange={() => onToggle(e.path, e.rawCount)}
                />
              </span>
              <Folder className="size-4 shrink-0 text-muted-foreground" strokeWidth={1.5} />
              <span
                className={cn(
                  'flex-1 truncate text-[13px]',
                  e.rawCount === 0 ? 'text-faint' : 'text-foreground',
                )}
              >
                {e.name}
              </span>
              <span
                className={cn(
                  'font-mono text-[11px]',
                  e.rawCount > 0 ? 'text-secondary-foreground' : 'text-faint',
                )}
              >
                {e.rawCount > 0 ? `${e.rawCount.toLocaleString()} RAW` : e.hasSubdirs ? '—' : 'no RAW'}
              </span>
            </div>
          );
        })}
        {entries != null && entries.length === 0 && (
          <div className="p-3 text-xs text-muted-foreground">No subfolders here.</div>
        )}
      </div>
    </div>
  );
}

// FooterCount sums the RAW files the current selection would import. Flat
// counts are already known from the rows; recursive totals come from the
// daemon, debounced while the user keeps ticking boxes.
function FooterCount({
  selected,
  subfolders,
}: {
  selected: { path: string; direct: number }[];
  subfolders: boolean;
}) {
  const client = useApiClient();
  // Recursive totals are keyed by the selection they were computed for, so a
  // stale response never shows against a changed selection.
  const [recursive, setRecursive] = useState<{ key: string; files: number } | null>(null);
  const reqId = useRef(0);

  const flatTotal = useMemo(() => selected.reduce((n, s) => n + s.direct, 0), [selected]);
  const paths = selected.map((s) => s.path);
  const pathsKey = paths.join('|').toLowerCase();

  useEffect(() => {
    if (!subfolders || paths.length === 0) return;
    const id = ++reqId.current;
    const t = setTimeout(() => {
      countRaws(client, paths, true)
        .then((res) => {
          if (reqId.current === id) setRecursive({ key: pathsKey, files: res.files });
        })
        .catch(() => {});
    }, 250);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pathsKey, subfolders, client]);

  if (selected.length === 0) {
    return <span className="font-mono text-[11.5px] text-faint">No folders selected</span>;
  }
  const total = subfolders
    ? recursive?.key === pathsKey
      ? recursive.files
      : null
    : flatTotal;
  return (
    <span className="font-mono text-[11.5px] text-muted-foreground">
      {selected.length} folder{selected.length === 1 ? '' : 's'} ·{' '}
      {total == null ? 'counting…' : `${total.toLocaleString()} RAW files`}
    </span>
  );
}
