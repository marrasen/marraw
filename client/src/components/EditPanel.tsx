import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { Star, Check, X, Pipette, Undo2, Redo2, Crop } from 'lucide-react';
import type { Photo } from '@/api/library';
import { cn } from '@/lib/utils';
import { applyRating, applyFlag } from '@/lib/actions';
import { applyBatchEdit, type Delta, type Params } from '@/api/edits';
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
  esSetCropping,
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

// "auto" stands in for the stored "" default (AHD, with the faster PPG at
// interactive 1:1) — Radix toggle items cannot carry an empty value.
const DEMOSAIC_OPTIONS = [
  { value: 'auto', label: 'Auto' },
  { value: 'vng', label: 'VNG' },
  { value: 'ppg', label: 'PPG' },
  { value: 'ahd', label: 'AHD' },
  { value: 'dht', label: 'DHT' },
];

// Display for the ±1 sliders shown as ±100.
const pct = (v: number) => (v === 0 ? '0' : `${v > 0 ? '+' : ''}${Math.round(v * 100)}`);

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
  const cropping = useEditSession((s) => s.cropping);
  const canUndo = useEditSession(esCanUndo);
  const canRedo = useEditSession(esCanRedo);
  const setClipboard = useUIStore((s) => s.setClipboard);
  const clipboard = useUIStore((s) => s.clipboard);
  const setView = useUIStore((s) => s.setView);

  if (!draft) return <div className="p-4 text-sm text-muted-foreground">Loading edits…</div>;

  const update = (patch: Partial<Params>) => esUpdate(client, patch);
  const commit = (patch?: Partial<Params>) => esCommit(client, patch);

  const num = (control: ControlId) => ({
    active: activeControl === control,
    onFocusControl: () => esSetActive(control),
  });

  const kelvinMode = draft.wbMode === 'kelvin';
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

      <Section>Crop &amp; straighten</Section>
      <Button
        size="sm"
        variant={cropping ? 'default' : 'outline'}
        className="justify-start"
        onClick={() => {
          // The overlay lives in the loupe, so entering crop opens it.
          if (!cropping) setView('loupe');
          esSetCropping(client, !cropping);
        }}
        title="Crop &amp; straighten (R)"
      >
        <Crop data-icon="inline-start" />
        {cropping ? 'Done cropping' : 'Crop'}
        <kbd className="ml-auto text-[10px] opacity-60">R</kbd>
      </Button>
      <EditSlider
        label="Straighten"
        value={draft.cropAngle}
        display={draft.cropAngle === 0 ? '0°' : `${draft.cropAngle > 0 ? '+' : ''}${draft.cropAngle.toFixed(1)}°`}
        min={-15}
        max={15}
        step={0.1}
        onChange={(v) => update({ cropAngle: v })}
        onCommit={(v) => commit({ cropAngle: v })}
        active={activeControl === 'cropAngle'}
        // Straightening opens the crop overlay, where the angle previews as an
        // instant client-side rotation instead of a backend re-render.
        onFocusControl={() => {
          esSetActive('cropAngle');
          if (!cropping) {
            setView('loupe');
            esSetCropping(client, true);
          }
        }}
      />

      <Section>Tone</Section>
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
      <PctSlider label="Contrast" hotkey="C" field="contrast" draft={draft} update={update} commit={commit} {...num('contrast')} />
      <PctSlider label="Whites" field="whites" draft={draft} update={update} commit={commit} {...num('whites')} />
      <PctSlider label="Blacks" field="blacks" draft={draft} update={update} commit={commit} {...num('blacks')} />
      <PctSlider label="Shadows" field="toneShadows" draft={draft} update={update} commit={commit} {...num('toneShadows')} />
      <PctSlider label="Highlights" field="toneHighlights" draft={draft} update={update} commit={commit} {...num('toneHighlights')} />

      <Section>White balance</Section>
      <div className={cn('flex flex-col gap-1.5 rounded-md', activeControl === 'wbMode' && 'ring-2 ring-ring ring-offset-2 ring-offset-background')}>
        <span className="text-xs text-muted-foreground">
          Mode <kbd className="text-[10px] opacity-60">W</kbd>
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
                  : v === 'kelvin'
                    ? { wbMode: 'kelvin', wbKelvin: draft.wbKelvin || 5500, wbMul: [0, 0, 0, 0] }
                    : { wbMode: v as Params['wbMode'], wbKelvin: 0, wbMul: [0, 0, 0, 0] };
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
            <ToggleGroupItem value="kelvin" className="flex-1">
              Kelvin
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

      {kelvinMode ? (
        <EditSlider
          label="Temperature"
          hotkey="K"
          value={draft.wbKelvin === 0 ? 5500 : draft.wbKelvin}
          display={`${Math.round(draft.wbKelvin === 0 ? 5500 : draft.wbKelvin)} K`}
          min={2000}
          max={12000}
          step={50}
          onChange={(v) => update({ wbKelvin: v })}
          onCommit={(v) => commit({ wbKelvin: v })}
          {...num('wbKelvin')}
        />
      ) : (
        <EditSlider
          label="Temperature"
          hotkey="T"
          value={draft.wbTemp * 100}
          display={pct(draft.wbTemp)}
          min={-100}
          max={100}
          step={2}
          disabled={draft.wbMode === 'auto'}
          onChange={(v) => update({ wbTemp: v / 100 })}
          onCommit={(v) => commit({ wbTemp: v / 100 })}
          {...num('wbTemp')}
        />
      )}
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

      <Section>Color</Section>
      <PctSlider label="Saturation" hotkey="A" field="saturation" draft={draft} update={update} commit={commit} {...num('saturation')} />
      <PctSlider label="Vibrance" hotkey="V" field="vibrance" draft={draft} update={update} commit={commit} {...num('vibrance')} />
      <HueSlider label="Shadow tint" field="splitShadowHue" draft={draft} update={update} commit={commit} {...num('splitShadowHue')} />
      <AmtSlider label="Shadow tint amount" field="splitShadowAmt" draft={draft} update={update} commit={commit} {...num('splitShadowAmt')} />
      <HueSlider label="Highlight tint" field="splitHighlightHue" draft={draft} update={update} commit={commit} {...num('splitHighlightHue')} />
      <AmtSlider label="Highlight tint amount" field="splitHighlightAmt" draft={draft} update={update} commit={commit} {...num('splitHighlightAmt')} />

      <Section>Effects</Section>
      <PctSlider label="Vignette" hotkey="O" field="vignette" draft={draft} update={update} commit={commit} {...num('vignette')} />

      <Section>Detail</Section>
      <ButtonRow
        label="Highlight recovery"
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

      <ButtonRow
        label="Demosaic"
        hotkey="D"
        active={activeControl === 'demosaic'}
        options={DEMOSAIC_OPTIONS}
        // Same generated-union lie as wbMode: the stored default is "".
        value={(draft.demosaic as string) || 'auto'}
        onChange={(v) => {
          const patch = { demosaic: (v === 'auto' ? '' : v) as Params['demosaic'] };
          update(patch);
          commit(patch);
        }}
      />
      <PctSlider label="CA red/cyan" field="caRed" draft={draft} update={update} commit={commit} {...num('caRed')} />
      <PctSlider label="CA blue/yellow" field="caBlue" draft={draft} update={update} commit={commit} {...num('caBlue')} />

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

