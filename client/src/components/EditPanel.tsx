import { useEffect, useState } from 'react';
import {
  Pipette, Undo2, Redo2, Crop, ChevronRight, Aperture, Loader2,
  Image as ImageIcon,
} from 'lucide-react';
import { useFolderScan } from '@/lib/useFolderScan';
import type { Photo } from '@/api/library';
import { cn } from '@/lib/utils';
import { applyRating, applyFlag } from '@/lib/actions';
// (aprot's camelCasing lowercases exactly one leading character: aIModelStatus.)
import {
  CURVE_KEYS,
  curveOf,
  hasToneCurve,
  type CurveKey,
} from '@/lib/toneCurve';
import { useApiClient, type ApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import {
  AmtSlider,
  ButtonRow,
  EditRangeSlider,
  EditSlider,
  HueSlider,
  PctSlider,
} from '@/components/edit/controls';
import { useAIMapGate } from '@/components/edit/useAIMapGate';
import { TILT_DEFAULT, tiltShift } from '@/lib/controlSpecs';
import { TEMP_GRADIENT, TINT_GRADIENT, pct, useActiveScroll } from '@/components/edit/controlUtils';
import { MasksSection } from '@/components/edit/MasksSection';
import { BatchPanel } from '@/components/edit/BatchPanel';
import { ColorMixer, ToneCurve } from '@/components/edit/CurveEditors';
import { RetouchSection } from '@/components/edit/RetouchSection';
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
  esWBPickDone,
  useEditSession,
  NEUTRAL,
  type ControlId,
  type GroupId,
} from '@/lib/editSession';

import type { Params } from '@/api/edit';
import type { LensProfileInfo } from '@/api/edits';
import { lensProfile } from '@/api/edits';
const TREATMENT_OPTIONS = [
  { value: 0, label: 'Color' },
  { value: 1, label: 'B&W' },
];

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


// variant tells the two mount sites apart: the Develop drawer carries the full
// tab strip, the Library aside is read-only info for a single photo (editing
// there means opening Develop). Both share the batch panel.
export function EditPanel({ photos, variant }: { photos: Photo[]; variant: 'library' | 'develop' }) {
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
      void esLoad(client, focusId, ids, photos.find((p) => p.id === focusId)?.baseExpEV ?? 0);
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
  const photo = photos.find((p) => p.id === focusId);
  // A multi-photo selection swaps the panel for the batch stack: relative
  // deltas, the whole-selection paste/restore pair, and presets.
  if (ids.length > 1) {
    return <BatchPanel client={client} ids={ids} photo={photo} />;
  }
  if (variant === 'library') {
    return <LibraryInfoPanel photo={photo} photos={photos} />;
  }
  return <SinglePhotoPanel client={client} photo={photo} photos={photos} targetCount={ids.length} />;
}

// LibraryInfoPanel: the Library aside for a single photo — the identity/cull
// header over the read-only info stack. No tabs and no navigator: the grid has
// no loupe image to navigate, and the edit controls live in Develop.
function LibraryInfoPanel({ photo, photos }: { photo?: Photo; photos: Photo[] }) {
  return (
    <div className="flex h-full flex-col overflow-hidden">
      {photo && <PhotoHeader photo={photo} />}
      <div className="min-h-0 flex-1 overflow-y-auto">
        {photo && <InfoPanel photo={photo} photos={photos} showNavigator={false} />}
      </div>
    </div>
  );
}

// SinglePhotoPanel: the identity/cull header, then the Develop / Local /
// Presets / Info tab strip and its content. Tab state is client-only (uiStore) so it
// persists across the two mount sites (Develop drawer ⇄ Library aside).
const TAB_ITEMS = [
  { value: 'develop' as const, label: 'Develop' },
  // The point curve gets its own tab: the editor is a square canvas, which
  // crowds the Develop slider stack out of view when it sits inline.
  { value: 'curve' as const, label: 'Curve' },
  // "Local" holds the local, targeted corrections — masks and retouch spots —
  // as opposed to Develop's global sliders. The value stays 'masks' (it is
  // client-only uiStore state, but keyboard.ts and the palette key off it).
  { value: 'masks' as const, label: 'Local' },
  { value: 'presets' as const, label: 'Presets' },
  { value: 'info' as const, label: 'Info' },
];

