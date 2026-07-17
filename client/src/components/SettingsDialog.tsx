import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { X } from 'lucide-react';
import { useGetAppSettings, setSidecarWrites, useListCameras } from '@/api/library';
import {
  useGetCacheInfo,
  clearCache,
  setCacheCap,
  setCacheDir,
  useGetModelsInfo,
  deleteModel,
} from '@/api/system';
import { useApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Segmented } from '@/components/ui/segmented';
import { Slider } from '@/components/ui/slider';
import { Switch } from '@/components/ui/switch';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import { useTheme } from '@/components/theme-provider';
import '@/lib/electron';
import { DIALS, type DialDef, type DialKey } from '@/lib/dials';
import {
  newAutoPreset,
  offsetIsAdditive,
  OFFSET_KEYS,
  DEFAULT_PRESETS,
  type AutoPreset,
  type OffsetKey,
  type OffsetUnit,
} from '@/lib/autoPresets';
import { CONTROL_SPECS, type ControlId } from '@/lib/controlSpecs';
import type { AutoSection } from '@/lib/editSession';
import {
  updateAutoPresets,
  updateBurstGapSeconds,
  updateBurstHamming,
  updateCullDials,
  updateDefaultPresets,
  updatePrerenderFullres,
  updateQuickDials,
  updateThumbFit,
} from '@/lib/uiSettings';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { cn } from '@/lib/utils';
import { useUIStore } from '@/stores/uiStore';
import '@/lib/electron';