function Section({ children }: { children: React.ReactNode }) {
  return <h3 className="-mb-2 mt-1 text-xs font-medium text-muted-foreground/80">{children}</h3>;
}

// The ±1 params rendered as ±100 sliders share everything but the field.
type PctField =
  | 'contrast'
  | 'whites'
  | 'blacks'
  | 'toneShadows'
  | 'toneHighlights'
  | 'saturation'
  | 'vibrance'
  | 'vignette'
  | 'caRed'
  | 'caBlue';

function PctSlider({
  label,
  hotkey,
  field,
  draft,
  update,
  commit,
  active,
  onFocusControl,
}: {
  label: string;
  hotkey?: string;
  field: PctField;
  draft: Params;
  update: (patch: Partial<Params>) => void;
  commit: (patch?: Partial<Params>) => void;
  active?: boolean;
  onFocusControl?: () => void;
}) {
  return (
    <EditSlider
      label={label}
      hotkey={hotkey}
      value={draft[field] * 100}
      display={pct(draft[field])}
      min={-100}
      max={100}
      step={2}
      onChange={(v) => update({ [field]: v / 100 })}
      onCommit={(v) => commit({ [field]: v / 100 })}
      active={active}
      onFocusControl={onFocusControl}
    />
  );
}

function HueSlider({
  label,
  field,
  draft,
  update,
  commit,
  active,
  onFocusControl,
}: {
  label: string;
  field: 'splitShadowHue' | 'splitHighlightHue';
  draft: Params;
  update: (patch: Partial<Params>) => void;
  commit: (patch?: Partial<Params>) => void;
  active?: boolean;
  onFocusControl?: () => void;
}) {
  return (
    <EditSlider
      label={label}
      value={draft[field]}
      display={`${Math.round(draft[field])}°`}
      min={0}
      max={359}
      step={5}
      onChange={(v) => update({ [field]: v })}
      onCommit={(v) => commit({ [field]: v })}
      active={active}
      onFocusControl={onFocusControl}
    />
  );
}

