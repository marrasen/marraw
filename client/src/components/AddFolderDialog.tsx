import { useEffect, useState } from 'react';
import { ChevronLeft, Folder, HardDrive, X } from 'lucide-react';
import { toast } from 'sonner';
import {
  countRaws,
  useListDrives,
  useListDirRaws,
  type LibraryRoot,
} from '@/api/library';
import { useApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import { Segmented } from '@/components/ui/segmented';
import { Switch } from '@/components/ui/switch';
import { cn } from '@/lib/utils';
import { openRoot, parentPath, samePath, saveRoots, useLibraryRoots } from '@/lib/library';
import { useUIStore } from '@/stores/uiStore';

type ImportMode = 'shoot' | 'library';

// A bare drive ("C:\") is never addable: importing a whole drive is always a
// misclick, and even counting its RAWs means walking the entire filesystem.
function isDriveRoot(path: string): boolean {
  return /^[a-zA-Z]:[\\/]?$/.test(path);
}

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
  const [mode, setMode] = useState<ImportMode>('shoot');
  // Shoot mode only. A library parent always scans non-recursively (its own row
  // holds loose RAWs; nested folders become its children), so this is ignored
  // there — see scanRecursionFor in internal/api/roots.go.
  const [subfolders, setSubfolders] = useState(true);

  const location = path ?? drives?.[0]?.path ?? null;
  const isLibrary = mode === 'library';
  const recursive = isLibrary || subfolders;
  const already = location != null && roots.some((r) => samePath(r.path, location));
  const atDriveRoot = location != null && isDriveRoot(location);

  // Recursive RAW total for the folder Add would import, debounced and keyed
  // by (path, recursive) so a stale response never shows against a changed
  // target. Aborting on cleanup also cancels the daemon-side walk, so browsing
  // through large ancestors doesn't stack abandoned filesystem scans.
  const [count, setCount] = useState<{ key: string; files: number } | null>(null);
  const countKey = location ? `${location.toLowerCase()}|${recursive}` : '';
  useEffect(() => {
    if (!location || already || atDriveRoot) return;
    const ctrl = new AbortController();
    const t = setTimeout(() => {
      countRaws(client, [location], recursive, { signal: ctrl.signal })
        .then((res) => setCount({ key: countKey, files: res.files }))
        .catch(() => {});
    }, 250);
    return () => {
      clearTimeout(t);
      ctrl.abort();
    };
  }, [countKey, location, recursive, already, atDriveRoot, client]);
  const files = count?.key === countKey ? count.files : null;

  // A shoot needs RAWs to import; a library parent may legitimately be empty
  // today (it is watched for shoots that land later).
  const nothingToImport = !isLibrary && files === 0;

  const add = async () => {
    if (!location || already) return;

    const newRoot: LibraryRoot = {
      path: location,
      alias: '',
      includeSubfolders: isLibrary ? false : subfolders,
      photoCount: 0,
      isParent: isLibrary,
    };

    // A shoot that sits directly inside a new library folder is now discovered
    // from disk. Leaving the hand-added root in place would render the same
    // folder twice, under two identically-named headers.
    const absorbed = isLibrary
      ? roots.filter((r) => !r.isParent && samePath(parentPath(r.path), location))
      : [];
    const kept = roots.filter((r) => !absorbed.includes(r));

    const all = [...kept, newRoot];
    await saveRoots(client, all);
    onClose();

    const noun = isLibrary ? 'library folder' : 'folder';
    toast.success(`Added ${noun} to the library`, {
      description:
        absorbed.length > 0
          ? `${absorbed.length} existing folder${absorbed.length === 1 ? ' is' : 's are'} now managed here.`
          : undefined,
    });

    if (!isLibrary) void openRoot(client, all, newRoot);
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        showCloseButton={false}
        className="flex h-[520px] w-[760px] max-w-none flex-col gap-0 overflow-hidden rounded-[14px] border-glass-border bg-card p-0 sm:max-w-none"
      >
        <div className="flex items-center justify-between border-b px-[22px] py-[15px]">
          <div className="flex flex-col gap-0.5">
            <span className="text-base font-semibold">Add folder to library</span>
            <span className="text-xs text-muted-foreground">
              Pick an import mode, then open the folder you want to add
            </span>
          </div>
          <button
            className="flex size-7 items-center justify-center rounded-[7px] border text-muted-foreground hover:text-foreground"
            onClick={onClose}
            aria-label="Close"
          >
            <X className="size-3.5" />
          </button>
        </div>

        <div className="flex flex-col gap-3 border-b px-[22px] py-3">
          <Segmented
            items={[
              { value: 'shoot', label: 'Import single shoot' },
              { value: 'library', label: 'Import library parent folder' },
            ]}
            value={mode}
            onValueChange={setMode}
            aria-label="Import mode"
          />
          <div className="text-[12.5px] leading-relaxed text-muted-foreground">
            {mode === 'shoot' ? (
              <>
                <strong>Import single shoot:</strong> Adds one folder as a single shoot. Its photos refresh
                automatically while it&rsquo;s open. New folders alongside it are not picked up — use a library
                parent folder for that.
              </>
            ) : (
              <>
                <strong>Import library parent folder:</strong> Adds a folder whose subfolders are each a shoot.
                Marraw keeps watching it, so new shoots and new photos appear automatically as they land on disk.
              </>
            )}
          </div>
          {mode === 'shoot' && (
            <label className="flex w-fit cursor-pointer items-center gap-2.5 text-[12.5px] text-secondary-foreground">
              <Switch checked={subfolders} onCheckedChange={setSubfolders} />
              Include photos in subfolders
            </label>
          )}
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
            <FolderList path={location} onNavigate={setPath} />
          )}
        </div>

        <div className="flex items-center gap-4 border-t px-[22px] py-3">
          <FooterInfo
            location={location}
            already={already}
            atDriveRoot={atDriveRoot}
            files={files}
            shoot={!isLibrary}
          />
          <Button variant="outline" size="lg" onClick={onClose}>
            Cancel
          </Button>
          <Button
            size="lg"
            disabled={!location || already || atDriveRoot || nothingToImport}
            onClick={() => void add()}
          >
            {isLibrary ? 'Add as library folder' : 'Add to library'}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}


