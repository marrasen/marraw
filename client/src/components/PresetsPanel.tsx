// PresetsPanel is the Presets tab of the develop drawer: one-tap auto options,
// the user's creative-auto presets shown as live preview thumbnails, the
// copy/paste/reset clipboard actions (moved off the develop stack), and a
// clickable edit-history timeline. It drives the same edit-session functions
// the keyboard and command palette do — nothing here holds edit state itself.
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { Copy, ClipboardPaste, Plus, RotateCcw, X } from 'lucide-react';
import type { Photo } from '@/api/library';
import { previewEdit } from '@/api/edits';
import type { Params } from '@/api/edit';
import type { UserPreset } from '@/api/settings';
import type { ApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { AutoPreset } from '@/lib/autoPresets';
import { updateUserPresets } from '@/lib/uiSettings';
import {
  computePresetParams,
  esApplyAutoPreset,
  esApplyParams,
  esAuto,
  esJumpTo,
  esReset,
  NEUTRAL,
  useEditSession,
} from '@/lib/editSession';
import { useUIStore } from '@/stores/uiStore';

const THUMB_PX = 168;

export function PresetsPanel({
  client,
  photo,
  targetCount,
}: {
  client: ApiClient;
  photo?: Photo;
  targetCount: number;
}) {
  const draft = useEditSession((s) => s.draft ?? s.lastDraft);
  const clipboard = useUIStore((s) => s.clipboard);
  const setClipboard = useUIStore((s) => s.setClipboard);
  const setSettingsOpen = useUIStore((s) => s.setSettingsOpen);
  const presets = useUIStore((s) => s.autoPresets);

  return (
    <div className="flex flex-col gap-5 px-4 pt-3 pb-4 text-sm">
      {targetCount > 1 && (
        <span className="-mb-2 w-fit rounded bg-primary/15 px-1.5 py-0.5 text-[11px] text-primary">
          applies to {targetCount} photos
        </span>
      )}

      <Section title="Auto">
        <div className="flex flex-wrap gap-2">
          <Button size="sm" variant="outline" onClick={() => void esAuto(client, ['all'])} title="Auto everything (Ctrl+Alt+U)">
            Auto everything
          </Button>
          <Button size="sm" variant="outline" onClick={() => void esAuto(client, ['tone'])} title="Auto tone (Ctrl+U)">
            Auto tone
          </Button>
          <Button size="sm" variant="outline" onClick={() => void esAuto(client, ['wb', 'color'])} title="Auto colour (Ctrl+Shift+U)">
            Auto colour
          </Button>
        </div>
      </Section>

      <UserPresetsSection client={client} photo={photo} draft={draft} />

      <Section title="Creative presets">
        {presets.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            No presets yet.{' '}
            <button className="text-accent-text hover:underline" onClick={() => setSettingsOpen(true)}>
              Create one in Settings → Auto presets
            </button>
            .
          </p>
        ) : (
          <PresetGrid client={client} photo={photo} presets={presets} />
        )}
      </Section>

      <Section title="Clipboard">
        <div className="flex flex-wrap gap-2">
          <Button
            size="sm"
            variant="outline"
            disabled={!draft}
            onClick={() => {
              if (!draft) return;
              setClipboard(draft);
              toast.success('Edit settings copied');
            }}
          >
            <Copy data-icon="inline-start" />
            Copy
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={!clipboard}
            onClick={() => clipboard && esApplyParams(client, clipboard, { label: 'Paste' })}
          >
            <ClipboardPaste data-icon="inline-start" />
            Paste
          </Button>
          <Button size="sm" variant="outline" onClick={() => esReset(client)}>
            <RotateCcw data-icon="inline-start" />
            Reset
          </Button>
        </div>
      </Section>

      <Section title="History">
        <HistoryList client={client} />
      </Section>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <span className="text-[10px] tracking-[.07em] text-muted-foreground uppercase">{title}</span>
      {children}
    </div>
  );
}

