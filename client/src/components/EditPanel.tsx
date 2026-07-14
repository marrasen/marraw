import { useEffect, useMemo, useRef, useState } from 'react';
import { toast } from 'sonner';
import {
  Pipette, Undo2, Redo2, Crop, ChevronRight, Info, RotateCcw,
  Image as ImageIcon, Plus, Trash2, Paintbrush, Circle, Eraser,
  Focus, Layers, Loader2, Shapes,
} from 'lucide-react';
import { useFolderScan } from '@/lib/useFolderScan';
import type { Photo } from '@/api/library';
import { cn } from '@/lib/utils';
import { applyRating, applyFlag } from '@/lib/actions';
// (aprot's camelCasing lowercases exactly one leading character: aIModelStatus.)
import { aIModelStatus as aiModelStatus, applyBatchEdit, generateAIMap, type AIMapResult, type Delta } from '@/api/edits';
import { AIModelDialog, type PendingAIDownload } from '@/components/AIModelDialog';
import type { AIKindType, Mask, MaskAdjust, Params } from '@/api/edit';
import {
  DEPTH_WINDOW_DEFAULT,
  MASK_CONTROL_ORDER,
  MASK_CONTROL_SPECS,
  aiClassMask,
  aiMask,
  maskAdjustIsNeutral,
  maskLabel,
  type MaskControlId,
} from '@/lib/controlSpecs';
import { useApiClient, type ApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Slider } from '@/components/ui/slider';
import { Segmented } from '@/components/ui/segmented';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { Histogram } from '@/components/Histogram';
import { PresetsPanel } from '@/components/PresetsPanel';
import { InfoPanel } from '@/components/InfoPanel';
import { formatAperture, formatShutter } from '@/lib/exif';
import { updateEditGroupOpen } from '@/lib/uiSettings';
import { bumpImgBust } from '@/lib/imgCacheBust';
import { useUIStore } from '@/stores/uiStore';
import {
  esAddMask,
  esAddMaskObject,
  esAuto,
  esCanRedo,
  esCanUndo,
  esCommit,
  esLoad,
  esRedo,
  esRemoveMask,
  esSetActive,
  esSetActiveMask,
  esSetActiveMaskControl,
  esSetTintMask,
  esSetApplyIds,
  esSetBrushTool,
  esSetCropping,
  esSetMaskPaint,
  esSetWBPicking,
  esUndo,
  esUpdate,
  esUpdateMask,
  esWBPickDone,
  useEditSession,
  NEUTRAL,
  type ControlId,
  type GroupId,
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

// WB dial gradient tracks per the handoff eyedropper plate.
const TEMP_GRADIENT = 'bg-gradient-to-r from-[#6fa8ff] via-[#e9e3d0] to-[#ffb066]';
const TINT_GRADIENT = 'bg-gradient-to-r from-[#5cd06e] via-[#d9d9d9] to-[#c86fd0]';

export function EditPanel({ photos }: { photos: Photo[] }) {
  const client = useApiClient();
  const selection = useUIStore((s) => s.selection);
  const focusId = useUIStore((s) => s.focusId);
  const ids = selection.size > 1 ? [...selection] : focusId != null ? [focusId] : [];

  // Open an edit session whenever the focus moves; keep commit targets in
  // sync when only the selection changes. A remount with the session already
  // on the focused photo (Library aside ⇄ Develop drawer swap on mode
  // switches) must NOT reload: esLoad resets overlay state, which would kill
  // the crop overlay in the very click that opened it from Library.
  useEffect(() => {
    if (focusId != null && useEditSession.getState().photoId !== focusId)
      void esLoad(client, focusId, ids);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, focusId]);
  const idsKey = ids.join(',');
  useEffect(() => {
    esSetApplyIds(ids);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [idsKey]);

  if (focusId == null) {
    return <PanelPlaceholder />;
  }
  // A multi-photo selection swaps the panel for relative adjustment: deltas
  // that add to each photo's own current values (handoff "BATCH"). Keyed by
  // the selection so a different set starts from zero deltas.
  if (ids.length > 1) {
    return <BatchSection key={ids.join(',')} client={client} ids={ids} />;
  }
  const photo = photos.find((p) => p.id === focusId);
  return <SinglePhotoPanel client={client} photo={photo} targetCount={ids.length} />;
}

// SinglePhotoPanel: the identity/cull header, then the Develop / Presets /
// Info tab strip and its content. Tab state is client-only (uiStore) so it
// persists across the two mount sites (Develop drawer ⇄ Library aside).
const TAB_ITEMS = [
  { value: 'develop' as const, label: 'Develop' },
  { value: 'masks' as const, label: 'Masks' },
  { value: 'presets' as const, label: 'Presets' },
  { value: 'info' as const, label: 'Info' },
];

function SinglePhotoPanel({
  client,
  photo,
  targetCount,
}: {
  client: ApiClient;
  photo?: Photo;
  targetCount: number;
}) {
  const tab = useUIStore((s) => s.developTab);
  const setTab = useUIStore((s) => s.setDevelopTab);
  return (
    <div className="flex h-full flex-col overflow-hidden">
      {photo && <PhotoHeader photo={photo} />}
      <div className="px-4 pt-[11px] pb-1">
        <Segmented size="sm" aria-label="Panel" value={tab} onValueChange={setTab} items={TAB_ITEMS} />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {tab === 'develop' && (
          <>
            {photo && <Histogram photo={photo} />}
            <DevelopPanel client={client} photo={photo} targetCount={targetCount} />
          </>
        )}
        {tab === 'masks' && (
          <>
            {photo && <Histogram photo={photo} />}
            <MasksPanel client={client} targetCount={targetCount} />
          </>
        )}
        {tab === 'presets' && <PresetsPanel client={client} photo={photo} targetCount={targetCount} />}
        {tab === 'info' && photo && <InfoPanel photo={photo} />}
      </div>
    </div>
  );
}

// PanelPlaceholder: nothing focused yet (handoff "SCANNING" right panel).
function PanelPlaceholder() {
  const folderPath = useUIStore((s) => s.folderPath);
  const scan = useFolderScan(folderPath);
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 p-8 text-center">
      <div className="flex size-11 items-center justify-center rounded-xl border bg-black/5 dark:bg-white/3">
        <ImageIcon className="size-5 text-faint" strokeWidth={1.4} />
      </div>
      <span className="text-[12.5px] leading-normal text-faint">
        {scan ? (
          <>
            Select a photo to develop
            <br />
            once previews are ready
          </>
        ) : (
          'Select a photo to develop.'
        )}
      </span>
    </div>
  );
}

// PhotoHeader shows and edits the cull state of the focused photo — the
// loupe itself stays clean, so stars/flags live here. Styled per the
// handoff Library plate: mono filename, 24px P/X squares, amber star row,
// mono EXIF line.
function PhotoHeader({ photo }: { photo: Photo }) {
  const client = useApiClient();
  const displayName = photo.fileName.split(/[\\/]/).pop() ?? photo.fileName;
  return (
    <div className="flex flex-col border-b px-4 pt-[15px] pb-[13px]">
      <div className="flex items-center justify-between gap-2">
        <span className="truncate font-mono text-[12.5px]" title={photo.fileName}>
          {displayName}
        </span>
        <div className="flex shrink-0 gap-1.5" role="group" aria-label="Flag">
          <button
            title="Pick (P)"
            aria-pressed={photo.flag === 'pick'}
            className={cn(
              'flex size-6 items-center justify-center rounded-md border text-[11px] font-semibold',
              photo.flag === 'pick'
                ? 'border-success/45 bg-success/15 text-success-text'
                : 'border-border text-muted-foreground hover:text-foreground',
            )}
            onClick={() => applyFlag(client, [photo.id], photo.flag === 'pick' ? 'none' : 'pick')}
          >
            P
          </button>
          <button
            title="Exclude (X)"
            aria-pressed={photo.flag === 'exclude'}
            className={cn(
              'flex size-6 items-center justify-center rounded-md border text-[11px] font-semibold',
              photo.flag === 'exclude'
                ? 'border-destructive/45 bg-destructive/15 text-danger-text'
                : 'border-border text-muted-foreground hover:text-foreground',
            )}
            onClick={() =>
              applyFlag(client, [photo.id], photo.flag === 'exclude' ? 'none' : 'exclude')
            }
          >
            X
          </button>
        </div>
      </div>
      <div className="mt-2 flex" role="group" aria-label="Rating">
        {[1, 2, 3, 4, 5].map((n) => (
          <button
            key={n}
            className="pr-1 text-base leading-none"
            aria-label={`${n} stars`}
            onClick={() => applyRating(client, [photo.id], photo.rating === n ? 0 : n)}
          >
            <span className={n <= photo.rating ? 'text-rating' : 'text-black/25 dark:text-white/25'}>
              ★
            </span>
          </button>
        ))}
      </div>
      {photo.metaLoaded && (
        <span className="mt-2 font-mono text-[10.5px] text-muted-foreground">
          {photo.model} · ƒ/{formatAperture(photo.aperture)} · {formatShutter(photo.shutter)} · ISO{' '}
          {photo.iso} · {Math.round(photo.focalLen)}mm
        </span>
      )}
    </div>
  );
}

function DevelopPanel({
  client,
  photo,
  targetCount,
}: {
  client: ApiClient;
  photo?: Photo;
  targetCount: number;
}) {
  // The exposure dial's neutral is the photo's seeded camera-mimic
  // compensation (base_exp_ev), NOT 0: a fresh photo shows e.g. +0.85 EV and
  // that IS its default, so "reset" returns here and the ↺ button hides while
  // exposure sits at the seed. Zero when unmeasured — the pre-seed behaviour.
  const seedExpEV = photo?.baseExpEV ?? 0;
  const liveDraft = useEditSession((s) => s.draft);
  // Falling back to the held previous draft keeps the panel rendered through
  // esLoad's null gap: swapping everything for "Loading edits…" and back on
  // each photo switch reads as flicker. Input is inert meanwhile
  // (esUpdate/esCommit no-op on a null store draft); values snap when the
  // new photo's params land.
  const draft = useEditSession((s) => s.draft ?? s.lastDraft);
  const activeControl = useEditSession((s) => s.activeControl);
  const wbPicking = useEditSession((s) => s.wbPicking);
  const cropping = useEditSession((s) => s.cropping);
  const canUndo = useEditSession(esCanUndo);
  const canRedo = useEditSession(esCanRedo);
  const setMode = useUIStore((s) => s.setMode);
  const wbModeRef = useActiveScroll(activeControl === 'wbMode');

  if (!draft) return <div className="p-4 text-sm text-muted-foreground">Loading edits…</div>;

  const update = (patch: Partial<Params>) => esUpdate(client, patch);
  const commit = (patch?: Partial<Params>) => esCommit(client, patch);

  const num = (control: ControlId) => ({
    active: activeControl === control,
    onFocusControl: () => esSetActive(client, control),
  });

  // Clear button handler: preview + persist the default in one go.
  const clear = (patch: Partial<Params>) => {
    update(patch);
    commit(patch);
  };

  const changed = {
    crop: groupChanged(draft, ['rotate', 'flipH', 'cropX', 'cropY', 'cropW', 'cropH', 'cropAngle']),
    tone: groupChanged(draft, [
      'expEV', 'expPreserve', 'bright', 'gamma', 'shadow',
      'contrast', 'whites', 'blacks', 'toneShadows', 'toneHighlights',
    ], seedExpEV),
    presence: groupChanged(draft, ['clarity', 'texture', 'dehaze']),
    wb: groupChanged(draft, ['wbMode', 'wbMul', 'wbTemp', 'wbTint', 'wbKelvin']),
    color: groupChanged(draft, [
      'saturation', 'vibrance',
      'splitShadowHue', 'splitShadowAmt', 'splitHighlightHue', 'splitHighlightAmt',
      'hslHue', 'hslSat', 'hslLum',
    ]),
    effects: groupChanged(draft, ['vignette']),
    detail: groupChanged(draft, [
      'sharpen', 'highlight', 'nrThreshold', 'fbddNoiseRd', 'medPasses',
      'demosaic', 'caRed', 'caBlue',
    ]),
  };

  const kelvinMode = draft.wbMode === 'kelvin';
  return (
    <div className={cn('flex flex-col px-4 pt-1 pb-3 text-sm', !liveDraft && 'pointer-events-none')}>
      <div className="flex items-center gap-2">
        <h2 className="text-[13px] font-medium">Develop</h2>
        {targetCount > 1 && (
          <span className="rounded bg-primary/15 px-1.5 py-0.5 text-[11px] text-primary">
            applies to {targetCount} photos
          </span>
        )}
        <span className="ml-auto flex items-center gap-1">
          <Button size="icon-sm" variant="ghost" disabled={!canUndo} onClick={() => esUndo(client)} title="Undo (Ctrl+Z)">
            <Undo2 />
          </Button>
          <Button size="icon-sm" variant="ghost" disabled={!canRedo} onClick={() => esRedo(client)} title="Redo (Ctrl+Y)">
            <Redo2 />
          </Button>
        </span>
      </div>

      <Group id="crop" title="Geometry" changed={changed.crop}>
        <Button
          size="sm"
          variant={cropping ? 'default' : 'outline'}
          className="justify-start"
          onClick={() => {
            // The overlay lives on the Develop canvas, so entering crop from
            // Library switches mode for real (keeps the mode tabs truthful).
            if (!cropping) setMode('develop');
            esSetCropping(client, !cropping);
          }}
          title="Crop &amp; straighten (R)"
        >
          <Crop data-icon="inline-start" />
          {cropping ? 'Done cropping' : 'Crop & straighten'}
          <span className="ml-auto flex items-center gap-1.5">
            {changed.crop && !cropping && (
              <span
                className="rounded-[4px] bg-primary/18 px-1 py-px text-[9px] font-semibold tracking-[.05em] text-accent-text uppercase"
                title="A crop, rotation, flip or straighten is applied"
              >
                on
              </span>
            )}
            <kbd className="text-[10px] opacity-60">R</kbd>
          </span>
        </Button>
        <EditSlider
          label="Straighten"
          value={draft.cropAngle}
          display={draft.cropAngle === 0 ? '0°' : `${draft.cropAngle > 0 ? '+' : ''}${draft.cropAngle.toFixed(1)}°`}
          min={-15}
          max={15}
          step={0.1}
          neutral={0}
          onChange={(v) => update({ cropAngle: v })}
          onCommit={(v) => commit({ cropAngle: v })}
          onClear={() => clear({ cropAngle: 0 })}
          // Deliberately NOT auto-entering crop mode here: doing it on
          // pointerdown hid/relocated this very slider mid-gesture (Develop
          // slides the drawer out, Library switches modes), and base-ui then
          // recomputed the pointer against the moved track — every click
          // slammed the angle to the -15° end. Outside crop mode the angle
          // previews through the ordinary backend render path.
          {...num('cropAngle')}
        />
      </Group>

      <Group
        id="tone"
        title="Tone"
        changed={changed.tone}
        action={<AutoButton client={client} sections={['tone']} title="Auto dynamics (Ctrl+U)" />}
      >
        <EditSlider
          label="Exposure"
          hotkey="E"
          value={draft.expEV}
          display={`${draft.expEV >= 0 ? '+' : ''}${draft.expEV.toFixed(2)} EV`}
          min={-2}
          max={3}
          step={0.05}
          // Default is the seeded camera-mimic lift, not 0 — reset returns
          // there (a lone expEV=0 renders identically to the seed anyway,
          // since neutral params re-enable LibRaw auto-brighten).
          neutral={seedExpEV}
          onChange={(v) => update({ expEV: v })}
          onCommit={(v) => commit({ expEV: v })}
          onClear={() => clear({ expEV: seedExpEV })}
          {...num('expEV')}
        />
        <EditSlider
          label="Preserve highlights"
          value={draft.expPreserve}
          display={draft.expPreserve === 0 ? 'Off' : draft.expPreserve.toFixed(2)}
          min={0}
          max={1}
          step={0.05}
          neutral={0}
          onChange={(v) => update({ expPreserve: v })}
          onCommit={(v) => commit({ expPreserve: v })}
          onClear={() => clear({ expPreserve: 0 })}
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
          neutral={1}
          onChange={(v) => update({ bright: v })}
          onCommit={(v) => commit({ bright: v })}
          onClear={() => clear({ bright: 0 })}
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
          neutral={2.222}
          onChange={(v) => update({ gamma: v })}
          onCommit={(v) => commit({ gamma: v })}
          onClear={() => clear({ gamma: 0 })}
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
          neutral={4.5}
          onChange={(v) => update({ shadow: v })}
          onCommit={(v) => commit({ shadow: v })}
          onClear={() => clear({ shadow: 0 })}
          {...num('shadow')}
        />
        <PctSlider label="Contrast" hotkey="C" field="contrast" draft={draft} update={update} commit={commit} {...num('contrast')} />
        <PctSlider label="Whites" field="whites" draft={draft} update={update} commit={commit} {...num('whites')} />
        <PctSlider label="Blacks" field="blacks" draft={draft} update={update} commit={commit} {...num('blacks')} />
        <PctSlider label="Shadows" field="toneShadows" draft={draft} update={update} commit={commit} {...num('toneShadows')} />
        <PctSlider label="Highlights" field="toneHighlights" draft={draft} update={update} commit={commit} {...num('toneHighlights')} />
      </Group>

      <Group id="presence" title="Presence" changed={changed.presence}>
        <PctSlider label="Clarity" field="clarity" draft={draft} update={update} commit={commit} {...num('clarity')} />
        <PctSlider label="Texture" field="texture" draft={draft} update={update} commit={commit} {...num('texture')} />
        <PctSlider label="Dehaze" field="dehaze" draft={draft} update={update} commit={commit} {...num('dehaze')} />
      </Group>

      <Group id="wb" title="White balance" changed={changed.wb}>
      <div ref={wbModeRef} className={cn('flex flex-col gap-1.5 rounded-md', activeControl === 'wbMode' && 'ring-2 ring-ring ring-offset-2 ring-offset-background')}>
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
          </ToggleGroup>
          <Button
            size="icon-sm"
            variant={wbPicking || draft.wbMode === 'custom' ? 'default' : 'outline'}
            className={cn(wbPicking && 'ring-2 ring-ring ring-offset-1 ring-offset-background')}
            title={
              wbPicking
                ? 'Keep white balance (Enter)'
                : 'Pick white balance (W): click a neutral gray in the image'
            }
            onClick={() => (wbPicking ? esWBPickDone(client) : esSetWBPicking(true))}
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
          neutral={5500}
          onChange={(v) => update({ wbKelvin: v })}
          onCommit={(v) => commit({ wbKelvin: v })}
          onClear={() => clear({ wbKelvin: 5500 })}
          gradient={TEMP_GRADIENT}
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
          neutral={0}
          disabled={draft.wbMode === 'auto'}
          onChange={(v) => update({ wbTemp: v / 100 })}
          onCommit={(v) => commit({ wbTemp: v / 100 })}
          onClear={() => clear({ wbTemp: 0 })}
          gradient={TEMP_GRADIENT}
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
        neutral={0}
        disabled={draft.wbMode === 'auto'}
        onChange={(v) => update({ wbTint: v / 100 })}
        onCommit={(v) => commit({ wbTint: v / 100 })}
        onClear={() => clear({ wbTint: 0 })}
        gradient={TINT_GRADIENT}
        {...num('wbTint')}
      />
      </Group>

      <Group
        id="color"
        title="Color"
        changed={changed.color}
        action={<AutoButton client={client} sections={['wb', 'color']} title="Auto colours (Ctrl+Shift+U)" />}
      >
        <PctSlider label="Saturation" hotkey="A" field="saturation" draft={draft} update={update} commit={commit} {...num('saturation')} />
        <PctSlider label="Vibrance" hotkey="V" field="vibrance" draft={draft} update={update} commit={commit} {...num('vibrance')} />
        <HueSlider label="Shadow tint" field="splitShadowHue" draft={draft} update={update} commit={commit} {...num('splitShadowHue')} />
        <AmtSlider label="Shadow tint amount" field="splitShadowAmt" draft={draft} update={update} commit={commit} {...num('splitShadowAmt')} />
        <HueSlider label="Highlight tint" field="splitHighlightHue" draft={draft} update={update} commit={commit} {...num('splitHighlightHue')} />
        <AmtSlider label="Highlight tint amount" field="splitHighlightAmt" draft={draft} update={update} commit={commit} {...num('splitHighlightAmt')} />
        <ColorMixer draft={draft} update={update} commit={commit} clear={clear} />
      </Group>

      <Group id="effects" title="Effects" changed={changed.effects}>
        <PctSlider label="Vignette" hotkey="O" field="vignette" draft={draft} update={update} commit={commit} {...num('vignette')} />
      </Group>

      <Group id="detail" title="Detail" changed={changed.detail}>
        <EditSlider
          label="Sharpen"
          value={draft.sharpen * 100}
          display={draft.sharpen === 0 ? 'Off' : String(Math.round(draft.sharpen * 100))}
          min={0}
          max={100}
          step={2}
          neutral={0}
          onChange={(v) => update({ sharpen: v / 100 })}
          onCommit={(v) => commit({ sharpen: v / 100 })}
          onClear={() => clear({ sharpen: 0 })}
          {...num('sharpen')}
        />
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
          neutral={0}
          onChange={(v) => update({ nrThreshold: v })}
          onCommit={(v) => commit({ nrThreshold: v })}
          onClear={() => clear({ nrThreshold: 0 })}
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
          neutral={0}
          onChange={(v) => update({ medPasses: v })}
          onCommit={(v) => commit({ medPasses: v })}
          onClear={() => clear({ medPasses: 0 })}
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
      </Group>

      <p className="mt-4 mb-1 text-xs text-muted-foreground">
        Drag a slider for a live preview; release to save. Press a control's key (E, B, W, …) or
        walk with Ctrl+↑/↓, then +/- adjusts; Esc returns to the image. Ctrl+Z/Ctrl+Y undo/redo
        per photo. Copy, paste, reset and presets live in the Presets tab.
      </p>
    </div>
  );
}

// useActiveScroll keeps the keyboard-focused control visible: walking the
// controls with Ctrl+↑/↓ (or a hotkey) scrolls the drawer to the ring.
function useActiveScroll(active?: boolean) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (active) ref.current?.scrollIntoView({ block: 'nearest' });
  }, [active]);
  return ref;
}

