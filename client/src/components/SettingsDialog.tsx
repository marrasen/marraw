import { useState } from 'react';
import { toast } from 'sonner';
import { FileJson, FolderOpen, HardDrive, RotateCcw, Trash2 } from 'lucide-react';
import { useGetAppSettings, setSidecarWrites } from '@/api/library';
import { useGetCacheInfo, clearCache, setCacheDir } from '@/api/system';
import { useApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useUIStore } from '@/stores/uiStore';

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

export function SettingsDialog() {
  const open = useUIStore((s) => s.settingsOpen);
  const setOpen = useUIStore((s) => s.setSettingsOpen);
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Settings</DialogTitle>
        </DialogHeader>
        {open && <SettingsBody />}
      </DialogContent>
    </Dialog>
  );
}

function SettingsBody() {
  return (
    <div className="flex flex-col divide-y">
      <SidecarSetting />
      <CacheSetting />
    </div>
  );
}

// Row is a labelled setting: title + description on the left, control on the right.
function Row({
  icon: Icon,
  title,
  description,
  control,
}: {
  icon: typeof FileJson;
  title: string;
  description: React.ReactNode;
  control: React.ReactNode;
}) {
  return (
    <div className="flex items-start gap-3 py-4">
      <Icon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium">{title}</div>
        <div className="mt-0.5 text-xs text-muted-foreground">{description}</div>
      </div>
      <div className="shrink-0">{control}</div>
    </div>
  );
}

function SidecarSetting() {
  const client = useApiClient();
  const { data } = useGetAppSettings();
  const enabled = data?.sidecarWrites ?? true;
  const toggle = () =>
    setSidecarWrites(client, !enabled).catch((err) => toast.error((err as Error).message));
  return (
    <Row
      icon={FileJson}
      title="Write edit sidecars"
      description="Mirror ratings and develop settings to a .marraw.json file next to each RAW, so copying a folder carries your edits to another machine. Folders that already contain sidecars are always imported."
      control={
        <button
          role="switch"
          aria-checked={enabled}
          onClick={toggle}
          className={`relative h-6 w-10 rounded-full transition-colors ${
            enabled ? 'bg-primary' : 'bg-muted'
          }`}
        >
          <span
            className={`absolute top-0.5 size-5 rounded-full bg-white shadow transition-transform ${
              enabled ? 'translate-x-4' : 'translate-x-0.5'
            }`}
          />
        </button>
      }
    />
  );
}

function CacheSetting() {
  const client = useApiClient();
  const { data: info } = useGetCacheInfo();
  const [busy, setBusy] = useState(false);
  const [confirmClear, setConfirmClear] = useState(false);

  const changeFolder = async () => {
    if (!window.marraw) return;
    const dir = await window.marraw.pickDirectory();
    if (!dir) return;
    setBusy(true);
    try {
      await setCacheDir(client, dir);
      toast.success('Cache folder changed');
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const resetFolder = async () => {
    setBusy(true);
    try {
      await setCacheDir(client, '');
      toast.success('Cache folder reset to default');
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const doClear = async () => {
    setBusy(true);
    try {
      await clearCache(client);
      toast.success('Cache cleared');
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setBusy(false);
      setConfirmClear(false);
    }
  };

  return (
    <Row
      icon={HardDrive}
      title="Preview cache"
      description={
        <div className="flex flex-col gap-1">
          <span>
            Rendered previews and 1:1 tiles. Deleting them is safe — they rebuild on demand.
          </span>
          <span className="flex flex-wrap items-center gap-x-1.5">
            <span className="font-medium text-foreground">
              {info ? formatBytes(info.bytes) : '…'}
            </span>
            {info != null && <span>· {info.files.toLocaleString()} files</span>}
          </span>
          {info?.dir && (
            <button
              className="max-w-full truncate text-left font-mono text-[11px] underline-offset-2 hover:underline"
              title={window.marraw ? `${info.dir} — click to reveal` : info.dir}
              onClick={() => window.marraw?.revealInExplorer(info.dir)}
            >
              {info.dir}
              {info.isCustom ? '' : ' (default)'}
            </button>
          )}
        </div>
      }
      control={
        <div className="flex flex-col items-stretch gap-1.5">
          {window.marraw && (
            <Button variant="outline" size="sm" onClick={changeFolder} disabled={busy}>
              <FolderOpen className="size-3.5" />
              Change…
            </Button>
          )}
          {info?.isCustom && (
            <Button variant="ghost" size="sm" onClick={resetFolder} disabled={busy}>
              <RotateCcw className="size-3.5" />
              Use default
            </Button>
          )}
          {confirmClear ? (
            <div className="flex gap-1.5">
              <Button variant="ghost" size="sm" onClick={() => setConfirmClear(false)} disabled={busy}>
                Cancel
              </Button>
              <Button variant="destructive" size="sm" onClick={doClear} disabled={busy}>
                Clear
              </Button>
            </div>
          ) : (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setConfirmClear(true)}
              disabled={busy || !info || info.files === 0}
            >
              <Trash2 className="size-3.5" />
              Clear cache
            </Button>
          )}
        </div>
      }
    />
  );
}