// UserPresetsSection is the saved-looks library: saving snapshots the current
// draft minus geometry (a preset is a look, not a crop), applying lays the
// look over the photo while keeping its own rotation/flip/crop/straighten.
// Cards carry a live low-res render of the look on the focused photo.
function UserPresetsSection({
  client,
  photo,
  draft,
}: {
  client: ApiClient;
  photo?: Photo;
  draft: Params | null;
}) {
  const presets = useUIStore((s) => s.userPresets);
  const [naming, setNaming] = useState(false);
  const [name, setName] = useState('');
  const thumbs = useUserPresetThumbs(client, photo, presets);

  const save = () => {
    const trimmed = name.trim();
    if (!draft || !trimmed) return;
    const params: Params = {
      ...draft,
      rotate: 0,
      flipH: false,
      cropX: 0,
      cropY: 0,
      cropW: 0,
      cropH: 0,
      cropAngle: 0,
      // Masks are local geometry tied to one photo's content, not a look.
      masks: undefined,
    };
    updateUserPresets(client, [...presets, { id: crypto.randomUUID(), name: trimmed, params }]);
    setNaming(false);
    setName('');
    toast.success(`Saved preset “${trimmed}”`);
  };

  const apply = (p: UserPreset) => {
    if (!draft) return;
    esApplyParams(
      client,
      {
        ...p.params,
        rotate: draft.rotate,
        flipH: draft.flipH,
        cropX: draft.cropX,
        cropY: draft.cropY,
        cropW: draft.cropW,
        cropH: draft.cropH,
        cropAngle: draft.cropAngle,
        // The photo keeps its own local adjustments (like its crop) — and a
        // preset saved by an older build can't clobber them either.
        masks: draft.masks,
      },
      { label: p.name },
    );
  };

  const remove = (p: UserPreset) => {
    updateUserPresets(client, presets.filter((x) => x.id !== p.id));
    toast.success(`Removed preset “${p.name}”`);
  };

  return (
    <Section title="My presets">
      {presets.length > 0 && (
        <div className="grid grid-cols-2 gap-2">
          {presets.map((p) => (
            <div key={p.id} className="group relative">
              <button
                className="flex w-full flex-col overflow-hidden rounded-lg border bg-inset text-left transition-colors hover:border-primary/50"
                onClick={() => apply(p)}
                title={`Apply ${p.name} (keeps the photo's crop)`}
              >
                <div className="aspect-[3/2] w-full overflow-hidden bg-black/40">
                  {thumbs[p.id] ? (
                    <img src={thumbs[p.id]} alt="" draggable={false} className="h-full w-full object-cover" />
                  ) : (
                    <div className="h-full w-full animate-pulse bg-white/5" />
                  )}
                </div>
                <span className="truncate px-2 py-1.5 text-[12px] group-hover:text-foreground">{p.name}</span>
              </button>
              <button
                className="absolute top-1 right-1 hidden size-5 items-center justify-center rounded-md bg-black/60 text-white/80 group-hover:flex hover:bg-black/80 hover:text-white"
                onClick={() => remove(p)}
                title={`Delete preset ${p.name}`}
                aria-label={`Delete preset ${p.name}`}
              >
                <X className="size-3" />
              </button>
            </div>
          ))}
        </div>
      )}
      {naming ? (
        <div className="flex items-center gap-2">
          <input
            autoFocus
            className="h-[30px] min-w-0 flex-1 rounded-lg border border-input bg-secondary px-2.5 text-xs text-secondary-foreground outline-none focus:border-ring dark:bg-white/5"
            placeholder="Preset name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            // Stop the global keyboard map from rating/flagging while typing.
            onKeyDown={(e) => {
              e.stopPropagation();
              if (e.key === 'Enter') save();
              if (e.key === 'Escape') setNaming(false);
            }}
            aria-label="Preset name"
          />
          <Button size="sm" onClick={save} disabled={!name.trim()}>
            Save
          </Button>
        </div>
      ) : (
        <Button size="sm" variant="outline" className="w-fit" disabled={!draft} onClick={() => setNaming(true)}>
          <Plus data-icon="inline-start" />
          Save current look
        </Button>
      )}
    </Section>
  );
}

