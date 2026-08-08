// The Local tab's retouch spots: the list of heal, clone and fill spots and
// the controls for the selected one. Split out of EditPanel alongside the
// masks it sits next to.

import { AIModelDialog } from '@/components/AIModelDialog';
import type { ApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Circle, Eye, EyeOff, Focus, Loader2, Paintbrush, ScanSearch, Trash2 } from 'lucide-react';
import { EditSlider } from '@/components/edit/controls';
import type { Params, Spot } from '@/api/edit';
import type { PendingAIDownload } from '@/components/AIModelDialog';
import type { SpotMode } from '@/lib/editSession';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { cn } from '@/lib/utils';
import { esCommit, esConfirmFillDownload, esDeclineFillDownload, esRemoveSpot, esSetActiveSpot, esSetHealing, esSetSpotBrush, esSetSpotMode, esSetSpotTool, esSetSpotVisualize, esSetSpotVisualizeThreshold, esSetTintSpot, esUpdateSpot, useEditSession } from '@/lib/editSession';
import { fillModelStatus } from '@/api/edits';
import { pct } from '@/components/edit/controlUtils';
import { useEffect, useState } from 'react';
import { useUIStore } from '@/stores/uiStore';
export function RetouchSection({ client, draft }: { client: ApiClient; draft: Params }) {
  const healing = useEditSession((s) => s.healing);
  const activeSpot = useEditSession((s) => s.activeSpot);
  const spotMode = useEditSession((s) => s.spotMode);
  const spotTool = useEditSession((s) => s.spotTool);
  const brushRadius = useEditSession((s) => s.spotBrushRadius);
  const brushFeather = useEditSession((s) => s.spotBrushFeather);
  const visualize = useEditSession((s) => s.spotVisualize);
  const visualizeThreshold = useEditSession((s) => s.spotVisualizeThreshold);
  const fillConsent = useEditSession((s) => s.fillConsent);
  const setMode = useUIStore((s) => s.setMode);
  const spots = draft.spots ?? [];
  // Download-consent dialog for the fill model: fillConsent is set when the
  // server refused a fill for lack of the model; the dialog opens once the
  // model size arrives (the fetch resolves in ms — a local stat).
  const [fillPending, setFillPending] = useState<PendingAIDownload | null>(null);
  useEffect(() => {
    if (fillConsent == null) return;
    let stale = false;
    fillModelStatus(client)
      .then((st) => {
        if (!stale) setFillPending({ kind: 'fill', bytes: st.bytes, mode: 'add' });
      })
      .catch(() => {
        if (!stale) setFillPending({ kind: 'fill', bytes: 0, mode: 'add' });
      });
    return () => {
      stale = true;
    };
  }, [client, fillConsent]);
  return (
    <div className="flex flex-col gap-2">
      <Button
        size="sm"
        variant={healing ? 'default' : 'outline'}
        className="justify-start"
        onClick={() => {
          if (!healing) setMode('develop'); // the overlay lives on the Develop canvas
          esSetHealing(!healing);
        }}
        title="Heal / spot removal (Q)"
      >
        <Focus data-icon="inline-start" />
        {healing ? 'Done healing' : 'Heal spots'}
        <span className="ml-auto flex items-center gap-1.5">
          {spots.length > 0 && (
            <span
              className="rounded-[4px] bg-primary/18 px-1 py-px text-[9px] font-semibold tracking-[.05em] text-accent-text uppercase"
              title={`${spots.length} spot${spots.length > 1 ? 's' : ''}`}
            >
              {spots.length}
            </span>
          )}
          <kbd className="text-[10px] opacity-60">Q</kbd>
        </span>
      </Button>
      {healing && (
        <>
          <div className="flex items-center gap-1.5">
            <ToggleGroup
              className="flex-1"
              value={[spotTool]}
              onValueChange={(g) => {
                const v = (g as string[])[0];
                if (v) esSetSpotTool(v as 'spot' | 'brush');
              }}
              aria-label="Retouch tool"
            >
              <ToggleGroupItem value="spot" className="flex-1" title="Circular spot: click, or drag to size">
                <Circle data-icon="inline-start" />
                Spot
              </ToggleGroupItem>
              <ToggleGroupItem value="brush" className="flex-1" title="Paint an arbitrary region to heal">
                <Paintbrush data-icon="inline-start" />
                Brush
              </ToggleGroupItem>
            </ToggleGroup>
            <Button
              size="icon-sm"
              variant={visualize ? 'default' : 'outline'}
              title="Visualize spots: high-pass dust view (A)"
              aria-pressed={visualize}
              onClick={() => esSetSpotVisualize(!visualize)}
            >
              <ScanSearch />
            </Button>
          </div>
          <ToggleGroup
            className="flex-1"
            value={[spotMode]}
            onValueChange={(g) => {
              const v = (g as string[])[0];
              if (v) esSetSpotMode(v as SpotMode);
            }}
            aria-label="New spot mode"
          >
            <ToggleGroupItem value="heal" className="flex-1" title="Match source texture to the destination">
              Heal
            </ToggleGroupItem>
            <ToggleGroupItem value="clone" className="flex-1" title="Copy the source verbatim">
              Clone
            </ToggleGroupItem>
            <ToggleGroupItem value="fill" className="flex-1" title="Inpaint the region from its surround (ML)">
              Fill
            </ToggleGroupItem>
          </ToggleGroup>
          {spotTool === 'brush' && (
            <>
              <EditSlider
                label="Size"
                value={brushRadius * 100}
                display={String(Math.round(brushRadius * 200))}
                min={0.3}
                max={15}
                step={0.1}
                onChange={(v) => esSetSpotBrush({ spotBrushRadius: v / 100 })}
                onCommit={(v) => esSetSpotBrush({ spotBrushRadius: v / 100 })}
              />
              <EditSlider
                label="Feather"
                value={brushFeather * 100}
                display={String(Math.round(brushFeather * 100))}
                min={0}
                max={100}
                step={2}
                onChange={(v) => esSetSpotBrush({ spotBrushFeather: v / 100 })}
                onCommit={(v) => esSetSpotBrush({ spotBrushFeather: v / 100 })}
              />
            </>
          )}
          {visualize && (
            <EditSlider
              label="Sensitivity"
              value={visualizeThreshold * 100}
              display={String(Math.round(visualizeThreshold * 100))}
              min={0}
              max={100}
              step={2}
              onChange={(v) => esSetSpotVisualizeThreshold(v / 100)}
              onCommit={(v) => esSetSpotVisualizeThreshold(v / 100)}
            />
          )}
        </>
      )}
      {spots.length > 0 ? (
        <div className="flex flex-col gap-1.5">
          {spots.map((spot, i) => (
            <SpotRow
              key={i}
              client={client}
              spot={spot}
              index={i}
              selected={i === activeSpot}
              onSelect={() => esSetActiveSpot(i === activeSpot ? null : i)}
            />
          ))}
        </div>
      ) : (
        <p className="text-xs text-muted-foreground">
          {healing
            ? spotTool === 'brush'
              ? 'Paint over a blemish; marraw fills the painted region from a nearby patch — drag the dashed copy to choose a different source.'
              : 'Click a dust spot or blemish on the photo; drag to size it. marraw fills it from a nearby patch — drag the dashed circle to choose a different source.'
            : 'Remove sensor dust and blemishes. Spots stay anchored to image content through crops and straightens.'}
        </p>
      )}
      <AIModelDialog
        pending={fillConsent != null ? fillPending : null}
        onConfirm={() => {
          setFillPending(null);
          esConfirmFillDownload(client);
        }}
        onCancel={() => {
          setFillPending(null);
          esDeclineFillDownload(client);
        }}
      />
    </div>
  );
}

