import { useRef, useState } from 'react';
import { toast } from 'sonner';
import { exportPhotos, type ExportFormatType } from '@/api/export';
import { useApiClient } from '@/api/client';
import type { Photo } from '@/api/library';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Progress } from '@/components/ui/progress';
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

declare global {
  interface Window {
    marraw?: {
      pickDirectory: () => Promise<string | null>;
      revealInExplorer: (path: string) => void;
    };
  }
}

export function ExportDialog({ photos }: { photos: Photo[] }) {
  const client = useApiClient();
  const open = useUIStore((s) => s.exportOpen);
  const setOpen = useUIStore((s) => s.setExportOpen);
  const selection = useUIStore((s) => s.selection);

  const [destDir, setDestDir] = useState('');
  const [format, setFormat] = useState<ExportFormatType>('jpeg');
  const [quality, setQuality] = useState(90);
  const [longEdge, setLongEdge] = useState(0);
  const [running, setRunning] = useState(false);
  const [done, setDone] = useState(0);
  const [failed, setFailed] = useState<string[]>([]);
  const abortRef = useRef<AbortController | null>(null);

  const ids = selection.size > 0 ? [...selection] : photos.map((p) => p.id);

  const start = async () => {
    if (!destDir) {
      toast.error('Choose a destination folder first');
      return;
    }
    setRunning(true);
    setDone(0);
    setFailed([]);
    const ac = new AbortController();
    abortRef.current = ac;
    let ok = 0;
    const errors: string[] = [];
    try {
      for await (const item of exportPhotos(
        client,
        { photoIds: ids, destDir, format, jpegQuality: quality, longEdge },
        { signal: ac.signal },
      )) {
        if (item.ok) ok++;
        else errors.push(`${item.fileName}: ${item.error}`);
        setDone(ok + errors.length);
        setFailed([...errors]);
      }
      if (errors.length === 0) {
        toast.success(`Exported ${ok} photos to ${destDir}`);
        setOpen(false);
      } else {
        toast.error(`Exported ${ok} photos, ${errors.length} failed`);
      }
    } catch (err) {
      if (!ac.signal.aborted) toast.error(`Export failed: ${(err as Error).message}`);
    } finally {
      setRunning(false);
      abortRef.current = null;
    }
  };

  const close = (next: boolean) => {
    if (!next && running) abortRef.current?.abort();
    setOpen(next);
  };

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Export {ids.length} photos</DialogTitle>
          <DialogDescription>
            {selection.size > 0 ? 'Exporting the current selection.' : 'Exporting all photos matching the filter.'}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-3">
          <div className="flex gap-2">
            <Input
              placeholder="Destination folder, e.g. D:\Exports"
              value={destDir}
              onChange={(e) => setDestDir(e.target.value)}
              disabled={running}
            />
            {window.marraw && (
              <Button
                variant="outline"
                disabled={running}
                onClick={async () => {
                  const dir = await window.marraw!.pickDirectory();
                  if (dir) setDestDir(dir);
                }}
              >
                Browse…
              </Button>
            )}
          </div>

          <div className="flex gap-2">
            <Select value={format} onValueChange={(v) => setFormat(v as ExportFormatType)} disabled={running}>
              <SelectTrigger className="flex-1">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value="jpeg">JPEG</SelectItem>
                  <SelectItem value="tiff16">TIFF (16-bit)</SelectItem>
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
                disabled={running}
                aria-label="JPEG quality"
              />
            )}
            <Select value={String(longEdge)} onValueChange={(v) => setLongEdge(Number(v))} disabled={running}>
              <SelectTrigger className="w-36">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value="0">Full size</SelectItem>
                  <SelectItem value="4096">4096 px</SelectItem>
                  <SelectItem value="2048">2048 px</SelectItem>
                  <SelectItem value="1600">1600 px</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>

          {(running || done > 0) && (
            <div className="flex flex-col gap-1.5">
              <Progress value={(done / ids.length) * 100} />
              <span className="text-xs text-muted-foreground">
                {done} / {ids.length}
                {failed.length > 0 && ` — ${failed.length} failed`}
              </span>
            </div>
          )}
          {failed.length > 0 && (
            <div className="max-h-24 overflow-y-auto rounded border border-destructive/40 p-2 text-xs text-destructive">
              {failed.map((f) => (
                <div key={f} className="truncate">
                  {f}
                </div>
              ))}
            </div>
          )}
        </div>

        <DialogFooter>
          {running ? (
            <Button variant="destructive" onClick={() => abortRef.current?.abort()}>
              Cancel
            </Button>
          ) : (
            <Button onClick={start} disabled={ids.length === 0}>
              Export
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