// useUserPresetThumbs renders a small preview of each saved look applied to
// the focused photo. Same lifecycle rules as usePresetThumbs, minus the
// autoAdjust round trip — the params are already absolute.
function useUserPresetThumbs(client: ApiClient, photo: Photo | undefined, presets: UserPreset[]) {
  const [thumbs, setThumbs] = useState<Record<string, string>>({});
  const presetsKey = JSON.stringify(presets.map((p) => p.id));
  const photoId = photo?.id;

  useEffect(() => {
    if (!photo || presets.length === 0) return;
    let alive = true;
    const urls: string[] = [];
    setThumbs({});
    (async () => {
      for (const p of presets) {
        try {
          const blob = await previewEdit(client, photo.id, p.params, THUMB_PX);
          if (!alive) return;
          const url = URL.createObjectURL(blob);
          urls.push(url);
          setThumbs((t) => ({ ...t, [p.id]: url }));
        } catch {
          /* a failed thumbnail just stays a placeholder */
        }
      }
    })();
    return () => {
      alive = false;
      urls.forEach((u) => URL.revokeObjectURL(u));
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, photoId, presetsKey]);

  return thumbs;
}

// PresetGrid renders each creative-auto preset as a card with a live low-res
// render of the preset applied to the focused photo — a quick hunch of the look
// before committing — over its name.
function PresetGrid({
  client,
  photo,
  presets,
}: {
  client: ApiClient;
  photo?: Photo;
  presets: AutoPreset[];
}) {
  const thumbs = usePresetThumbs(client, photo, presets);
  return (
    <div className="grid grid-cols-2 gap-2">
      {presets.map((preset) => (
        <button
          key={preset.id}
          className="group flex flex-col overflow-hidden rounded-lg border bg-inset text-left transition-colors hover:border-primary/50"
          onClick={() => void esApplyAutoPreset(client, preset)}
          title={`Apply ${preset.name}`}
        >
          <div className="aspect-[3/2] w-full overflow-hidden bg-black/40">
            {thumbs[preset.id] ? (
              <img src={thumbs[preset.id]} alt="" draggable={false} className="h-full w-full object-cover" />
            ) : (
              <div className="h-full w-full animate-pulse bg-white/5" />
            )}
          </div>
          <span className="truncate px-2 py-1.5 text-[12px] group-hover:text-foreground">{preset.name}</span>
        </button>
      ))}
    </div>
  );
}

// usePresetThumbs renders a thumbnail per preset for the focused photo. It
// computes each preset's params (auto sections + offsets) the same way applying
// it would, then asks the server for a small preview JPEG. Runs sequentially
// (one autoAdjust + one preview per preset) and revokes its object URLs on
// unmount or when the photo / preset set changes.
function usePresetThumbs(client: ApiClient, photo: Photo | undefined, presets: AutoPreset[]) {
  const [thumbs, setThumbs] = useState<Record<string, string>>({});
  // Re-render when a preset's definition (not just its id) changes.
  const presetsKey = JSON.stringify(presets.map((p) => [p.id, p.sections, p.offsets]));
  const photoId = photo?.id;

  useEffect(() => {
    if (!photo || presets.length === 0) return;
    let alive = true;
    const urls: string[] = [];
    // Snapshot the base ONCE (the draft when this photo / preset set loaded).
    // Keying on photoId (not editHash) keeps the thumbnails a stable function
    // of photo × preset: they regenerate on a photo switch or a preset-def
    // edit, NOT on every commit — a commit only nudges editHash, and firing an
    // autoAdjust + preview round-trip per preset on each one is what made the
    // grid churn.
    const base = useEditSession.getState().draft ?? { ...NEUTRAL };
    setThumbs({});
    (async () => {
      for (const preset of presets) {
        try {
          const params = await computePresetParams(client, photo.id, base, preset);
          const blob = await previewEdit(client, photo.id, params, THUMB_PX);
          if (!alive) return;
          const url = URL.createObjectURL(blob);
          urls.push(url);
          setThumbs((t) => ({ ...t, [preset.id]: url }));
        } catch {
          /* a failed preset thumbnail just stays a placeholder */
        }
      }
    })();
    return () => {
      alive = false;
      urls.forEach((u) => URL.revokeObjectURL(u));
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, photoId, presetsKey]);

  return thumbs;
}

// HistoryList is the focused photo's edit timeline: each labeled snapshot,
// current one highlighted, click to jump straight to it (undo/redo in one hop).
function HistoryList({ client }: { client: ApiClient }) {
  const photoId = useEditSession((s) => s.photoId);
  const historyMap = useEditSession((s) => s.history);
  const entry = photoId != null ? historyMap[photoId] : undefined;

  if (!entry || entry.stack.length <= 1) {
    return <p className="text-xs text-muted-foreground">No edits yet — adjustments show up here.</p>;
  }

  return (
    <div className="flex max-h-[240px] flex-col-reverse gap-0.5 overflow-y-auto">
      {/* Reverse layout: newest at top, oldest ("Original") at the bottom. */}
      {entry.stack.map((snap, i) => {
        const current = i === entry.index;
        const future = i > entry.index;
        return (
          <button
            key={i}
            onClick={() => esJumpTo(client, i)}
            className={cn(
              'flex items-center justify-between rounded-md px-2 py-1 text-left text-[12px] transition-colors',
              current
                ? 'bg-primary/15 text-accent-text'
                : future
                  ? 'text-faint hover:bg-white/5 hover:text-foreground'
                  : 'text-muted-foreground hover:bg-white/5 hover:text-foreground',
            )}
          >
            <span className="truncate">{snap.label}</span>
            {current && <span className="ml-2 shrink-0 text-[10px] tracking-[.06em] uppercase">now</span>}
          </button>
        );
      })}
    </div>
  );
}
