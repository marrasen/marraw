import { Toaster } from '@/components/ui/sonner';
import { FolderTree } from '@/components/FolderTree';
import { FilterBar } from '@/components/FilterBar';
import { EditPanel } from '@/components/EditPanel';
import { ExportDialog } from '@/components/ExportDialog';
import { StatusBar } from '@/components/StatusBar';
import { GridView } from '@/views/GridView';
import { LoupeView } from '@/views/LoupeView';
import { useKeyboard } from '@/lib/keyboard';
import { usePatchEvents, usePhotos } from '@/lib/usePhotos';
import { useUIStore } from '@/stores/uiStore';
import { useEffect } from 'react';
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
  usePatchEvents();
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
          <main className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
            Pick a folder with RAW photos on the left to get started.
          </main>
        )}
      </div>
      <Toaster position="bottom-right" />
    </div>
  );
}

function Workspace({ folderId }: { folderId: number }) {
  const view = useUIStore((s) => s.view);
  const { all, visible } = usePhotos(folderId);

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
        <EditPanel />
      </aside>
      <ExportDialog photos={visible} />
    </>
  );
}