// isDefault reports whether one param still holds its stored default —
// used for the per-group "has adjustments" dot and the per-slider clear
// buttons. The WB mode and demosaic defaults are stored as "" (see the
// generated-union notes above); everything else defaults to NEUTRAL.
function isDefault(draft: Params, key: keyof Params, seedExpEV = 0): boolean {
  const v = draft[key];
  // Exposure's default is the photo's seeded camera-mimic lift, not 0, so an
  // untouched seeded photo reads as unchanged (no group dot).
  if (key === 'expEV') return Math.abs(draft.expEV - seedExpEV) <= 1e-9;
  if (key === 'wbMode') return (v as string) === '' || v === 'camera';
  if (key === 'demosaic') return (v as string) === '';
  // Array-valued params (wbMul, the hsl mixer bands) default to all-zero.
  if (Array.isArray(v)) return v.every((m) => m === 0);
  return v === NEUTRAL[key];
}

function groupChanged(draft: Params, keys: (keyof Params)[], seedExpEV = 0): boolean {
  return keys.some((k) => !isDefault(draft, k, seedExpEV));
}

// Group is one collapsible develop-panel section, drawn flat per the
// develop-drawer plate: an uppercase eyebrow header with the "has
// adjustments" dot, rows beneath, no card chrome. Open state persists per
// group in the catalog (uiSettings, absent = open).
function Group({
  id,
  title,
  changed,
  action,
  children,
}: {
  // Must match CONTROL_GROUP in editSession so hotkey/palette selection can
  // open the right section and Ctrl+↑/↓ can skip closed ones.
  id: GroupId;
  title: string;
  changed?: boolean;
  // Optional header action (e.g. a section Auto button) — rendered beside
  // the toggle, outside it, since a button cannot nest in a button.
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  const client = useApiClient();
  const open = useUIStore((s) => s.editGroups[id] !== false);
  const toggle = () => updateEditGroupOpen(client, id, !open);
  return (
    <section>
      <div className="group/hdr mt-3 mb-2 flex items-center gap-1.5">
        <button
          type="button"
          className="flex flex-1 items-center gap-1.5 text-left"
          onClick={toggle}
          aria-expanded={open}
        >
          <span className="text-[10px] tracking-[.06em] text-muted-foreground uppercase group-hover/hdr:text-foreground">
            {title}
          </span>
          {changed && (
            <span className="size-[5px] shrink-0 rounded-full bg-primary" title="Has adjustments" />
          )}
          <ChevronRight
            className={cn(
              'size-3 shrink-0 text-faint opacity-0 transition-transform group-hover/hdr:opacity-100',
              open && 'rotate-90',
            )}
          />
        </button>
        {action}
      </div>
      {open && <div className="flex flex-col gap-[7px]">{children}</div>}
    </section>
  );
}

// AutoButton is the small per-section (or global) auto-adjust trigger.
function AutoButton({
  client,
  sections,
  title,
}: {
  client: ApiClient;
  sections: Parameters<typeof esAuto>[1];
  title: string;
}) {
  return (
    <button
      type="button"
      className="rounded px-1 text-[10px] tracking-[.06em] text-faint uppercase hover:text-foreground"
      onClick={() => void esAuto(client, sections)}
      title={title}
    >
      Auto
    </button>
  );
}

// MasksPanel is the Masks tab: add buttons, the mask list, and — for the
// selected mask — its adjustment sliders (plus the brush tool row). Masks
// live in draft.masks, so every change flows through the same
// esUpdate/esCommit path as any slider; the on-canvas shape/paint overlay is
// MaskOverlay on the Develop loupe, driven by the same activeMask state.
// Mirrors DevelopPanel's shell: held lastDraft through photo switches (inert
// input meanwhile), undo/redo in the header.
function MasksPanel({ client, targetCount }: { client: ApiClient; targetCount: number }) {
  const liveDraft = useEditSession((s) => s.draft);
  const draft = useEditSession((s) => s.draft ?? s.lastDraft);
  const canUndo = useEditSession(esCanUndo);
  const canRedo = useEditSession(esCanRedo);
  if (!draft) return <div className="p-4 text-sm text-muted-foreground">Loading edits…</div>;
  return (
    <div className={cn('flex flex-col px-4 pt-1 pb-3 text-sm', !liveDraft && 'pointer-events-none')}>
      <div className="mb-2 flex items-center gap-2">
        <h2 className="text-[13px] font-medium">Masks</h2>
        {targetCount > 1 && (
          <span className="rounded bg-primary/15 px-1.5 py-0.5 text-[11px] text-primary">
            applies to {targetCount} photos
          </span>
        )}
        <span className="ml-auto flex items-center gap-1">
          <Button size="icon-sm" variant="ghost" disabled={!canUndo} onClick={() => esUndo(client)} title="Undo (Ctrl+Z)">
            <Undo2 />
          </Button>
          <Button size="icon-sm" variant="ghost" disabled={!canRedo} onClick={() => esRedo(client)} title="Redo (Ctrl+Y)">
            <Redo2 />
          </Button>
        </span>
      </div>
      <MasksSection client={client} draft={draft} />
      <p className="mt-4 mb-1 text-xs text-muted-foreground">
        A mask is a local adjustment: a gradient, ellipse, brushed region or
        AI-detected area carrying its own exposure, tone and color. Subject
        and Depth run a local model once per photo; masks stay anchored to
        image content through crops and straightens.
      </p>
    </div>
  );
}

// aiRestoreFired remembers which (photo, kind) map restores this session has
// already requested — GenerateAIMap is idempotent and cheap when the map is
// on disk, but there's no reason to re-fire it on every render (or for
// StrictMode's double effect). A consent-declined kind stays in the set so
// the dialog doesn't nag on every re-render; it re-asks next session.
const aiRestoreFired = new Set<string>();

// isModelNotDownloaded matches the server's consent sentinel (aimasks.go).
const isModelNotDownloaded = (err: unknown) =>
  err instanceof Error && err.message.includes('model not downloaded');

function MasksSection({ client, draft }: { client: ApiClient; draft: Params }) {
  const activeMask = useEditSession((s) => s.activeMask);
  const photoId = useEditSession((s) => s.photoId);
  const setMode = useUIStore((s) => s.setMode);
  const [generating, setGenerating] = useState<AIKindType | null>(null);
  // Scene detection result for THIS photo: mapVer + the category chips.
  const [scene, setScene] = useState<AIMapResult | null>(null);
  // A feature waiting on download consent; non-null renders the dialog.
  const [pendingAI, setPendingAI] = useState<PendingAIDownload | null>(null);
  // Drop the scene result when the photo changes — adjust during render, not
  // an effect (photoId is a primitive, so no re-render loop).
  const [prevPhotoId, setPrevPhotoId] = useState(photoId);
  if (photoId !== prevPhotoId) {
    setPrevPhotoId(photoId);
    setScene(null);
  }
  const masks = useMemo(() => draft.masks ?? [], [draft.masks]);
  const add = (type: Mask['type']) => {
    setMode('develop'); // the overlay lives on the Develop canvas
    esAddMask(client, type);
  };

  // runAI generates the map (downloading only with explicit consent) and
  // applies the mode: add a fresh mask / show scene chips, or — for a
  // restore — nudge a preview re-render so the now-live mask shows.
  const runAI = async (kind: AIKindType, allowDownload: boolean, mode: 'add' | 'restore') => {
    if (photoId == null) return;
    setGenerating(kind);
    try {
      const res = await generateAIMap(client, photoId, kind, allowDownload);
      if (mode === 'restore') {
        // Repaint ONLY when a map actually regenerated: an unconditional
        // nudge forces a transient (non-abortable) decode on every first
        // visit to a masked photo — those piled up into browse stalls.
        // The nudge heals the loupe (live preview); bump the cache-buster so
        // the immutably-cached grid thumbnail refetches too.
        if (res.generated) {
          esUpdate(client, {});
          bumpImgBust(photoId);
        }
      } else if (kind === 'class') {
        // Scene detection adds no mask by itself — it offers one chip per
        // detected category; clicking a chip adds that category's mask.
        setScene(res);
      } else {
        setMode('develop');
        esAddMaskObject(client, aiMask(kind as 'subject' | 'depth', res.mapVer));
      }
    } catch (err) {
      toast.error(`AI mask failed: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setGenerating(null);
    }
  };

  // Button path: ask for consent first when the model isn't on disk yet.
  const addAI = async (kind: 'subject' | 'depth' | 'class') => {
    if (photoId == null || generating) return;
    try {
      const status = await aiModelStatus(client, kind);
      if (!status.downloaded) {
        setPendingAI({ kind, bytes: status.bytes, mode: 'add' });
        return;
      }
    } catch (err) {
      toast.error(`AI mask failed: ${err instanceof Error ? err.message : String(err)}`);
      return;
    }
    void runAI(kind, false, 'add');
  };

  // Restore maps for AI masks that arrived without local map files (sidecar
  // from another machine, cleared data dir): idempotent per photo+kind, and
  // never a silent download — a missing model opens the consent dialog.
  useEffect(() => {
    if (photoId == null) return;
    for (const m of masks) {
      if (m.type !== 'ai' || !m.aiKind) continue;
      const kind = m.aiKind;
      const key = `${photoId}|${kind}`;
      if (aiRestoreFired.has(key)) continue;
      aiRestoreFired.add(key);
      generateAIMap(client, photoId, kind, false)
        .then((res) => {
          if (res.generated) {
            esUpdate(client, {});
            bumpImgBust(photoId);
          }
        })
        .catch(async (err) => {
          if (isModelNotDownloaded(err)) {
            const status = await aiModelStatus(client, kind).catch(() => null);
            if (status) setPendingAI({ kind, bytes: status.bytes, mode: 'restore' });
            return; // stays in aiRestoreFired: don't re-nag this session
          }
          aiRestoreFired.delete(key); // transient failure: allow a retry
        });
    }
  }, [client, photoId, masks]);

  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex gap-1.5" role="group" aria-label="Add mask">
        <Button size="sm" variant="outline" className="flex-1" title="Add linear gradient" onClick={() => add('linear')}>
          <Plus data-icon="inline-start" />
          Linear
        </Button>
        <Button size="sm" variant="outline" className="flex-1" title="Add radial mask" onClick={() => add('radial')}>
          <Circle data-icon="inline-start" />
          Radial
        </Button>
        <Button size="sm" variant="outline" className="flex-1" title="Add brush mask" onClick={() => add('brush')}>
          <Paintbrush data-icon="inline-start" />
          Brush
        </Button>
      </div>
      <div className="flex gap-1.5" role="group" aria-label="Add AI mask">
        <Button
          size="sm"
          variant="outline"
          className="flex-1"
          title="Detect the subject and mask it (runs a local model)"
          disabled={generating != null}
          onClick={() => addAI('subject')}
          data-testid="ai-mask-subject"
        >
          {generating === 'subject' ? <Loader2 data-icon="inline-start" className="animate-spin" /> : <Focus data-icon="inline-start" />}
          Subject
        </Button>
        <Button
          size="sm"
          variant="outline"
          className="flex-1"
          title="Estimate depth and mask a distance range (runs a local model)"
          disabled={generating != null}
          onClick={() => addAI('depth')}
          data-testid="ai-mask-depth"
        >
          {generating === 'depth' ? <Loader2 data-icon="inline-start" className="animate-spin" /> : <Layers data-icon="inline-start" />}
          Depth
        </Button>
        <Button
          size="sm"
          variant="outline"
          className="flex-1"
          title="Detect scene regions (sky, people, foliage, …) to mask (runs a local model)"
          disabled={generating != null}
          onClick={() => addAI('class')}
          data-testid="ai-mask-scene"
        >
          {generating === 'class' ? <Loader2 data-icon="inline-start" className="animate-spin" /> : <Shapes data-icon="inline-start" />}
          Scene
        </Button>
      </div>
      {scene && (
        <div className="flex flex-wrap gap-1" role="group" aria-label="Detected regions" data-testid="scene-chips">
          {(scene.categories ?? []).length === 0 && (
            <span className="px-1 text-[11px] text-muted-foreground">No distinct regions detected.</span>
          )}
          {(scene.categories ?? []).map((c) => (
            <button
              key={c.id}
              type="button"
              className="rounded-full border border-border px-2 py-0.5 text-[11px] text-secondary-foreground hover:border-primary/45 hover:text-foreground"
              title={`Mask ${c.name} (${Math.round(c.fraction * 100)}% of frame)`}
              onClick={() => {
                setMode('develop');
                esAddMaskObject(client, aiClassMask(c.id, scene.mapVer));
              }}
            >
              {c.name} · {Math.round(c.fraction * 100)}%
            </button>
          ))}
        </div>
      )}
      {masks.map((m, i) => (
        <MaskRow
          key={i}
          client={client}
          mask={m}
          index={i}
          selected={activeMask === i}
          onSelect={() => {
            setMode('develop');
            esSetActiveMask(activeMask === i ? null : i);
          }}
        />
      ))}
      <AIModelDialog
        pending={pendingAI}
        onConfirm={(p) => {
          setPendingAI(null);
          void runAI(p.kind, true, p.mode);
        }}
        onCancel={() => setPendingAI(null)}
      />
    </div>
  );
}

function MaskRow({
  client,
  mask,
  index,
  selected,
  onSelect,
}: {
  client: ApiClient;
  mask: Mask;
  index: number;
  selected: boolean;
  onSelect: () => void;
}) {
  const activeMaskControl = useEditSession((s) => s.activeMaskControl);
  const adjust = mask.adjust ?? {};
  const changed = !maskAdjustIsNeutral(adjust);
  const patchAdjust = (key: MaskControlId, v: number): { adjust: MaskAdjust } => ({
    adjust: { ...adjust, [key]: v },
  });
  return (
    <div className={cn('flex flex-col rounded-md border', selected ? 'border-primary/45' : 'border-border')}>
      {/* Hovering the row header shows this mask's red weight tint on the
          loupe (the only way to SEE an AI mask's detected region); it fades
          out on leave. */}
      <div
        className="flex items-center gap-1.5 px-2 py-1.5"
        onMouseEnter={() => esSetTintMask(index)}
        onMouseLeave={() => esSetTintMask(null)}
      >
        <button type="button" className="flex flex-1 items-center gap-1.5 text-left" onClick={onSelect} aria-pressed={selected}>
          <span className="text-[11.5px] text-secondary-foreground">{maskLabel(mask, index)}</span>
          {changed && <span className="size-[5px] shrink-0 rounded-full bg-primary" title="Has adjustments" />}
        </button>
        <button
          type="button"
          className={cn(
            'rounded px-1 text-[9px] font-semibold tracking-[.05em] uppercase',
            mask.invert ? 'bg-primary/18 text-accent-text' : 'text-faint hover:text-foreground',
          )}
          title="Invert mask"
          aria-pressed={!!mask.invert}
          onClick={() => {
            esUpdateMask(client, index, { invert: !mask.invert });
            esCommit(client);
          }}
        >
          Invert
        </button>
        <button
          type="button"
          className="text-muted-foreground hover:text-foreground"
          title="Delete mask"
          aria-label="Delete mask"
          onClick={() => esRemoveMask(client, index)}
        >
          <Trash2 className="size-3" />
        </button>
      </div>
      {selected && (
        <div className="flex flex-col gap-[7px] px-2 pb-2">
          {mask.type === 'brush' && <BrushToolRow client={client} mask={mask} index={index} />}
          {mask.type === 'ai' && <AIShapeRows client={client} mask={mask} index={index} />}
          {MASK_CONTROL_ORDER.map((key) => {
            const spec = MASK_CONTROL_SPECS[key];
            const raw = adjust[key] ?? 0;
            const isEV = key === 'expEV';
            return (
              <EditSlider
                key={key}
                label={spec.label}
                value={isEV ? raw : raw * 100}
                display={isEV ? `${raw >= 0 ? '+' : ''}${raw.toFixed(2)} EV` : pct(raw)}
                min={isEV ? spec.min : spec.min * 100}
                max={isEV ? spec.max : spec.max * 100}
                step={isEV ? spec.step : spec.step * 100}
                neutral={0}
                gradient={key === 'temp' ? TEMP_GRADIENT : key === 'tint' ? TINT_GRADIENT : undefined}
                active={selected && activeMaskControl === key}
                onFocusControl={() => esSetActiveMaskControl(index, key)}
                onChange={(v) => esUpdateMask(client, index, patchAdjust(key, isEV ? v : v / 100))}
                onCommit={(v) => {
                  esUpdateMask(client, index, patchAdjust(key, isEV ? v : v / 100));
                  esCommit(client);
                }}
                onClear={() => {
                  esUpdateMask(client, index, patchAdjust(key, 0));
                  esCommit(client);
                }}
              />
            );
          })}
        </div>
      )}
    </div>
  );
}

// AIShapeRows: the map-shaping sliders for an AI mask — unlike the brush tool
// row this IS photo state (threshold/feather/depth window live in the mask
// params and change pixels), so every move flows through esUpdateMask.
function AIShapeRows({ client, mask, index }: { client: ApiClient; mask: Mask; index: number }) {
  const patch = (p: Partial<Mask>) => esUpdateMask(client, index, p);
  const commit = (p: Partial<Mask>) => {
    esUpdateMask(client, index, p);
    esCommit(client);
  };
  const shapeSlider = (
    label: string,
    raw: number,
    displayDefault: number, // shown when raw is 0 (server default)
    onValue: (v: number) => Partial<Mask>,
    min = 0,
  ) => {
    const shown = raw === 0 ? displayDefault : raw;
    return (
      <EditSlider
        key={label}
        label={label}
        value={shown * 100}
        display={pct(shown)}
        min={min * 100}
        max={100}
        step={1}
        neutral={displayDefault * 100}
        onChange={(v) => patch(onValue(v / 100))}
        onCommit={(v) => commit(onValue(v / 100))}
        onClear={() => commit(onValue(0))}
      />
    );
  };
  return (
    <>
      {/* Threshold's floor is 2%: a raw 0 means "server default (50%)", so the
          slider must never land exactly on 0. */}
      {mask.aiKind === 'subject' && shapeSlider('Threshold', mask.threshold ?? 0, 0.5, (v) => ({ threshold: v }), 0.02)}
      {mask.aiKind === 'depth' && (
        <EditRangeSlider
          label="Depth range"
          value={[(mask.depthLo ?? 0) * 100, (mask.depthHi ?? 0) * 100]}
          display={`${Math.round((mask.depthLo ?? 0) * 100)}–${Math.round((mask.depthHi ?? 0) * 100)}`}
          min={0}
          max={100}
          step={1}
          neutral={[DEPTH_WINDOW_DEFAULT.depthLo * 100, DEPTH_WINDOW_DEFAULT.depthHi * 100]}
          onChange={([lo, hi]) => patch({ depthLo: lo / 100, depthHi: hi / 100 })}
          onCommit={([lo, hi]) => commit({ depthLo: lo / 100, depthHi: hi / 100 })}
          onClear={() => commit({ ...DEPTH_WINDOW_DEFAULT })}
        />
      )}
      {shapeSlider('Edge feather', mask.feather ?? 0, 0, (v) => ({ feather: v }))}
    </>
  );
}

// BrushToolRow: paint-mode toggle plus the shared stroke settings (size /
// feather / flow / erase) for the next stroke. Tool state, not photo state —
// nothing here touches the draft.
function BrushToolRow({ client, mask, index }: { client: ApiClient; mask: Mask; index: number }) {
  const paint = useEditSession((s) => s.maskPaint);
  const radius = useEditSession((s) => s.brushRadius);
  const feather = useEditSession((s) => s.brushFeather);
  const flow = useEditSession((s) => s.brushFlow);
  const erase = useEditSession((s) => s.brushErase);
  const strokes = mask.strokes ?? [];
  return (
    <div className="flex flex-col gap-[7px]">
      <div className="flex items-center gap-1.5">
        <Button
          size="sm"
          variant={paint ? 'default' : 'outline'}
          className="flex-1 justify-start"
          onClick={() => esSetMaskPaint(!paint)}
        >
          <Paintbrush data-icon="inline-start" />
          {paint ? 'Done painting' : 'Paint'}
        </Button>
        <Button
          size="icon-sm"
          variant={erase ? 'default' : 'outline'}
          title="Erase strokes"
          aria-pressed={erase}
          onClick={() => esSetBrushTool({ brushErase: !erase })}
        >
          <Eraser />
        </Button>
        {strokes.length > 0 && (
          <Button
            size="icon-sm"
            variant="outline"
            title="Clear all strokes"
            onClick={() => {
              esUpdateMask(client, index, { strokes: [] });
              esCommit(client);
            }}
          >
            <RotateCcw />
          </Button>
        )}
      </div>
      <EditSlider
        label="Size"
        value={radius * 100}
        display={String(Math.round(radius * 100))}
        min={0.5}
        max={25}
        step={0.5}
        onChange={(v) => esSetBrushTool({ brushRadius: v / 100 })}
        onCommit={(v) => esSetBrushTool({ brushRadius: v / 100 })}
      />
      <EditSlider
        label="Feather"
        value={feather * 100}
        display={String(Math.round(feather * 100))}
        min={0}
        max={100}
        step={2}
        onChange={(v) => esSetBrushTool({ brushFeather: v / 100 })}
        onCommit={(v) => esSetBrushTool({ brushFeather: v / 100 })}
      />
      <EditSlider
        label="Flow"
        value={flow * 100}
        display={String(Math.round(flow * 100))}
        min={5}
        max={100}
        step={5}
        onChange={(v) => esSetBrushTool({ brushFlow: v / 100 })}
        onCommit={(v) => esSetBrushTool({ brushFlow: v / 100 })}
      />
    </div>
  );
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
  | 'texture'
  | 'clarity'
  | 'dehaze'
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
      neutral={0}
      onChange={(v) => update({ [field]: v / 100 })}
      onCommit={(v) => commit({ [field]: v / 100 })}
      onClear={() => {
        update({ [field]: 0 });
        commit({ [field]: 0 });
      }}
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
      neutral={0}
      onChange={(v) => update({ [field]: v })}
      onCommit={(v) => commit({ [field]: v })}
      onClear={() => {
        update({ [field]: 0 });
        commit({ [field]: 0 });
      }}
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
      neutral={0}
      onChange={(v) => update({ [field]: v / 100 })}
      onCommit={(v) => commit({ [field]: v / 100 })}
      onClear={() => {
        update({ [field]: 0 });
        commit({ [field]: 0 });
      }}
      active={active}
      onFocusControl={onFocusControl}
    />
  );
}

// The HSL color mixer: eight fixed hue bands (mirroring pyramid.HSLBandCenters
// on the Go side, chip order = band order), a chip row to pick the band, and
// Hue/Saturation/Luminance sliders for the picked band. A dot on a chip marks
// a band carrying an adjustment.
const MIXER_BANDS = [
  { name: 'Red', color: '#e5484d' },
  { name: 'Orange', color: '#f76b15' },
  { name: 'Yellow', color: '#d9c400' },
  { name: 'Green', color: '#46a758' },
  { name: 'Aqua', color: '#12a594' },
  { name: 'Blue', color: '#3d7dff' },
  { name: 'Purple', color: '#8e4ec6' },
  { name: 'Magenta', color: '#d6409f' },
];
type MixerKey = 'hslHue' | 'hslSat' | 'hslLum';

function ColorMixer({
  draft,
  update,
  commit,
  clear,
}: {
  draft: Params;
  update: (patch: Partial<Params>) => void;
  commit: (patch?: Partial<Params>) => void;
  clear: (patch: Partial<Params>) => void;
}) {
  const [band, setBand] = useState(0);
  const bandPatch = (key: MixerKey, v: number): Partial<Params> => {
    const next = [...draft[key]] as Params[MixerKey];
    next[band] = v;
    return { [key]: next };
  };
  const val = (key: MixerKey) => draft[key][band] ?? 0;
  const bandChanged = (i: number) =>
    draft.hslHue[i] !== 0 || draft.hslSat[i] !== 0 || draft.hslLum[i] !== 0;

  return (
    <div className="flex flex-col">
      <div className="flex items-center gap-2 pt-2 pb-1" role="group" aria-label="Color mixer band">
        <span className="text-[11px] text-muted-foreground">Mixer</span>
        <div className="flex flex-1 items-center justify-end gap-[7px]">
          {MIXER_BANDS.map((b, i) => (
            <button
              key={b.name}
              onClick={() => setBand(i)}
              title={`${b.name} band`}
              aria-label={`${b.name} band`}
              aria-pressed={band === i}
              className={cn(
                'relative size-[16px] rounded-full transition-opacity',
                band === i
                  ? 'ring-2 ring-ring ring-offset-1 ring-offset-background'
                  : 'opacity-70 hover:opacity-100',
              )}
              style={{ backgroundColor: b.color }}
            >
              {bandChanged(i) && (
                <span className="absolute -top-[3px] -right-[3px] size-[6px] rounded-full border border-background bg-primary" />
              )}
            </button>
          ))}
        </div>
      </div>
      <EditSlider
        label={`${MIXER_BANDS[band].name} hue`}
        value={val('hslHue') * 100}
        display={
          val('hslHue') === 0 ? '0°' : `${val('hslHue') > 0 ? '+' : ''}${Math.round(val('hslHue') * 30)}°`
        }
        min={-100}
        max={100}
        step={2}
        neutral={0}
        onChange={(v) => update(bandPatch('hslHue', v / 100))}
        onCommit={(v) => commit(bandPatch('hslHue', v / 100))}
        onClear={() => clear(bandPatch('hslHue', 0))}
      />
      <EditSlider
        label={`${MIXER_BANDS[band].name} saturation`}
        value={val('hslSat') * 100}
        display={pct(val('hslSat'))}
        min={-100}
        max={100}
        step={2}
        neutral={0}
        onChange={(v) => update(bandPatch('hslSat', v / 100))}
        onCommit={(v) => commit(bandPatch('hslSat', v / 100))}
        onClear={() => clear(bandPatch('hslSat', 0))}
      />
      <EditSlider
        label={`${MIXER_BANDS[band].name} luminance`}
        value={val('hslLum') * 100}
        display={pct(val('hslLum'))}
        min={-100}
        max={100}
        step={2}
        neutral={0}
        onChange={(v) => update(bandPatch('hslLum', v / 100))}
        onCommit={(v) => commit(bandPatch('hslLum', v / 100))}
        onClear={() => clear(bandPatch('hslLum', 0))}
      />
    </div>
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
  const ref = useActiveScroll(active);
  return (
    <div ref={ref} className={cn('flex flex-col gap-1.5 rounded-md', active && 'ring-2 ring-ring ring-offset-2 ring-offset-background')}>
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
  neutral,
  disabled,
  active,
  onFocusControl,
  onChange,
  onCommit,
  onClear,
  gradient,
}: {
  label: string;
  hotkey?: string;
  value: number;
  display: string;
  min: number;
  max: number;
  step: number;
  /** Display-space default: the fill runs from here to the thumb, and the
   * clear button shows only while the value differs from it. */
  neutral?: number;
  disabled?: boolean;
  active?: boolean;
  onFocusControl?: () => void;
  onChange: (v: number) => void;
  onCommit: (v: number) => void;
  /** Resets the control to its default (shown only when neutral is set). */
  onClear?: () => void;
  /** Gradient track (WB dials); replaces the value fill. */
  gradient?: string;
}) {
  // During a drag the thumb tracks a local value, so it stays smooth even
  // while the store update (which re-renders the whole panel) is coalesced to
  // one frame. `dragging === null` means idle → follow the prop.
  const [dragging, setDragging] = useState<number | null>(null);
  const shown = dragging ?? value;
  const changed = neutral != null && Math.abs(value - neutral) > 1e-9;
  const ref = useActiveScroll(active);
  // One row per the develop-drawer plate: label · track · mono value, the
  // reset affordance surfacing only when the value left its default.
  return (
    <div
      ref={ref}
      className={cn(
        'flex items-center gap-2.5 rounded-md',
        active && 'ring-2 ring-ring ring-offset-2 ring-offset-background',
        disabled && 'opacity-50',
      )}
      onPointerDown={onFocusControl}
      title={hotkey ? `${label} (${hotkey})` : undefined}
    >
      <span className="w-[96px] shrink-0 truncate text-[11.5px] text-secondary-foreground">
        {label}
      </span>
      <div className="min-w-0 flex-1">
        <Slider
          value={shown}
          min={min}
          max={max}
          step={step}
          fillFrom={neutral}
          gradient={gradient}
          disabled={disabled}
          aria-label={label}
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
      <span className="w-[56px] shrink-0 text-right font-mono text-[11px] text-foreground tabular-nums">
        {display}
      </span>
      {onClear && neutral != null ? (
        <button
          type="button"
          className={cn(
            'shrink-0 text-muted-foreground transition-colors hover:text-foreground',
            !changed && 'invisible',
          )}
          title={`Reset ${label.toLowerCase()}`}
          aria-label={`Reset ${label.toLowerCase()}`}
          onClick={(e) => {
            e.stopPropagation();
            setDragging(null);
            onClear();
          }}
        >
          <RotateCcw className="size-3" />
        </button>
      ) : (
        <span className="w-3 shrink-0" />
      )}
    </div>
  );
}

// EditSlider's two-thumb sibling for window controls (the depth range): same
// row plate, but the value is a [lo, hi] pair and the fill spans the kept
// window between the thumbs.
export function EditRangeSlider({
  label,
  value,
  display,
  min,
  max,
  step,
  neutral,
  onChange,
  onCommit,
  onClear,
}: {
  label: string;
  value: [number, number];
  display: string;
  min: number;
  max: number;
  step: number;
  /** Display-space default window; the clear button shows while the value differs from it. */
  neutral?: [number, number];
  onChange: (v: [number, number]) => void;
  onCommit: (v: [number, number]) => void;
  onClear?: () => void;
}) {
  const [dragging, setDragging] = useState<[number, number] | null>(null);
  const shown = dragging ?? value;
  const changed =
    neutral != null &&
    (Math.abs(value[0] - neutral[0]) > 1e-9 || Math.abs(value[1] - neutral[1]) > 1e-9);
  return (
    <div className="flex items-center gap-2.5 rounded-md">
      <span className="w-[96px] shrink-0 truncate text-[11.5px] text-secondary-foreground">
        {label}
      </span>
      <div className="min-w-0 flex-1">
        <Slider
          value={shown}
          min={min}
          max={max}
          step={step}
          aria-label={label}
          onValueChange={(v) => {
            const pair = [...(v as number[])] as [number, number];
            setDragging(pair);
            onChange(pair);
          }}
          onValueCommitted={(v) => {
            setDragging(null);
            onCommit([...(v as number[])] as [number, number]);
          }}
        />
      </div>
      <span className="w-[56px] shrink-0 text-right font-mono text-[11px] text-foreground tabular-nums">
        {display}
      </span>
      {onClear && neutral != null ? (
        <button
          type="button"
          className={cn(
            'shrink-0 text-muted-foreground transition-colors hover:text-foreground',
            !changed && 'invisible',
          )}
          title={`Reset ${label.toLowerCase()}`}
          aria-label={`Reset ${label.toLowerCase()}`}
          onClick={(e) => {
            e.stopPropagation();
            setDragging(null);
            onClear();
          }}
        >
          <RotateCcw className="size-3" />
        </button>
      ) : (
        <span className="w-3 shrink-0" />
      )}
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
// BatchSection is the whole right panel while several photos are selected:
// relative deltas with NO apply button — each slider release applies the
// increment since the last one, so thumbnails follow the drag and mixed
// per-photo edits stay intact.
function BatchSection({ client, ids }: { client: ApiClient; ids: number[] }) {
  type Field = 'expEV' | 'contrast' | 'saturation';
  const [pos, setPos] = useState<Record<Field, number>>({ expEV: 0, contrast: 0, saturation: 0 });
  const [busy, setBusy] = useState(0);
  // Applied totals live in a ref updated optimistically at send time, so
  // rapid consecutive releases each carry only their own increment. The
  // component is keyed by the selection, so both start at zero per set.
  const applied = useRef<Record<Field, number>>({ expEV: 0, contrast: 0, saturation: 0 });

  const commit = (field: Field) => (v: number) => {
    setPos((p) => ({ ...p, [field]: v }));
    const inc = v - applied.current[field];
    if (Math.abs(inc) < 1e-9) return;
    applied.current[field] = v;
    setBusy((n) => n + 1);
    applyBatchEdit(client, ids, { ...NULL_DELTA, [field]: inc })
      .catch((err) => toast.error((err as Error).message))
      .finally(() => setBusy((n) => n - 1));
  };

  return (
    <div className="flex h-full flex-col overflow-y-auto">
      <div className="border-b px-4 pt-[15px] pb-[13px]">
        <span className="text-[10px] tracking-[.07em] text-muted-foreground uppercase">
          Relative adjustment
        </span>
        <span className="mt-1.5 block text-[13px] text-foreground">{ids.length} photos selected</span>
        <div className="mt-1 text-[11.5px] leading-snug text-muted-foreground">
          Deltas add to each photo's own current value — mixed edits stay intact.
        </div>
      </div>
      <div className="flex flex-col gap-4 p-4">
        <EditSlider
          label="Exposure"
          value={pos.expEV}
          display={`${pos.expEV >= 0 ? '+' : ''}${pos.expEV.toFixed(2)} EV`}
          min={-2}
          max={2}
          step={0.05}
          neutral={0}
          onChange={(v) => setPos((p) => ({ ...p, expEV: v }))}
          onCommit={commit('expEV')}
          onClear={() => commit('expEV')(0)}
        />
        <EditSlider
          label="Contrast"
          value={pos.contrast * 100}
          display={pct(pos.contrast)}
          min={-100}
          max={100}
          step={2}
          neutral={0}
          onChange={(v) => setPos((p) => ({ ...p, contrast: v / 100 }))}
          onCommit={(v) => commit('contrast')(v / 100)}
          onClear={() => commit('contrast')(0)}
        />
        <EditSlider
          label="Saturation"
          value={pos.saturation * 100}
          display={pct(pos.saturation)}
          min={-100}
          max={100}
          step={2}
          neutral={0}
          onChange={(v) => setPos((p) => ({ ...p, saturation: v / 100 }))}
          onCommit={(v) => commit('saturation')(v / 100)}
          onClear={() => commit('saturation')(0)}
        />
        <div className="flex items-center gap-2 rounded-[9px] border border-primary/22 bg-primary/8 px-3 py-2.5">
          <Info className="size-3.5 shrink-0 text-[#aab0ff]" strokeWidth={1.5} />
          <span className="text-[11.5px] leading-snug text-secondary-foreground">
            Absolute edits? Open one in Develop and Paste settings across.
          </span>
        </div>
      </div>
      <div className="mt-auto flex items-center gap-2 border-t px-4 py-[13px] text-[11.5px] text-muted-foreground">
        {busy > 0 ? (
          <>
            <span className="size-1.5 shrink-0 animate-pulse rounded-full bg-primary" />
            Applying to {ids.length} photos…
          </>
        ) : (
          <>
            <span className="size-1.5 shrink-0 rounded-full bg-primary" />
            Thumbnails update live as you drag
          </>
        )}
      </div>
    </div>
  );
}
