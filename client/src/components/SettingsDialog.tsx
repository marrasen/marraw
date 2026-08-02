import { useCallback, useEffect, useState } from 'react';
import { toast } from 'sonner';
import { Download, RefreshCw, RotateCw, X } from 'lucide-react';
import { useGetAppSettings, setSidecarWrites, useListCameras } from '@/api/library';
import {
  useGetCacheInfo,
  clearCache,
  setCacheCap,
  setCacheDir,
  useGetModelsInfo,
  deleteModel,
  useGetRemoteAccess,
  useListRemoteDevices,
  regeneratePairingToken,
  revokeRemoteDevice,
  setDeviceName,
  setPairingOpen,
} from '@/api/system';
import { backend, canUseHostFs } from '@/lib/backend';
import type {
  DiscoveredHost,
  RemoteAccessPrefs,
  RemoteConnection,
  RemoteProbe,
} from '@/lib/electron';
import {
  cancelPairing,
  deleteRemote,
  discoverySupported,
  openRemoteWindow,
  pairWithHost,
  remoteStatusText,
  saveRemote,
  scanRemotes,
  useRemotes,
  waitForPairing,
} from '@/stores/remoteStore';
import {
  checkForUpdates,
  downloadUpdate,
  installUpdate,
  updateStatusText,
  updatesSupported,
  useUpdates,
} from '@/stores/updateStore';
import { DirPickerDialog } from '@/components/DirPickerDialog';
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
import { FEATURES, FEATURE_GROUPS, FEATURE_IDS, resolveFeature } from '@/lib/features';
import { isMasksOnlyPreset } from '@/lib/presetSections';
import {
  updateAutoPresets,
  updateBurstGapSeconds,
  updateBurstHamming,
  updateCullDials,
  updateDefaultPresets,
  updateFeature,
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

const SECTIONS = ['General', 'Features', 'Toolbars', 'Auto presets', 'Default presets', 'Cache', 'Models', 'Sidecars', 'Remote', 'Updates'] as const;
type Section = (typeof SECTIONS)[number];

/**
 * Settings (handoff plate "SETTINGS"): a 760×480 left-nav modal — General
 * (theme), Cache (location + size limit + usage meter + clear), Sidecars.
 */
export function SettingsDialog() {
  const open = useUIStore((s) => s.settingsOpen);
  const setOpen = useUIStore((s) => s.setSettingsOpen);
  // The Remote pane covers both halves of remote work — the connections this
  // machine can open, and hosting this library to others — so it shows
  // wherever the shell bridges exist, including a remote window (whose shell
  // is still this machine's). What's hidden there is the hosting half.
  const showRemote = !!window.marraw?.getRemoteAccess;
  // Hidden where nothing can be updated (a browser tab, a .deb, macOS) rather
  // than shown as a pane whose every button fails — see updatesSupported.
  const showUpdates = updatesSupported();
  const sections = SECTIONS.filter(
    (s) => (s !== 'Remote' || showRemote) && (s !== 'Updates' || showUpdates),
  );
  // The visible pane lives in the store so anything can deep-link to one
  // (the rail's "Manage connections…"); a stale name falls back to General.
  const setSection = useUIStore((s) => s.setSettingsSection);
  const stored = useUIStore((s) => s.settingsSection);
  const section = (sections as readonly string[]).includes(stored)
    ? (stored as Section)
    : 'General';

  return (
    <Dialog open={open} onOpenChange={(v) => setOpen(v)}>
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
            {sections.map((s) => (
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
            {open && section === 'Features' && <FeaturesSection />}
            {open && section === 'Toolbars' && <ToolbarsSection />}
            {open && section === 'Auto presets' && <AutoPresetsSection />}
            {open && section === 'Default presets' && <DefaultPresetsSection />}
            {open && section === 'Cache' && <CacheSection />}
            {open && section === 'Models' && <ModelsSection />}
            {open && section === 'Sidecars' && <SidecarSection />}
            {open && section === 'Remote' && showRemote && <RemoteSection />}
            {open && section === 'Updates' && showUpdates && <UpdatesSection />}
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
  title: React.ReactNode;
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
    </div>
  );
}

// FeaturesSection: whole-feature switches (registry in lib/features.ts).
// Disabling hides a feature's buttons, badges, and shortcuts everywhere;
// experimental features default off until opted into here.
function FeaturesSection() {
  const client = useApiClient();
  const features = useUIStore((s) => s.features);
  return (
    <div className="flex flex-col">
      {FEATURE_GROUPS.map(({ key, label }, i) => (
        <div key={key} className={cn(i > 0 && 'mt-6')}>
          <div className="mb-1.5 text-[10px] tracking-[.06em] text-muted-foreground uppercase">
            {label}
          </div>
          <div className="flex flex-col">
            {FEATURE_IDS.filter((id) => FEATURES[id].group === key).map((id) => {
              const def = FEATURES[id];
              const enabled = resolveFeature(features, id);
              return (
                <div key={id}>
                  <SettingRow
                    title={
                      def.experimental ? (
                        <span className="flex items-center gap-1.5">
                          {def.label}
                          <span className="rounded-[4px] bg-amber-400/15 px-1.5 py-px text-[9px] font-semibold tracking-[.05em] text-amber-500 uppercase dark:text-amber-400">
                            Experimental
                          </span>
                        </span>
                      ) : (
                        def.label
                      )
                    }
                    description={def.description}
                    control={
                      <Switch
                        checked={enabled}
                        onCheckedChange={(on) => updateFeature(client, id, on)}
                        aria-label={def.label}
                      />
                    }
                  />
                  {id === 'bursts' && enabled && <BurstTuningRows />}
                </div>
              );
            })}
          </div>
        </div>
      ))}
    </div>
  );
}

// The burst grouping/time-window sliders, shown under the Bursts toggle
// while it's on.
function BurstTuningRows() {
  const client = useApiClient();
  const burstHamming = useUIStore((s) => s.burstHamming);
  const burstGapSeconds = useUIStore((s) => s.burstGapSeconds);
  // Follow the thumb live during a drag; only commit to the server (which
  // re-clusters open folders) on release — same pattern as OffsetSlider.
  const [burstDrag, setBurstDrag] = useState<number | null>(null);
  const burstShown = burstDrag ?? burstHamming;
  const [gapDrag, setGapDrag] = useState<number | null>(null);
  const gapShown = gapDrag ?? burstGapSeconds;
  return (
    <>
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
    </>
  );
}

/**
 * Updates: where the current version stands, an explicit check, and the
 * download/install steps with their progress. The whole flow is visible on
 * demand here — an update that arrives while you're working leaves a badge in
 * the rail, so there is nothing to catch in the moment.
 */
function UpdatesSection() {
  const { state, currentVersion, loaded } = useUpdates();
  const busy = state.status === 'checking' || state.status === 'downloading';

  return (
    <div className="flex flex-col">
      <div className="mb-4 rounded-[10px] border p-4">
        <div className="flex items-start gap-4">
          <div className="min-w-0 flex-1">
            <div className="text-sm font-medium">
              marraw {currentVersion || '…'}
              {state.status === 'available' || state.status === 'downloading' ? (
                <span className="ml-2 rounded-[4px] bg-primary/15 px-1.5 py-px text-[9px] font-semibold tracking-[.05em] text-accent-text uppercase">
                  Update available
                </span>
              ) : null}
            </div>
            <div
              className={cn(
                'mt-0.5 text-xs leading-normal',
                state.status === 'error' ? 'text-destructive' : 'text-muted-foreground',
              )}
            >
              {updateStatusText(state)}
            </div>
          </div>
          <div className="flex shrink-0 gap-1.5">
            {state.status === 'available' && (
              <Button size="sm" onClick={downloadUpdate}>
                <Download className="size-3.5" />
                Download
              </Button>
            )}
            {state.status === 'downloaded' && (
              <Button size="sm" onClick={installUpdate}>
                <RotateCw className="size-3.5" />
                Restart &amp; install
              </Button>
            )}
            <Button variant="outline" size="sm" disabled={busy || !loaded} onClick={checkForUpdates}>
              <RefreshCw className={cn('size-3.5', state.status === 'checking' && 'animate-spin')} />
              Check for updates
            </Button>
          </div>
        </div>
        {state.status === 'downloading' && (
          <div className="mt-3 flex flex-col gap-1.5">
            <div className="h-1 w-full overflow-hidden rounded-sm bg-black/10 dark:bg-white/12">
              <div
                className="h-full rounded-sm bg-primary transition-[width]"
                style={{ width: `${Math.min(100, Math.max(0, state.percent))}%` }}
              />
            </div>
            <span className="font-mono text-[11px] text-muted-foreground tabular-nums">
              {formatBytes(state.transferred)} / {formatBytes(state.total)}
              {state.bytesPerSecond > 0 && <> · {formatBytes(state.bytesPerSecond)}/s</>}
            </span>
          </div>
        )}
        {state.status === 'downloaded' && (
          <div className="mt-3 text-xs text-muted-foreground">
            marraw will close and reopen on the new version. It also installs by itself the next
            time you quit, so you can keep working and restart whenever it suits you.
          </div>
        )}
      </div>
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
      description="Check on launch and download a new version in the background, so it is ready to install the moment you want it. Turn this off to decide every step yourself — the button above still checks and downloads on demand."
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
  // autos — offer only non-adaptive presets. Masks-only presets are out for
  // the same reason: seeding lands the look, and their look is deliberately
  // nothing, so they'd sit in the list doing nothing on import.
  const seedable = userPresets.filter(
    (p) => (p.autoSections?.length ?? 0) === 0 && !isMasksOnlyPreset(p),
  );
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
  const [pickerOpen, setPickerOpen] = useState(false);
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
              title={canUseHostFs() ? `${info.dir} — click to reveal` : info.dir}
              onClick={() => canUseHostFs() && window.marraw?.revealInExplorer(info.dir)}
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
            <Button variant="outline" size="sm" disabled={busy} onClick={() => setPickerOpen(true)}>
              Change…
            </Button>
          </div>
        }
      />
      {pickerOpen && (
        <DirPickerDialog
          title="Choose cache folder"
          description="A marraw-previews folder is created inside your pick"
          initialPath={info?.isCustom ? info.dir : undefined}
          onSelect={(dir) => run(() => setCacheDir(client, dir), 'Cache folder changed')}
          onClose={() => setPickerOpen(false)}
        />
      )}
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

// RemoteSection: the two halves of remote work. "Connections" is the list of
// other machines' libraries this machine can open — shell prefs, so it works
// in a remote window too. "Host this library" is the other direction, and
// only makes sense for the daemon on THIS machine.
function RemoteSection() {
  return (
    <div className="flex flex-col gap-6">
      <ConnectionsSection />
      {!backend.isRemote && <HostSection />}
    </div>
  );
}

/**
 * Saved connections to libraries on other machines. Adding one normally means
 * picking this machine off a scan and having someone approve it over there —
 * no address to find, no token to copy. Manual entry stays for the cases a
 * scan cannot reach: blocked multicast, a non-default port, or a host set up
 * with the shared pairing token.
 */
function ConnectionsSection() {
  const { conns, probes } = useRemotes();
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<RemoteConnection | null>(null);

  return (
    <div className="flex flex-col">
      <div className="mb-1.5 text-[10px] tracking-[.06em] text-muted-foreground uppercase">
        Connections
      </div>
      <div className="flex flex-col gap-1.5">
        {conns.length === 0 && !adding && !editing && (
          <div className="rounded-lg border border-dashed px-3 py-4 text-center text-xs text-muted-foreground">
            No connections yet. Turn on “Allow remote connections” on the computer that holds the
            library, then add it here — it will ask you to approve this computer.
          </div>
        )}
        {conns.map((c) => (
          <div
            key={c.id}
            className="flex items-center gap-3 rounded-lg border bg-secondary px-3 py-2 dark:bg-white/5"
          >
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13px] font-medium">{c.name}</div>
              <div className="truncate font-mono text-[10.5px] text-faint">{c.host}</div>
            </div>
            <RemoteStatus probe={probes[c.id]} />
            <Button variant="outline" size="sm" onClick={() => openRemoteWindow(c.id)}>
              Open
            </Button>
            <Button variant="ghost" size="sm" onClick={() => setEditing(c)}>
              Edit
            </Button>
          </div>
        ))}
      </div>
      {editing ? (
        <ConnectionEditor conn={editing} onClose={() => setEditing(null)} />
      ) : adding ? (
        <AddConnectionPanel onClose={() => setAdding(false)} />
      ) : (
        <div className="mt-2.5">
          <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
            Add connection…
          </Button>
        </div>
      )}
    </div>
  );
}

/**
 * Add a connection: scan first, type only if you have to.
 *
 * The scan starts the moment this opens — someone who clicked "Add" has
 * already told us what they want, and making them press a second button to
 * begin looking is pure ceremony.
 */
function AddConnectionPanel({ onClose }: { onClose: () => void }) {
  const [hosts, setHosts] = useState<DiscoveredHost[] | null>(null);
  // The scan starts on mount, so "scanning" is the state this opens in rather
  // than something an effect flips on afterwards. A shell too old to scan goes
  // straight to the manual form.
  const [scanning, setScanning] = useState(discoverySupported);
  const [manual, setManual] = useState(() => !discoverySupported());
  const [pairing, setPairing] = useState<{ host: string; name: string } | null>(null);

  const runScan = useCallback((alive: () => boolean) => {
    return scanRemotes()
      .catch(() => [] as DiscoveredHost[])
      .then((found) => {
        if (!alive()) return;
        setHosts(found);
        setScanning(false);
      });
  }, []);

  useEffect(() => {
    if (!discoverySupported()) return;
    let live = true;
    void runScan(() => live);
    return () => {
      live = false;
    };
  }, [runScan]);

  const rescan = () => {
    setScanning(true);
    void runScan(() => true);
  };

  if (pairing) {
    return (
      <PairingWaitPanel
        host={pairing.host}
        hostName={pairing.name}
        onDone={onClose}
        onCancel={() => setPairing(null)}
      />
    );
  }

  if (manual) {
    return (
      <ConnectionEditor
        conn={{}}
        onClose={onClose}
        onBack={discoverySupported() ? () => setManual(false) : undefined}
      />
    );
  }

  return (
    <div className="mt-2.5 flex flex-col gap-2 rounded-lg border border-primary/50 bg-primary/5 p-3">
      <div className="flex items-center gap-2">
        <span className="flex-1 text-[11px] text-muted-foreground">
          {scanning
            ? 'Looking for computers…'
            : hosts && hosts.length > 0
              ? 'Computers found on your network'
              : 'No other computers found'}
        </span>
        <Button variant="ghost" size="sm" disabled={scanning} onClick={rescan}>
          {scanning ? 'Scanning…' : 'Scan again'}
        </Button>
      </div>

      <div className="flex flex-col gap-1.5" data-testid="remote-scan-results">
        {(hosts ?? []).map((h) => (
          <div
            key={h.host}
            className="flex items-center gap-3 rounded-lg border bg-secondary px-3 py-2 dark:bg-white/5"
          >
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13px] font-medium">{h.name}</div>
              <div className="truncate font-mono text-[10.5px] text-faint">
                {h.host} · {h.source === 'tailscale' ? 'Tailscale' : 'Local network'}
              </div>
            </div>
            <Button
              size="sm"
              disabled={!h.pairing}
              title={h.pairing ? undefined : 'That computer is not accepting new connections'}
              onClick={() => setPairing({ host: h.host, name: h.name })}
            >
              Connect
            </Button>
          </div>
        ))}
        {!scanning && hosts?.length === 0 && (
          <div className="rounded-lg border border-dashed px-3 py-3 text-center text-[11px] text-muted-foreground">
            Check that the other computer is awake and has “Allow remote connections” turned on. On
            some networks the search is blocked — you can still add it by address.
          </div>
        )}
      </div>

      <div className="mt-0.5 flex items-center gap-1.5">
        <Button variant="outline" size="sm" onClick={() => setManual(true)}>
          Enter details manually
        </Button>
        <Button variant="ghost" size="sm" onClick={onClose}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

/**
 * The waiting half of pairing: this machine has asked, and someone at the
 * other end has to say yes. The code shown here is the same one on their
 * screen — it is what makes "Allow" a decision rather than a reflex.
 */
function PairingWaitPanel({
  host,
  hostName,
  onDone,
  onCancel,
}: {
  host: string;
  hostName: string;
  onDone: () => void;
  onCancel: () => void;
}) {
  const [code, setCode] = useState('');
  const [error, setError] = useState('');

  useEffect(() => {
    let live = true;
    let requestId = '';
    void (async () => {
      const req = await pairWithHost(host);
      if (!live) return;
      if (!req.ok) {
        setError(req.error);
        return;
      }
      requestId = req.requestId;
      setCode(req.code);

      const res = await waitForPairing(host, requestId);
      if (!live) return;
      if (res.status === 'approved' && res.token) {
        await saveRemote({ name: res.hostName || hostName, host, token: res.token });
        toast.success(`Connected to ${res.hostName || hostName}`);
        onDone();
        return;
      }
      setError(
        res.status === 'denied'
          ? 'That computer declined the connection.'
          : res.status === 'expired'
            ? 'Nobody approved it in time. Try again when someone is at that computer.'
            : res.status === 'canceled'
              ? ''
              : (res.error ?? 'The connection could not be set up.'),
      );
    })();
    return () => {
      live = false;
      if (requestId) cancelPairing(requestId);
    };
  }, [host, hostName, onDone]);

  return (
    <div className="mt-2.5 flex flex-col gap-2 rounded-lg border border-primary/50 bg-primary/5 p-3">
      {error ? (
        <div className="text-[11px] text-destructive">{error}</div>
      ) : (
        <>
          <div className="text-[11px] text-muted-foreground">
            Waiting for someone to approve this computer on{' '}
            <span className="text-foreground">{hostName}</span>.
          </div>
          <div className="flex flex-col items-center gap-1 rounded-lg border border-dashed py-3">
            <span className="text-[10px] tracking-[.06em] text-muted-foreground uppercase">
              Check this code matches
            </span>
            <span
              className="font-mono text-2xl tracking-[.3em] tabular-nums select-text"
              data-testid="pairing-wait-code"
            >
              {code || '····'}
            </span>
          </div>
        </>
      )}
      <div className="mt-0.5 flex items-center gap-1.5">
        <Button variant="ghost" size="sm" onClick={onCancel}>
          {error ? 'Back' : 'Cancel'}
        </Button>
      </div>
    </div>
  );
}

/** Live reachability of one saved connection, as of the last 30s poll. */
function RemoteStatus({ probe }: { probe: RemoteProbe | undefined }) {
  const label = remoteStatusText(probe);
  return (
    <span
      className={cn(
        'shrink-0 font-mono text-[10.5px]',
        !probe ? 'text-faint' : probe.ok ? 'text-emerald-500' : 'text-destructive',
      )}
      title={probe && !probe.ok ? probe.error : undefined}
    >
      {label}
    </span>
  );
}

/**
 * Add/edit one connection by hand. This is the fallback path — a scan and an
 * approval is the normal way in — so it keeps the pairing-token field: a host
 * behind blocked multicast, on a non-default port, or set up before pairing
 * existed is still reachable this way.
 *
 * The token is tested before saving: a wrong token would only bounce at
 * connect time, but an asleep host is not an error — that saves with a
 * warning, since it will answer later.
 */
function ConnectionEditor({
  conn,
  onClose,
  onBack,
}: {
  conn: Partial<RemoteConnection>;
  onClose: () => void;
  onBack?: () => void;
}) {
  const [name, setName] = useState(conn.name ?? '');
  const [host, setHost] = useState(conn.host ?? '');
  const [token, setToken] = useState(conn.token ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const submit = async () => {
    if (!host.trim()) {
      setError('Host is required.');
      return;
    }
    setBusy(true);
    setError('');
    const probe = await window.marraw?.testRemote?.(host.trim(), token.trim());
    if (probe && !probe.ok && probe.error === 'invalid token') {
      setBusy(false);
      setError('The daemon answered, but rejected this token.');
      return;
    }
    await saveRemote({ id: conn.id, name: name.trim(), host: host.trim(), token: token.trim() });
    setBusy(false);
    if (probe && !probe.ok) toast.warning(`Saved — ${host.trim()} is not answering (${probe.error})`);
    else toast.success(`Saved ${name.trim() || host.trim()}`);
    onClose();
  };

  const remove = async () => {
    if (conn.id) await deleteRemote(conn.id);
    onClose();
  };

  return (
    <div className="mt-2.5 flex flex-col gap-2 rounded-lg border border-primary/50 bg-primary/5 p-3">
      <ConnectionField label="Name" value={name} onChange={setName} placeholder="Home desktop" />
      <ConnectionField
        label="Host (name or IP, optionally :port)"
        value={host}
        onChange={setHost}
        placeholder="100.64.0.12 or desktop:8482"
        mono
      />
      <ConnectionField
        label="Pairing token (Settings → Remote on that machine)"
        value={token}
        onChange={setToken}
        placeholder="32-character token"
        mono
      />
      {error && <div className="text-[11px] text-destructive">{error}</div>}
      <div className="mt-0.5 flex items-center gap-1.5">
        <Button size="sm" disabled={busy} onClick={() => void submit()}>
          {busy ? 'Checking…' : 'Save'}
        </Button>
        <Button variant="ghost" size="sm" disabled={busy} onClick={onBack ?? onClose}>
          {onBack ? 'Back' : 'Cancel'}
        </Button>
        <span className="flex-1" />
        {conn.id && (
          <Button variant="destructive" size="sm" disabled={busy} onClick={() => void remove()}>
            Remove
          </Button>
        )}
      </div>
    </div>
  );
}

function ConnectionField({
  label,
  value,
  onChange,
  placeholder,
  mono,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  mono?: boolean;
}) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-[11px] text-muted-foreground">{label}</span>
      <input
        className={cn(
          'h-8 rounded-lg border border-input bg-secondary px-2.5 text-xs outline-none focus:border-ring dark:bg-white/5',
          mono && 'font-mono',
        )}
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
      />
    </label>
  );
}

// HostSection: host this library to other machines (e.g. a laptop over a
// Tailscale network). The listen/port toggle is a shell preference applied at
// daemon spawn — hence the relaunch dance — while the pairing token lives in
// the daemon and swaps live.
function HostSection() {
  const client = useApiClient();
  const [prefs, setPrefs] = useState<RemoteAccessPrefs | null>(null);
  // Subscribed, not fetched once: renaming this machine or approving a device
  // pushes a fresh snapshot, so two open windows never disagree.
  const { data: info } = useGetRemoteAccess();
  const [port, setPort] = useState('');
  const [confirmRegen, setConfirmRegen] = useState(false);
  const [name, setName] = useState<string | null>(null);

  useEffect(() => {
    void window.marraw?.getRemoteAccess?.().then((p) => {
      setPrefs(p);
      setPort(String(p.port));
    });
  }, []);

  // The name field tracks the server until the user starts typing; `name`
  // being null means "not edited", which is what keeps a push from yanking
  // characters out from under them mid-edit.
  const nameValue = name ?? info?.deviceName ?? '';
  const applyName = () => {
    if (name === null || name === info?.deviceName) {
      setName(null);
      return;
    }
    setDeviceName(client, name.trim())
      .then(() => setName(null))
      .catch((err) => {
        setName(null);
        toast.error((err as Error).message);
      });
  };

  const update = (patch: Partial<RemoteAccessPrefs>) =>
    window.marraw
      ?.setRemoteAccess?.(patch)
      .then((p) => {
        setPrefs(p);
        setPort(String(p.port));
      })
      .catch((err) => toast.error((err as Error).message));

  const applyPort = () => {
    const n = Number(port);
    if (!prefs || !Number.isInteger(n) || n < 1 || n > 65535 || n === prefs.port) {
      setPort(prefs ? String(prefs.port) : '');
      return;
    }
    void update({ port: n });
  };

  const regen = () => {
    setConfirmRegen(false);
    regeneratePairingToken(client)
      .then(() => toast.success('Pairing token regenerated — saved connections need the new token'))
      .catch((err) => toast.error((err as Error).message));
  };

  return (
    <div className="flex flex-col">
      <div className="mb-1.5 text-[10px] tracking-[.06em] text-muted-foreground uppercase">
        Host this library
      </div>
      <SettingRow
        title="Allow remote connections"
        description="Let marraw on another machine (e.g. your laptop over Tailscale) open this library. The daemon listens on all interfaces on the port below, and other computers can find it by name — but nothing gets in until you approve it here."
        control={
          <Switch
            checked={prefs?.enabled ?? false}
            disabled={!prefs}
            onCheckedChange={(v) => update({ enabled: v })}
            aria-label="Allow remote connections"
          />
        }
      />
      {prefs?.enabled && (
        <SettingRow
          title="This computer's name"
          description="What other computers see when they find this one."
          control={
            <input
              className="h-8 w-44 rounded-lg border border-input bg-secondary px-2.5 text-xs outline-none focus:border-ring dark:bg-white/5"
              value={nameValue}
              placeholder="…"
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && applyName()}
              onBlur={applyName}
              aria-label="This computer's name"
            />
          }
        />
      )}
      {prefs?.enabled && (
        <SettingRow
          title="Accept new connection requests"
          description="When off, computers you have already approved keep working, but nobody new can ask. Turn it off once your machines are set up."
          control={
            <Switch
              checked={info?.pairingOpen ?? true}
              disabled={!info}
              onCheckedChange={(v) =>
                setPairingOpen(client, v).catch((err) => toast.error((err as Error).message))
              }
              aria-label="Accept new connection requests"
            />
          }
        />
      )}
      {prefs?.enabled && (
        <SettingRow
          title="Port"
          description="Remote machines connect to this port. Pick one that's free on this machine; saved connections on other machines include it."
          control={
            <input
              className="h-8 w-20 rounded-lg border border-input bg-secondary px-2 text-right font-mono text-xs outline-none focus:border-ring dark:bg-white/5"
              value={port}
              onChange={(e) => setPort(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && applyPort()}
              onBlur={applyPort}
              aria-label="Remote access port"
            />
          }
        />
      )}
      {prefs?.restartRequired && (
        <SettingRow
          title={<span className="text-accent-text">Restart required</span>}
          description="Remote access settings apply when the app starts."
          control={
            <Button variant="outline" size="sm" onClick={() => void window.marraw?.relaunch?.()}>
              Restart now
            </Button>
          }
        />
      )}
      {prefs?.enabled && <ApprovedDevices />}
      <SettingRow
        title="Pairing token"
        description={
          <div className="flex flex-col gap-1">
            <span>
              The manual way in, for a computer the search cannot reach. Enter it there under
              Settings → Remote → Add connection → Enter details manually. Regenerating locks out
              every connection set up this way; approved computers above are unaffected.
            </span>
            <span className="font-mono text-[11.5px] text-foreground select-text">
              {info ? info.pairingToken : '…'}
            </span>
          </div>
        }
        control={
          <div className="flex gap-1.5">
            <Button
              variant="outline"
              size="sm"
              disabled={!info}
              onClick={() => {
                void navigator.clipboard.writeText(info!.pairingToken);
                toast.success('Pairing token copied');
              }}
            >
              Copy
            </Button>
            {confirmRegen ? (
              <>
                <Button variant="ghost" size="sm" onClick={() => setConfirmRegen(false)}>
                  Cancel
                </Button>
                <Button variant="destructive" size="sm" onClick={regen}>
                  Regenerate
                </Button>
              </>
            ) : (
              <Button
                variant="destructive"
                size="sm"
                disabled={!info}
                onClick={() => setConfirmRegen(true)}
              >
                Regenerate
              </Button>
            )}
          </div>
        }
      />
    </div>
  );
}

/**
 * The computers approved through the pairing dialog. Each holds a token of
 * its own, so revoking one here is exactly that — the others keep working,
 * unlike regenerating the shared pairing token.
 */
function ApprovedDevices() {
  const client = useApiClient();
  const { data } = useListRemoteDevices();
  const devices = data ?? [];
  const [confirmID, setConfirmID] = useState('');

  if (devices.length === 0) return null;

  const revoke = (id: string, name: string) => {
    setConfirmID('');
    revokeRemoteDevice(client, id)
      .then(() => toast.success(`${name} can no longer connect`))
      .catch((err) => toast.error((err as Error).message));
  };

  return (
    <SettingRow
      title="Approved computers"
      description="Computers you let in. Revoking one disconnects it now and leaves the others alone."
      control={
        <div className="flex w-64 flex-col gap-1.5" data-testid="approved-devices">
          {devices.map((d) => (
            <div
              key={d.id}
              className="flex items-center gap-2 rounded-lg border bg-secondary px-2.5 py-1.5 dark:bg-white/5"
            >
              <div className="min-w-0 flex-1">
                <div className="truncate text-[12px] font-medium">{d.name}</div>
                <div className="truncate font-mono text-[10px] text-faint">
                  last seen {relativeTime(d.lastSeen)}
                </div>
              </div>
              {confirmID === d.id ? (
                <>
                  <Button variant="ghost" size="sm" onClick={() => setConfirmID('')}>
                    Cancel
                  </Button>
                  <Button variant="destructive" size="sm" onClick={() => revoke(d.id, d.name)}>
                    Revoke
                  </Button>
                </>
              ) : (
                <Button variant="ghost" size="sm" onClick={() => setConfirmID(d.id)}>
                  Revoke
                </Button>
              )}
            </div>
          ))}
        </div>
      }
    />
  );
}

/** "3 minutes ago" for the devices list — coarse on purpose. */
function relativeTime(ms: number): string {
  if (!ms) return 'never';
  const secs = Math.max(0, Math.round((Date.now() - ms) / 1000));
  if (secs < 90) return 'just now';
  const mins = Math.round(secs / 60);
  if (mins < 60) return `${mins} min ago`;
  const hours = Math.round(mins / 60);
  if (hours < 48) return `${hours} h ago`;
  return `${Math.round(hours / 24)} days ago`;
}
