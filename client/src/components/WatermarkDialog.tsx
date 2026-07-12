import { useEffect, useMemo, useRef, useState } from 'react';
import { ChevronDown, ChevronUp, Copy, Image as ImageIcon, Plus, Type, X } from 'lucide-react';
import { toast } from 'sonner';
import { addWatermarkAsset, setWatermarks, type Watermark, type WatermarkElement } from '@/api/settings';
import { useApiClient } from '@/api/client';
import type { ApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Segmented } from '@/components/ui/segmented';
import { Slider } from '@/components/ui/slider';
import { watermarkAssetUrl } from '@/lib/backend';
import { updateWatermarks } from '@/lib/uiSettings';
import { cn } from '@/lib/utils';
import { newImageElement, newTextElement, renderWatermark, WATERMARK_LIMITS } from '@/lib/watermarks';
import { ensureWatermarkFonts, WATERMARK_FONTS, watermarkFontFamily } from '@/lib/watermarkFonts';
import { useUIStore } from '@/stores/uiStore';
import '@/lib/electron';

const ANCHORS: WatermarkElement['anchor'][] = [
  'topLeft', 'top', 'topRight',
  'left', 'center', 'right',
  'bottomLeft', 'bottom', 'bottomRight',
];

// Keystrokes inside the editor must not reach the global photo keymap.
const stop = (e: React.KeyboardEvent) => e.stopPropagation();

/**
 * Watermark editor: any number of named watermarks, each a stack of text and
 * image elements with per-element size, anchor, and margin. The preview is a
 * canvas twin of the Go exporter (lib/watermarks.ts mirrors
 * internal/watermark), rendered with the same font files the daemon embeds.
 */