function SpotRow({
  client,
  spot,
  index,
  selected,
  onSelect,
}: {
  client: ApiClient;
  spot: Spot;
  index: number;
  selected: boolean;
  onSelect: () => void;
}) {
  const mode: SpotMode = spot.mode === 'clone' || spot.mode === 'fill' ? spot.mode : 'heal';
  const stroke = spot.kind === 'stroke';
  const fillBusy = useEditSession((s) => s.fillBusy.includes(index));
  // A stroke spot's edge softness lives per-stroke; surface the first stroke's
  // value and write changes back to every stroke in the region.
  const feather = (stroke ? spot.strokes?.[0]?.feather : spot.feather) ?? 0.5;
  const patchFeather = (v: number): Partial<Spot> =>
    stroke
      ? { strokes: (spot.strokes ?? []).map((st) => ({ ...st, feather: v })) }
      : { feather: v };
  // Opacity 0 means "full" on the wire (the Flow precedent); show it as 100%.
  const opacity = spot.opacity && spot.opacity > 0 ? spot.opacity : 1;
  const commitPatch = (p: Partial<Spot>) => {
    esUpdateSpot(client, index, p);
    esCommit(client);
  };
  return (
    <div className={cn('flex flex-col rounded-md border', selected ? 'border-primary/45' : 'border-border')}>
      <div
        className="flex items-center gap-1.5 px-2 py-1.5"
        onMouseEnter={() => esSetTintSpot(index)}
        onMouseLeave={() => esSetTintSpot(null)}
      >
        <button
          type="button"
          className={cn('flex flex-1 items-center gap-1.5 text-left', spot.disabled && 'opacity-45')}
          onClick={onSelect}
          aria-pressed={selected}
        >
          <span className="text-[11.5px] text-secondary-foreground">
            {stroke ? 'Brush' : 'Spot'} {index + 1} · {mode}
          </span>
          {fillBusy && <Loader2 className="size-3 animate-spin text-muted-foreground" />}
        </button>
        <button
          type="button"
          className={spot.disabled ? 'text-accent-text' : 'text-muted-foreground hover:text-foreground'}
          title={spot.disabled ? 'Show spot' : 'Hide spot'}
          aria-label={spot.disabled ? 'Show spot' : 'Hide spot'}
          aria-pressed={!!spot.disabled}
          onClick={() => commitPatch({ disabled: spot.disabled ? undefined : true })}
        >
          {spot.disabled ? <EyeOff className="size-3" /> : <Eye className="size-3" />}
        </button>
        <button
          type="button"
          className="text-muted-foreground hover:text-foreground"
          title="Delete spot"
          aria-label="Delete spot"
          onClick={() => esRemoveSpot(client, index)}
        >
          <Trash2 className="size-3" />
        </button>
      </div>
      {selected && (
        <div className="flex flex-col gap-[7px] px-2 pb-2">
          <ToggleGroup
            className="flex-1"
            value={[mode]}
            onValueChange={(g) => {
              const v = (g as string[])[0];
              if (v) commitPatch({ mode: v === 'heal' ? undefined : (v as SpotMode) });
            }}
            aria-label="Spot mode"
          >
            <ToggleGroupItem value="heal" className="flex-1">Heal</ToggleGroupItem>
            <ToggleGroupItem value="clone" className="flex-1">Clone</ToggleGroupItem>
            <ToggleGroupItem value="fill" className="flex-1">Fill</ToggleGroupItem>
          </ToggleGroup>
          <EditSlider
            label="Feather"
            value={feather * 100}
            display={pct(feather)}
            min={0}
            max={100}
            step={1}
            neutral={50}
            onChange={(v) => esUpdateSpot(client, index, patchFeather(v / 100))}
            onCommit={(v) => commitPatch(patchFeather(v / 100))}
            onClear={() => commitPatch(patchFeather(0.5))}
          />
          <EditSlider
            label="Opacity"
            value={opacity * 100}
            display={`${Math.round(opacity * 100)}%`}
            min={10}
            max={100}
            step={1}
            neutral={100}
            onChange={(v) => esUpdateSpot(client, index, { opacity: v >= 100 ? undefined : v / 100 })}
            onCommit={(v) => commitPatch({ opacity: v >= 100 ? undefined : v / 100 })}
            onClear={() => commitPatch({ opacity: undefined })}
          />
        </div>
      )}
    </div>
  );
}

// LocalPanel is the Local tab: the local, targeted corrections. Masks — add
// buttons, the mask list, and the selected mask's adjustment sliders (plus
// the brush tool row) — and the Retouch group (heal/clone spots). Masks live
// in draft.masks, so every change flows through the same esUpdate/esCommit
// path as any slider; the on-canvas shape/paint overlay is MaskOverlay on the
// Develop loupe, driven by the same activeMask state. Mirrors DevelopPanel's
// shell: held lastDraft through photo switches (inert input meanwhile),
// undo/redo in the header.
// CurvePanel is the Curve tab: the point-curve editor on its own, where the
// square canvas has room to be big. Mirrors LocalPanel's shell (header, target
// badge, undo/redo) and reuses the same draft/commit plumbing as DevelopPanel.
