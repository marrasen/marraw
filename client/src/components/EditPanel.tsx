import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import {
  getEditParams,
  previewEdit,
  setEditParams,
  resetEdits,
  applyBatchEdit,
  pasteEditParams,
  type Params,
} from '@/api/edits';
import { useApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Slider } from '@/components/ui/slider';
import { Separator } from '@/components/ui/separator';
import { Progress } from '@/components/ui/progress';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { useUIStore } from '@/stores/uiStore';

export const NEUTRAL: Params = {
  expEV: 0,
  expPreserve: 0,
  wbMode: 'camera',
  wbMul: [0, 0, 0, 0],
  bright: 0,
  highlight: 0,
  nrThreshold: 0,
  fbddNoiseRd: 0,
  medPasses: 0,
};

const HIGHLIGHT_OPTIONS = [
  { value: 0, label: 'Clip' },
  { value: 1, label: 'Unclip' },
  { value: 2, label: 'Blend' },
  { value: 3, label: 'Rebuild (soft)' },
  { value: 5, label: 'Rebuild' },
  { value: 9, label: 'Rebuild (strong)' },
];

export function EditPanel() {
  const selection = useUIStore((s) => s.selection);
  const focusId = useUIStore((s) => s.focusId);
  if (selection.size > 1) return <BatchPanel ids={[...selection]} />;
  if (focusId == null) {
    return <div className="p-4 text-sm text-muted-foreground">Select a photo to edit.</div>;
  }
  return <SinglePanel key={focusId} photoId={focusId} />;
}