function AmtSlider({
  label,
  field,
  draft,
  update,
  commit,
  active,
  onFocusControl,
}: {
  label: string;
  field: 'splitShadowAmt' | 'splitHighlightAmt';
  draft: Params;
  update: (patch: Partial<Params>) => void;
  commit: (patch?: Partial<Params>) => void;
  active?: boolean;
  onFocusControl?: () => void;
}) {
  return (
    <EditSlider
      label={label}
      value={draft[field] * 100}
      display={draft[field] === 0 ? 'Off' : String(Math.round(draft[field] * 100))}
      min={0}
      max={100}
      step={2}
      onChange={(v) => update({ [field]: v / 100 })}
      onCommit={(v) => commit({ [field]: v / 100 })}
      active={active}
      onFocusControl={onFocusControl}
    />
  );
}

function ButtonRow<V extends string | number>({
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
  options: { value: V; label: string }[];
  value: V;
  onChange: (v: V) => void;
}) {
  const selected = options.some((o) => o.value === value) ? value : options[0].value;
  return (
    <div className={cn('flex flex-col gap-1.5 rounded-md', active && 'ring-2 ring-ring ring-offset-2 ring-offset-background')}>
      <span className="text-xs text-muted-foreground">
        {label} {hotkey && <kbd className="text-[10px] opacity-60">{hotkey}</kbd>}
      </span>
      <ToggleGroup
        size="sm"
        className="w-full"
        value={[String(selected)]}
        onValueChange={(groupValue) => {
          const v = (groupValue as string[])[0];
          const opt = options.find((o) => String(o.value) === v);
          if (opt) onChange(opt.value);
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
  // During a drag the thumb tracks a local value, so it stays smooth even
  // while the store update (which re-renders the whole panel) is coalesced to
  // one frame. `dragging === null` means idle → follow the prop.
  const [dragging, setDragging] = useState<number | null>(null);
  const shown = dragging ?? value;
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
        value={shown}
        min={min}
        max={max}
        step={step}
        disabled={disabled}
        onValueChange={(v) => {
          setDragging(v as number);
          onChange(v as number);
        }}
        onValueCommitted={(v) => {
          setDragging(null);
          onCommit(v as number);
        }}
      />
    </div>
  );
}

// A Delta with every field untouched; spread and override to build one.
const NULL_DELTA: Delta = {
  expEV: null,
  bright: null,
  highlight: null,
  nrThreshold: null,
  fbddNoiseRd: null,
  medPasses: null,
  contrast: null,
  whites: null,
  blacks: null,
  toneShadows: null,
  toneHighlights: null,
  saturation: null,
  vibrance: null,
};

// BatchSection offers relative adjustments on top of the absolute controls
// above (which already apply to the whole selection).
function BatchSection({ client, ids }: { client: ApiClient; ids: number[] }) {
  const [ev, setEv] = useState(0.5);
  const [contrast, setContrast] = useState(0);
  const [saturation, setSaturation] = useState(0);
  const [progress, setProgress] = useState<number | null>(null);

  const run = (fn: () => Promise<void>, label: string) => {
    setProgress(0);
    fn()
      .then(() => toast.success(label))
      .catch((err) => toast.error((err as Error).message))
      .finally(() => setProgress(null));
  };

  const noChange = ev === 0 && contrast === 0 && saturation === 0;
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
      <EditSlider
        label="Contrast adjustment"
        value={contrast * 100}
        display={pct(contrast)}
        min={-100}
        max={100}
        step={2}
        onChange={(v) => setContrast(v / 100)}
        onCommit={(v) => setContrast(v / 100)}
      />
      <EditSlider
        label="Saturation adjustment"
        value={saturation * 100}
        display={pct(saturation)}
        min={-100}
        max={100}
        step={2}
        onChange={(v) => setSaturation(v / 100)}
        onCommit={(v) => setSaturation(v / 100)}
      />
      <Button
        size="sm"
        disabled={progress != null || noChange}
        onClick={() =>
          run(
            () =>
              applyBatchEdit(
                client,
                ids,
                {
                  ...NULL_DELTA,
                  expEV: ev === 0 ? null : ev,
                  contrast: contrast === 0 ? null : contrast,
                  saturation: saturation === 0 ? null : saturation,
                },
                { onProgress: (cur, total) => setProgress((cur / total) * 100) },
              ),
            `Adjusted ${ids.length} photos`,
          )
        }
      >
        Apply to {ids.length} photos
      </Button>
      {progress != null && <Progress value={progress} />}
    </div>
  );
}