function FolderList({
  path,
  onNavigate,
}: {
  path: string;
  onNavigate: (p: string) => void;
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
          // Every folder is enterable — the one you open is the one that gets
          // added, so a leaf folder full of RAWs (a shoot) must be reachable too.
          return (
            <button
              key={e.path}
              className="flex h-10 shrink-0 cursor-pointer items-center gap-[11px] rounded-lg px-3 text-left transition-colors hover:bg-accent"
              onClick={() => onNavigate(e.path)}
            >
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
            </button>
          );
        })}
        {entries != null && entries.length === 0 && (
          <div className="p-3 text-xs text-muted-foreground">No subfolders here.</div>
        )}
      </div>
    </div>
  );
}

// Names the folder that Add would import and why it can or cannot be added:
// its recursive RAW total, or the reason the Add button is disabled.
function FooterInfo({
  location,
  already,
  atDriveRoot,
  files,
  shoot,
}: {
  location: string | null;
  already: boolean;
  atDriveRoot: boolean;
  files: number | null;
  shoot: boolean;
}) {
  if (!location) return <div className="flex-1" />;

  const name = location.replace(/[\\/]+$/, '').split(/[\\/]+/).pop() || location;

  return (
    <div className="flex min-w-0 flex-1 flex-col gap-0.5" title={location}>
      <span className="truncate text-[12.5px]">
        <span className="text-muted-foreground">Add </span>
        <span className="font-medium text-foreground">{name}</span>
      </span>
      <span className="truncate font-mono text-[11px] text-muted-foreground">
        {already
          ? 'Already in your library'
          : atDriveRoot
            ? 'Open a folder on this drive to add it'
            : files == null
              ? 'Counting RAW files…'
              : files === 0 && shoot
                ? 'No RAW files in this folder'
                : `${files.toLocaleString()} RAW file${files === 1 ? '' : 's'}`}
      </span>
    </div>
  );
}