function SinglePanel({ photoId }: { photoId: number }) {
  const client = useApiClient();
  const [draft, setDraft] = useState<Params | null>(null);
  const setPreviewHash = useUIStore((s) => s.setPreviewHash);
  const setClipboard = useUIStore((s) => s.setClipboard);
  const clipboard = useUIStore((s) => s.clipboard);

  const previewTimer = useRef<number>(0);
  const previewAbort = useRef<AbortController | null>(null);

  useEffect(() => {
    let alive = true;
    getEditParams(client, photoId)
      .then((p) => alive && setDraft(p ?? { ...NEUTRAL }))
      .catch(() => alive && setDraft({ ...NEUTRAL }));
    return () => {
      alive = false;
      window.clearTimeout(previewTimer.current);
      previewAbort.current?.abort();
      setPreviewHash(null);
    };
  }, [client, photoId, setPreviewHash]);

  if (!draft) return <div className="p-4 text-sm text-muted-foreground">Loading edits…</div>;

  // update: change the draft and (debounced) render a live preview.
  const update = (patch: Partial<Params>) => {
    const next = { ...draft, ...patch };
    setDraft(next);
    window.clearTimeout(previewTimer.current);
    previewTimer.current = window.setTimeout(() => {
      previewAbort.current?.abort();
      const ac = new AbortController();
      previewAbort.current = ac;
      previewEdit(client, photoId, next, { signal: ac.signal })
        .then((r) => setPreviewHash(r.editHash))
        .catch(() => {}); // aborted or superseded
    }, 150);
  };

  // commit: persist on slider release / control close.
  const commit = (patch?: Partial<Params>) => {
    const params = patch ? { ...draft, ...patch } : draft;
    setEditParams(client, photoId, params).catch((err) => toast.error(`Save failed: ${err.message}`));
  };

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4 text-sm">
      <h2 className="font-medium">Develop</h2>

      <EditSlider
        label="Exposure"
        value={draft.expEV}
        display={`${draft.expEV >= 0 ? '+' : ''}${draft.expEV.toFixed(2)} EV`}
        min={-2}
        max={3}
        step={0.05}
        onChange={(v) => update({ expEV: v })}
        onCommit={(v) => commit({ expEV: v })}
      />
      <EditSlider
        label="Brightness"
        value={draft.bright === 0 ? 1 : draft.bright}
        display={`${(draft.bright === 0 ? 1 : draft.bright).toFixed(2)}×`}
        min={0.25}
        max={4}
        step={0.05}
        onChange={(v) => update({ bright: v })}
        onCommit={(v) => commit({ bright: v })}
      />

      <div className="flex flex-col gap-1.5">
        <span className="text-xs text-muted-foreground">Highlights</span>
        <Select
          value={String(draft.highlight)}
          onValueChange={(v) => {
            update({ highlight: Number(v) });
            commit({ highlight: Number(v) });
          }}
        >
          <SelectTrigger className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              {HIGHLIGHT_OPTIONS.map((o) => (
                <SelectItem key={o.value} value={String(o.value)}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      </div>

      <div className="flex flex-col gap-1.5">
        <span className="text-xs text-muted-foreground">White balance</span>
        <ToggleGroup
          className="w-full"
          value={[draft.wbMode === 'auto' ? 'auto' : 'camera']}
          onValueChange={(groupValue) => {
            const v = (groupValue as string[])[0];
            if (v) {
              update({ wbMode: v as Params['wbMode'] });
              commit({ wbMode: v as Params['wbMode'] });
            }
          }}
        >
          <ToggleGroupItem value="camera" className="flex-1">
            As shot
          </ToggleGroupItem>
          <ToggleGroupItem value="auto" className="flex-1">
            Auto
          </ToggleGroupItem>
        </ToggleGroup>
      </div>

      <EditSlider
        label="Noise reduction"
        value={draft.nrThreshold}
        display={draft.nrThreshold === 0 ? 'Off' : String(Math.round(draft.nrThreshold))}
        min={0}
        max={1000}
        step={25}
        onChange={(v) => update({ nrThreshold: v })}
        onCommit={(v) => commit({ nrThreshold: v })}
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
          onClick={() => {
            if (!clipboard) return;
            setDraft(clipboard);
            setEditParams(client, photoId, clipboard).catch((err) => toast.error(err.message));
          }}
        >
          Paste
        </Button>
        <Button
          size="sm"
          variant="outline"
          onClick={() => {
            setDraft({ ...NEUTRAL });
            setPreviewHash(null);
            resetEdits(client, [photoId]).catch((err) => toast.error(err.message));
          }}
        >
          Reset
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        Drag a slider for a live preview; release to save. Ctrl+C / Ctrl+V copies edits between photos.
      </p>
    </div>
  );
}

function EditSlider({
  label,
  value,
  display,
  min,
  max,
  step,
  onChange,
  onCommit,
}: {
  label: string;
  value: number;
  display: string;
  min: number;
  max: number;
  step: number;
  onChange: (v: number) => void;
  onCommit: (v: number) => void;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-baseline justify-between">
        <span className="text-xs text-muted-foreground">{label}</span>
        <span className="text-xs tabular-nums">{display}</span>
      </div>
      <Slider
        value={value}
        min={min}
        max={max}
        step={step}
        onValueChange={(v) => onChange(v as number)}
        onValueCommitted={(v) => onCommit(v as number)}
      />
    </div>
  );
}

function BatchPanel({ ids }: { ids: number[] }) {
  const client = useApiClient();
  const clipboard = useUIStore((s) => s.clipboard);
  const [ev, setEv] = useState(0.5);
  const [progress, setProgress] = useState<number | null>(null);

  const run = (fn: () => Promise<void>, label: string) => {
    setProgress(0);
    fn()
      .then(() => toast.success(label))
      .catch((err) => toast.error(err.message))
      .finally(() => setProgress(null));
  };

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4 text-sm">
      <h2 className="font-medium">Batch edit — {ids.length} photos</h2>

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

      <Separator />

      <div className="flex flex-wrap gap-2">
        <Button
          size="sm"
          variant="outline"
          disabled={!clipboard || progress != null}
          onClick={() => clipboard && run(() => pasteEditParams(client, ids, clipboard), 'Edit settings pasted')}
        >
          Paste settings
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={progress != null}
          onClick={() => run(() => resetEdits(client, ids), 'Edits reset')}
        >
          Reset all
        </Button>
      </div>
    </div>
  );
}
