import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import { Pipette, Undo2, Redo2, Crop, ChevronRight, Info, RotateCcw, Image as ImageIcon } from 'lucide-react';
import { useFolderScan } from '@/lib/useFolderScan';
import type { Photo } from '@/api/library';
import { cn } from '@/lib/utils';
import { applyRating, applyFlag } from '@/lib/actions';
import { applyBatchEdit, type Delta, type Params } from '@/api/edits';
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
import { useUIStore } from '@/stores/uiStore';
import {
  esAuto,
  esCanRedo,
  esCanUndo,
  esCommit,
  esLoad,
  esRedo,
  esSetActive,
  esSetApplyIds,
  esSetCropping,
  esSetWBPicking,
  esUndo,
  esUpdate,
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
    crop: groupChanged(draft, ['cropX', 'cropY', 'cropW', 'cropH', 'cropAngle']),
    tone: groupChanged(draft, [
      'expEV', 'expPreserve', 'bright', 'gamma', 'shadow',
      'contrast', 'whites', 'blacks', 'toneShadows', 'toneHighlights',
    ], seedExpEV),
    presence: groupChanged(draft, ['clarity', 'texture', 'dehaze']),
    wb: groupChanged(draft, ['wbMode', 'wbMul', 'wbTemp', 'wbTint', 'wbKelvin']),
    color: groupChanged(draft, [
      'saturation', 'vibrance',
      'splitShadowHue', 'splitShadowAmt', 'splitHighlightHue', 'splitHighlightAmt',
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

      <Group id="crop" title="Crop & straighten" changed={changed.crop}>
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
                title="A crop or straighten is applied"
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
              draft.wbMode === 'custom'
                ? 'Picked white balance — click to pick a new neutral gray'
                : 'Pick white balance: click a neutral gray in the image'
            }
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
  if (key === 'wbMul') return (v as number[]).every((m) => m === 0);
  if (key === 'demosaic') return (v as string) === '';
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
