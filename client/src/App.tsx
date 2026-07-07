import { useEffect } from 'react';
import { FolderPlus, Plus } from 'lucide-react';
import { toast } from 'sonner';
import { Toaster } from '@/components/ui/sonner';
import { Button } from '@/components/ui/button';
import { TopBar } from '@/components/TopBar';
import { LibraryRail } from '@/components/LibraryRail';
import { AddFolderDialog } from '@/components/AddFolderDialog';
import { FilterBar } from '@/components/FilterBar';
import { EditPanel } from '@/components/EditPanel';
import { ExportDialog } from '@/components/ExportDialog';
import { SettingsDialog } from '@/components/SettingsDialog';
import { StatusBar } from '@/components/StatusBar';
import { GridView } from '@/views/GridView';
import { LoupeView } from '@/views/LoupeView';
import { useKeyboard } from '@/lib/keyboard';
import { usePhotos } from '@/lib/usePhotos';
import { openRoot, samePath, saveRoots, useLibraryRoots } from '@/lib/library';
import { openFolder } from '@/api/library';
import { useApiClient } from '@/api/client';
import { useUIStore } from '@/stores/uiStore';
import '@/lib/electron';

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

// Dropping a folder anywhere in the window adds it to the library.
function useDropFolder() {
  const client = useApiClient();
  const { roots } = useLibraryRoots();
  useEffect(() => {
    const over = (e: DragEvent) => e.preventDefault();
    const drop = async (e: DragEvent) => {
      e.preventDefault();
      const file = e.dataTransfer?.files?.[0];
      if (!file || !window.marraw) return;
      const path = window.marraw.getPathForFile(file);
      if (!path || !(await window.marraw.isDirectory(path))) return;
      if (roots.some((r) => samePath(r.path, path))) {
        toast.info('That folder is already in the library');
        return;
      }
      const next = [...roots, { path, alias: '', includeSubfolders: true, photoCount: 0 }];
      await saveRoots(client, next);
      toast.success(`Added ${path} to the library`);
      void openRoot(client, next, next[next.length - 1]);
    };
    window.addEventListener('dragover', over);
    window.addEventListener('drop', drop);
    return () => {
      window.removeEventListener('dragover', over);
      window.removeEventListener('drop', drop);
    };
  }, [client, roots]);
}

export default function App() {
  useKeyboard();
  useAutoOpenFolder();
  useDropFolder();
  const folderId = useUIStore((s) => s.folderId);
  const mode = useUIStore((s) => s.mode);
  const { roots, isLoading } = useLibraryRoots();
  const empty = roots.length === 0 && folderId == null;

  return (
    <div className="flex h-screen flex-col bg-background text-foreground">
      <TopBar />
      <div className="flex min-h-0 flex-1">
        {mode === 'library' && (
          <aside className="w-[214px] shrink-0 border-r">
            <LibraryRail />
          </aside>
        )}
        {folderId != null ? (
          <Workspace folderId={folderId} />
        ) : isLoading ? null : empty ? (
          <EmptyLibrary />
        ) : (
          <main className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
            Pick a shoot on the left to get started.
          </main>
        )}
      </div>
      <AddFolderDialog />
      <SettingsDialog />
      <Toaster position="bottom-right" />
    </div>
  );
}

// First-run empty state (handoff plate "EMPTY"): one clear call to action
// and the files-never-move reassurance, right where the commitment is made.
function EmptyLibrary() {
  const setAddFolderOpen = useUIStore((s) => s.setAddFolderOpen);
  return (
    <main className="flex flex-1 items-center justify-center p-10">
      <div className="flex max-w-[420px] flex-col items-center gap-5 text-center">
        <div className="flex size-[72px] items-center justify-center rounded-[18px] border border-primary/35 bg-primary/10">
          <FolderPlus className="size-8 text-[#aab0ff]" strokeWidth={1.6} />
        </div>
        <div className="flex flex-col gap-2">
          <h2 className="text-[23px] font-semibold tracking-[-.02em]">Your library is empty</h2>
          <p className="text-sm leading-relaxed text-muted-foreground">
            Add a folder of RAW photos to start culling. marraw reads them where they live —{' '}
            <strong className="font-medium text-secondary-foreground">
              your files never move or change
            </strong>
            .
          </p>
        </div>
        <div className="flex flex-col items-center gap-3">
          <Button size="lg" className="h-[42px] px-5 text-sm" onClick={() => setAddFolderOpen(true)}>
            <Plus data-icon="inline-start" />
            Add folder
          </Button>
          <span className="text-xs text-faint">or drop a folder anywhere in this window</span>
        </div>
      </div>
    </main>
  );
}

function Workspace({ folderId }: { folderId: number }) {
  const view = useUIStore((s) => s.view);
  const mode = useUIStore((s) => s.mode);
  const { all, visible } = usePhotos(folderId);
  const picked = all.filter((p) => p.flag === 'pick').length;

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
        {mode === 'library' && view === 'grid' ? (
          <>
            <FilterBar shownCount={visible.length} totalCount={all.length} />
            <GridView photos={visible} folderId={folderId} />
            <StatusBar shown={visible.length} total={all.length} picked={picked} />
          </>
        ) : (
          <LoupeView photos={visible} />
        )}
      </main>
      {(mode === 'library' || mode === 'develop') && (
        <aside className="w-[300px] shrink-0 border-l">
          <EditPanel photos={all} />
        </aside>
      )}
      <ExportDialog photos={visible} />
    </>
  );
}
