import { useEffect, useState } from 'react';
import { X } from 'lucide-react';
import { toast } from 'sonner';
import {
  type ColorSpaceType,
  type ExportFormatType,
  type SharpenAmountType,
  type SharpenTargetType,
} from '@/api/api';
import { checkDest, startExport } from '@/api/export';
import { useApiClient } from '@/api/client';
import type { Photo } from '@/api/library';
import { Button } from '@/components/ui/button';
import { Segmented } from '@/components/ui/segmented';
import { Slider } from '@/components/ui/slider';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import { rootName, samePath, useLibraryRoots } from '@/lib/library';
import { updateExportDir, updateExportOptions } from '@/lib/uiSettings';
import { useUIStore } from '@/stores/uiStore';
import '@/lib/electron';

const FORMAT_ITEMS: { value: ExportFormatType; label: string }[] = [
  { value: 'jpeg', label: 'JPEG' },
  { value: 'tiff8', label: 'TIFF' },
  { value: 'png', label: 'PNG' },
];
const COLOR_ITEMS: { value: ColorSpaceType; label: string }[] = [
  { value: 'srgb', label: 'sRGB' },
  { value: 'adobergb', label: 'Adobe RGB' },
  { value: 'prophoto', label: 'ProPhoto' },
];
const SHARPEN_TARGET_ITEMS: { value: SharpenTargetType; label: string }[] = [
  { value: 'off', label: 'Off' },
  { value: 'screen', label: 'Screen' },
  { value: 'matte', label: 'Matte' },
  { value: 'glossy', label: 'Glossy' },
];
const SHARPEN_AMOUNT_ITEMS: { value: SharpenAmountType; label: string }[] = [
  { value: 'low', label: 'Low' },
  { value: 'standard', label: 'Standard' },
  { value: 'high', label: 'High' },
];

// Mirrors the backend's namer (internal/export): {name} source file name,
// {seq} batch position, {date}/{time} capture stamp — for the live example
// only; the backend expansion is the authority.
function exampleFileName(
  template: string,
  photo: Photo | undefined,
  total: number,
  format: ExportFormatType,
): string {
  const name = photo ? photo.fileName.replace(/\.[^.]+$/, '') : 'DSC00001';
  const pad = (n: number, w: number) => String(n).padStart(w, '0');
  let date = '';
  let time = '';
  if (photo && photo.takenAt > 0) {
    const d = new Date(photo.takenAt * 1000);
    date = `${d.getFullYear()}${pad(d.getMonth() + 1, 2)}${pad(d.getDate(), 2)}`;
    time = `${pad(d.getHours(), 2)}${pad(d.getMinutes(), 2)}${pad(d.getSeconds(), 2)}`;
  }
  const base = (template.trim() || '{name}')
    .replaceAll('{name}', name)
    .replaceAll('{seq}', pad(1, Math.max(3, String(total).length)))
    .replaceAll('{date}', date)
    .replaceAll('{time}', time)
    .replace(/[<>:"/\\|?*]/g, '-')
    .replace(/[. ]+$/, '');
  const ext = format === 'png' ? '.png' : format === 'tiff8' ? '.tif' : '.jpg';
  return (base || name) + ext;
}

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
  const [fileTemplate, setFileTemplate] = useState('');
  const [format, setFormat] = useState<ExportFormatType>('jpeg');
  const [quality, setQuality] = useState(90);
  const [resize, setResize] = useState<'full' | 'edge'>('full');
  const [edgePx, setEdgePx] = useState(2160);
  const [colorSpace, setColorSpace] = useState<ColorSpaceType>('srgb');
  const [sharpenTarget, setSharpenTarget] = useState<SharpenTargetType>('off');
  const [sharpenAmount, setSharpenAmount] = useState<SharpenAmountType>('standard');
  const [starting, setStarting] = useState(false);
  const [needsCreate, setNeedsCreate] = useState(false);

  // Prefill from the last-used options when the dialog opens: the previous
  // destination (else "<current folder>\Exports") plus the persisted export
  // options blob.
  useEffect(() => {
    if (!open) return;
    setNeedsCreate(false);
    // Read imperatively: a subscription echo must not clobber a value the
    // user is editing while the dialog is open.
    const { exportDir, exportOptions } = useUIStore.getState();
    setDestDir(exportDir || (folderPath ? `${folderPath}\\Exports` : ''));
    setFileTemplate(exportOptions.fileNameTemplate);
    setFormat(exportOptions.format);
    setQuality(exportOptions.jpegQuality);
    setResize(exportOptions.resizeMode === 'edge' ? 'edge' : 'full');
    setEdgePx(exportOptions.edgePx);
    setColorSpace(exportOptions.colorSpace);
    setSharpenTarget(exportOptions.sharpenTarget);
    setSharpenAmount(exportOptions.sharpenAmount);
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
        sharpenTarget,
        sharpenAmount,
        fileNameTemplate: fileTemplate.trim(),
        createDir,
      });
      updateExportDir(client, destDir);
      updateExportOptions(client, {
        format,
        jpegQuality: quality,
        resizeMode: resize,
        edgePx,
        colorSpace,
        sharpenTarget,
        sharpenAmount,
        fileNameTemplate: fileTemplate.trim(),
      });
      setOpen(false); // progress lives in the top-bar task chip
    } catch (err) {
      toast.error(`Export failed to start: ${(err as Error).message}`);
    } finally {
      setStarting(false);
    }
  };

  const summary = [
    `${ids.length} file${ids.length === 1 ? '' : 's'}`,
    format === 'jpeg' ? `JPEG q${quality}` : format === 'png' ? 'PNG lossless' : 'TIFF lossless',
    resize === 'edge' ? `${edgePx}px` : 'full res',
    ...(colorSpace !== 'srgb' ? [COLOR_ITEMS.find((c) => c.value === colorSpace)!.label] : []),
    ...(sharpenTarget !== 'off' ? [`sharpen ${sharpenTarget}`] : []),
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
            'File name',
            <div className="flex min-w-0 flex-1 flex-col gap-[5px]">
              <input
                className="flex h-[34px] items-center rounded-lg border border-input bg-secondary px-2.5 font-mono text-xs text-secondary-foreground outline-none focus:border-ring dark:bg-white/5"
                placeholder="{name}"
                value={fileTemplate}
                onChange={(e) => setFileTemplate(e.target.value)}
                aria-label="File name template"
              />
              <span className="font-mono text-[10.5px] text-muted-foreground">
                {'{name}'} source · {'{seq}'} number · {'{date}'} {'{time}'} captured — e.g.{' '}
                {exampleFileName(fileTemplate, photos.find((p) => p.id === ids[0]), ids.length, format)}
              </span>
            </div>,
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
          {/* Output sharpening is a property of the final render, not of the
              container, so it applies to TIFF too. */}
          {row(
            'Sharpen for',
            <Segmented
              aria-label="Sharpen for"
              size="sm"
              items={SHARPEN_TARGET_ITEMS}
              value={sharpenTarget}
              onValueChange={setSharpenTarget}
              className="border-0 bg-secondary dark:bg-white/5"
            />,
          )}
          {sharpenTarget !== 'off' &&
            row(
              'Amount',
              <Segmented
                aria-label="Sharpen amount"
                size="sm"
                items={SHARPEN_AMOUNT_ITEMS}
                value={sharpenAmount}
                onValueChange={setSharpenAmount}
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
