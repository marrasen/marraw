// The Local tab's masks feature: the list of masks, each row's shape controls
// (AI, range, brush) and the local adjustments under them.
//
// Split out of EditPanel, where it sat between the retouch spots and the tone
// curve — about a third of that file, and the part that changes most. It talks
// to the edit session through the same es* helpers as any other control.
import { useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';
import {
  Pipette, ChevronRight, RotateCcw, Image as Plus, Trash2, Paintbrush, Circle, Eraser,
  Eye, EyeOff, Focus, GripVertical, Layers, Loader2, Shapes, Users, Check, Blend, Aperture,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { fillModelStatus } from '@/api/edits';
import type { AIMapResult } from '@/api/edits';
import { AIModelDialog, type PendingAIDownload } from '@/components/AIModelDialog';
import { useAIMapGate } from '@/components/edit/useAIMapGate';
import type { AIKindType, Mask, MaskAdjust, Params } from '@/api/edit';
import {
  DEPTH_WINDOW_DEFAULT,
  RANGE_LUMA_DEFAULT,
  RANGE_HUE_DEFAULT,
  MASK_CONTROL_ORDER,
  MASK_CONTROL_SPECS,
  MASK_FX_ORDER,
  aiClassMask,
  aiMask,
  aiPersonMask,
  backgroundMask,
  tiltShiftMask,
  isMaskShapeControl,
  maskAdjustIsNeutral,
  maskCanRemove,
  maskHasFX,
  maskLabel,
  type MaskControlId,
  type MaskShapeControlId,
} from '@/lib/controlSpecs';
import { type ApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import {
  EditRangeSlider,
  EditSlider,
} from '@/components/edit/controls';
import { HUE_GRADIENT, TEMP_GRADIENT, TINT_GRADIENT, pct } from '@/components/edit/controlUtils';
import { bumpImgBust } from '@/lib/imgCacheBust';
import { useUIStore } from '@/stores/uiStore';
import {
  esAddMask,
  esAddMaskObject,
  esClearAIMapRestore,
  esCommit,
  esMoveMask,
  esRemoveMask,
  esSetActiveMask,
  esSetActiveMaskControl,
  esSetRangePicking,
  esConfirmMaskFillDownload,
  esDeclineMaskFillDownload,
  esToggleMaskRemove,
  esSetTintMask,
  esSetAIHover,
  esArmAIPick,
  esSetAIDetect,
  esSetBrushTool,
  esSetMaskPaint,
  esUpdate,
  esUpdateMask,
  useEditSession,
} from '@/lib/editSession';

export function MasksSection({ client, draft }: { client: ApiClient; draft: Params }) {
  const activeMask = useEditSession((s) => s.activeMask);
  // Both AI region-mask detections live in the session, shared with the
  // AIPickOverlay: people instances and scene categories. The store clears
  // them on a photo switch, so the panel keeps no per-photo copy.
  const aiDetect = useEditSession((s) => s.aiDetect);
  const aiPickArmed = useEditSession((s) => s.aiPickArmed);
  const aiMapRestore = useEditSession((s) => s.aiMapRestore);
  const personDetect = aiDetect.person;
  const sceneDetect = aiDetect.class;
  const photoId = useEditSession((s) => s.photoId);
  const setMode = useUIStore((s) => s.setMode);
  // Consent + generation for every AI-map kind this section offers; only what
  // to do with the finished map is local (applyAIMap).
  const aiGate = useAIMapGate(client, photoId);
  const generating = aiGate.generating;
  // The same gate for the inpainting model behind a mask's Remove pill — kept
  // apart from pendingAI because declining reverts the flag rather than just
  // closing (the Retouch group's fill dialog precedent).
  const maskFillConsent = useEditSession((s) => s.maskFillConsent);
  const [pendingMaskFill, setPendingMaskFill] = useState<PendingAIDownload | null>(null);
  useEffect(() => {
    if (maskFillConsent == null) return;
    let stale = false;
    fillModelStatus(client)
      .then((st) => {
        if (!stale) setPendingMaskFill({ kind: 'fill', bytes: st.bytes, mode: 'add' });
      })
      .catch(() => {
        if (!stale) setPendingMaskFill({ kind: 'fill', bytes: 0, mode: 'add' });
      });
    return () => {
      stale = true;
    };
  }, [client, maskFillConsent]);
  const masks = useMemo(() => draft.masks ?? [], [draft.masks]);
  // A chip is "added" when the draft already carries its mask — marked with a
  // check, but still clickable (a second mask of the same region with its own
  // adjustments is a legitimate thing to want).
  const isAdded = (kind: 'class' | 'person', id: number) =>
    masks.some((m) => m.type === 'ai' && m.aiKind === kind && m.classId === id);
  const add = (type: Mask['type']) => {
    setMode('develop'); // the overlay lives on the Develop canvas
    esAddMask(client, type);
  };

  // What to do with a generated map, by mode: add a fresh mask / show scene
  // chips, or — for a restore — nudge a preview re-render so the now-live
  // mask shows. The consent and generation around it are useAIMapGate's.
  const applyAIMap = (
    kind: AIKindType,
    res: AIMapResult,
    mode: 'add' | 'restore',
    variant: 'subject' | 'background' | 'tilt' = 'subject',
  ) => {
    if (photoId == null) return;
    {
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
      } else if (kind === 'class' || kind === 'person') {
        // Scene / People detection adds no mask by itself: it fills the chip
        // row and arms the loupe pick tool — hover a chip or the photo to
        // highlight a region, click to add its mask (staying armed for more).
        setMode('develop');
        if (kind === 'class') {
          esSetAIDetect('class', { mapVer: res.mapVer, categories: res.categories ?? [] });
          if ((res.categories ?? []).length > 0) esArmAIPick('class');
          else toast.info('No distinct regions detected in this photo.');
        } else {
          esSetAIDetect('person', { mapVer: res.mapVer, instances: res.instances ?? [] });
          if ((res.instances ?? []).length > 0) esArmAIPick('person');
          else toast.info('No people detected in this photo.');
        }
      } else {
        setMode('develop');
        let m: Mask;
        if (kind === 'subject' && variant === 'background') m = backgroundMask(res.mapVer);
        else if (kind === 'depth' && variant === 'tilt') m = tiltShiftMask(res.mapVer);
        else m = aiMask(kind as 'subject' | 'depth', res.mapVer);
        esAddMaskObject(client, m);
      }
    }
  };

  // Button path: the gate handles the consent ask and the generation.
  const addAI = (
    kind: 'subject' | 'depth' | 'class' | 'person',
    variant: 'subject' | 'background' | 'tilt' = 'subject',
  ) => {
    aiGate.request(kind, (res) => applyAIMap(kind, res, 'add', variant), { variant });
  };

  // Generating the maps an AI mask references is the edit session's job (it
  // has to happen wherever the masks land — a paste from another photo, a
  // preset, a sidecar from another machine — not just while this tab is
  // mounted). All that's left here is the consent dialog it can't show
  // itself: esEnsureAIMaps parks the kind whose model is missing, and this
  // turns it into the download ask, once.
  useEffect(() => {
    if (aiMapRestore == null) return;
    esClearAIMapRestore(); // consumed — the dialog owns the ask from here
    const kind = aiMapRestore;
    aiGate.request(kind, (res) => applyAIMap(kind, res, 'restore'), { mode: 'restore' });
    // aiGate.request is stable enough for this one-shot hand-off; re-running
    // on every render of the section would re-ask.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, aiMapRestore]);

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
        <Button
          size="sm"
          variant="outline"
          className="flex-1"
          title="Add a luminance / colour range mask — select pixels by tone and hue"
          onClick={() => add('range')}
        >
          <Blend data-icon="inline-start" />
          Range
        </Button>
      </div>
      {/* Five buttons no longer fit one line at the drawer's width, so this
          row wraps; basis-[30%] keeps it to three per line rather than
          letting one long label strand the others. */}
      <div className="flex flex-wrap gap-1.5" role="group" aria-label="Add AI mask">
        <Button
          size="sm"
          variant="outline"
          className="flex-1 basis-[30%]"
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
          className="flex-1 basis-[30%]"
          title="Mask everything except the subject and separate it with glow, light streaks and prism (runs a local model)"
          disabled={generating != null}
          onClick={() => addAI('subject', 'background')}
          data-testid="ai-mask-background"
        >
          {generating === 'subject' ? <Loader2 data-icon="inline-start" className="animate-spin" /> : <Aperture data-icon="inline-start" />}
          Background
        </Button>
        <Button
          size="sm"
          variant="outline"
          className="flex-1 basis-[30%]"
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
          className="flex-1 basis-[30%]"
          title="Blur by distance: keep a depth band sharp and defocus the rest, blur growing with distance — set the band with Focus distance and Focus depth (runs a local model)"
          disabled={generating != null}
          onClick={() => addAI('depth', 'tilt')}
          data-testid="ai-mask-tilt"
        >
          {generating === 'depth' ? <Loader2 data-icon="inline-start" className="animate-spin" /> : <Aperture data-icon="inline-start" />}
          Tilt shift
        </Button>
        <Button
          size="sm"
          variant="outline"
          className={cn('flex-1 basis-[30%]', aiPickArmed === 'class' && 'border-primary/60 text-foreground')}
          title="Detect scene regions (sky, foliage, architecture, …) — hover the photo or a chip to pick one to mask (runs a local model)"
          disabled={generating != null}
          aria-pressed={aiPickArmed === 'class'}
          onClick={() => {
            // Toggle: re-press disarms; a cached detection re-arms with no RPC.
            if (aiPickArmed === 'class') esArmAIPick(null);
            else if (sceneDetect) {
              setMode('develop');
              esArmAIPick('class');
            } else void addAI('class');
          }}
          data-testid="ai-mask-scene"
        >
          {generating === 'class' ? <Loader2 data-icon="inline-start" className="animate-spin" /> : <Shapes data-icon="inline-start" />}
          Scene
        </Button>
        <Button
          size="sm"
          variant="outline"
          className={cn('flex-1 basis-[30%]', aiPickArmed === 'person' && 'border-primary/60 text-foreground')}
          title="Separate individual people — hover the photo or a chip to pick one to mask (runs a local model)"
          disabled={generating != null}
          aria-pressed={aiPickArmed === 'person'}
          onClick={() => {
            // Toggle: re-press disarms; a cached detection re-arms with no RPC.
            if (aiPickArmed === 'person') esArmAIPick(null);
            else if (personDetect) {
              setMode('develop');
              esArmAIPick('person');
            } else void addAI('person');
          }}
          data-testid="ai-mask-person"
        >
          {generating === 'person' ? <Loader2 data-icon="inline-start" className="animate-spin" /> : <Users data-icon="inline-start" />}
          People
        </Button>
      </div>
      {sceneDetect && (
        <div className="flex flex-wrap gap-1" role="group" aria-label="Detected regions" data-testid="scene-chips">
          {sceneDetect.categories.length === 0 && (
            <span className="px-1 text-[11px] text-muted-foreground">No distinct regions detected.</span>
          )}
          {sceneDetect.categories.map((c) => {
            const added = isAdded('class', c.id);
            return (
              <button
                key={c.id}
                type="button"
                className={cn(
                  'inline-flex items-center gap-0.5 rounded-full border px-2 py-0.5 text-[11px] text-secondary-foreground hover:border-primary/45 hover:text-foreground',
                  added ? 'border-primary/45 text-foreground' : 'border-border',
                )}
                title={`Mask ${c.name} (${Math.round(c.fraction * 100)}% of frame)`}
                onMouseEnter={() => esSetAIHover({ kind: 'class', id: c.id })}
                onMouseLeave={() => esSetAIHover(null)}
                onClick={() => {
                  setMode('develop');
                  esAddMaskObject(client, aiClassMask(c.id, sceneDetect.mapVer));
                }}
              >
                {added && <Check className="size-2.5" />}
                {c.name} · {Math.round(c.fraction * 100)}%
              </button>
            );
          })}
        </div>
      )}
      {personDetect && personDetect.instances.length > 0 && (
        <div className="flex flex-wrap gap-1" role="group" aria-label="Detected people" data-testid="person-chips">
          {personDetect.instances.map((p) => {
            const added = isAdded('person', p.id);
            return (
              <button
                key={p.id}
                type="button"
                className={cn(
                  'inline-flex items-center gap-0.5 rounded-full border px-2 py-0.5 text-[11px] text-secondary-foreground hover:border-primary/45 hover:text-foreground',
                  added ? 'border-primary/45 text-foreground' : 'border-border',
                )}
                title={`Mask person ${p.id} (${Math.round(p.fraction * 100)}% of frame)`}
                onMouseEnter={() => esSetAIHover({ kind: 'person', id: p.id })}
                onMouseLeave={() => esSetAIHover(null)}
                onClick={() => {
                  setMode('develop');
                  esAddMaskObject(client, aiPersonMask(p.id, personDetect.mapVer));
                }}
              >
                {added && <Check className="size-2.5" />}
                Person {p.id} · {Math.round(p.fraction * 100)}%
              </button>
            );
          })}
        </div>
      )}
      {masks.map((m, i) => (
        <MaskRow
          key={i}
          client={client}
          mask={m}
          index={i}
          reorderable={masks.length > 1}
          selected={activeMask === i}
          onSelect={() => {
            setMode('develop');
            esSetActiveMask(activeMask === i ? null : i);
          }}
        />
      ))}
      {aiGate.dialog}
      <AIModelDialog
        pending={maskFillConsent != null ? pendingMaskFill : null}
        onConfirm={() => {
          setPendingMaskFill(null);
          esConfirmMaskFillDownload(client);
        }}
        onCancel={() => {
          setPendingMaskFill(null);
          esDeclineMaskFillDownload(client);
        }}
      />
    </div>
  );
}

// Drag payload for a mask row: the index it started at. Custom types must be
// lowercase or dataTransfer.types won't match them (the marraw/preset and
// marraw/block precedent).
const MASK_DRAG_TYPE = 'marraw/mask';

function MaskRow({
  client,
  mask,
  index,
  reorderable,
  selected,
  onSelect,
}: {
  client: ApiClient;
  mask: Mask;
  index: number;
  reorderable: boolean;
  selected: boolean;
  onSelect: () => void;
}) {
  const activeMaskControl = useEditSession((s) => s.activeMaskControl);
  const maskFillBusy = useEditSession((s) => s.maskFillBusy.includes(index));
  const adjust = mask.adjust ?? {};
  const changed = !maskAdjustIsNeutral(adjust);
  const fxChanged = maskHasFX(adjust);
  const canRemove = maskCanRemove(mask);
  // Collapse state is pure UI, not photo state, so it stays local — the
  // BrushToolRow precedent. Opens on its own when the mask already carries an
  // effect (a preset, a pasted look, the Background button).
  const [fxExpanded, setFxExpanded] = useState(fxChanged);
  // Lit while another mask is dragged over this row: the drop lands the dragged
  // mask in THIS slot and pushes this one aside, so the highlight has to name
  // the destination, not a gap between rows.
  const [dropHere, setDropHere] = useState(false);
  // Also forced open while the keyboard walk is inside the group, so ↑/↓ can
  // never park the focus ring on a slider hidden behind a collapsed header.
  const fxOpen =
    fxExpanded ||
    (selected &&
      activeMaskControl != null &&
      !isMaskShapeControl(activeMaskControl) &&
      MASK_FX_ORDER.includes(activeMaskControl));
  const patchAdjust = (key: MaskControlId, v: number): { adjust: MaskAdjust } => ({
    adjust: { ...adjust, [key]: v },
  });
  // One slider, with the display convention taken from the spec's unit rather
  // than from a chain of per-control special cases.
  const maskSlider = (key: MaskControlId) => {
    const spec = MASK_CONTROL_SPECS[key];
    const raw = adjust[key] ?? 0;
    const scale = spec.unit ? 1 : 100;
    const display =
      spec.unit === 'ev' ? `${raw >= 0 ? '+' : ''}${raw.toFixed(2)} EV`
      : spec.unit === 'deg' ? `${Math.round(raw)}°`
      : pct(raw);
    const set = (v: number) => patchAdjust(key, v / scale);
    return (
      <EditSlider
        key={key}
        label={spec.label}
        value={raw * scale}
        display={display}
        min={spec.min * scale}
        max={spec.max * scale}
        step={spec.step * scale}
        neutral={0}
        gradient={key === 'temp' ? TEMP_GRADIENT : key === 'tint' ? TINT_GRADIENT : undefined}
        active={selected && activeMaskControl === key}
        onFocusControl={() => esSetActiveMaskControl(index, key)}
        onChange={(v) => esUpdateMask(client, index, set(v))}
        onCommit={(v) => {
          esUpdateMask(client, index, set(v));
          esCommit(client);
        }}
        onClear={() => {
          esUpdateMask(client, index, patchAdjust(key, 0));
          esCommit(client);
        }}
      />
    );
  };
  return (
    <div
      className={cn(
        'flex flex-col rounded-md border',
        dropHere ? 'border-primary' : selected ? 'border-primary/45' : 'border-border',
      )}
      data-testid="mask-row"
      // Every row is a drop target, including the dragged one's own neighbours
      // — dropping onto the last row is how a mask gets moved to the bottom.
      onDragOver={(e) => {
        if (!e.dataTransfer.types.includes(MASK_DRAG_TYPE)) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
        setDropHere(true);
      }}
      onDragLeave={() => setDropHere(false)}
      onDrop={(e) => {
        setDropHere(false);
        if (!e.dataTransfer.types.includes(MASK_DRAG_TYPE)) return;
        const from = Number(e.dataTransfer.getData(MASK_DRAG_TYPE));
        // getData returns '' for a payload that isn't ours, and Number('') is
        // 0 — a valid mask index — so the empty string has to be rejected
        // before the number is believed.
        if (!Number.isInteger(from) || from === index) return;
        e.preventDefault();
        e.stopPropagation();
        esMoveMask(client, from, index);
      }}
    >
      {/* Hovering the row header shows this mask's red weight tint on the
          loupe (the only way to SEE an AI mask's detected region); it fades
          out on leave. */}
      <div
        className="flex items-center gap-1.5 px-2 py-1.5"
        onMouseEnter={() => esSetTintMask(index)}
        onMouseLeave={() => esSetTintMask(null)}
      >
        {/* Drag by the grip, never by the row: the header's own click selects
            the mask and the body below it is full of sliders. */}
        {reorderable && (
          <span
            draggable
            className="-ml-1 cursor-grab text-faint active:cursor-grabbing hover:text-foreground"
            title="Drag onto another mask to reorder — a mask applies over the ones above it"
            data-testid="mask-grip"
            onDragStart={(e) => {
              e.dataTransfer.setData(MASK_DRAG_TYPE, String(index));
              e.dataTransfer.effectAllowed = 'move';
            }}
            // The pointer stays put while the rows move under it, so no
            // mouseleave arrives to take the hover tint down.
            onDragEnd={() => esSetTintMask(null)}
          >
            <GripVertical className="size-3" aria-hidden="true" />
          </span>
        )}
        <button
          type="button"
          className={cn('flex flex-1 items-center gap-1.5 text-left', mask.disabled && 'opacity-45')}
          onClick={onSelect}
          aria-pressed={selected}
        >
          <span className="text-[11.5px] text-secondary-foreground">{maskLabel(mask, index)}</span>
          {changed && <span className="size-[5px] shrink-0 rounded-full bg-primary" title="Has adjustments" />}
          {maskFillBusy && <Loader2 className="size-3 animate-spin text-muted-foreground" />}
        </button>
        {/* Remove: inpaint the masked region away. Only on types whose region
            is binary and bounded — the server refuses the rest, so offering
            the pill there would just un-toggle itself. */}
        {canRemove && (
          <button
            type="button"
            className={cn(
              'rounded px-1 text-[9px] font-semibold tracking-[.05em] uppercase',
              mask.remove ? 'bg-primary/18 text-accent-text' : 'text-faint hover:text-foreground',
            )}
            title="Remove what this mask covers, filling from the surround"
            aria-pressed={!!mask.remove}
            data-testid="mask-remove-toggle"
            onClick={() => esToggleMaskRemove(client, index, !mask.remove)}
          >
            Remove
          </button>
        )}
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
          className={mask.disabled ? 'text-accent-text' : 'text-muted-foreground hover:text-foreground'}
          title={mask.disabled ? 'Show mask' : 'Hide mask'}
          aria-label={mask.disabled ? 'Show mask' : 'Hide mask'}
          aria-pressed={!!mask.disabled}
          onClick={() => {
            esUpdateMask(client, index, { disabled: mask.disabled ? undefined : true });
            esCommit(client);
          }}
        >
          {mask.disabled ? <EyeOff className="size-3" /> : <Eye className="size-3" />}
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
          {mask.type === 'range' && <RangeShapeRows client={client} mask={mask} index={index} />}
          {MASK_CONTROL_ORDER.map(maskSlider)}
          {/* Effects: spatial, so they behave unlike everything above —
              collapsed by default to keep the tone block readable. */}
          <div className="mt-0.5 flex flex-col gap-[7px] border-t border-border/60 pt-1.5">
            <button
              type="button"
              className="flex items-center gap-1.5 text-left text-[10px] font-semibold tracking-[.05em] text-faint uppercase hover:text-foreground"
              aria-expanded={fxOpen}
              data-testid="mask-fx-toggle"
              onClick={() => setFxExpanded(!fxOpen)}
            >
              <ChevronRight className={cn('size-3 transition-transform', fxOpen && 'rotate-90')} />
              Effects
              {fxChanged && <span className="size-[5px] shrink-0 rounded-full bg-primary" title="Has effects" />}
            </button>
            {fxOpen &&
              MASK_FX_ORDER.map((key) =>
                // The direction only steers the two smears; the server zeroes
                // it when neither is live, so hiding it matches what it does.
                key === 'fxAngle' && !(adjust.motionBlur || adjust.streaks) ? null : maskSlider(key),
              )}
          </div>
        </div>
      )}
    </div>
  );
}

// AIShapeRows: the map-shaping sliders for an AI mask — unlike the brush tool
// row this IS photo state (threshold/feather/depth window live in the mask
// params and change pixels), so every move flows through esUpdateMask.
function AIShapeRows({ client, mask, index }: { client: ApiClient; mask: Mask; index: number }) {
  // These sliders join the ↑/↓ walk (they render above the adjust block), so
  // they carry the same focus ring and focus handoff as maskSlider.
  const focused = useEditSession((s) => (s.activeMask === index ? s.activeMaskControl : null));
  const patch = (p: Partial<Mask>) => esUpdateMask(client, index, p);
  const commit = (p: Partial<Mask>) => {
    esUpdateMask(client, index, p);
    esCommit(client);
  };
  const shapeSlider = (
    id: MaskShapeControlId,
    label: string,
    raw: number,
    displayDefault: number, // shown when raw is 0 (server default)
    onValue: (v: number) => Partial<Mask>,
    min = 0,
  ) => {
    const shown = raw === 0 ? displayDefault : raw;
    return (
      <EditSlider
        key={id}
        label={label}
        value={shown * 100}
        display={pct(shown)}
        min={min * 100}
        max={100}
        step={1}
        neutral={displayDefault * 100}
        active={focused === id}
        onFocusControl={() => esSetActiveMaskControl(index, id)}
        onChange={(v) => patch(onValue(v / 100))}
        onCommit={(v) => commit(onValue(v / 100))}
        onClear={() => commit(onValue(0))}
      />
    );
  };
  return (
    <>
      {/* Threshold's floor is 2%: a raw 0 means "server default (50%)", so the
          slider must never land exactly on 0. Background thresholds the same
          subject matte — the slider moves the boundary either way. */}
      {(mask.aiKind === 'subject' || mask.aiKind === 'background') &&
        shapeSlider('threshold', 'Threshold', mask.threshold ?? 0, 0.5, (v) => ({ threshold: v }), 0.02)}
      {mask.aiKind === 'depth' && <DepthWindowRows mask={mask} patch={patch} commit={commit} />}
      {shapeSlider('feather', 'Edge feather', mask.feather ?? 0, 0, (v) => ({ feather: v }))}
    </>
  );
}

// DepthWindowRows presents the stored [depthLo, depthHi] window as the pair a
// photographer thinks in: Focus distance (the window's centre, 100 = nearest)
// and Focus depth (its width) — the focus ring and the aperture, rather than
// two ends of a band. Pure re-parameterization: centre/width and lo/hi are
// the same window, so the stored params, hashes and sidecars don't move, and
// the pair round-trips exactly through the stored form. Width wins at the
// edges — pushing the distance to an end with a wide window slides the window
// against the edge rather than silently narrowing it.
function DepthWindowRows({
  mask,
  patch,
  commit,
}: {
  mask: Mask;
  patch: (p: Partial<Mask>) => void;
  commit: (p: Partial<Mask>) => void;
}) {
  const lo = mask.depthLo ?? 0;
  const hi = mask.depthHi ?? 0;
  const width = hi - lo;
  const center = (lo + hi) / 2;
  const window = (c: number, w: number): Partial<Mask> => {
    const half = Math.min(w, 1) / 2;
    const cc = Math.min(Math.max(c, half), 1 - half);
    return { depthLo: cc - half, depthHi: cc + half };
  };
  const defC =
    (DEPTH_WINDOW_DEFAULT.depthLo + DEPTH_WINDOW_DEFAULT.depthHi) / 2;
  const defW = DEPTH_WINDOW_DEFAULT.depthHi - DEPTH_WINDOW_DEFAULT.depthLo;
  return (
    <>
      <EditSlider
        label="Focus distance"
        value={center * 100}
        display={String(Math.round(center * 100))}
        min={0}
        max={100}
        step={1}
        neutral={defC * 100}
        onChange={(v) => patch(window(v / 100, width))}
        onCommit={(v) => commit(window(v / 100, width))}
        onClear={() => commit(window(defC, width))}
      />
      <EditSlider
        label="Focus depth"
        value={width * 100}
        display={String(Math.round(width * 100))}
        min={2}
        max={100}
        step={1}
        neutral={defW * 100}
        onChange={(v) => patch(window(center, v / 100))}
        onCommit={(v) => commit(window(center, v / 100))}
        onClear={() => commit(window(center, defW))}
      />
    </>
  );
}

// RangeShapeRows: the window controls for a luminance/colour range mask. All
// are photo state (the windows live in the mask params and pick pixels), so
// every move flows through esUpdateMask. Luminance is a plain two-thumb window;
// hue is circular, so it is edited as centre (0–360°, rainbow track) + range
// (±°) and converted to/from the stored [lo,hi] window, which may wrap through
// red. The eyedropper seeds the hue window from a pixel on the photo.
function RangeShapeRows({ client, mask, index }: { client: ApiClient; mask: Mask; index: number }) {
  const picking = useEditSession((s) => s.rangePicking && s.activeMask === index);
  const focused = useEditSession((s) => (s.activeMask === index ? s.activeMaskControl : null));
  const patch = (p: Partial<Mask>) => esUpdateMask(client, index, p);
  const commit = (p: Partial<Mask>) => {
    esUpdateMask(client, index, p);
    esCommit(client);
  };

  const lumaLo = mask.rangeLumaLo ?? 0;
  const lumaHi = mask.rangeLumaHi ?? 1;

  // Stored hue window → centre + tolerance. span uses the wrap-aware branch
  // (matching the server), so the full 0..1 window reads as tol 0.5 (all hues)
  // rather than collapsing to zero.
  const hueLo = mask.rangeHueLo ?? 0;
  const hueHi = mask.rangeHueHi ?? 1;
  let span = hueHi - hueLo;
  if (span < 0) span += 1;
  const tol = span / 2; // 0.5 = the whole wheel
  const center = (hueLo + span / 2) % 1;
  // centreDeg 0..360, tolDeg 0..180 → stored window (wrapping when narrow and
  // near the red seam). tol ≥ 180° means "all hues": the canonical full window.
  const setHue = (centreDeg: number, tolDeg: number, done: boolean) => {
    const t = tolDeg / 360;
    const c = (((centreDeg / 360) % 1) + 1) % 1;
    const next: Partial<Mask> =
      t >= 0.5
        ? { ...RANGE_HUE_DEFAULT }
        : { rangeHueLo: (((c - t) % 1) + 1) % 1, rangeHueHi: (c + t) % 1 };
    (done ? commit : patch)(next);
  };

  const satMin = mask.rangeSatMin ?? 0;
  const feather = mask.feather ?? 0;

  return (
    <>
      <Button
        size="sm"
        variant={picking ? 'default' : 'outline'}
        className="w-full justify-start"
        title="Pick a colour off the photo to select similar hues"
        onClick={() => esSetRangePicking(!picking)}
      >
        <Pipette data-icon="inline-start" />
        {picking ? 'Picking colour…' : 'Pick colour'}
      </Button>
      <EditRangeSlider
        label="Luminance"
        value={[lumaLo * 100, lumaHi * 100]}
        display={`${Math.round(lumaLo * 100)}–${Math.round(lumaHi * 100)}`}
        min={0}
        max={100}
        step={1}
        neutral={[RANGE_LUMA_DEFAULT.rangeLumaLo * 100, RANGE_LUMA_DEFAULT.rangeLumaHi * 100]}
        onChange={([lo, hi]) => patch({ rangeLumaLo: lo / 100, rangeLumaHi: hi / 100 })}
        onCommit={([lo, hi]) => commit({ rangeLumaLo: lo / 100, rangeLumaHi: hi / 100 })}
        onClear={() => commit({ ...RANGE_LUMA_DEFAULT })}
      />
      <EditSlider
        label="Hue centre"
        value={center * 360}
        display={`${Math.round(center * 360)}°`}
        min={0}
        max={360}
        step={1}
        gradient={HUE_GRADIENT}
        onChange={(v) => setHue(v, tol * 360, false)}
        onCommit={(v) => setHue(v, tol * 360, true)}
      />
      <EditSlider
        label="Hue range"
        value={tol * 360}
        display={tol >= 0.5 ? 'All' : `±${Math.round(tol * 360)}°`}
        min={0}
        max={180}
        step={1}
        neutral={180}
        onChange={(v) => setHue(center * 360, v, false)}
        onCommit={(v) => setHue(center * 360, v, true)}
        onClear={() => commit({ ...RANGE_HUE_DEFAULT })}
      />
      {/* These two are scalar, so they join the ↑/↓ walk like the AI shape
          rows. The windows above cannot: two thumbs, and hue is circular. */}
      <EditSlider
        label="Min saturation"
        value={satMin * 100}
        display={pct(satMin)}
        min={0}
        max={100}
        step={1}
        neutral={0}
        active={focused === 'rangeSatMin'}
        onFocusControl={() => esSetActiveMaskControl(index, 'rangeSatMin')}
        onChange={(v) => patch({ rangeSatMin: v / 100 })}
        onCommit={(v) => commit({ rangeSatMin: v / 100 })}
        onClear={() => commit({ rangeSatMin: 0 })}
      />
      <EditSlider
        label="Edge feather"
        value={feather * 100}
        display={pct(feather)}
        min={0}
        max={100}
        step={1}
        neutral={0}
        active={focused === 'feather'}
        onFocusControl={() => esSetActiveMaskControl(index, 'feather')}
        onChange={(v) => patch({ feather: v / 100 })}
        onCommit={(v) => commit({ feather: v / 100 })}
        onClear={() => commit({ feather: 0 })}
      />
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
