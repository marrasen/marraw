// PresetsPanel is the Presets tab of the develop drawer: one-tap auto options,
// the user's creative-auto presets shown as live preview thumbnails, the
// copy/paste/reset clipboard actions (moved off the develop stack), and a
// clickable edit-history timeline. It drives the same edit-session functions
// the keyboard and command palette do — nothing here holds edit state itself.
import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import { Copy, ClipboardPaste, CopyPlus, Download, Pencil, Plus, RefreshCw, RotateCcw, Upload, X } from 'lucide-react';
import type { Photo } from '@/api/library';
import { aIModelStatus as aiModelStatus, autoAdjust, generateAIMap, previewEdit, suggestEdits } from '@/api/edits';
import type { Suggestion } from '@/api/edits';
import type { AIKindType } from '@/api/edit';
import { AIModelDialog, type PendingAIDownload } from '@/components/AIModelDialog';
import type { Params } from '@/api/edit';
import type { UserPreset } from '@/api/settings';
import type { ApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { AutoPreset } from '@/lib/autoPresets';
import { updateUserPresets } from '@/lib/uiSettings';
import { Slider } from '@/components/ui/slider';
import {
  computePresetParams,
  esApplyAutoPreset,
  esApplyParams,
  esApplyPresetMasks,
  esApplySuggestion,
  esApplyUserPreset,
  esAuto,
  esHoverSuggestion,
  esCommitPresetAmount,
  esHoverAutoPreset,
  esHoverEnd,
  esHoverPreset,
  esJumpTo,
  esReset,
  esSetPresetAmount,
  NEUTRAL,
  resolveUserPreset,
  useEditSession,
  type AutoSection,
} from '@/lib/editSession';
import {
  adaptiveLookDiff,
  aiMaskRecipes,
  PRESET_GROUPS,
  presetSections,
  stripToLook,
  type PresetGroup,
} from '@/lib/presetSections';
import { isModelNotDownloaded } from '@/lib/aiConsent';
import { parseUserPresetsFile, userPresetsFileBlob } from '@/lib/userPresets';
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
        <SuggestionsGrid client={client} photo={photo} />
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

      <AmountSection client={client} />

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

// AmountSection is the post-apply strength scrubber: after a preset lands it
// re-derives the apply at 0–200% (lerp between the pre-apply draft and the
// preset result). Scrubbing amends the preset's single history entry rather
// than stacking undo steps; any other edit, undo, reset, or photo switch
// dismisses it.
function AmountSection({ client }: { client: ApiClient }) {
  const apply = useEditSession((s) => s.lastPresetApply);
  const photoId = useEditSession((s) => s.photoId);
  if (!apply || apply.photoId !== photoId) return null;
  const pct = Math.round(apply.amount * 100);
  return (
    <Section title="Amount">
      <div className="flex items-center gap-2.5">
        <span className="w-20 shrink-0 truncate text-xs text-muted-foreground" title={apply.name}>
          {apply.name}
        </span>
        <div className="min-w-0 flex-1">
          <Slider
            value={pct}
            min={0}
            max={200}
            step={5}
            fillFrom={100}
            aria-label={`${apply.name} amount`}
            onValueChange={(v) => esSetPresetAmount(client, (v as number) / 100)}
            onValueCommitted={(v) => {
              esSetPresetAmount(client, (v as number) / 100);
              esCommitPresetAmount(client);
            }}
          />
        </div>
        <span className="w-14 shrink-0 text-right font-mono text-[11px] text-foreground tabular-nums">
          {pct}%
        </span>
      </div>
    </Section>
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
// draft minus geometry and local adjustments (a preset is a look, not a
// crop), with a section picker choosing which look groups the preset
// carries. Applying overlays only those sections onto the photo's draft.
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
  const [sections, setSections] = useState<PresetGroup[]>(PRESET_GROUPS.map((g) => g.id));
  const [relative, setRelative] = useState(false);
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameVal, setRenameVal] = useState('');
  const [autoSecs, setAutoSecs] = useState<AutoSection[]>([]);
  const [withMasks, setWithMasks] = useState(false);
  // A preset whose AI-mask phase hit a missing model, waiting on download
  // consent; non-null renders the AIModelDialog.
  const [maskConsent, setMaskConsent] = useState<{ preset: UserPreset; pending: PendingAIDownload } | null>(null);
  const draftAIMaskCount = (draft?.masks ?? []).filter((m) => m.type === 'ai').length;
  const importInput = useRef<HTMLInputElement>(null);
  const thumbs = useUserPresetThumbs(client, photo, presets);

  const save = async () => {
    const trimmed = name.trim();
    if (!draft || !trimmed || sections.length === 0) return;
    // Adaptive auto sections only make sense for look groups the preset
    // carries (their names coincide with the group ids by design).
    const autos = autoSecs.filter((a) => sections.includes(a));
    // Painted masks and retouch spots are local geometry tied to one
    // photo's content, not a look; geometry stays with the photo too. AI
    // masks travel as recipes when chosen — applying re-runs detection.
    let params: Params = {
      ...stripToLook(draft),
      masks: withMasks ? aiMaskRecipes(draft) : undefined,
    };
    let rel = relative;
    // The source photo's calibrated exposure baseline: apply re-anchors
    // the look's creative exposure to the target photo's baseline, so a
    // preset saved on a +1.3 EV-compensated shot doesn't drag that
    // compensation onto differently calibrated photos.
    let baseEV = photo?.baseExpEV || undefined;
    if (autos.length > 0 && photo) {
      // Adaptive save: the preset stores the CREATIVE DIFFERENCE between
      // this look and the photo's own auto — applying re-runs the auto on
      // the target and lays the diff on top, so the look adapts per photo.
      let auto: Params;
      try {
        auto = await autoAdjust(client, photo.id, draft, autos);
      } catch (err) {
        toast.error(`Auto adjust failed: ${(err as Error).message}`);
        return;
      }
      params = { ...adaptiveLookDiff(draft, auto), masks: withMasks ? aiMaskRecipes(draft) : undefined };
      rel = true;
      // Exposure rides the per-photo auto result; no baseline to anchor.
      baseEV = undefined;
    }
    const preset: UserPreset = {
      id: crypto.randomUUID(),
      name: trimmed,
      params,
      // All sections checked stores as "all" (empty) — the legacy shape,
      // and new sections added by future builds stay included.
      sections: sections.length === PRESET_GROUPS.length ? undefined : sections,
      relative: rel || undefined,
      baseExpEV: baseEV,
      autoSections: autos.length > 0 ? autos : undefined,
    };
    updateUserPresets(client, [...presets, preset]);
    setNaming(false);
    setName('');
    toast.success(`Saved preset “${trimmed}”`);
  };

  const remove = (p: UserPreset) => {
    updateUserPresets(client, presets.filter((x) => x.id !== p.id));
    toast.success(`Removed preset “${p.name}”`);
  };

  const rename = (p: UserPreset, newName: string) => {
    const trimmed = newName.trim();
    setRenamingId(null);
    if (!trimmed || trimmed === p.name) return;
    updateUserPresets(client, presets.map((x) => (x.id === p.id ? { ...x, name: trimmed } : x)));
  };

  const duplicate = (p: UserPreset) => {
    const copy = { ...p, id: crypto.randomUUID(), name: uniqueName(p.name, presets) };
    const at = presets.findIndex((x) => x.id === p.id) + 1;
    updateUserPresets(client, [...presets.slice(0, at), copy, ...presets.slice(at)]);
  };

  // Overwrite re-snapshots the look from the current draft, keeping the
  // preset's identity and semantics (id → the Ctrl+Shift+n slot and any
  // default-preset reference stay valid; sections/relative unchanged). A
  // preset that carries AI-mask recipes refreshes them from the draft's AI
  // masks; one without stays mask-free.
  const overwrite = (p: UserPreset) => {
    if (!draft) return;
    updateUserPresets(
      client,
      presets.map((x) =>
        x.id === p.id
          ? {
              ...x,
              params: {
                ...stripToLook(draft),
                masks: (x.params.masks?.length ?? 0) > 0 ? aiMaskRecipes(draft) : undefined,
              },
              baseExpEV: photo?.baseExpEV || undefined,
            }
          : x,
      ),
    );
    toast.success(`“${p.name}” overwritten with the current look`);
  };

  const exportAll = () => {
    const url = URL.createObjectURL(userPresetsFileBlob(presets));
    const a = document.createElement('a');
    a.href = url;
    a.download = 'marraw-presets.json';
    a.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  };

  const importFile = async (file: File) => {
    try {
      const imported = parseUserPresetsFile(await file.text());
      if (imported.length === 0) {
        toast.error('No presets found in the file');
        return;
      }
      const taken = [...presets];
      const named = imported.map((p) => {
        const withName = { ...p, name: uniqueName(p.name, taken, true) };
        taken.push(withName);
        return withName;
      });
      updateUserPresets(client, [...presets, ...named]);
      toast.success(`Imported ${named.length} preset${named.length === 1 ? '' : 's'}`);
    } catch (err) {
      toast.error(`Import failed: ${(err as Error).message}`);
    }
  };

  // A preset's mask phase hit a missing model: look up the download size
  // and open the consent dialog (models are never fetched silently).
  const requestMaskConsent = async (p: UserPreset, kind: AIKindType) => {
    const status = await aiModelStatus(client, kind).catch(() => null);
    if (!status) return;
    setMaskConsent({ preset: p, pending: { kind, bytes: status.bytes, mode: 'add' } });
  };

  // Drag a card onto another to reorder (LibraryRail's block pattern). The
  // stored order IS the Ctrl+Shift+1..9 binding order — that's the point.
  const dragProps = (p: UserPreset) => ({
    draggable: true,
    onDragStart: (e: React.DragEvent) => e.dataTransfer.setData('marraw/preset', p.id),
    onDragOver: (e: React.DragEvent) => {
      if (e.dataTransfer.types.includes('marraw/preset')) e.preventDefault();
    },
    onDrop: (e: React.DragEvent) => {
      const from = e.dataTransfer.getData('marraw/preset');
      if (!from || from === p.id) return;
      e.preventDefault();
      const moving = presets.find((x) => x.id === from);
      if (!moving) return;
      const ordered = presets.filter((x) => x.id !== from);
      ordered.splice(ordered.findIndex((x) => x.id === p.id), 0, moving);
      updateUserPresets(client, ordered);
    },
  });

  return (
    <Section title="My presets">
      {presets.length > 0 && (
        <div className="grid grid-cols-2 gap-2">
          {presets.map((p, i) => (
            <div
              key={p.id}
              className="group relative"
              // Hovering the card previews the look full-size on the loupe
              // (debounced, low-res, draft untouched); leaving reverts.
              onMouseEnter={() => esHoverPreset(client, p)}
              onMouseLeave={() => esHoverEnd(client)}
              {...dragProps(p)}
            >
              <button
                className="flex w-full flex-col overflow-hidden rounded-lg border bg-inset text-left transition-colors hover:border-primary/50"
                onClick={() =>
                  esApplyUserPreset(client, p, {
                    onMasksNeedDownload: (kind) => void requestMaskConsent(p, kind),
                  })
                }
                title={`Apply ${p.name} (keeps the photo's crop)${(p.params.masks?.length ?? 0) > 0 ? ' + AI masks' : ''}${i < 9 ? ` (Ctrl+Shift+${i + 1})` : ''} — drag to reorder`}
              >
                <div className="aspect-[3/2] w-full overflow-hidden bg-black/40">
                  {thumbs[p.id] ? (
                    <img src={thumbs[p.id]} alt="" draggable={false} className="h-full w-full object-cover" />
                  ) : (
                    <div className="h-full w-full animate-pulse bg-white/5" />
                  )}
                </div>
                {renamingId === p.id ? (
                  <input
                    autoFocus
                    className="m-1 h-[24px] min-w-0 rounded-md border border-input bg-secondary px-1.5 text-[12px] text-secondary-foreground outline-none focus:border-ring dark:bg-white/5"
                    value={renameVal}
                    onChange={(e) => setRenameVal(e.target.value)}
                    onClick={(e) => e.stopPropagation()}
                    onBlur={() => rename(p, renameVal)}
                    onKeyDown={(e) => {
                      e.stopPropagation();
                      if (e.key === 'Enter') rename(p, renameVal);
                      if (e.key === 'Escape') setRenamingId(null);
                    }}
                    aria-label={`Rename preset ${p.name}`}
                  />
                ) : (
                  <span className="flex items-baseline gap-1.5 truncate px-2 py-1.5">
                    <span className="truncate text-[12px] group-hover:text-foreground">{p.name}</span>
                    {((p.sections?.length ?? 0) > 0 || p.relative || (p.autoSections?.length ?? 0) > 0) && (
                      <span
                        className="shrink-0 text-[9px] tracking-[.05em] text-muted-foreground uppercase"
                        title={`${
                          (p.autoSections?.length ?? 0) > 0
                            ? 'Adaptive preset (re-runs auto per photo)'
                            : p.relative
                              ? 'Relative preset (stacks on existing edits)'
                              : 'Partial preset'
                        }: ${presetSections(p)
                          .map((id) => PRESET_GROUPS.find((g) => g.id === id)?.label ?? id)
                          .join(', ')}`}
                      >
                        {(p.autoSections?.length ?? 0) > 0 ? 'auto ' : p.relative ? '± ' : ''}
                        {(p.sections?.length ?? 0) > 0 ? `${presetSections(p).length}/${PRESET_GROUPS.length}` : ''}
                      </span>
                    )}
                  </span>
                )}
              </button>
              <div className="absolute top-1 right-1 hidden gap-0.5 group-hover:flex">
                <CardAction
                  icon={<Pencil className="size-3" />}
                  label={`Rename preset ${p.name}`}
                  onClick={() => {
                    setRenamingId(p.id);
                    setRenameVal(p.name);
                  }}
                />
                <CardAction
                  icon={<CopyPlus className="size-3" />}
                  label={`Duplicate preset ${p.name}`}
                  onClick={() => duplicate(p)}
                />
                <CardAction
                  icon={<RefreshCw className="size-3" />}
                  label={`Overwrite ${p.name} with the current look`}
                  disabled={!draft}
                  onClick={() => overwrite(p)}
                />
                <CardAction
                  icon={<X className="size-3" />}
                  label={`Delete preset ${p.name}`}
                  onClick={() => remove(p)}
                />
              </div>
            </div>
          ))}
        </div>
      )}
      {naming ? (
        <div className="flex flex-col gap-2">
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
                if (e.key === 'Enter') void save();
                if (e.key === 'Escape') setNaming(false);
              }}
              aria-label="Preset name"
            />
            <Button size="sm" onClick={() => void save()} disabled={!name.trim() || sections.length === 0}>
              Save
            </Button>
          </div>
          <div className="flex flex-wrap items-center gap-1">
            <button
              className={cn(
                'rounded-md border px-1.5 py-0.5 text-[11px] transition-colors',
                (relative || autoSecs.length > 0)
                  ? 'border-primary/50 bg-primary/15 text-accent-text'
                  : 'border-input text-muted-foreground hover:text-foreground',
                autoSecs.length > 0 && 'opacity-60',
              )}
              disabled={autoSecs.length > 0}
              onClick={() => setRelative((r) => !r)}
              title={
                autoSecs.length > 0
                  ? 'Adaptive presets are always relative — the diff lands on each photo’s own auto'
                  : 'Relative: apply the look as offsets on top of a photo’s existing edits instead of replacing them'
              }
            >
              ± Relative
            </button>
            <span className="mx-0.5 h-3.5 w-px bg-border" />
            {PRESET_GROUPS.map((g) => {
              const on = sections.includes(g.id);
              return (
                <button
                  key={g.id}
                  className={cn(
                    'rounded-md border px-1.5 py-0.5 text-[11px] transition-colors',
                    on
                      ? 'border-primary/50 bg-primary/15 text-accent-text'
                      : 'border-input text-muted-foreground hover:text-foreground',
                  )}
                  onClick={() =>
                    setSections((cur) =>
                      // Keep catalog order so the stored list reads stably.
                      on
                        ? cur.filter((s) => s !== g.id)
                        : PRESET_GROUPS.map((x) => x.id).filter((id) => id === g.id || cur.includes(id)),
                    )
                  }
                  title={on ? `Exclude ${g.label} from the preset` : `Include ${g.label} in the preset`}
                >
                  {g.label}
                </button>
              );
            })}
          </div>
          <div className="flex flex-wrap items-center gap-1">
            <span
              className="text-[10px] tracking-[.05em] text-muted-foreground uppercase"
              title="Adaptive: the preset re-runs these autos on each photo and applies your look as the difference from this photo's auto"
            >
              Adapts
            </span>
            {(
              [
                { id: 'tone', label: 'Auto tone' },
                { id: 'wb', label: 'Auto WB' },
                { id: 'color', label: 'Auto colour' },
              ] as { id: AutoSection; label: string }[]
            ).map((a) => {
              const on = autoSecs.includes(a.id);
              const inSections = sections.includes(a.id);
              return (
                <button
                  key={a.id}
                  className={cn(
                    'rounded-md border px-1.5 py-0.5 text-[11px] transition-colors',
                    on && inSections
                      ? 'border-primary/50 bg-primary/15 text-accent-text'
                      : 'border-input text-muted-foreground hover:text-foreground',
                    !inSections && 'opacity-50',
                  )}
                  disabled={!photo || !inSections}
                  onClick={() => setAutoSecs((cur) => (on ? cur.filter((x) => x !== a.id) : [...cur, a.id]))}
                  title={
                    !inSections
                      ? `Include the ${a.id} section to adapt it`
                      : on
                        ? `Don't re-run ${a.label.toLowerCase()} per photo`
                        : `Re-run ${a.label.toLowerCase()} on each photo and layer this look's difference on top`
                  }
                >
                  {a.label}
                </button>
              );
            })}
            {draftAIMaskCount > 0 && (
              <>
                <span className="mx-0.5 h-3.5 w-px bg-border" />
                <button
                  className={cn(
                    'rounded-md border px-1.5 py-0.5 text-[11px] transition-colors',
                    withMasks
                      ? 'border-primary/50 bg-primary/15 text-accent-text'
                      : 'border-input text-muted-foreground hover:text-foreground',
                  )}
                  onClick={() => setWithMasks((w) => !w)}
                  title="Include this photo's AI masks as recipes — applying re-detects the subject/scene/depth on each target photo"
                >
                  AI masks ({draftAIMaskCount})
                </button>
              </>
            )}
          </div>
        </div>
      ) : (
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" className="w-fit" disabled={!draft} onClick={() => setNaming(true)}>
            <Plus data-icon="inline-start" />
            Save current look
          </Button>
          <span className="flex-1" />
          <Button
            size="sm"
            variant="ghost"
            disabled={presets.length === 0}
            onClick={exportAll}
            title="Export all presets to a file"
            aria-label="Export presets"
          >
            <Download />
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => importInput.current?.click()}
            title="Import presets from a file"
            aria-label="Import presets"
          >
            <Upload />
          </Button>
          <input
            ref={importInput}
            type="file"
            accept="application/json,.json"
            className="hidden"
            onChange={(e) => {
              const f = e.target.files?.[0];
              // Reset so re-importing the same file fires onChange again.
              e.target.value = '';
              if (f) void importFile(f);
            }}
          />
        </div>
      )}
      <AIModelDialog
        pending={maskConsent?.pending ?? null}
        onCancel={() => setMaskConsent(null)}
        onConfirm={() => {
          const c = maskConsent;
          setMaskConsent(null);
          // allowDownload is safe to set for the whole retry post-consent —
          // same rule as EyeScanDialog: the user just approved the fetch.
          if (c) void esApplyPresetMasks(client, c.preset, { allowDownload: true });
        }}
      />
    </Section>
  );
}

