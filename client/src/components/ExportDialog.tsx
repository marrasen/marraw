import { useEffect, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { toast } from 'sonner';
import {
  type ColorSpaceType,
  type ExifModeType,
  type ExportFormatType,
  type SharpenAmountType,
  type SharpenTargetType,
} from '@/api/api';
import { checkDest, startExport } from '@/api/export';
import { useApiClient } from '@/api/client';
import type { Photo } from '@/api/library';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Segmented } from '@/components/ui/segmented';
import { Slider } from '@/components/ui/slider';
import { Switch } from '@/components/ui/switch';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import { copyPhotoToClipboard } from '@/lib/clipboardExport';
import { rootName, samePath, useLibraryRoots } from '@/lib/library';
import { updateExportDir, updateExportOptions } from '@/lib/uiSettings';
import { useUIStore } from '@/stores/uiStore';
import '@/lib/electron';

const FORMAT_ITEMS: { value: ExportFormatType; label: string }[] = [
  { value: 'jpeg', label: 'JPEG' },
  { value: 'tiff8', label: 'TIFF' },
  { value: 'png', label: 'PNG' },
  { value: 'rawXmp', label: 'RAW + XMP' },
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
const METADATA_ITEMS: { value: ExifModeType; label: string }[] = [
  { value: 'all', label: 'All metadata' },
  { value: 'copyright', label: 'Copyright only' },
  { value: 'none', label: 'None' },
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
  // RAW + XMP copies keep the source's own extension.
  const ext =
    format === 'rawXmp'
      ? (photo?.fileName.match(/\.[^.]+$/)?.[0] ?? '.ARW')
      : format === 'png'
        ? '.png'
        : format === 'tiff8'
          ? '.tif'
          : '.jpg';
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
  const watermarks = useUIStore((s) => s.watermarks);
  const setWatermarkEditorOpen = useUIStore((s) => s.setWatermarkEditorOpen);
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
  const [exifMode, setExifMode] = useState<ExifModeType>('all');
  const [removeLocation, setRemoveLocation] = useState(false);
  const [artist, setArtist] = useState('');
  const [copyright, setCopyright] = useState('');
  const [watermarkId, setWatermarkId] = useState('');
  const [starting, setStarting] = useState(false);
  const [copying, setCopying] = useState(false);
  const [needsCreate, setNeedsCreate] = useState(false);
  // Closing the dialog aborts an in-flight clipboard render — the RPC signal
  // cancels the decode server-side, so it stops burning a core.
  const copyAbort = useRef<AbortController | null>(null);
  useEffect(() => {
    if (!open) copyAbort.current?.abort();
  }, [open]);

  // Prefill from the last-used options when the dialog opens: the previous
  // destination (else "<current folder>\Exports") plus the persisted export
  // options blob.
  useEffect(() => {
    if (!open) return;
    // Prefill the form from the external UI store when the dialog opens —
    // reading getState() during render would be impure, so this stays an effect.
    // eslint-disable-next-line react-hooks/set-state-in-effect
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
    setExifMode(exportOptions.exifMode);
    setRemoveLocation(exportOptions.removeLocation);
    setArtist(exportOptions.artist);
    setCopyright(exportOptions.copyright);
    // A remembered watermark that was deleted since falls back to none.
    const { watermarks } = useUIStore.getState();
    setWatermarkId(
      watermarks.some((w) => w.id === exportOptions.watermarkId) ? exportOptions.watermarkId : '',
    );
  }, [open, folderPath]);

  const ids = selection.size > 0 ? [...selection] : photos.map((p) => p.id);
  const current = folderPath ? roots.find((r) => samePath(r.path, folderPath)) : undefined;
  const shootName = current ? rootName(current) : folderPath;
  const longEdge = resize === 'edge' ? edgePx : 0;
  // RAW + XMP copies the source files and writes .xmp sidecars — nothing
  // renders, so the pixel options (resize, color space, sharpen) hide. Into
  // the photos' own folder, the backend skips the copy and only writes the
  // sidecars next to the originals.
  const isRaw = format === 'rawXmp';
  const inPlace = isRaw && !!folderPath && samePath(destDir, folderPath);

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
        exifMode,
        removeLocation: exifMode === 'all' ? removeLocation : false,
        artist: artist.trim(),
        copyright: copyright.trim(),
        watermarkId: isRaw ? '' : watermarkId,
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
        exifMode,
        // Remembered as toggled even when another mode hides it.
        removeLocation,
        artist: artist.trim(),
        copyright: copyright.trim(),
        watermarkId,
      });
      setOpen(false); // progress lives in the top-bar task chip
    } catch (err) {
      toast.error(`Export failed to start: ${(err as Error).message}`);
    } finally {
      setStarting(false);
    }
  };

  // Renders the single targeted photo server-side and puts it on the system
  // clipboard, honoring the dialog's pixel options (resize, sharpen,
  // watermark) — format/metadata are fixed by the RPC (PNG, sRGB, no EXIF).
  const copy = async () => {
    const ac = new AbortController();
    copyAbort.current = ac;
    setCopying(true);
    try {
      await copyPhotoToClipboard(
        client,
        {
          photoId: ids[0],
          longEdge,
          sharpenTarget,
          sharpenAmount,
          watermarkId: isRaw ? '' : watermarkId,
        },
        ac.signal,
      );
      toast.success('Copied — ready to paste');
      setOpen(false);
    } catch (err) {
      if (!ac.signal.aborted) toast.error(`Copy failed: ${(err as Error).message}`);
    } finally {
      copyAbort.current = null;
      setCopying(false);
    }
  };

  const summary = [
    `${ids.length} file${ids.length === 1 ? '' : 's'}`,
    ...(isRaw
      ? [inPlace ? 'XMP sidecars next to the originals' : 'RAW copies + XMP sidecars']
      : [
          format === 'jpeg'
            ? `JPEG q${quality}`
            : format === 'png'
              ? 'PNG lossless'
              : 'TIFF lossless',
          resize === 'edge' ? `${edgePx}px` : 'full res',
          ...(colorSpace !== 'srgb'
            ? [COLOR_ITEMS.find((c) => c.value === colorSpace)!.label]
            : []),
          ...(sharpenTarget !== 'off' ? [`sharpen ${sharpenTarget}`] : []),
          ...(watermarkId && watermarks.some((w) => w.id === watermarkId)
            ? [`wm ${watermarks.find((w) => w.id === watermarkId)!.name}`]
            : []),
          ...(exifMode === 'none'
            ? ['no metadata']
            : exifMode === 'copyright'
              ? ['© only']
              : removeLocation
                ? ['no location']
                : []),
        ]),
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
              {isRaw && folderPath && !inPlace && (
                <Button
                  variant="outline"
                  className="h-[34px]"
                  onClick={() => {
                    setDestDir(folderPath);
                    setNeedsCreate(false);
                  }}
                >
                  Use current folder
                </Button>
              )}
            </>,
          )}
          {inPlace && (
            <div className="rounded-lg border bg-secondary/50 p-2.5 text-xs text-muted-foreground dark:bg-white/5">
              Destination is the photos&apos; own folder: XMP sidecars are written next to the
              originals and nothing is copied.
            </div>
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
                {isRaw && " · ignored when exporting into the photos' own folder"}
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
          {!isRaw &&
            row(
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
          {!isRaw &&
            row(
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
          {!isRaw &&
            row(
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
          {!isRaw &&
            sharpenTarget !== 'off' &&
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
          {/* The watermark is composited onto the rendered pixels (like
              output sharpening), so it applies to every format but RAW. */}
          {!isRaw &&
            row(
              'Watermark',
              <>
                <DropdownMenu>
                  <DropdownMenuTrigger className="flex h-[34px] items-center gap-2 rounded-lg border border-input bg-secondary px-2.5 text-xs text-secondary-foreground dark:bg-white/5">
                    <span className="max-w-[220px] truncate">
                      {watermarks.find((w) => w.id === watermarkId)?.name ?? 'None'}
                    </span>
                    <span className="text-[10px] opacity-60">▾</span>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="start" className="w-[220px] rounded-[11px] border-glass-border bg-popover/98 p-[7px]">
                    <DropdownMenuItem
                      className="flex h-8 rounded-[7px] px-2.5 text-[13px] text-muted-foreground"
                      onClick={() => setWatermarkId('')}
                    >
                      None
                    </DropdownMenuItem>
                    {watermarks.map((w) => (
                      <DropdownMenuItem
                        key={w.id}
                        className={
                          w.id === watermarkId
                            ? 'flex h-8 rounded-[7px] px-2.5 text-[13px] font-semibold text-foreground'
                            : 'flex h-8 rounded-[7px] px-2.5 text-[13px]'
                        }
                        onClick={() => setWatermarkId(w.id)}
                      >
                        <span className="truncate">{w.name}</span>
                      </DropdownMenuItem>
                    ))}
                  </DropdownMenuContent>
                </DropdownMenu>
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-[34px]"
                  onClick={() => setWatermarkEditorOpen(true)}
                >
                  Edit…
                </Button>
              </>,
            )}
          {/* RAW + XMP copies the source file, whose own EXIF rides along
              untouched — the metadata options only apply to rendered files. */}
          {!isRaw &&
            row(
              'Metadata',
              <>
                <Segmented
                  aria-label="Metadata"
                  size="sm"
                  items={METADATA_ITEMS}
                  value={exifMode}
                  onValueChange={setExifMode}
                  className="border-0 bg-secondary dark:bg-white/5"
                />
                {exifMode === 'all' && (
                  <label className="flex w-fit cursor-pointer items-center gap-2.5 text-[12.5px] text-secondary-foreground">
                    <Switch checked={removeLocation} onCheckedChange={setRemoveLocation} />
                    Remove location info
                  </label>
                )}
              </>,
            )}
          {!isRaw &&
            exifMode !== 'none' &&
            row(
              'Credit',
              <>
                <input
                  className="flex h-[34px] min-w-0 flex-1 items-center rounded-lg border border-input bg-secondary px-2.5 font-mono text-xs text-secondary-foreground outline-none focus:border-ring dark:bg-white/5"
                  placeholder="Artist, e.g. Jane Doe"
                  value={artist}
                  onChange={(e) => setArtist(e.target.value)}
                  maxLength={120}
                  aria-label="Artist"
                />
                <input
                  className="flex h-[34px] min-w-0 flex-1 items-center rounded-lg border border-input bg-secondary px-2.5 font-mono text-xs text-secondary-foreground outline-none focus:border-ring dark:bg-white/5"
                  placeholder="Copyright, e.g. © 2026 Jane Doe"
                  value={copyright}
                  onChange={(e) => setCopyright(e.target.value)}
                  maxLength={120}
                  aria-label="Copyright"
                />
              </>,
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
              {ids.length === 1 && (
                <Button variant="outline" size="lg" onClick={copy} disabled={starting || copying}>
                  {copying ? 'Rendering…' : 'Copy to clipboard'}
                </Button>
              )}
              <Button variant="outline" size="lg" onClick={() => setOpen(false)} disabled={starting}>
                Cancel
              </Button>
              <Button size="lg" onClick={() => start(false)} disabled={ids.length === 0 || starting || copying}>
                Export
              </Button>
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
