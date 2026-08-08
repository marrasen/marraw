// The multi-selection panel: relative adjustments applied across every
// selected photo, and the actions that go with them. Its sliders send a
// delta rather than a value, which is why they are not the develop ones.

import type { ApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import { ClipboardPaste, RotateCcw } from 'lucide-react';
import type { Delta } from '@/api/edits';
import { EditSlider } from '@/components/edit/controls';
import type { Photo } from '@/api/library';
import { PresetsPanel } from '@/components/PresetsPanel';
import { applyBatchEdit } from '@/api/edits';
import { esApplyParams, esReset } from '@/lib/editSession';
import { pct } from '@/components/edit/controlUtils';
import { toast } from 'sonner';
import { useRef, useState } from 'react';
import { useUIStore } from '@/stores/uiStore';
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

// BatchPanel is the whole right panel while several photos are selected:
// relative deltas on top, then the absolute whole-selection actions and the
// presets — every apply path below follows the session's applyIds, which the
// effect above keeps pinned to this selection.
export function BatchPanel({ client, ids, photo }: { client: ApiClient; ids: number[]; photo?: Photo }) {
  return (
    <div className="flex h-full flex-col overflow-hidden">
      <div className="border-b px-4 pt-[15px] pb-[13px]">
        <span className="text-[10px] tracking-[.07em] text-muted-foreground uppercase">
          Relative adjustment
        </span>
        <span className="mt-1.5 block text-[13px] text-foreground">{ids.length} photos selected</span>
        <div className="mt-1 text-[11.5px] leading-snug text-muted-foreground">
          Deltas add to each photo's own current value — mixed edits stay intact.
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {/* Keyed here rather than on the panel: a changed set restarts the
            deltas at zero without remounting PresetsPanel, whose thumbnails
            cost a render per preset. */}
        <BatchSliders key={ids.join(',')} client={client} ids={ids} />
        <BatchActions client={client} count={ids.length} />
        <PresetsPanel
          client={client}
          photo={photo}
          targetCount={ids.length}
          showClipboardHistory={false}
        />
      </div>
    </div>
  );
}

// BatchActions: the absolute whole-selection actions. They used to sit in the
// grid's SelectionBar, where paste rode applyIds the panel had set — so hiding
// the panel silently narrowed it to the focused photo. Here they can't drift.
function BatchActions({ client, count }: { client: ApiClient; count: number }) {
  const clipboard = useUIStore((s) => s.clipboard);
  return (
    <div className="flex flex-col gap-2 px-4 pb-1">
      <span className="text-[10px] tracking-[.07em] text-muted-foreground uppercase">Selection</span>
      <div className="flex flex-wrap gap-2">
        <Button
          size="sm"
          variant="outline"
          disabled={!clipboard}
          title={clipboard ? `Paste copied settings onto ${count} photos` : 'Copy settings first (Ctrl+C)'}
          onClick={() => {
            if (!clipboard) return;
            esApplyParams(client, clipboard, { label: 'Paste' });
            toast.success(`Settings pasted to ${count} photos`);
          }}
        >
          <ClipboardPaste data-icon="inline-start" />
          Paste settings
        </Button>
        {/* esReset over the raw ResetEdits call the SelectionBar used: same
            RPC across applyIds, but it also reloads the focused draft and
            records the step in history. */}
        <Button
          size="sm"
          variant="outline"
          title="Reset all edits on the selection"
          onClick={() => {
            esReset(client);
            toast.success(`Restoring ${count} photos to original`);
          }}
        >
          <RotateCcw data-icon="inline-start" />
          Restore original
        </Button>
      </div>
    </div>
  );
}

// BatchSliders: relative deltas with NO apply button — each slider release
// applies the increment since the last one, so thumbnails follow the drag and
// mixed per-photo edits stay intact.
function BatchSliders({ client, ids }: { client: ApiClient; ids: number[] }) {
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
    <div className="flex flex-col">
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
      </div>
      <div className="flex items-center gap-2 px-4 pb-3 text-[11.5px] text-muted-foreground">
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
