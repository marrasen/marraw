import { Toaster } from '@/components/ui/sonner';
import { FolderTree } from '@/components/FolderTree';
import { FilterBar } from '@/components/FilterBar';
import { EditPanel } from '@/components/EditPanel';
import { ExportDialog } from '@/components/ExportDialog';
import { SettingsDialog } from '@/components/SettingsDialog';
import { StatusBar } from '@/components/StatusBar';
import { GridView } from '@/views/GridView';
import { LoupeView } from '@/views/LoupeView';
import { useKeyboard } from '@/lib/keyboard';
import { usePhotos } from '@/lib/usePhotos';
import { useUIStore } from '@/stores/uiStore';
import { useEffect } from 'react';
import { Settings } from 'lucide-react';
import { openFolder } from '@/api/library';
import { useApiClient } from '@/api/client';

// Auto-open a folder passed via ?openFolder= (used by the UI smoke test and
// handy for jumping straight into a shoot).
function useAutoOpenFolder() {
  const client = useApiClient();
  const setFolder = useUIStore((s) => s.setFolder);
  useEffect(() => {
    const path = new URLSearchParams(window.location.search).get('openFolder');
    if (!path) return;
    openFolder(client, path).then((info) => setFolder(info.folderId, path)).catch(() => {});
  }, [client, setFolder]);
}

export default function App() {
  useKeyboard();
  useAutoOpenFolder();
  const folderId = useUIStore((s) => s.folderId);

  return (
    <div className="flex h-screen flex-col bg-background text-foreground">
      <div className="flex min-h-0 flex-1">
        <aside className="w-60 shrink-0 border-r">
          <FolderTree />
        </aside>
        {folderId != null ? (
          <Workspace folderId={folderId} />
        ) : (
          <main className="flex flex-1 flex-col items-center justify-center gap-3 text-sm text-muted-foreground">
            Pick a folder with RAW photos on the left to get started.
            <button
              className="flex items-center gap-1.5 rounded px-2 py-1 text-xs hover:bg-accent hover:text-foreground"
              onClick={() => useUIStore.getState().setSettingsOpen(true)}
            >
              <Settings className="size-3.5" />
              Settings
            </button>
          </main>
        )}
      </div>
      <SettingsDialog />
      <Toaster position="bottom-right" />
    </div>
  );
}

function Workspace({ folderId }: { folderId: number }) {
  const view = useUIStore((s) => s.view);
  const { all, visible } = usePhotos(folderId);

  // ?loupe=1 jumps straight into loupe on the first photo (UI smoke test).
  useEffect(() => {
    const s = useUIStore.getState();
    if (visible.length > 0 && s.focusId == null && new URLSearchParams(window.location.search).has('loupe')) {
      s.focus(visible[0].id);
      s.setView('loupe');
    }
  }, [visible]);

  return (
    <>
      <main className="flex min-w-0 flex-1 flex-col">
        <FilterBar shownCount={visible.length} totalCount={all.length} />
        {view === 'grid' ? (
          <GridView photos={visible} folderId={folderId} />
        ) : (
          <LoupeView photos={visible} />
        )}
        <StatusBar shown={visible.length} total={all.length} />
      </main>
      <aside className="w-72 shrink-0 border-l">
        <EditPanel photos={all} />
      </aside>
      <ExportDialog photos={visible} />
    </>
  );
}
