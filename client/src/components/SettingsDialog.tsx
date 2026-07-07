import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { X } from 'lucide-react';
import { useGetAppSettings, setSidecarWrites } from '@/api/library';
import { useGetCacheInfo, clearCache, setCacheCap, setCacheDir } from '@/api/system';
import { useApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Segmented } from '@/components/ui/segmented';
import { Switch } from '@/components/ui/switch';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import { useTheme } from '@/components/theme-provider';
import { DIALS, type DialKey } from '@/lib/dials';
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

const SECTIONS = ['General', 'Toolbars', 'Cache', 'Sidecars', 'Performance'] as const;
type Section = (typeof SECTIONS)[number];

/**
 * Settings (handoff plate "SETTINGS"): a 760×480 left-nav modal — General
 * (theme), Cache (location + usage meter + clear), Sidecars, Performance
 * (cache size limit).
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
            {open && section === 'Cache' && <CacheSection />}
            {open && section === 'Sidecars' && <SidecarSection />}
            {open && section === 'Performance' && <PerformanceSection />}
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
  return (
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
  );
}

// ToolbarsSection: which develop dials float in the Cull confirm bar and
// the Develop quick dock. None (the default) keeps those bars compact.
function ToolbarsSection() {
  const cullDials = useUIStore((s) => s.cullDials);
  const quickDials = useUIStore((s) => s.quickDials);
  const setCullDials = useUIStore((s) => s.setCullDials);
  const setQuickDials = useUIStore((s) => s.setQuickDials);
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
  return (
    <div className="border-b py-4 first:pt-0 last:border-0">
      <div className="text-sm font-medium">{title}</div>
      <div className="mt-0.5 text-xs leading-normal text-muted-foreground">{description}</div>
      <div className="mt-2.5 flex flex-wrap gap-1.5">
        {chip(value.length === 0, 'None', () => onChange([]))}
        {DIALS.map((d) => chip(value.includes(d.key), d.label, () => toggle(d.key)))}
      </div>
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
  const [confirmClear, setConfirmClear] = useState(false);

  const run = (fn: () => Promise<unknown>, done: string) => {
    setBusy(true);
    fn()
      .then(() => toast.success(done))
      .catch((err) => toast.error((err as Error).message))
      .finally(() => {
        setBusy(false);
        setConfirmClear(false);
      });
  };

  const usedPct =
    info && info.capBytes > 0 ? Math.min(100, (info.bytes / info.capBytes) * 100) : 0;

  return (
    <div className="flex flex-col">
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
          confirmClear ? (
            <div className="flex gap-1.5">
              <Button variant="ghost" size="sm" onClick={() => setConfirmClear(false)} disabled={busy}>
                Cancel
              </Button>
              <Button
                variant="destructive"
                size="sm"
                disabled={busy}
                onClick={() => run(() => clearCache(client), 'Cache cleared')}
              >
                Clear
              </Button>
            </div>
          ) : (
            <Button
              variant="destructive"
              size="sm"
              onClick={() => setConfirmClear(true)}
              disabled={busy || !info || info.files === 0}
            >
              Clear cache
            </Button>
          )
        }
      />
    </div>
  );
}

function PerformanceSection() {
  const client = useApiClient();
  const { data: info } = useGetCacheInfo();
  const [gb, setGb] = useState('');
  useEffect(() => {
    if (info && info.capBytes > 0) setGb(String(Math.round(info.capBytes / (1 << 30))));
  }, [info]);

  const apply = () => {
    const n = Number(gb);
    if (!Number.isFinite(n) || n < 1) return;
    setCacheCap(client, Math.round(n))
      .then(() => toast.success(`Cache limit set to ${Math.round(n)} GB`))
      .catch((err) => toast.error((err as Error).message));
  };

  return (
    <SettingRow
      title="Preview cache limit"
      description="When the cache grows past this size, the least-recently viewed previews are evicted in the background. Bigger caches keep more shoots instant."
      control={
        <div className="flex items-center gap-1.5">
          <input
            className="h-8 w-16 rounded-lg border border-input bg-secondary px-2 text-right font-mono text-xs outline-none focus:border-ring dark:bg-white/5"
            value={gb}
            onChange={(e) => setGb(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && apply()}
            onBlur={apply}
            aria-label="Cache limit in GB"
          />
          <span className="font-mono text-[11px] text-muted-foreground">GB</span>
        </div>
      }
    />
  );
}