function SinglePhotoPanel({
  client,
  photo,
  photos,
  targetCount,
}: {
  client: ApiClient;
  photo?: Photo;
  photos: Photo[];
  targetCount: number;
}) {
  const tab = useUIStore((s) => s.developTab);
  const setTab = useUIStore((s) => s.setDevelopTab);
  // The Curve tab carries the same "has adjustments" dot as a panel group —
  // otherwise a curve set on another tab is invisible from Develop.
  const curveSet = useEditSession((s) => {
    const d = s.draft ?? s.lastDraft;
    return !!d && CURVE_KEYS.some((k) => hasToneCurve(curveOf(d, k)));
  });
  const items = TAB_ITEMS.map((t) =>
    t.value === 'curve' && curveSet
      ? {
          ...t,
          label: (
            <span className="flex items-center gap-1">
              {t.label}
              <span className="size-[5px] rounded-full bg-primary" title="Has adjustments" />
            </span>
          ),
        }
      : t,
  );
  return (
    <div className="flex h-full flex-col overflow-hidden">
      {photo && <PhotoHeader photo={photo} />}
      <div className="px-4 pt-[11px] pb-1">
        <Segmented size="sm" aria-label="Panel" value={tab} onValueChange={setTab} items={items} />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {tab === 'develop' && (
          <>
            {photo && <Histogram photo={photo} />}
            <DevelopPanel client={client} photo={photo} targetCount={targetCount} />
          </>
        )}
        {tab === 'curve' && (
          <>
            {photo && <Histogram photo={photo} />}
            <CurvePanel client={client} targetCount={targetCount} />
          </>
        )}
        {tab === 'masks' && (
          <>
            {photo && <Histogram photo={photo} />}
            <LocalPanel client={client} targetCount={targetCount} />
          </>
        )}
        {tab === 'presets' && <PresetsPanel client={client} photo={photo} targetCount={targetCount} />}
        {tab === 'info' && photo && <InfoPanel photo={photo} photos={photos} />}
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
      'bw', 'saturation', 'vibrance',
      'splitShadowHue', 'splitShadowAmt', 'splitHighlightHue', 'splitHighlightAmt',
      'hslHue', 'hslSat', 'hslLum',
    ]),
    effects: groupChanged(draft, ['vignette', 'tiltAmount', 'tiltLo', 'tiltHi']),
    detail: groupChanged(draft, [
      'sharpen', 'highlight', 'nrThreshold', 'fbddNoiseRd', 'medPasses',
      'demosaic', 'caRed', 'caBlue',
      'lensMode', 'lensDistortion', 'lensVignetting', 'lensCA',
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
          min={-5}
          max={5}
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
            onClick={() => (wbPicking ? esWBPickDone(client) : esSetWBPicking(client, true))}
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
        {/* The treatment switch leads the group: everything below it reads
            differently depending on which side it's on. */}
        <ButtonRow
          label="Treatment"
          active={activeControl === 'bw'}
          options={TREATMENT_OPTIONS}
          value={draft.bw ? 1 : 0}
          onChange={(v) => {
            update({ bw: v === 1 });
            commit({ bw: v === 1 });
          }}
        />
        {/* Saturation and vibrance have nothing to act on once the frame is
            gray; the tints below stay live — they're what makes sepia. */}
        <PctSlider label="Saturation" hotkey="A" field="saturation" draft={draft} update={update} commit={commit} disabled={draft.bw} {...num('saturation')} />
        <PctSlider label="Vibrance" hotkey="V" field="vibrance" draft={draft} update={update} commit={commit} disabled={draft.bw} {...num('vibrance')} />
        <HueSlider label="Shadow tint" field="splitShadowHue" draft={draft} update={update} commit={commit} {...num('splitShadowHue')} />
        <AmtSlider label="Shadow tint amount" field="splitShadowAmt" draft={draft} update={update} commit={commit} {...num('splitShadowAmt')} />
        <HueSlider label="Highlight tint" field="splitHighlightHue" draft={draft} update={update} commit={commit} {...num('splitHighlightHue')} />
        <AmtSlider label="Highlight tint amount" field="splitHighlightAmt" draft={draft} update={update} commit={commit} {...num('splitHighlightAmt')} />
        <ColorMixer draft={draft} update={update} commit={commit} clear={clear} />
      </Group>

      <Group id="effects" title="Effects" changed={changed.effects}>
        <PctSlider label="Vignette" hotkey="O" field="vignette" draft={draft} update={update} commit={commit} {...num('vignette')} />
        <TiltShiftRows client={client} draft={draft} update={update} commit={commit} />
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

        <LensRows
          client={client}
          photoId={photo?.id}
          draft={draft}
          update={update}
          commit={commit}
          clear={clear}
          num={num}
        />
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

// The tilt-shift params, which stand or fall together on the amount.
const TILT_KEYS = ['tiltAmount', 'tiltLo', 'tiltHi', 'tiltMapVer'] as const;

// TiltShiftRows is the develop panel's depth-graded defocus: an amount and the
// depth band that stays sharp. Unlike every other control in this panel it
// cannot simply be dragged up from zero — the render needs a depth map for the
// photo, so switching it on runs the model (behind the download-consent gate
// every AI feature shares) and stamps the version it produced. Once on, the
// two sliders are ordinary params; clearing the amount takes the window and the
// stamp with it, so the panel reads the same as what the server stores.
function TiltShiftRows({
  client,
  draft,
  update,
  commit,
}: {
  client: ApiClient;
  draft: Params;
  update: (patch: Partial<Params>) => void;
  commit: (patch?: Partial<Params>) => void;
}) {
  const photoId = useEditSession((s) => s.photoId);
  const gate = useAIMapGate(client, photoId);
  const amount = draft.tiltAmount ?? 0;
  const off = () => {
    const cleared = { tiltAmount: 0, tiltLo: 0, tiltHi: 0, tiltMapVer: '' };
    update(cleared);
    commit(cleared);
  };

  if (amount === 0) {
    return (
      <>
        <div className="flex items-center gap-2.5 rounded-md">
          <span className="w-[96px] shrink-0 truncate text-[11.5px] text-secondary-foreground">
            Tilt shift
          </span>
          <Button
            variant="outline"
            size="sm"
            className="min-w-0 flex-1 justify-start"
            disabled={photoId == null || gate.generating != null}
            data-testid="tilt-enable"
            title="Blur by distance, keeping a depth band sharp (runs a local model)"
            onClick={() => gate.request('depth', (res) => commit(tiltShift(res.mapVer)))}
          >
            {gate.generating === 'depth' ? (
              <Loader2 data-icon="inline-start" className="animate-spin" />
            ) : (
              <Aperture data-icon="inline-start" />
            )}
            Blur by distance
          </Button>
        </div>
        {gate.dialog}
      </>
    );
  }

  const lo = draft.tiltLo ?? 0;
  const hi = draft.tiltHi ?? 0;
  return (
    <>
      <EditSlider
        label="Tilt shift"
        value={amount * 100}
        display={String(Math.round(amount * 100))}
        min={0}
        max={100}
        step={2}
        neutral={0}
        onChange={(v) => update({ tiltAmount: v / 100 })}
        onCommit={(v) => (v === 0 ? off() : commit({ tiltAmount: v / 100 }))}
        onClear={off}
      />
      {/* 1 is nearest, the depth map's own convention — so the window reads
          left-to-right as far → near, like the depth mask's. */}
      <EditRangeSlider
        label="Focus range"
        value={[lo * 100, hi * 100]}
        display={`${Math.round(lo * 100)}–${Math.round(hi * 100)}`}
        min={0}
        max={100}
        step={1}
        neutral={[TILT_DEFAULT.tiltLo * 100, TILT_DEFAULT.tiltHi * 100]}
        onChange={([l, h]) => update({ tiltLo: l / 100, tiltHi: h / 100 })}
        onCommit={([l, h]) => commit({ tiltLo: l / 100, tiltHi: h / 100 })}
        onClear={() => commit({ tiltLo: TILT_DEFAULT.tiltLo, tiltHi: TILT_DEFAULT.tiltHi })}
      />
      {gate.dialog}
    </>
  );
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
  // Color is the default, and the server omits the field entirely for it —
  // so absent and false both have to read as unchanged.
  if (key === 'bw') return !v;
  // Tilt shift is omitted the same way when off, and its window and map
  // version only mean anything while the amount is up — so an off effect is
  // unchanged whatever they happen to hold.
  if (TILT_KEYS.includes(key as (typeof TILT_KEYS)[number])) return !draft.tiltAmount;
  // A tone curve (master or per-channel) is default when it bends nothing.
  if (CURVE_KEYS.includes(key as CurveKey)) return !hasToneCurve(curveOf(draft, key as CurveKey));
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

// RetouchSection is the Local-tab Retouch group: the heal-tool toggle, the
// clone/heal mode for newly placed spots, and the list of spots. Spots live in
// draft.spots, so add/move/remove flow through the ordinary esUpdate/esCommit
// path (history, copy/paste and persistence come for free).
function CurvePanel({ client, targetCount }: { client: ApiClient; targetCount: number }) {
  const liveDraft = useEditSession((s) => s.draft);
  const draft = useEditSession((s) => s.draft ?? s.lastDraft);
  const canUndo = useEditSession(esCanUndo);
  const canRedo = useEditSession(esCanRedo);
  if (!draft) return <div className="p-4 text-sm text-muted-foreground">Loading edits…</div>;
  const update = (patch: Partial<Params>) => esUpdate(client, patch);
  const commit = (patch?: Partial<Params>) => esCommit(client, patch);
  const clear = (patch: Partial<Params>) => {
    update(patch);
    commit(patch);
  };
  return (
    <div className={cn('flex flex-col px-4 pt-1 pb-3 text-sm', !liveDraft && 'pointer-events-none')}>
      <div className="mb-2 flex items-center gap-2">
        <h2 className="text-[13px] font-medium">Curve</h2>
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
      <ToneCurve draft={draft} update={update} commit={commit} clear={clear} />
      <p className="mt-4 text-xs text-muted-foreground">
        Drag a point to move it, click the grid to add one, double-click a
        point to remove it. RGB shapes overall tone; the R, G and B channels
        grade color on top of it, and the channels you are not editing stay
        drawn as guides. The curve can never invert tones.
      </p>
    </div>
  );
}

function LocalPanel({ client, targetCount }: { client: ApiClient; targetCount: number }) {
  const liveDraft = useEditSession((s) => s.draft);
  const draft = useEditSession((s) => s.draft ?? s.lastDraft);
  const canUndo = useEditSession(esCanUndo);
  const canRedo = useEditSession(esCanRedo);
  if (!draft) return <div className="p-4 text-sm text-muted-foreground">Loading edits…</div>;
  return (
    <div className={cn('flex flex-col px-4 pt-1 pb-3 text-sm', !liveDraft && 'pointer-events-none')}>
      <div className="mb-2 flex items-center gap-2">
        <h2 className="text-[13px] font-medium">Local</h2>
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
        AI-detected area carrying its own exposure, tone and color. They apply
        top to bottom, each over the result of the ones above it — drag a mask
        by its grip to move it in the stack. Subject and Depth run a local
        model once per photo; masks stay anchored to image content through
        crops and straightens.
      </p>
      <Group id="retouch" title="Retouch" changed={(draft.spots?.length ?? 0) > 0}>
        <RetouchSection client={client} draft={draft} />
      </Group>
    </div>
  );
}

// LensRows renders the lens-profile section: what was matched, whether the
// correction is on, and how far each of its three components goes.
//
// The controls are deliberately offsets from the profile rather than raw
// amounts. A profile is a measurement of what the lens did to the frame, so
// its own figure is the neutral — 100% — and the slider exists for the cases
// where the photographer disagrees: keeping some of a wide lens's vignette
// because it flatters the subject, or backing distortion off on a portrait
// where perfectly straight edges look worse than slightly curved ones.
function LensRows({
  client,
  photoId,
  draft,
  update,
  commit,
  clear,
  num,
}: {
  client: ApiClient;
  photoId?: number;
  draft: Params;
  update: (patch: Partial<Params>) => void;
  commit: (patch?: Partial<Params>) => void;
  clear: (patch: Partial<Params>) => void;
  num: (control: ControlId) => { active: boolean; onFocusControl: () => void };
}) {
  const info = useLensProfileInfo(client, photoId);
  const off = draft.lensMode === 'off';

  // Nothing to say before the lookup lands, and nothing worth a section on a
  // file that records no lens at all (adapted and manual glass, mostly) —
  // there is no correction to offer and no setting that would change that.
  if (!info || (!info.lens && !info.profile)) return null;

  const amount = (field: 'lensDistortion' | 'lensVignetting' | 'lensCA') =>
    ((draft[field] ?? 0) + 1) * 100;

  const row = (
    label: string,
    field: 'lensDistortion' | 'lensVignetting' | 'lensCA',
    control: ControlId,
    available: boolean,
  ) => (
    <EditSlider
      label={label}
      value={amount(field)}
      display={available ? `${Math.round(amount(field))}%` : '—'}
      min={0}
      max={200}
      step={5}
      neutral={100}
      // A slider for a correction this profile never measured would be a
      // lie: the lens is matched, but that component of it isn't in the
      // database.
      disabled={off || !available}
      onChange={(v) => update({ [field]: v / 100 - 1 })}
      onCommit={(v) => commit({ [field]: v / 100 - 1 })}
      onClear={() => clear({ [field]: 0 })}
      {...num(control)}
    />
  );

  return (
    <div className="flex flex-col gap-1.5 border-t pt-3">
      <ButtonRow
        label="Lens correction"
        options={LENS_MODE_OPTIONS}
        value={off ? 'off' : 'auto'}
        onChange={(v) => {
          const patch = { lensMode: (v === 'off' ? 'off' : '') as Params['lensMode'] };
          update(patch);
          commit(patch);
        }}
      />
      <p className="text-xs text-muted-foreground">
        {info.profile ? (
          <>
            {info.profile}
            {info.focal > 0 && ` · ${formatFocal(info.focal)}`}
            {info.aperture > 0 && ` · f/${trimNum(info.aperture)}`}
          </>
        ) : info.cameraKnown ? (
          <>No profile for “{info.lens}”.</>
        ) : (
          <>This camera body isn’t in the lens database, so no profile can be matched.</>
        )}
      </p>
      {info.profile && (
        <>
          {row('Distortion', 'lensDistortion', 'lensDistortion', info.hasDistortion)}
          {row('Vignetting', 'lensVignetting', 'lensVignetting', info.hasVignetting)}
          {row('Chromatic aberration', 'lensCA', 'lensCA', info.hasCA)}
        </>
      )}
    </div>
  );
}

const LENS_MODE_OPTIONS = [
  { value: 'auto', label: 'Auto' },
  { value: 'off', label: 'Off' },
];

// trimNum drops the trailing zeros EXIF rationals leave behind (2.7999999).
function trimNum(v: number): string {
  return String(Math.round(v * 10) / 10);
}

function formatFocal(mm: number): string {
  return `${trimNum(mm)}mm`;
}

// useLensProfileInfo fetches the matched profile for one photo. The answer
// depends only on the file's EXIF, so it is refetched on photo change and
// never during an edit.
function useLensProfileInfo(client: ApiClient, photoId?: number): LensProfileInfo | null {
  // The photo the answer belongs to is stored WITH it, so switching photos
  // reads as "not loaded yet" without an effect that clears state on the way
  // in — a synchronous setState in an effect body is a cascading render.
  const [loaded, setLoaded] = useState<{ photoId: number; info: LensProfileInfo | null } | null>(null);
  useEffect(() => {
    if (!photoId) return;
    let live = true;
    lensProfile(client, photoId)
      .then((info) => {
        if (live) setLoaded({ photoId, info });
      })
      .catch(() => {
        // A failed lookup is indistinguishable from an unprofiled lens as
        // far as the panel is concerned: no profile, no section.
        if (live) setLoaded({ photoId, info: null });
      });
    return () => {
      live = false;
    };
  }, [client, photoId]);
  return loaded && loaded.photoId === photoId ? loaded.info : null;
}

// The ±1 params rendered as ±100 sliders share everything but the field.


// The HSL color mixer: eight fixed hue bands (mirroring pyramid.HSLBandCenters
// on the Go side, chip order = band order), a chip row to pick the band, and
// Hue/Saturation/Luminance sliders for the picked band. A dot on a chip marks
// a band carrying an adjustment.