// formatBytes renders a byte count as a compact human-readable size.
function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 10 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`;
}

const SECTIONS = ['General', 'Toolbars', 'Auto presets', 'Default presets', 'Cache', 'Models', 'Sidecars'] as const;
type Section = (typeof SECTIONS)[number];

/**
 * Settings (handoff plate "SETTINGS"): a 760×480 left-nav modal — General
 * (theme), Cache (location + size limit + usage meter + clear), Sidecars.
 */
export function SettingsDialog() {
  const open = useUIStore((s) => s.settingsOpen);
  const setOpen = useUIStore((s) => s.setSettingsOpen);
  const [section, setSection] = useState<Section>('General');
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent
        showCloseButton={false}
        className="flex h-[480px] w-[760px] max-w-none flex-col gap-0 overflow-hidden rounded-[14px] border-glass-border p-0 sm:max-w-none"
      >
        <div className="flex items-center border-b px-[22px] py-[15px]">
          <span className="text-base font-semibold">Settings</span>
          <button
            className="ml-auto flex size-7 items-center justify-center rounded-[7px] border text-muted-foreground hover:text-foreground"
            onClick={() => setOpen(false)}
            aria-label="Close"
          >
            <X className="size-3.5" />
          </button>
        </div>
        <div className="flex min-h-0 flex-1">
          <div className="flex w-[168px] shrink-0 flex-col gap-px border-r bg-sidebar p-2.5">
            {SECTIONS.map((s) => (
              <button
                key={s}
                className={cn(
                  'flex h-8 items-center rounded-[7px] px-2.5 text-left text-[12.5px]',
                  section === s
                    ? 'bg-sidebar-accent font-medium text-foreground'
                    : 'text-secondary-foreground hover:bg-accent',
                )}
                onClick={() => setSection(s)}
              >
                {s}
              </button>
            ))}
          </div>
          <div className="flex-1 overflow-y-auto p-5">
            {open && section === 'General' && <GeneralSection />}
            {open && section === 'Toolbars' && <ToolbarsSection />}
            {open && section === 'Auto presets' && <AutoPresetsSection />}
            {open && section === 'Default presets' && <DefaultPresetsSection />}
            {open && section === 'Cache' && <CacheSection />}
            {open && section === 'Models' && <ModelsSection />}
            {open && section === 'Sidecars' && <SidecarSection />}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function SettingRow({
  title,
  description,
  control,
}: {
  title: string;
  description: React.ReactNode;
  control?: React.ReactNode;
}) {
  return (
    <div className="flex items-start gap-4 border-b py-4 first:pt-0 last:border-0">
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium">{title}</div>
        <div className="mt-0.5 text-xs leading-normal text-muted-foreground">{description}</div>
      </div>
      {control && <div className="shrink-0">{control}</div>}
    </div>
  );
}

function GeneralSection() {
  const { theme, setTheme } = useTheme();
  const client = useApiClient();
  const thumbFit = useUIStore((s) => s.thumbFit);
  const burstHamming = useUIStore((s) => s.burstHamming);
  const burstGapSeconds = useUIStore((s) => s.burstGapSeconds);
  // Follow the thumb live during a drag; only commit to the server (which
  // re-clusters open folders) on release — same pattern as OffsetSlider.
  const [burstDrag, setBurstDrag] = useState<number | null>(null);
  const burstShown = burstDrag ?? burstHamming;
  const [gapDrag, setGapDrag] = useState<number | null>(null);
  const gapShown = gapDrag ?? burstGapSeconds;
  return (
    <div className="flex flex-col">
      <SettingRow
        title="Appearance"
        description="marraw is dark by default so photos read true; a full light theme is available."
        control={
          <Segmented
            aria-label="Theme"
            size="sm"
            items={[
              { value: 'dark', label: 'Dark' },
              { value: 'light', label: 'Light' },
              { value: 'system', label: 'System' },
            ]}
            value={theme}
            onValueChange={(v) => setTheme(v)}
          />
        }
      />
      <SettingRow
        title="Thumbnails"
        description="Crop fills a uniform 3:2 cell (portraits lose their top and bottom). Fit shows the whole frame in a square cell. Natural sizes each frame to its own aspect ratio in justified rows."
        control={
          <Segmented
            aria-label="Thumbnail framing"
            size="sm"
            items={[
              { value: 'crop', label: 'Crop' },
              { value: 'fit', label: 'Fit' },
              { value: 'natural', label: 'Natural' },
            ]}
            value={thumbFit}
            onValueChange={(v) => updateThumbFit(client, v as 'crop' | 'fit' | 'natural')}
          />
        }
      />
      <SettingRow
        title="Burst grouping"
        description="How different two frames can be and still group as a near-duplicate burst. Higher groups shots where the subject shifts pose between frames; lower groups only near-identical frames. Measured in dHash bits (of 64) — at 64 similarity is ignored and anything shot within the time window below groups."
        control={
          <div className="flex w-56 items-center gap-2.5">
            <div className="min-w-0 flex-1">
              <Slider
                value={burstShown}
                min={4}
                max={64}
                step={1}
                aria-label="Burst grouping sensitivity"
                onValueChange={(v) => setBurstDrag(v as number)}
                onValueCommitted={(v) => {
                  setBurstDrag(null);
                  updateBurstHamming(client, v as number);
                }}
              />
            </div>
            <span className="w-6 shrink-0 text-right font-mono text-[11px] text-foreground tabular-nums">
              {Math.round(burstShown)}
            </span>
          </div>
        }
      />
      <SettingRow
        title="Burst time window"
        description="How far apart in time two frames can be and still chain into the same burst. Capture times are whole seconds, so the window is loose by design — similarity does the discriminating. Widen it when grouping at 64 above, where time is the only gate."
        control={
          <div className="flex w-56 items-center gap-2.5">
            <div className="min-w-0 flex-1">
              <Slider
                value={gapShown}
                min={1}
                max={30}
                step={1}
                aria-label="Burst time window"
                onValueChange={(v) => setGapDrag(v as number)}
                onValueCommitted={(v) => {
                  setGapDrag(null);
                  updateBurstGapSeconds(client, v as number);
                }}
              />
            </div>
            <span className="w-6 shrink-0 text-right font-mono text-[11px] text-foreground tabular-nums">
              {Math.round(gapShown)}s
            </span>
          </div>
        }
      />
      <AutoUpdateRow />
      <BetaChannelRow />
    </div>
  );
}

/**
 * Auto-update lives in the Electron shell rather than the daemon's settings:
 * the check runs at launch, before marrawd is up. Hidden in a browser tab and
 * on macOS, where an unsigned bundle can never update itself.
 */
function AutoUpdateRow() {
  const bridge = window.marraw;
  const [enabled, setEnabled] = useState<boolean | null>(null);

  useEffect(() => {
    let live = true;
    bridge?.getAutoUpdate?.().then((v) => live && setEnabled(v), () => {});
    return () => {
      live = false;
    };
  }, [bridge]);

  if (!bridge?.getAutoUpdate || !bridge.updatesSupported || enabled === null) return null;

  const toggle = (on: boolean) => {
    setEnabled(on); // optimistic: the switch must not lag the pointer
    bridge.setAutoUpdate?.(on).then(
      (v) => setEnabled(v),
      (err: Error) => {
        setEnabled(!on);
        toast.error(err.message);
      },
    );
  };

  return (
    <SettingRow
      title="Automatic updates"
      description="Check for a new version on launch, download it in the background, and install it when marraw quits. Turn this off to update by hand from the releases page."
      control={
        <Switch checked={enabled} onCheckedChange={toggle} aria-label="Automatic updates" />
      }
    />
  );
}

/**
 * Beta-channel opt-in, stored next to the auto-update pref in the shell's
 * preferences.json. Left untouched it follows the running version (a beta
 * install tracks its cycle's betas); flipping the switch pins the choice
 * across updates.
 */
function BetaChannelRow() {
  const bridge = window.marraw;
  const [enabled, setEnabled] = useState<boolean | null>(null);

  useEffect(() => {
    let live = true;
    bridge?.getBetaChannel?.().then((v) => live && setEnabled(v), () => {});
    return () => {
      live = false;
    };
  }, [bridge]);

  if (!bridge?.getBetaChannel || !bridge.updatesSupported || enabled === null) return null;

  const toggle = (on: boolean) => {
    setEnabled(on); // optimistic: the switch must not lag the pointer
    bridge.setBetaChannel?.(on).then(
      (v) => setEnabled(v),
      (err: Error) => {
        setEnabled(!on);
        toast.error(err.message);
      },
    );
  };

  return (
    <SettingRow
      title="Beta versions"
      description="Update to beta releases as well as stable ones. Betas are for trying features early; a beta always moves on to the final stable release when it ships."
      control={<Switch checked={enabled} onCheckedChange={toggle} aria-label="Beta versions" />}
    />
  );
}

// ToolbarsSection: which develop dials float in the Cull confirm bar and
// the Develop quick dock. None (the default) keeps those bars compact.
function ToolbarsSection() {
  const client = useApiClient();
  const cullDials = useUIStore((s) => s.cullDials);
  const quickDials = useUIStore((s) => s.quickDials);
  const setCullDials = (dials: DialKey[]) => updateCullDials(client, dials);
  const setQuickDials = (dials: DialKey[]) => updateQuickDials(client, dials);
  return (
    <div className="flex flex-col">
      <DialPickerRow
        title="Cull toolbar dials"
        description="Develop dials shown in the Cull confirm bar, next to Pick / Reject. None keeps the bar compact."
        value={cullDials}
        onChange={setCullDials}
      />
      <DialPickerRow
        title="Develop quick dials"
        description="Dials floating in the Develop quick dock over the photo. None leaves just the zoom controls."
        value={quickDials}
        onChange={setQuickDials}
      />
    </div>
  );
}

function DialPickerRow({
  title,
  description,
  value,
  onChange,
}: {
  title: string;
  description: string;
  value: DialKey[];
  onChange: (v: DialKey[]) => void;
}) {
  // Adding a dial keeps catalog order, so the toolbar layout is stable no
  // matter the order the user clicks in.
  const toggle = (k: DialKey) =>
    onChange(DIALS.map((d) => d.key).filter((x) => (x === k ? !value.includes(k) : value.includes(x))));
  const chip = (selected: boolean, label: string, onClick: () => void) => (
    <button
      key={label}
      className={cn(
        'h-7 rounded-lg border px-2.5 text-xs',
        selected
          ? 'border-primary/60 bg-primary/15 font-medium text-accent-text'
          : 'border-input text-muted-foreground hover:text-foreground',
      )}
      aria-pressed={selected}
      onClick={onClick}
    >
      {label}
    </button>
  );
  // The catalog is the full develop control set (33 controls), so the chips
  // are clustered under the develop panel's section names for scannability.
  const groups: { title: string; dials: DialDef[] }[] = [];
  for (const d of DIALS) {
    const g = groups[groups.length - 1];
    if (g?.title === d.group) g.dials.push(d);
    else groups.push({ title: d.group, dials: [d] });
  }
  return (
    <div className="border-b py-4 first:pt-0 last:border-0">
      <div className="text-sm font-medium">{title}</div>
      <div className="mt-0.5 text-xs leading-normal text-muted-foreground">{description}</div>
      <div className="mt-2.5">{chip(value.length === 0, 'None', () => onChange([]))}</div>
      {groups.map((g) => (
        <div key={g.title} className="mt-2.5">
          <div className="mb-1.5 text-[10px] tracking-[.06em] text-muted-foreground uppercase">
            {g.title}
          </div>
          <div className="flex flex-wrap gap-1.5">
            {g.dials.map((d) => chip(value.includes(d.key), d.label, () => toggle(d.key)))}
          </div>
        </div>
      ))}
    </div>
  );
}

// AutoPresetsSection: user-configurable "creative autos" — each preset runs
// the chosen auto sections, then adds its style offsets. Presets 1–9 are
// reachable via Ctrl+1..9 and the command palette.
function AutoPresetsSection() {
  const client = useApiClient();
  const presets = useUIStore((s) => s.autoPresets);
  const setPresets = (next: AutoPreset[]) => updateAutoPresets(client, next);

  const update = (i: number, patch: Partial<AutoPreset>) => {
    const next = presets.slice();
    next[i] = { ...next[i], ...patch };
    setPresets(next);
  };

  const sectionChips: { key: AutoSection; label: string }[] = [
    { key: 'tone', label: 'Tone' },
    { key: 'wb', label: 'White balance' },
    { key: 'color', label: 'Colour' },
  ];

  return (
    <div className="flex flex-col">
      <div className="pb-4">
        <div className="text-sm font-medium">Creative auto presets</div>
        <div className="mt-0.5 text-xs leading-normal text-muted-foreground">
          A preset runs the selected autos, then layers your style on top. Sliders whose auto is
          active are added to the computed value; the rest are set to their exact value (0 included).
          Apply the first nine with Ctrl+1…9 or from the Ctrl+K palette.
        </div>
      </div>
      {presets.map((p, i) => (
        <div key={p.id} className="mb-3 rounded-[10px] border p-3">
          <div className="flex items-center gap-2">
            <span className="w-5 text-center font-mono text-[11px] text-muted-foreground">
              {i < 9 ? `${i + 1}` : '·'}
            </span>
            <input
              className="h-8 flex-1 rounded-lg border border-input bg-secondary px-2 text-xs outline-none focus:border-ring dark:bg-white/5"
              value={p.name}
              onChange={(e) => update(i, { name: e.target.value })}
              aria-label="Preset name"
            />
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setPresets(presets.filter((x) => x.id !== p.id))}
            >
              Delete
            </Button>
          </div>
          <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
            <span className="mr-1 text-[11px] text-muted-foreground">Auto</span>
            {sectionChips.map((c) => {
              const selected = p.sections.includes(c.key);
              return (
                <button
                  key={c.key}
                  className={cn(
                    'h-7 rounded-lg border px-2.5 text-xs',
                    selected
                      ? 'border-primary/60 bg-primary/15 font-medium text-accent-text'
                      : 'border-input text-muted-foreground hover:text-foreground',
                  )}
                  aria-pressed={selected}
                  onClick={() =>
                    update(i, {
                      sections: selected
                        ? p.sections.filter((s) => s !== c.key)
                        : sectionChips.map((x) => x.key).filter((k) => k === c.key || p.sections.includes(k)),
                    })
                  }
                >
                  {c.label}
                </button>
              );
            })}
          </div>
          {(() => {
            const setOffset = (key: OffsetKey, v: number) =>
              update(i, { offsets: { ...p.offsets, [key]: v } });
            const additive = OFFSET_KEYS.filter((o) => offsetIsAdditive(o.key, p.sections));
            const absolute = OFFSET_KEYS.filter((o) => !offsetIsAdditive(o.key, p.sections));
            const block = (
              title: string,
              hint: string,
              keys: typeof OFFSET_KEYS,
            ) =>
              keys.length === 0 ? null : (
                <div className="mt-2.5">
                  <div className="text-[11px] font-medium text-muted-foreground">
                    {title}
                    <span className="ml-1.5 font-normal text-muted-foreground/70">{hint}</span>
                  </div>
                  <div className="mt-1.5 grid grid-cols-2 gap-x-6 gap-y-2">
                    {keys.map((o) => (
                      <OffsetSlider
                        key={`${p.id}:${o.key}`}
                        label={o.label}
                        offsetKey={o.key}
                        unit={o.unit}
                        additive={offsetIsAdditive(o.key, p.sections)}
                        value={p.offsets[o.key] ?? 0}
                        onChange={(v) => setOffset(o.key, v)}
                      />
                    ))}
                  </div>
                </div>
              );
            return (
              <>
                {block('On top of auto', 'added to the auto result', additive)}
                {block('Creative', 'set to the exact value', absolute)}
              </>
            );
          })()}
        </div>
      ))}
      <div className="flex gap-1.5">
        <Button variant="outline" size="sm" onClick={() => setPresets([...presets, newAutoPreset()])}>
          Add preset
        </Button>
        <Button
          variant="ghost"
          size="sm"
          // Reset the six shipped presets to their pristine values and order (so
          // Ctrl+1…6 stay put), keeping any presets the user added after them.
          onClick={() =>
            setPresets([
              ...DEFAULT_PRESETS,
              ...presets.filter((p) => !DEFAULT_PRESETS.some((dp) => dp.id === p.id)),
            ])
          }
        >
          Restore defaults
        </Button>
      </div>
    </div>
  );
}

// DefaultPresetsSection maps cameras to the saved look the calibrate pass
// seeds onto NEW photos (never-edited ones) right after measuring their
// exposure baseline. An exact "Make Model" match beats the any-camera row;
// adaptive presets are excluded (seeding can't run their per-photo auto).
// Reset returns a photo to camera neutral, not the default preset.
function DefaultPresetsSection() {
  const client = useApiClient();
  const defaults = useUIStore((s) => s.defaultPresets);
  const userPresets = useUIStore((s) => s.userPresets);
  const cameras = useListCameras();
  // Seeding runs presetLook server-side, which can't resolve per-photo
  // autos — offer only non-adaptive presets.
  const seedable = userPresets.filter((p) => (p.autoSections?.length ?? 0) === 0);
  // Cameras with a stale mapping (folder removed from the catalog) still
  // show, so the entry can be seen and cleared.
  const cameraKeys = new Set((cameras.data ?? []).map((c) => c.key));
  const staleKeys = Object.keys(defaults).filter((k) => k !== '*' && !cameraKeys.has(k));

  const setDefault = (key: string, presetId: string) => {
    const next = { ...defaults };
    if (presetId === '') delete next[key];
    else next[key] = presetId;
    updateDefaultPresets(client, next);
  };

  const row = (key: string, label: string, sub?: string) => (
    <div key={key} className="flex items-center gap-3 py-1.5">
      <div className="min-w-0 flex-1">
        <div className="truncate text-xs">{label}</div>
        {sub && <div className="text-[11px] text-muted-foreground">{sub}</div>}
      </div>
      <Select value={defaults[key] ?? ''} onValueChange={(v) => setDefault(key, v ?? '')}>
        <SelectTrigger className="w-52" size="sm" aria-label={`Default preset for ${label}`}>
          <SelectValue placeholder="No default" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="">No default</SelectItem>
          {seedable.map((p) => (
            <SelectItem key={p.id} value={p.id}>
              {p.name}
            </SelectItem>
          ))}
          {/* A mapping to a deleted preset stays visible so it can be cleared. */}
          {defaults[key] && !seedable.some((p) => p.id === defaults[key]) && (
            <SelectItem value={defaults[key]}>(deleted preset)</SelectItem>
          )}
        </SelectContent>
      </Select>
    </div>
  );

  return (
    <div className="flex flex-col">
      <div className="pb-4">
        <div className="text-sm font-medium">Default presets</div>
        <div className="mt-0.5 text-xs leading-normal text-muted-foreground">
          New photos get the chosen look applied automatically as they are calibrated — per camera,
          or one default for everything. Only photos you have never edited are touched; Reset
          returns a photo to camera neutral. Adaptive presets can&apos;t be seeded and aren&apos;t
          offered here.
        </div>
      </div>
      {seedable.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          No saved presets yet — save a look in Develop → Presets first.
        </p>
      ) : (
        <div className="flex flex-col divide-y divide-border/50">
          {row('*', 'Any camera', 'Used when no camera row matches')}
          {(cameras.data ?? []).map((c) => row(c.key, c.key))}
          {staleKeys.map((k) => row(k, k, 'No photos from this camera in the catalog'))}
        </div>
      )}
    </div>
  );
}

// OffsetSlider edits one preset value as a center-anchored slider: exposure
// in EV, the split hues in degrees, everything else in the panel's ±100 units.
// Domain and step come from the control catalog. When `additive` the value is
// a delta layered on the auto result; otherwise it's an absolute setting.
// Persists on release (each commit writes the preset list to the catalog).
function OffsetSlider({
  label,
  offsetKey,
  unit,
  additive,
  value,
  onChange,
}: {
  label: string;
  offsetKey: OffsetKey;
  unit: OffsetUnit;
  additive: boolean;
  value: number;
  onChange: (v: number) => void;
}) {
  // Thumb follows a local value during the drag (same pattern as EditSlider).
  const [dragging, setDragging] = useState<number | null>(null);
  const spec = CONTROL_SPECS[offsetKey as ControlId];
  if (spec.kind !== 'numeric') return null;
  // Slider space: EV/degrees directly, everything else in ±100 (×100) units.
  const scale = unit === 'pct' ? 100 : 1;
  const sMin = unit === 'pct' ? spec.min * scale : spec.min;
  const sMax = unit === 'pct' ? spec.max * scale : spec.max;
  const sStep = unit === 'ev' ? 0.05 : unit === 'deg' ? spec.step : Math.max(1, Math.round(spec.step * scale));
  const signed = sMin < 0;
  const toSlider = (v: number) => (unit === 'ev' ? v : Math.round(v * scale));
  const fromSlider = (v: number) => (unit === 'ev' ? Math.round(v * 100) / 100 : v / scale);
  const shown = dragging ?? toSlider(value);
  const display =
    unit === 'ev'
      ? `${shown >= 0 ? '+' : ''}${shown.toFixed(2)}`
      : unit === 'deg'
        ? `${Math.round(shown)}°`
        : shown === 0
          ? '0'
          : signed
            ? `${shown > 0 ? '+' : ''}${Math.round(shown)}`
            : `${Math.round(shown)}`;
  return (
    <div className="flex items-center gap-2.5">
      <span className="w-20 shrink-0 text-xs text-muted-foreground">{label}</span>
      <div className="min-w-0 flex-1">
        <Slider
          value={shown}
          min={sMin}
          max={sMax}
          step={sStep}
          fillFrom={0}
          aria-label={`${label} ${additive ? 'offset' : 'value'}`}
          onValueChange={(v) => setDragging(v as number)}
          onValueCommitted={(v) => {
            setDragging(null);
            onChange(fromSlider(v as number));
          }}
        />
      </div>
      <span className="w-14 shrink-0 text-right font-mono text-[11px] text-foreground tabular-nums">
        {display}
        {unit === 'ev' && <span className="text-muted-foreground"> EV</span>}
      </span>
    </div>
  );
}

function SidecarSection() {
  const client = useApiClient();
  const { data } = useGetAppSettings();
  const enabled = data?.sidecarWrites ?? true;
  return (
    <SettingRow
      title="Write edit sidecars"
      description="Mirror ratings and develop settings to a .marraw.json file next to each RAW, so copying a folder carries your edits to another machine. Folders that already contain sidecars are always imported."
      control={
        <Switch
          checked={enabled}
          onCheckedChange={() =>
            setSidecarWrites(client, !enabled).catch((err) => toast.error((err as Error).message))
          }
          aria-label="Write edit sidecars"
        />
      }
    />
  );
}

function CacheSection() {
  const client = useApiClient();
  const { data: info } = useGetCacheInfo();
  const [busy, setBusy] = useState(false);
  const [gb, setGb] = useState('');
  useEffect(() => {
    // Seed the editable field from fetched cache info. Keyed on the query
    // snapshot's identity, so this can't live as an adjust-during-render.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (info && info.capBytes > 0) setGb(String(Math.round(info.capBytes / (1 << 30))));
  }, [info]);

  const applyCap = () => {
    const n = Number(gb);
    if (!Number.isFinite(n) || n < 1) return;
    setCacheCap(client, Math.round(n))
      .then(() => toast.success(`Cache limit set to ${Math.round(n)} GB`))
      .catch((err) => toast.error((err as Error).message));
  };

  const run = (fn: () => Promise<unknown>, done: string) => {
    setBusy(true);
    fn()
      .then(() => toast.success(done))
      .catch((err) => toast.error((err as Error).message))
      .finally(() => {
        setBusy(false);
      });
  };

  const usedPct =
    info && info.capBytes > 0 ? Math.min(100, (info.bytes / info.capBytes) * 100) : 0;

  const prerenderFullres = useUIStore((s) => s.prerenderFullres);

  return (
    <div className="flex flex-col">
      <SettingRow
        title="Pre-render 1:1 full resolution"
        description="After a folder's previews are built, render every photo's 1:1 tiles ahead of time so zooming to 100% is instant. Full-res tiles are large — raise the cache limit below for big libraries, or they'll be evicted before you view them. You can also render a single folder on demand from its right-click menu."
        control={
          <Switch
            checked={prerenderFullres}
            onCheckedChange={(v) => updatePrerenderFullres(client, v)}
            aria-label="Pre-render 1:1 full resolution"
          />
        }
      />
      <SettingRow
        title="Cache directory"
        description={
          info?.dir ? (
            <button
              className="max-w-full truncate text-left font-mono text-[11px] underline-offset-2 hover:underline"
              title={window.marraw ? `${info.dir} — click to reveal` : info.dir}
              onClick={() => window.marraw?.revealInExplorer(info.dir)}
            >
              {info.dir}
              {info.isCustom ? '' : ' (default)'}
            </button>
          ) : (
            '…'
          )
        }
        control={
          <div className="flex gap-1.5">
            {info?.isCustom && (
              <Button
                variant="ghost"
                size="sm"
                disabled={busy}
                onClick={() => run(() => setCacheDir(client, ''), 'Cache folder reset to default')}
              >
                Use default
              </Button>
            )}
            {window.marraw && (
              <Button
                variant="outline"
                size="sm"
                disabled={busy}
                onClick={async () => {
                  const dir = await window.marraw!.pickDirectory();
                  if (dir) run(() => setCacheDir(client, dir), 'Cache folder changed');
                }}
              >
                Change…
              </Button>
            )}
          </div>
        }
      />
      <SettingRow
        title="Preview cache limit"
        description="When the cache grows past this size, the least-recently viewed previews are evicted in the background. Bigger caches keep more shoots instant."
        control={
          <div className="flex items-center gap-1.5">
            <input
              className="h-8 w-16 rounded-lg border border-input bg-secondary px-2 text-right font-mono text-xs outline-none focus:border-ring dark:bg-white/5"
              value={gb}
              onChange={(e) => setGb(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && applyCap()}
              onBlur={applyCap}
              aria-label="Cache limit in GB"
            />
            <span className="font-mono text-[11px] text-muted-foreground">GB</span>
          </div>
        }
      />
      <SettingRow
        title="On-disk usage"
        description={
          <div className="flex flex-col gap-2">
            <span className="font-mono text-[11.5px]">
              {info ? (
                <>
                  <span className="text-foreground">{formatBytes(info.bytes)}</span> used
                  {info.capBytes > 0 && <> · {formatBytes(info.capBytes)} limit</>} ·{' '}
                  {info.files.toLocaleString()} files
                </>
              ) : (
                'measuring…'
              )}
            </span>
            <div className="h-1 w-64 overflow-hidden rounded-sm bg-black/10 dark:bg-white/12">
              <div className="h-full rounded-sm bg-primary" style={{ width: `${usedPct}%` }} />
            </div>
            <span>Rendered previews and 1:1 tiles. Deleting them is safe — they rebuild on demand.</span>
          </div>
        }
        control={
          <Button
            variant="destructive"
            size="sm"
            onClick={() => run(() => clearCache(client), 'Cache cleared')}
            disabled={busy || !info || info.files === 0}
          >
            Clear cache
          </Button>
        }
      />
    </div>
  );
}

// ModelsSection: the AI model weights on disk. Features download their model
// on first consented use and never clean up, so this is the inventory — and
// the only in-app way to reclaim that space (~1.6 GB with all three).
function ModelsSection() {
  const client = useApiClient();
  const { data: info } = useGetModelsInfo();
  const [busy, setBusy] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  const models = info?.models ?? [];
  const total = models.reduce((sum, m) => sum + m.bytes, 0);

  const remove = (fileName: string) => {
    setBusy(true);
    deleteModel(client, fileName)
      .then(() => toast.success('Model deleted'))
      .catch((err) => toast.error((err as Error).message))
      .finally(() => {
        setBusy(false);
        setConfirmDelete(null);
      });
  };

  return (
    <div className="flex flex-col">
      <div className="pb-4">
        <div className="text-sm font-medium">Downloaded models</div>
        <div className="mt-0.5 text-xs leading-normal text-muted-foreground">
          AI features fetch their model weights on first use, always after you confirm the
          download. Deleting one frees disk space without touching your edits or generated masks —
          it simply downloads again the next time a feature needs it.
        </div>
      </div>
      {info && models.length === 0 && (
        <SettingRow title="No models downloaded" description="Nothing on disk yet." />
      )}
      {models.map((m) => (
        <SettingRow
          key={m.fileName}
          title={m.name || m.fileName}
          description={
            <div className="flex flex-col gap-0.5">
              <span>{m.purpose || 'Not used by this version of marraw — safe to delete.'}</span>
              <span className="font-mono text-[11px]">
                {m.fileName} · <span className="text-foreground">{formatBytes(m.bytes)}</span>
              </span>
            </div>
          }
          control={
            confirmDelete === m.fileName ? (
              <div className="flex gap-1.5">
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setConfirmDelete(null)}
                  disabled={busy}
                >
                  Cancel
                </Button>
                <Button
                  variant="destructive"
                  size="sm"
                  disabled={busy}
                  onClick={() => remove(m.fileName)}
                >
                  Delete
                </Button>
              </div>
            ) : (
              <Button
                variant="destructive"
                size="sm"
                disabled={busy}
                onClick={() => setConfirmDelete(m.fileName)}
              >
                Delete
              </Button>
            )
          }
        />
      ))}
      {models.length > 0 && (
        <SettingRow
          title="On-disk usage"
          description={
            info?.dir ? (
              <button
                className="max-w-full truncate text-left font-mono text-[11px] underline-offset-2 hover:underline"
                title={window.marraw ? `${info.dir} — click to reveal` : info.dir}
                onClick={() => window.marraw?.revealInExplorer(info.dir)}
              >
                {info.dir}
              </button>
            ) : (
              '…'
            )
          }
          control={
            <span className="font-mono text-[11.5px]">
              <span className="text-foreground">{formatBytes(total)}</span> ·{' '}
              {models.length === 1 ? '1 model' : `${models.length} models`}
            </span>
          }
        />
      )}
    </div>
  );
}
