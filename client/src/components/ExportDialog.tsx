import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { checkDest, startExport, type ExportFormatType } from '@/api/export';
import { useApiClient } from '@/api/client';
import type { Photo } from '@/api/library';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { useUIStore } from '@/stores/uiStore';

import '@/lib/electron';

const LAST_DIR_KEY = 'marraw.exportDir';

// items passed to the Select roots so the trigger shows labels, not raw
// values ("Full size" instead of "0").
const FORMAT_ITEMS = [
  { value: 'jpeg', label: 'JPEG' },
  { value: 'tiff16', label: 'TIFF (16-bit)' },
];
const SIZE_ITEMS = [
  { value: '0', label: 'Full size' },
  { value: '4096', label: '4096 px' },
  { value: '2048', label: '2048 px' },
  { value: '1600', label: '1600 px' },
];

export function ExportDialog({ photos }: { photos: Photo[] }) {
  const client = useApiClient();
  const open = useUIStore((s) => s.exportOpen);
  const setOpen = useUIStore((s) => s.setExportOpen);
  const selection = useUIStore((s) => s.selection);
  const folderPath = useUIStore((s) => s.folderPath);

  const [destDir, setDestDir] = useState('');
  const [format, setFormat] = useState<ExportFormatType>('jpeg');
  const [quality, setQuality] = useState(90);
  const [longEdge, setLongEdge] = useState(0);
  const [starting, setStarting] = useState(false);
  const [needsCreate, setNeedsCreate] = useState(false);

  // Prefill the destination when the dialog opens: the previously used
  // directory, else "<current folder>\Exports".
  useEffect(() => {
    if (!open) return;
    setNeedsCreate(false);
    const last = localStorage.getItem(LAST_DIR_KEY);
    setDestDir(last || (folderPath ? `${folderPath}\\Exports` : ''));
  }, [open, folderPath]);

  const ids = selection.size > 0 ? [...selection] : photos.map((p) => p.id);

  const start = async (createDir: boolean) => {
    if (!destDir) {
      toast.error('Choose a destination folder first');
      return;
    }
    setStarting(true);
    try {
      if (!createDir) {
        const dest = await checkDest(client, destDir);
        if (!dest.exists) {
          setNeedsCreate(true); // ask before creating
          return;
        }
      }
      await startExport(client, {
        photoIds: ids,
        destDir,
        format,
        jpegQuality: quality,
        longEdge,
        createDir,
      });
      localStorage.setItem(LAST_DIR_KEY, destDir);
      setOpen(false); // progress lives in the bottom-left task tray
    } catch (err) {
      toast.error(`Export failed to start: ${(err as Error).message}`);
    } finally {
      setStarting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Export {ids.length} photos</DialogTitle>
          <DialogDescription>
            {selection.size > 0 ? 'Exporting the current selection.' : 'Exporting all photos matching the filter.'}
            {' '}Runs in the background — progress appears bottom left.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-3">
          <div className="flex gap-2">
            <Input
              placeholder="Destination folder, e.g. D:\Exports"
              value={destDir}
              onChange={(e) => {
                setDestDir(e.target.value);
                setNeedsCreate(false);
              }}
            />
            {window.marraw && (
              <Button
                variant="outline"
                onClick={async () => {
                  const dir = await window.marraw!.pickDirectory();
                  if (dir) {
                    setDestDir(dir);
                    setNeedsCreate(false);
                  }
                }}
              >
                Browse…
              </Button>
            )}
          </div>
          {folderPath && (
            <button
              className="self-start text-xs text-muted-foreground underline-offset-2 hover:underline"
              onClick={() => {
                setDestDir(`${folderPath}\\Exports`);
                setNeedsCreate(false);
              }}
            >
              Use {folderPath}\Exports
            </button>
          )}

          <div className="flex gap-2">
            <Select items={FORMAT_ITEMS} value={format} onValueChange={(v) => setFormat(v as ExportFormatType)}>
              <SelectTrigger className="flex-1">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {FORMAT_ITEMS.map((it) => (
                    <SelectItem key={it.value} value={it.value}>
                      {it.label}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            {format === 'jpeg' && (
              <Input
                type="number"
                className="w-24"
                min={1}
                max={100}
                value={quality}
                onChange={(e) => setQuality(Number(e.target.value))}
                aria-label="JPEG quality"
              />
            )}
            <Select items={SIZE_ITEMS} value={String(longEdge)} onValueChange={(v) => setLongEdge(Number(v))}>
              <SelectTrigger className="w-36">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {SIZE_ITEMS.map((it) => (
                    <SelectItem key={it.value} value={it.value}>
                      {it.label}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>

          {needsCreate && (
            <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-2 text-xs">
              The folder <span className="font-medium">{destDir}</span> does not exist. Create it?
            </div>
          )}
        </div>

        <DialogFooter>
          {needsCreate ? (
            <>
              <Button variant="outline" onClick={() => setNeedsCreate(false)} disabled={starting}>
                Back
              </Button>
              <Button onClick={() => start(true)} disabled={starting}>
                Create folder & export
              </Button>
            </>
          ) : (
            <Button onClick={() => start(false)} disabled={ids.length === 0 || starting}>
              Export
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
