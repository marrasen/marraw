import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { Star, Check, X, Pipette, Undo2, Redo2 } from 'lucide-react';
import type { Photo } from '@/api/library';
import { cn } from '@/lib/utils';
import { applyRating, applyFlag } from '@/lib/actions';
import { applyBatchEdit, type Params } from '@/api/edits';
import { useApiClient, type ApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Slider } from '@/components/ui/slider';
import { Separator } from '@/components/ui/separator';
import { Progress } from '@/components/ui/progress';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { Histogram } from '@/components/Histogram';
import { useUIStore } from '@/stores/uiStore';
import {
  esApplyParams,
  esCanRedo,
  esCanUndo,
  esCommit,
  esLoad,
  esRedo,
  esReset,
  esSetActive,
  esSetApplyIds,
  esSetWBPicking,
  esUndo,
  esUpdate,
  useEditSession,
  type ControlId,
} from '@/lib/editSession';

const HIGHLIGHT_OPTIONS = [
  { value: 0, label: 'Clip' },
  { value: 1, label: 'Unclip' },
  { value: 2, label: 'Blend' },
  { value: 5, label: 'Rebuild' },
];

const FBDD_OPTIONS = [
  { value: 0, label: 'Off' },
  { value: 1, label: 'Light' },
  { value: 2, label: 'Full' },
];