// CardAction is one tiny hover-revealed icon button on a preset card.
function CardAction({
  icon,
  label,
  onClick,
  disabled,
}: {
  icon: React.ReactNode;
  label: string;
  onClick: () => void;
  disabled?: boolean;
}) {
  return (
    <button
      className="flex size-5 items-center justify-center rounded-md bg-black/60 text-white/80 hover:bg-black/80 hover:text-white disabled:opacity-40"
      onClick={onClick}
      disabled={disabled}
      title={label}
      aria-label={label}
    >
      {icon}
    </button>
  );
}

// uniqueName appends " (2)", " (3)", … until the name is free. With
// `keepBase` the base name itself is used when free (imports keep their
// names unless taken); without it numbering always starts (duplicates).
function uniqueName(base: string, taken: { name: string }[], keepBase = false): string {
  const names = new Set(taken.map((t) => t.name));
  if (keepBase && !names.has(base)) return base;
  const stripped = base.replace(/ \(\d+\)$/, '');
  for (let n = 2; ; n++) {
    const candidate = `${stripped} (${n})`;
    if (!names.has(candidate)) return candidate;
  }
}

// useUserPresetThumbs renders a small preview of each saved look applied to
// the focused photo — through the same merge clicking would run (sections
// filter, relative deltas, exposure re-anchor), so a partial preset's thumb
// shows what applying it to the CURRENT draft actually produces. Same
// lifecycle rules as usePresetThumbs, minus the autoAdjust round trip.
function useUserPresetThumbs(client: ApiClient, photo: Photo | undefined, presets: UserPreset[]) {
  const [thumbs, setThumbs] = useState<Record<string, string>>({});
  const presetsKey = JSON.stringify(presets.map((p) => [p.id, p.sections, p.relative, p.autoSections]));
  const photoId = photo?.id;

  useEffect(() => {
    if (!photo || presets.length === 0) return;
    let alive = true;
    const urls: string[] = [];
    // Snapshot the base ONCE, like usePresetThumbs: thumbnails stay a stable
    // function of photo × preset instead of re-rendering per commit.
    const base = useEditSession.getState().draft ?? { ...NEUTRAL };
    const baseEV = photo.baseExpEV ?? 0;
    // Clear stale thumbs before the async regen loop below.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setThumbs({});
    (async () => {
      for (const p of presets) {
        try {
          const params = await resolveUserPreset(client, photo.id, base, p, baseEV);
          const blob = await previewEdit(client, photo.id, params, THUMB_PX);
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

// SuggestionsGrid is the scene-aware suggestion gallery: 3–5 server-computed
// candidate looks for the focused photo (SuggestEdits — histogram plus
// whatever AI maps are already cached), each a card with a live thumbnail.
// Click applies (one undo entry, Amount scrubber armed), hover previews on
// the loupe. When no scene map is cached a one-liner offers the analysis —
// suggestions themselves never trigger inference or downloads.
function SuggestionsGrid({ client, photo }: { client: ApiClient; photo?: Photo }) {
  const { suggestions, needsClassMap, thumbs, refresh } = useSuggestions(client, photo);
  const [classConsent, setClassConsent] = useState<PendingAIDownload | null>(null);

  // Generate the scene map (consent-gated download on first use, instant
  // when the map is already on disk), then recompute the suggestions with
  // the category gates unlocked. Same sentinel flow as esApplyPresetMasks.
  const analyze = async (allowDownload: boolean) => {
    if (!photo) return;
    try {
      await generateAIMap(client, photo.id, 'class', allowDownload);
      refresh();
    } catch (err) {
      if (isModelNotDownloaded(err)) {
        const status = await aiModelStatus(client, 'class').catch(() => null);
        if (status) setClassConsent({ kind: 'class', bytes: status.bytes, mode: 'add' });
      } else {
        toast.error(`Scene analysis failed: ${(err as Error).message}`);
      }
    }
  };

  if (suggestions.length === 0) return null;
  return (
    <>
      <div className="grid grid-cols-2 gap-2">
        {suggestions.map((s) => (
          <button
            key={s.id}
            className="group flex flex-col overflow-hidden rounded-lg border bg-inset text-left transition-colors hover:border-primary/50"
            onClick={() => esApplySuggestion(client, s)}
            onMouseEnter={() => esHoverSuggestion(client, s)}
            onMouseLeave={() => esHoverEnd(client)}
            title={`Apply ${s.label} (keeps the photo's crop and white balance)`}
          >
            <div className="aspect-[3/2] w-full overflow-hidden bg-black/40">
              {thumbs[s.id] ? (
                <img src={thumbs[s.id]} alt="" draggable={false} className="h-full w-full object-cover" />
              ) : (
                <div className="h-full w-full animate-pulse bg-white/5" />
              )}
            </div>
            <span className="truncate px-2 py-1.5 text-[12px] group-hover:text-foreground">{s.label}</span>
          </button>
        ))}
      </div>
      {needsClassMap && (
        <button
          className="w-fit text-[11px] text-muted-foreground hover:text-foreground hover:underline"
          onClick={() => void analyze(false)}
          title="Generate the scene map (sky, people, foliage …) so the suggestions can react to what's in the photo"
        >
          Analyze scene for smarter suggestions
        </button>
      )}
      <AIModelDialog
        pending={classConsent}
        onCancel={() => setClassConsent(null)}
        onConfirm={() => {
          setClassConsent(null);
          // The user just approved the fetch (same rule as the mask flow).
          void analyze(true);
        }}
      />
    </>
  );
}

// useSuggestions fetches the candidates once per focused photo — same
// anti-churn rule as the preset thumb hooks: the gallery is a stable
// function of the photo, not of every commit — then fills thumbnails
// sequentially off the same cached decode. refresh() refetches in place
// (after a scene analysis unlocks the category recipes).
function useSuggestions(client: ApiClient, photo: Photo | undefined) {
  const [result, setResult] = useState<{ suggestions: Suggestion[]; needsClassMap: boolean }>({
    suggestions: [],
    needsClassMap: false,
  });
  const [thumbs, setThumbs] = useState<Record<string, string>>({});
  const [gen, setGen] = useState(0);
  const photoId = photo?.id;

  useEffect(() => {
    if (photoId == null) return;
    let alive = true;
    const urls: string[] = [];
    // Snapshot the base ONCE (usePresetThumbs' rule); applying re-merges
    // onto the live draft, so a stale snapshot only affects thumbnails.
    const base = useEditSession.getState().draft ?? { ...NEUTRAL };
    // Clear stale cards before the async fetch below.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setResult({ suggestions: [], needsClassMap: false });
    setThumbs({});
    (async () => {
      try {
        const res = await suggestEdits(client, photoId, base);
        if (!alive) return;
        setResult({ suggestions: res.suggestions ?? [], needsClassMap: res.needsClassMap });
        for (const s of res.suggestions ?? []) {
          try {
            const blob = await previewEdit(client, photoId, s.params, THUMB_PX);
            if (!alive) return;
            const url = URL.createObjectURL(blob);
            urls.push(url);
            setThumbs((t) => ({ ...t, [s.id]: url }));
          } catch {
            /* a failed thumbnail just stays a placeholder */
          }
        }
      } catch {
        /* suggestions unavailable — the section simply doesn't render */
      }
    })();
    return () => {
      alive = false;
      urls.forEach((u) => URL.revokeObjectURL(u));
    };
  }, [client, photoId, gen]);

  return { ...result, thumbs, refresh: () => setGen((g) => g + 1) };
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
          onMouseEnter={() => esHoverAutoPreset(client, preset)}
          onMouseLeave={() => esHoverEnd(client)}
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
    // Clear stale thumbs before the async regen loop below.
    // eslint-disable-next-line react-hooks/set-state-in-effect
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
