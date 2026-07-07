import { useEffect, useState } from 'react';
import { X } from 'lucide-react';
import { toast } from 'sonner';
import { checkDest, startExport, type ColorSpaceType, type ExportFormatType } from '@/api/export';
import { useApiClient } from '@/api/client';
import type { Photo } from '@/api/library';
import { Button } from '@/components/ui/button';
import { Segmented } from '@/components/ui/segmented';
import { Slider } from '@/components/ui/slider';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import { rootName, samePath, useLibraryRoots } from '@/lib/library';
import { useUIStore } from '@/stores/uiStore';
import '@/lib/electron';

const LAST_DIR_KEY = 'marraw.exportDir';

const FORMAT_ITEMS: { value: ExportFormatType; label: string }[] = [
  { value: 'jpeg', label: 'JPEG' },
  { value: 'tiff16', label: '16-bit TIFF' },
];
const COLOR_ITEMS: { value: ColorSpaceType; label: string }[] = [
  { value: 'srgb', label: 'sRGB' },
  { value: 'adobergb', label: 'Adobe RGB' },
  { value: 'prophoto', label: 'ProPhoto' },
];

/**
 * Export dialog (handoff plate "EXPORT"): destination, format, quality,
 * resize, and color space. Export starts a background task and hands you
 * back to work — progress lives in the top-bar task chip.
 */
export function ExportDialog({ photos }: { photos: Photo[] }) {
  const client = useApiClient();
  const open = useUIStore((s) => s.exportOpen);
  const setOpen = useUIStore((s) => s.setExportOpen);
  const selection = useUIStore((s) => s.selection);
  const folderPath = useUIStore((s) => s.folderPath);
  const { roots } = useLibraryRoots();

  const [destDir, setDestDir] = useState('');
  const [format, setFormat] = useState<ExportFormatType>('jpeg');
  const [quality, setQuality] = useState(90);
  const [resize, setResize] = useState<'full' | 'edge'>('full');
  const [edgePx, setEdgePx] = useState(2160);
  const [colorSpace, setColorSpace] = useState<ColorSpaceType>('srgb');
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
  const current = folderPath ? roots.find((r) => samePath(r.path, folderPath)) : undefined;
  const shootName = current ? rootName(current) : folderPath;
  const longEdge = resize === 'edge' ? edgePx : 0;

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
        colorSpace,
        createDir,
      });
      localStorage.setItem(LAST_DIR_KEY, destDir);
      setOpen(false); // progress lives in the top-bar task chip
    } catch (err) {
      toast.error(`Export failed to start: ${(err as Error).message}`);
    } finally {
      setStarting(false);
    }
  };

  const summary = [
    `${ids.length} file${ids.length === 1 ? '' : 's'}`,
    format === 'jpeg' ? `JPEG q${quality}` : '16-bit TIFF',
    resize === 'edge' ? `${edgePx}px` : 'full res',
    ...(colorSpace !== 'srgb' ? [COLOR_ITEMS.find((c) => c.value === colorSpace)!.label] : []),
    'runs in the background',
  ].join(' · ');

  const row = (label: string, control: React.ReactNode) => (
    <div className="flex items-center gap-3.5">
      <span className="w-[110px] shrink-0 text-[13px] text-muted-foreground">{label}</span>
      {control}
    </div>
  );

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent
        showCloseButton={false}
        className="flex w-[680px] max-w-none flex-col gap-0 overflow-hidden rounded-[14px] border-glass-border p-0 sm:max-w-none"
      >
        <div className="flex items-center border-b px-[22px] py-[17px]">
          <div className="flex flex-col gap-0.5">
            <span className="text-base font-semibold">Export</span>
            <span className="font-mono text-[11.5px] text-muted-foreground">
              {selection.size > 0 ? `${ids.length} selected` : `${ids.length} shown`}
              {shootName ? ` · ${shootName}` : ''}
            </span>
          </div>
          <button
            className="ml-auto flex size-7 items-center justify-center rounded-[7px] border text-muted-foreground hover:text-foreground"
            onClick={() => setOpen(false)}
            aria-label="Close"
          >
            <X className="size-3.5" />
          </button>
        </div>

        <div className="flex flex-col gap-4 px-[22px] py-5">
          {row(
            'Destination',
            <>
              <input
                className="flex h-[34px] min-w-0 flex-1 items-center rounded-lg border border-input bg-secondary px-2.5 font-mono text-xs text-secondary-foreground outline-none focus:border-ring dark:bg-white/5"
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
                  className="h-[34px]"
                  onClick={async () => {
                    const dir = await window.marraw!.pickDirectory();
                    if (dir) {
                      setDestDir(dir);
                      setNeedsCreate(false);
                    }
                  }}
                >
                  Choose…
                </Button>
              )}
            </>,
          )}
          {row(
            'Format',
            <Segmented
              aria-label="Format"
              size="sm"
              items={FORMAT_ITEMS}
              value={format}
              onValueChange={setFormat}
              className="border-0 bg-secondary dark:bg-white/5"
            />,
          )}
          {format === 'jpeg' &&
            row(
              'Quality',
              <>
                <Slider
                  className="flex-1"
                  value={quality}
                  min={1}
                  max={100}
                  step={1}
                  onValueChange={(v) => setQuality(v as number)}
                  aria-label="JPEG quality"
                />
                <span className="w-9 text-right font-mono text-[13px] tabular-nums">{quality}</span>
              </>,
            )}
          {row(
            'Resize',
            <>
              <Segmented
                aria-label="Resize"
                size="sm"
                items={[
                  { value: 'full', label: 'Full res' },
                  { value: 'edge', label: 'Long edge' },
                ]}
                value={resize}
                onValueChange={setResize}
                className="border-0 bg-secondary dark:bg-white/5"
              />
              {resize === 'edge' && (
                <div className="flex h-[34px] items-center gap-1.5 rounded-lg border border-input bg-secondary px-2.5 dark:bg-white/5">
                  <input
                    className="w-[46px] bg-transparent text-right font-mono text-xs text-foreground outline-none"
                    value={edgePx}
                    onChange={(e) => {
                      const n = Number(e.target.value);
                      if (Number.isFinite(n)) setEdgePx(Math.max(0, Math.min(65536, Math.round(n))));
                    }}
                    aria-label="Long edge pixels"
                  />
                  <span className="font-mono text-[11px] text-muted-foreground">px</span>
                </div>
              )}
            </>,
          )}
          {row(
            'Color space',
            <Segmented
              aria-label="Color space"
              size="sm"
              items={COLOR_ITEMS}
              value={colorSpace}
              onValueChange={setColorSpace}
              className="border-0 bg-secondary dark:bg-white/5"
            />,
          )}
          {needsCreate && (
            <div className="rounded-lg border border-rating/40 bg-rating/10 p-2.5 text-xs">
              The folder <span className="font-mono">{destDir}</span> does not exist. Create it?
            </div>
          )}
        </div>

        <div className="flex items-center gap-4 border-t px-[22px] py-4">
          <span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-muted-foreground">
            {summary}
          </span>
          {needsCreate ? (
            <>
              <Button variant="outline" size="lg" onClick={() => setNeedsCreate(false)} disabled={starting}>
                Back
              </Button>
              <Button size="lg" onClick={() => start(true)} disabled={starting}>
                Create folder &amp; export
              </Button>
            </>
          ) : (
            <>
              <Button variant="outline" size="lg" onClick={() => setOpen(false)} disabled={starting}>
                Cancel
              </Button>
              <Button size="lg" onClick={() => start(false)} disabled={ids.length === 0 || starting}>
                Export
              </Button>
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