export function EditPanel({ photos }: { photos: Photo[] }) {
  const client = useApiClient();
  const selection = useUIStore((s) => s.selection);
  const focusId = useUIStore((s) => s.focusId);
  const ids = selection.size > 1 ? [...selection] : focusId != null ? [focusId] : [];

  // Open an edit session whenever the focus moves; keep commit targets in
  // sync when only the selection changes.
  useEffect(() => {
    if (focusId != null) void esLoad(client, focusId, ids);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, focusId]);
  const idsKey = ids.join(',');
  useEffect(() => {
    esSetApplyIds(ids);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [idsKey]);

  if (focusId == null) {
    return <div className="p-4 text-sm text-muted-foreground">Select a photo to edit.</div>;
  }
  const photo = photos.find((p) => p.id === focusId);
  return (
    <div className="flex h-full flex-col overflow-y-auto">
      {photo && <PhotoHeader photo={photo} />}
      {photo && <Histogram photo={photo} />}
      <DevelopPanel client={client} targetCount={ids.length} />
      {ids.length > 1 && <BatchSection client={client} ids={ids} />}
    </div>
  );
}

// PhotoHeader shows and edits the cull state of the focused photo — the
// loupe itself stays clean, so stars/flags live here.
function PhotoHeader({ photo }: { photo: Photo }) {
  const client = useApiClient();
  return (
    <div className="flex flex-col gap-2 border-b p-4 text-sm">
      <span className="truncate font-medium" title={photo.fileName}>
        {photo.fileName}
      </span>
      <div className="flex items-center gap-2">
        <div className="flex" role="group" aria-label="Rating">
          {[1, 2, 3, 4, 5].map((n) => (
            <button
              key={n}
              className="p-0.5"
              aria-label={`${n} stars`}
              onClick={() => applyRating(client, [photo.id], photo.rating === n ? 0 : n)}
            >
              <Star
                className={cn(
                  'size-5',
                  n <= photo.rating ? 'fill-amber-400 text-amber-400' : 'text-muted-foreground/40',
                )}
              />
            </button>
          ))}
        </div>
        <ToggleGroup
          size="sm"
          className="ml-auto"
          value={[photo.flag]}
          onValueChange={(groupValue) => {
            const v = ((groupValue as string[])[0] ?? 'none') as Photo['flag'];
            applyFlag(client, [photo.id], v);
          }}
        >
          <ToggleGroupItem value="pick" title="Pick (P)" className="data-pressed:text-emerald-500">
            <Check />
          </ToggleGroupItem>
          <ToggleGroupItem value="exclude" title="Exclude (X)" className="data-pressed:text-red-500">
            <X />
          </ToggleGroupItem>
        </ToggleGroup>
      </div>
      {photo.metaLoaded && (
        <span className="text-xs text-muted-foreground">
          {photo.model} · ISO {photo.iso} · {formatShutter(photo.shutter)} · f/{photo.aperture} ·{' '}
          {Math.round(photo.focalLen)}mm
        </span>
      )}
    </div>
  );
}

function formatShutter(s: number): string {
  if (s <= 0) return '—';
  if (s >= 1) return `${s.toFixed(1)}s`;
  return `1/${Math.round(1 / s)}s`;
}

function DevelopPanel({ client, targetCount }: { client: ApiClient; targetCount: number }) {
  const draft = useEditSession((s) => s.draft);
  const activeControl = useEditSession((s) => s.activeControl);
  const wbPicking = useEditSession((s) => s.wbPicking);
  const canUndo = useEditSession(esCanUndo);
  const canRedo = useEditSession(esCanRedo);
  const setClipboard = useUIStore((s) => s.setClipboard);
  const clipboard = useUIStore((s) => s.clipboard);

  if (!draft) return <div className="p-4 text-sm text-muted-foreground">Loading edits…</div>;

  const update = (patch: Partial<Params>) => esUpdate(client, patch);
  const commit = (patch?: Partial<Params>) => esCommit(client, patch);

  const num = (control: ControlId) => ({
    active: activeControl === control,
    onFocusControl: () => esSetActive(control),
  });

  return (
    <div className="flex flex-col gap-4 p-4 text-sm">
      <div className="flex items-center gap-2">
        <h2 className="font-medium">Develop</h2>
        {targetCount > 1 && (
          <span className="rounded bg-primary/15 px-1.5 py-0.5 text-[11px] text-primary">
            applies to {targetCount} photos
          </span>
        )}
        <span className="ml-auto flex gap-1">
          <Button size="icon-sm" variant="ghost" disabled={!canUndo} onClick={() => esUndo(client)} title="Undo (Ctrl+Z)">
            <Undo2 />
          </Button>
          <Button size="icon-sm" variant="ghost" disabled={!canRedo} onClick={() => esRedo(client)} title="Redo (Ctrl+Y)">
            <Redo2 />
          </Button>
        </span>
      </div>

      <EditSlider
        label="Exposure"
        hotkey="E"
        value={draft.expEV}
        display={`${draft.expEV >= 0 ? '+' : ''}${draft.expEV.toFixed(2)} EV`}
        min={-2}
        max={3}
        step={0.05}
        onChange={(v) => update({ expEV: v })}
        onCommit={(v) => commit({ expEV: v })}
        {...num('expEV')}
      />
      <EditSlider
        label="Preserve highlights"
        value={draft.expPreserve}
        display={draft.expPreserve === 0 ? 'Off' : draft.expPreserve.toFixed(2)}
        min={0}
        max={1}
        step={0.05}
        onChange={(v) => update({ expPreserve: v })}
        onCommit={(v) => commit({ expPreserve: v })}
        {...num('expPreserve')}
      />
      <EditSlider
        label="Brightness"
        hotkey="B"
        value={draft.bright === 0 ? 1 : draft.bright}
        display={`${(draft.bright === 0 ? 1 : draft.bright).toFixed(2)}×`}
        min={0.25}
        max={4}
        step={0.05}
        onChange={(v) => update({ bright: v })}
        onCommit={(v) => commit({ bright: v })}
        {...num('bright')}
      />
      <EditSlider
        label="Gamma"
        hotkey="G"
        value={draft.gamma === 0 ? 2.222 : draft.gamma}
        display={(draft.gamma === 0 ? 2.222 : draft.gamma).toFixed(2)}
        min={1}
        max={3.5}
        step={0.05}
        onChange={(v) => update({ gamma: v })}
        onCommit={(v) => commit({ gamma: v })}
        {...num('gamma')}
      />
      <EditSlider
        label="Shadow slope"
        hotkey="S"
        value={draft.shadow === 0 ? 4.5 : draft.shadow}
        display={(draft.shadow === 0 ? 4.5 : draft.shadow).toFixed(1)}
        min={1}
        max={12}
        step={0.5}
        onChange={(v) => update({ shadow: v })}
        onCommit={(v) => commit({ shadow: v })}
        {...num('shadow')}
      />

      <div className={cn('flex flex-col gap-1.5 rounded-md', activeControl === 'wbMode' && 'ring-2 ring-ring ring-offset-2 ring-offset-background')}>
        <span className="text-xs text-muted-foreground">
          White balance <kbd className="text-[10px] opacity-60">W</kbd>
        </span>
        <div className="flex items-center gap-1.5">
          <ToggleGroup
            className="flex-1"
            // The server normalizes "camera" (the default) to "".
            value={[(draft.wbMode as string) || 'camera']}
            onValueChange={(groupValue) => {
              const v = (groupValue as string[])[0];
              if (!v) return;
              const patch: Partial<Params> =
                v === 'custom'
                  ? { wbMode: 'custom' }
                  : { wbMode: v as Params['wbMode'], wbMul: [0, 0, 0, 0] };
              update(patch);
              commit(patch);
            }}
          >
            <ToggleGroupItem value="camera" className="flex-1">
              As shot
            </ToggleGroupItem>
            <ToggleGroupItem value="auto" className="flex-1">
              Auto
            </ToggleGroupItem>
            <ToggleGroupItem value="custom" className="flex-1" disabled={draft.wbMode !== 'custom'}>
              Picked
            </ToggleGroupItem>
          </ToggleGroup>
          <Button
            size="icon-sm"
            variant={wbPicking ? 'default' : 'outline'}
            title="Pick white balance: click a neutral gray in the image"
            onClick={() => esSetWBPicking(!wbPicking)}
          >
            <Pipette />
          </Button>
        </div>
      </div>

      <EditSlider
        label="Temperature"
        hotkey="T"
        value={draft.wbTemp * 100}
        display={draft.wbTemp === 0 ? '0' : `${draft.wbTemp > 0 ? '+' : ''}${Math.round(draft.wbTemp * 100)}`}
        min={-100}
        max={100}
        step={2}
        disabled={draft.wbMode === 'auto'}
        onChange={(v) => update({ wbTemp: v / 100 })}
        onCommit={(v) => commit({ wbTemp: v / 100 })}
        {...num('wbTemp')}
      />
      <EditSlider
        label="Tint"
        hotkey="I"
        value={draft.wbTint * 100}
        display={draft.wbTint === 0 ? '0' : `${draft.wbTint > 0 ? '+' : ''}${Math.round(draft.wbTint * 100)}`}
        min={-100}
        max={100}
        step={2}
        disabled={draft.wbMode === 'auto'}
        onChange={(v) => update({ wbTint: v / 100 })}
        onCommit={(v) => commit({ wbTint: v / 100 })}
        {...num('wbTint')}
      />

      <ButtonRow
        label="Highlights"
        hotkey="H"
        active={activeControl === 'highlight'}
        options={HIGHLIGHT_OPTIONS}
        value={draft.highlight}
        onChange={(v) => {
          update({ highlight: v });
          commit({ highlight: v });
        }}
      />

      <EditSlider
        label="Noise reduction"
        hotkey="N"
        value={draft.nrThreshold}
        display={draft.nrThreshold === 0 ? 'Off' : String(Math.round(draft.nrThreshold))}
        min={0}
        max={1000}
        step={25}
        onChange={(v) => update({ nrThreshold: v })}
        onCommit={(v) => commit({ nrThreshold: v })}
        {...num('nrThreshold')}
      />

      <ButtonRow
        label="FBDD denoise"
        active={activeControl === 'fbddNoiseRd'}
        options={FBDD_OPTIONS}
        value={draft.fbddNoiseRd}
        onChange={(v) => {
          update({ fbddNoiseRd: v });
          commit({ fbddNoiseRd: v });
        }}
      />

      <EditSlider
        label="Median passes"
        value={draft.medPasses}
        display={draft.medPasses === 0 ? 'Off' : String(draft.medPasses)}
        min={0}
        max={5}
        step={1}
        onChange={(v) => update({ medPasses: v })}
        onCommit={(v) => commit({ medPasses: v })}
        {...num('medPasses')}
      />

      <Separator />

      <div className="flex flex-wrap gap-2">
        <Button
          size="sm"
          variant="outline"
          onClick={() => {
            setClipboard(draft);
            toast.success('Edit settings copied');
          }}
        >
          Copy
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={!clipboard}
          onClick={() => clipboard && esApplyParams(client, clipboard)}
        >
          Paste
        </Button>
        <Button size="sm" variant="outline" onClick={() => esReset(client)}>
          Reset
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        Drag a slider for a live preview; release to save. Press a control's key (E, B, W, …) and
        use +/- to adjust; Esc returns to the image. Ctrl+Z/Ctrl+Y undo/redo per photo.
      </p>
    </div>
  );
}

function ButtonRow({
  label,
  hotkey,
  active,
  options,
  value,
  onChange,
}: {
  label: string;
  hotkey?: string;
  active?: boolean;
  options: { value: number; label: string }[];
  value: number;
  onChange: (v: number) => void;
}) {
  return (
    <div className={cn('flex flex-col gap-1.5 rounded-md', active && 'ring-2 ring-ring ring-offset-2 ring-offset-background')}>
      <span className="text-xs text-muted-foreground">
        {label} {hotkey && <kbd className="text-[10px] opacity-60">{hotkey}</kbd>}
      </span>
      <ToggleGroup
        size="sm"
        className="w-full"
        value={[String(options.some((o) => o.value === value) ? value : options[0].value)]}
        onValueChange={(groupValue) => {
          const v = (groupValue as string[])[0];
          if (v != null) onChange(Number(v));
        }}
      >
        {options.map((o) => (
          <ToggleGroupItem key={o.value} value={String(o.value)} className="flex-1">
            {o.label}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
    </div>
  );
}

export function EditSlider({
  label,
  hotkey,
  value,
  display,
  min,
  max,
  step,
  disabled,
  active,
  onFocusControl,
  onChange,
  onCommit,
}: {
  label: string;
  hotkey?: string;
  value: number;
  display: string;
  min: number;
  max: number;
  step: number;
  disabled?: boolean;
  active?: boolean;
  onFocusControl?: () => void;
  onChange: (v: number) => void;
  onCommit: (v: number) => void;
}) {
  return (
    <div
      className={cn(
        'flex flex-col gap-1.5 rounded-md',
        active && 'ring-2 ring-ring ring-offset-2 ring-offset-background',
        disabled && 'opacity-50',
      )}
      onPointerDown={onFocusControl}
    >
      <div className="flex items-baseline justify-between">
        <span className="text-xs text-muted-foreground">
          {label} {hotkey && <kbd className="text-[10px] opacity-60">{hotkey}</kbd>}
        </span>
        <span className="text-xs tabular-nums">{display}</span>
      </div>
      <Slider
        value={value}
        min={min}
        max={max}
        step={step}
        disabled={disabled}
        onValueChange={(v) => onChange(v as number)}
        onValueCommitted={(v) => onCommit(v as number)}
      />
    </div>
  );
}

// BatchSection offers relative adjustments on top of the absolute controls
// above (which already apply to the whole selection).
function BatchSection({ client, ids }: { client: ApiClient; ids: number[] }) {
  const [ev, setEv] = useState(0.5);
  const [progress, setProgress] = useState<number | null>(null);

  const run = (fn: () => Promise<void>, label: string) => {
    setProgress(0);
    fn()
      .then(() => toast.success(label))
      .catch((err) => toast.error((err as Error).message))
      .finally(() => setProgress(null));
  };

  return (
    <div className="flex flex-col gap-3 border-t p-4 text-sm">
      <h3 className="text-xs font-medium text-muted-foreground">Relative batch adjustment</h3>
      <EditSlider
        label="Exposure adjustment"
        value={ev}
        display={`${ev >= 0 ? '+' : ''}${ev.toFixed(2)} EV`}
        min={-2}
        max={2}
        step={0.25}
        onChange={setEv}
        onCommit={setEv}
      />
      <Button
        size="sm"
        disabled={progress != null || ev === 0}
        onClick={() =>
          run(
            () =>
              applyBatchEdit(
                client,
                ids,
                { expEV: ev, bright: null, highlight: null, nrThreshold: null, fbddNoiseRd: null, medPasses: null },
                { onProgress: (cur, total) => setProgress((cur / total) * 100) },
              ),
            `Applied ${ev > 0 ? '+' : ''}${ev} EV to ${ids.length} photos`,
          )
        }
      >
        Apply {ev >= 0 ? '+' : ''}
        {ev.toFixed(2)} EV to {ids.length} photos
      </Button>
      {progress != null && <Progress value={progress} />}
    </div>
  );
}