export function WatermarkDialog() {
  const client = useApiClient();
  const open = useUIStore((s) => s.watermarkEditorOpen);
  const setOpen = useUIStore((s) => s.setWatermarkEditorOpen);
  const watermarks = useUIStore((s) => s.watermarks);

  const [selectedId, setSelectedId] = useState<string | null>(null);
  const selected = watermarks.find((w) => w.id === selectedId) ?? watermarks[0];

  // Continuous controls (sliders, typing) write the store instantly and
  // debounce the server write; structural ops flush immediately. Closing the
  // dialog flushes a pending write so nothing is lost.
  const timer = useRef<number | null>(null);
  const flush = () => {
    if (timer.current == null) return;
    window.clearTimeout(timer.current);
    timer.current = null;
    setWatermarks(client, useUIStore.getState().watermarks).catch((err) =>
      console.error('uiSettings write failed:', err),
    );
  };
  const commitDebounced = (next: Watermark[]) => {
    useUIStore.setState({ watermarks: next });
    if (timer.current != null) window.clearTimeout(timer.current);
    timer.current = window.setTimeout(flush, 400);
  };
  const commitNow = (next: Watermark[]) => {
    if (timer.current != null) window.clearTimeout(timer.current);
    timer.current = null;
    updateWatermarks(client, next);
  };
  useEffect(() => {
    if (!open) flush();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const patchSelected = (patch: (w: Watermark) => Watermark, immediate = false) => {
    if (!selected) return;
    const next = watermarks.map((w) => (w.id === selected.id ? patch(w) : w));
    (immediate ? commitNow : commitDebounced)(next);
  };
  const patchElement = (
    id: string,
    patch: (e: WatermarkElement) => WatermarkElement,
    immediate = false,
  ) =>
    patchSelected(
      (w) => ({ ...w, elements: w.elements.map((e) => (e.id === id ? patch(e) : e)) }),
      immediate,
    );

  const addWatermark = () => {
    const wm: Watermark = {
      id: crypto.randomUUID(),
      name: `Watermark ${watermarks.length + 1}`,
      elements: [{ ...newTextElement(), text: 'marraw' }],
    };
    commitNow([...watermarks, wm]);
    setSelectedId(wm.id);
  };
  const duplicateWatermark = (wm: Watermark) => {
    const copy: Watermark = {
      id: crypto.randomUUID(),
      name: `${wm.name} copy`,
      elements: wm.elements.map((e) => ({ ...e, id: crypto.randomUUID() })),
    };
    commitNow([...watermarks, copy]);
    setSelectedId(copy.id);
  };
  const removeWatermark = (id: string) => {
    commitNow(watermarks.filter((w) => w.id !== id));
    if (selectedId === id) setSelectedId(null);
  };

  const pickImage = async (): Promise<{ fileName: string; width: number; height: number } | null> => {
    const path = await window.marraw?.pickImage?.();
    if (!path) return null;
    return addAssetFromPath(client, path);
  };

  const moveElement = (idx: number, dir: -1 | 1) =>
    patchSelected((w) => {
      const els = [...w.elements];
      const j = idx + dir;
      if (j < 0 || j >= els.length) return w;
      [els[idx], els[j]] = [els[j], els[idx]];
      return { ...w, elements: els };
    }, true);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent
        showCloseButton={false}
        className="flex h-[620px] w-[880px] max-w-none flex-col gap-0 overflow-hidden rounded-[14px] border-glass-border p-0 sm:max-w-none"
      >
        <div className="flex items-center border-b px-[22px] py-[15px]">
          <div className="flex flex-col gap-0.5">
            <span className="text-base font-semibold">Watermarks</span>
            <span className="font-mono text-[11.5px] text-muted-foreground">
              composited onto exports · sizes are % of the short edge
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

        <div className="flex min-h-0 flex-1">
          {/* Watermark list */}
          <div className="flex w-[212px] shrink-0 flex-col border-r">
            <div className="min-h-0 flex-1 overflow-y-auto p-2">
              {watermarks.map((wm) => (
                <div
                  key={wm.id}
                  className={cn(
                    'group relative mb-1 w-full rounded-[9px] border px-2.5 py-2 text-left transition-colors',
                    selected?.id === wm.id
                      ? 'border-primary/40 bg-primary/10'
                      : 'border-transparent hover:bg-white/5',
                  )}
                >
                  <button className="w-full text-left" onClick={() => setSelectedId(wm.id)}>
                    <div className="truncate pr-5 text-[13px]">{wm.name}</div>
                    <div className="font-mono text-[10.5px] text-muted-foreground">
                      {wm.elements.length} element{wm.elements.length === 1 ? '' : 's'}
                    </div>
                  </button>
                  <button
                    className="absolute top-2 right-2 hidden size-5 items-center justify-center rounded text-muted-foreground group-hover:flex hover:text-foreground"
                    onClick={() => removeWatermark(wm.id)}
                    aria-label={`Delete ${wm.name}`}
                  >
                    <X className="size-3" />
                  </button>
                </div>
              ))}
              {watermarks.length === 0 && (
                <div className="px-2 py-3 text-xs text-muted-foreground">
                  No watermarks yet — create one to stamp exports with your name or logo.
                </div>
              )}
            </div>
            <div className="border-t p-2">
              <Button size="sm" variant="outline" className="w-full" onClick={addWatermark}>
                <Plus data-icon="inline-start" />
                New watermark
              </Button>
            </div>
          </div>

          {/* Editor */}
          {selected ? (
            <WatermarkEditor
              key={selected.id}
              wm={selected}
              onRename={(name) => patchSelected((w) => ({ ...w, name }))}
              onDuplicate={() => duplicateWatermark(selected)}
              onAddText={() =>
                patchSelected((w) => ({ ...w, elements: [...w.elements, newTextElement()] }), true)
              }
              onAddImage={async () => {
                const info = await pickImage();
                if (!info) return;
                patchSelected(
                  (w) => ({
                    ...w,
                    elements: [...w.elements, newImageElement(info.fileName, info.width, info.height)],
                  }),
                  true,
                );
              }}
              onPatchElement={patchElement}
              onRemoveElement={(id) =>
                patchSelected((w) => ({ ...w, elements: w.elements.filter((e) => e.id !== id) }), true)
              }
              onMoveElement={moveElement}
              client={client}
            />
          ) : (
            <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
              Create a watermark to get started
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

// addAssetFromPath copies a local image into the daemon's asset store.
async function addAssetFromPath(client: ApiClient, path: string) {
  try {
    const info = await addWatermarkAsset(client, path);
    return info;
  } catch (err) {
    toast.error(`Could not add image: ${(err as Error).message}`);
    return null;
  }
}

function WatermarkEditor({
  wm,
  onRename,
  onDuplicate,
  onAddText,
  onAddImage,
  onPatchElement,
  onRemoveElement,
  onMoveElement,
  client,
}: {
  wm: Watermark;
  onRename: (name: string) => void;
  onDuplicate: () => void;
  onAddText: () => void;
  onAddImage: () => void;
  onPatchElement: (
    id: string,
    patch: (e: WatermarkElement) => WatermarkElement,
    immediate?: boolean,
  ) => void;
  onRemoveElement: (id: string) => void;
  onMoveElement: (idx: number, dir: -1 | 1) => void;
  client: ApiClient;
}) {
  const [name, setName] = useState(wm.name);
  const [aspect, setAspect] = useState<'landscape' | 'portrait'>('landscape');

  return (
    <div className="flex min-w-0 flex-1 flex-col">
      <div className="flex items-center gap-2 border-b px-4 py-2.5">
        <input
          className="h-[30px] min-w-0 flex-1 rounded-lg border border-input bg-secondary px-2.5 text-[13px] text-secondary-foreground outline-none focus:border-ring dark:bg-white/5"
          value={name}
          onChange={(e) => {
            setName(e.target.value);
            if (e.target.value.trim()) onRename(e.target.value.trim());
          }}
          onKeyDown={stop}
          aria-label="Watermark name"
        />
        <Button size="sm" variant="ghost" onClick={onDuplicate} title="Duplicate this watermark">
          <Copy data-icon="inline-start" />
          Duplicate
        </Button>
        <Segmented
          aria-label="Preview aspect"
          size="sm"
          items={[
            { value: 'landscape', label: 'Landscape' },
            { value: 'portrait', label: 'Portrait' },
          ]}
          value={aspect}
          onValueChange={setAspect}
          className="border-0 bg-secondary dark:bg-white/5"
        />
      </div>

      <WatermarkPreview wm={wm} aspect={aspect} />

      <div className="flex items-center gap-2 border-t px-4 py-2">
        <span className="text-[11px] tracking-[.06em] text-muted-foreground uppercase">Elements</span>
        <div className="ml-auto flex gap-1.5">
          <Button size="sm" variant="outline" onClick={onAddText}>
            <Type data-icon="inline-start" />
            Text
          </Button>
          {window.marraw?.pickImage && (
            <Button size="sm" variant="outline" onClick={onAddImage}>
              <ImageIcon data-icon="inline-start" />
              Image
            </Button>
          )}
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
        {wm.elements.length === 0 && (
          <div className="py-3 text-xs text-muted-foreground">
            No elements — add a text line or a logo image.
          </div>
        )}
        {wm.elements.map((el, idx) => (
          <ElementEditor
            key={el.id}
            el={el}
            first={idx === 0}
            last={idx === wm.elements.length - 1}
            onPatch={(patch, immediate) => onPatchElement(el.id, patch, immediate)}
            onRemove={() => onRemoveElement(el.id)}
            onMove={(dir) => onMoveElement(idx, dir)}
            client={client}
          />
        ))}
      </div>
    </div>
  );
}

/** Canvas preview — the placeholder frame is drawn in output-image pixel
 * space (backing pixels), so the % math matches an export of that size. */
function WatermarkPreview({ wm, aspect }: { wm: Watermark; aspect: 'landscape' | 'portrait' }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const [fontsReady, setFontsReady] = useState(false);
  const [assetTick, setAssetTick] = useState(0);
  const assetsRef = useRef(new Map<string, HTMLImageElement>());

  useEffect(() => {
    let alive = true;
    ensureWatermarkFonts().then(() => alive && setFontsReady(true));
    return () => {
      alive = false;
    };
  }, []);

  // Load asset bitmaps referenced by the elements; each arrival re-renders.
  useEffect(() => {
    for (const el of wm.elements) {
      if (el.type !== 'image' || !el.asset || assetsRef.current.has(el.asset)) continue;
      const img = new Image();
      img.onload = () => setAssetTick((t) => t + 1);
      img.src = watermarkAssetUrl(el.asset);
      assetsRef.current.set(el.asset, img);
    }
  }, [wm]);

  const box = aspect === 'landscape' ? { w: 384, h: 256 } : { w: 171, h: 256 };

  useEffect(() => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext('2d');
    if (!canvas || !ctx) return;
    const dpr = window.devicePixelRatio || 1;
    const W = Math.round(box.w * dpr);
    const H = Math.round(box.h * dpr);
    canvas.width = W;
    canvas.height = H;
    // Neutral placeholder "photograph": a soft diagonal ramp, dark enough
    // that white text reads, light enough that black does too.
    const g = ctx.createLinearGradient(0, 0, W, H);
    g.addColorStop(0, '#4a5060');
    g.addColorStop(0.55, '#343946');
    g.addColorStop(1, '#23262e');
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, W, H);
    renderWatermark(ctx, wm, W, H, assetsRef.current);
  }, [wm, box.w, box.h, fontsReady, assetTick]);

  return (
    <div className="flex h-[280px] shrink-0 items-center justify-center bg-inset/60 py-3">
      <canvas
        ref={canvasRef}
        style={{ width: box.w, height: box.h }}
        className="rounded-[6px] shadow-[0_2px_16px_rgba(0,0,0,.4)]"
      />
    </div>
  );
}

function ElementEditor({
  el,
  first,
  last,
  onPatch,
  onRemove,
  onMove,
  client,
}: {
  el: WatermarkElement;
  first: boolean;
  last: boolean;
  onPatch: (patch: (e: WatermarkElement) => WatermarkElement, immediate?: boolean) => void;
  onRemove: () => void;
  onMove: (dir: -1 | 1) => void;
  client: ApiClient;
}) {
  const isText = el.type === 'text';
  const fontLabel = useMemo(
    () => WATERMARK_FONTS.find((f) => f.id === el.font)?.label ?? 'Inter',
    [el.font],
  );

  const control = (label: string, node: React.ReactNode) => (
    <div className="flex items-center gap-2.5">
      <span className="w-[52px] shrink-0 text-[11.5px] text-muted-foreground">{label}</span>
      {node}
    </div>
  );

  const replaceImage = async () => {
    const path = await window.marraw?.pickImage?.();
    if (!path) return;
    const info = await addAssetFromPath(client, path);
    if (!info) return;
    onPatch(
      (e) => ({ ...e, asset: info.fileName, assetWidth: info.width, assetHeight: info.height }),
      true,
    );
  };

  return (
    <div
      className="mb-2 rounded-[10px] border bg-secondary/40 p-3 dark:bg-white/[.03]"
      onDragOver={isText ? undefined : (e) => e.preventDefault()}
      onDrop={
        isText
          ? undefined
          : async (e) => {
              e.preventDefault();
              const file = e.dataTransfer?.files?.[0];
              if (!file || !window.marraw) return;
              const info = await addAssetFromPath(client, window.marraw.getPathForFile(file));
              if (!info) return;
              onPatch(
                (x) => ({ ...x, asset: info.fileName, assetWidth: info.width, assetHeight: info.height }),
                true,
              );
            }
      }
    >
      <div className="mb-2.5 flex items-center gap-2">
        {isText ? (
          <Type className="size-3.5 shrink-0 text-muted-foreground" />
        ) : (
          <ImageIcon className="size-3.5 shrink-0 text-muted-foreground" />
        )}
        {isText ? (
          <input
            className="h-[30px] min-w-0 flex-1 rounded-lg border border-input bg-secondary px-2.5 text-[13px] text-secondary-foreground outline-none focus:border-ring dark:bg-white/5"
            style={{ fontFamily: watermarkFontFamily(el.font) }}
            placeholder="Watermark text…"
            value={el.text}
            maxLength={WATERMARK_LIMITS.textMax}
            onChange={(e) => onPatch((x) => ({ ...x, text: e.target.value }))}
            onKeyDown={stop}
            aria-label="Watermark text"
          />
        ) : (
          <>
            {el.asset ? (
              <img
                src={watermarkAssetUrl(el.asset)}
                alt=""
                className="h-[30px] max-w-[120px] rounded border border-input bg-[repeating-conic-gradient(#8883_0_25%,transparent_0_50%)] bg-[length:12px_12px] object-contain px-1"
              />
            ) : (
              <span className="text-xs text-muted-foreground">No image yet</span>
            )}
            {window.marraw?.pickImage && (
              <Button size="sm" variant="outline" onClick={replaceImage}>
                {el.asset ? 'Replace…' : 'Choose image…'}
              </Button>
            )}
          </>
        )}
        <div className="ml-auto flex shrink-0 items-center gap-0.5 text-muted-foreground">
          <button
            className="flex size-6 items-center justify-center rounded hover:text-foreground disabled:opacity-30"
            onClick={() => onMove(-1)}
            disabled={first}
            aria-label="Move element up"
          >
            <ChevronUp className="size-3.5" />
          </button>
          <button
            className="flex size-6 items-center justify-center rounded hover:text-foreground disabled:opacity-30"
            onClick={() => onMove(1)}
            disabled={last}
            aria-label="Move element down"
          >
            <ChevronDown className="size-3.5" />
          </button>
          <button
            className="flex size-6 items-center justify-center rounded hover:text-foreground"
            onClick={onRemove}
            aria-label="Remove element"
          >
            <X className="size-3.5" />
          </button>
        </div>
      </div>

      <div className="flex gap-4">
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          {isText &&
            control(
              'Font',
              <div className="flex min-w-0 flex-1 items-center gap-2">
                <DropdownMenu>
                  <DropdownMenuTrigger className="flex h-[28px] min-w-0 flex-1 items-center justify-between gap-2 rounded-lg border border-input bg-secondary px-2.5 text-xs text-secondary-foreground dark:bg-white/5">
                    <span style={{ fontFamily: watermarkFontFamily(el.font) }}>{fontLabel}</span>
                    <span className="text-[10px] opacity-60">▾</span>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="start" className="w-[200px] rounded-[11px] border-glass-border bg-popover/98 p-[7px]">
                    {WATERMARK_FONTS.map((f) => (
                      <DropdownMenuItem
                        key={f.id}
                        className="flex h-8 rounded-[7px] px-2.5 text-[14px]"
                        style={{ fontFamily: f.family }}
                        onClick={() => onPatch((x) => ({ ...x, font: f.id }), true)}
                      >
                        {f.label}
                      </DropdownMenuItem>
                    ))}
                  </DropdownMenuContent>
                </DropdownMenu>
                <input
                  type="color"
                  value={el.color}
                  onChange={(e) => onPatch((x) => ({ ...x, color: e.target.value }))}
                  className="h-[28px] w-9 shrink-0 cursor-pointer rounded-md border border-input bg-transparent p-0.5"
                  aria-label="Text color"
                />
              </div>,
            )}
          {control(
            'Size',
            <>
              <Slider
                className="flex-1"
                value={el.sizePct}
                min={WATERMARK_LIMITS.sizeMin}
                max={WATERMARK_LIMITS.sizeMax}
                step={0.5}
                onValueChange={(v) => onPatch((x) => ({ ...x, sizePct: v as number }))}
                aria-label="Element size"
              />
              <span className="w-11 text-right font-mono text-[11.5px] tabular-nums">
                {el.sizePct.toFixed(1)}%
              </span>
            </>,
          )}
          {control(
            'Margin',
            <>
              <Slider
                className="flex-1"
                value={el.marginPct}
                min={0}
                max={WATERMARK_LIMITS.marginMax}
                step={0.5}
                onValueChange={(v) => onPatch((x) => ({ ...x, marginPct: v as number }))}
                aria-label="Element margin"
              />
              <span className="w-11 text-right font-mono text-[11.5px] tabular-nums">
                {el.marginPct.toFixed(1)}%
              </span>
            </>,
          )}
          {control(
            'Opacity',
            <>
              <Slider
                className="flex-1"
                value={Math.round(el.opacity * 100)}
                min={5}
                max={100}
                step={5}
                onValueChange={(v) => onPatch((x) => ({ ...x, opacity: (v as number) / 100 }))}
                aria-label="Element opacity"
              />
              <span className="w-11 text-right font-mono text-[11.5px] tabular-nums">
                {Math.round(el.opacity * 100)}%
              </span>
            </>,
          )}
        </div>

        <div className="flex shrink-0 flex-col items-center gap-1">
          <span className="text-[10px] tracking-[.06em] text-muted-foreground uppercase">Anchor</span>
          <div className="grid grid-cols-3 gap-[3px]" role="radiogroup" aria-label="Anchor position">
            {ANCHORS.map((a) => (
              <button
                key={a}
                role="radio"
                aria-checked={el.anchor === a}
                aria-label={`Anchor ${a}`}
                className={cn(
                  'size-[22px] rounded-[5px] border transition-colors',
                  el.anchor === a
                    ? 'border-primary/60 bg-primary/30'
                    : 'border-input bg-secondary hover:bg-white/10 dark:bg-white/5',
                )}
                onClick={() => onPatch((x) => ({ ...x, anchor: a }), true)}
              />
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
