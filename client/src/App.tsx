import { useEffect } from 'react';
import { FolderPlus, Plus } from 'lucide-react';
import { toast } from 'sonner';
import { Toaster } from '@/components/ui/sonner';
import { Button } from '@/components/ui/button';
import { TopBar } from '@/components/TopBar';
import { LibraryRail } from '@/components/LibraryRail';
import { AddFolderDialog } from '@/components/AddFolderDialog';
import { CommandPalette } from '@/components/CommandPalette';
import { ShortcutsOverlay } from '@/components/ShortcutsOverlay';
import { FilterBar } from '@/components/FilterBar';
import { EditPanel } from '@/components/EditPanel';
import { ExportDialog } from '@/components/ExportDialog';
import { SettingsDialog } from '@/components/SettingsDialog';
import { WatermarkDialog } from '@/components/WatermarkDialog';
import { StatusBar } from '@/components/StatusBar';
import { TaskToasts } from '@/components/TaskTray';
import { clampRailWidth, RAIL_WIDTH_DEFAULT, useUIStore } from '@/stores/uiStore';
import { GridView } from '@/views/GridView';
import { CullView } from '@/views/CullView';
import { DevelopView } from '@/views/DevelopView';
import { useKeyboard } from '@/lib/keyboard';
import { usePhotos } from '@/lib/usePhotos';
import { useFolderScan } from '@/lib/useFolderScan';
import type { LibraryRoot } from '@/api/library';
import {
  baseName,
  openRoot,
  openShoot,
  parentPath,
  samePath,
  saveRoots,
  useLibraryRoots,
} from '@/lib/library';
import { updateRailWidth, UISettingsSync } from '@/lib/uiSettings';
import { openFolder } from '@/api/library';
import { useApiClient } from '@/api/client';
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
      // Already discovered as a child of a library folder: adding it as a root
      // of its own would pull it out of that block into a duplicate group.
      if (roots.some((r) => r.isParent && samePath(r.path, parentPath(path)))) {
        toast.info('That folder is already in the library');
        void openShoot(client, {
          path,
          name: baseName(path),
          photoCount: 0,
          isSelf: false,
          earliestTakenAt: 0,
        });
        return;
      }
      const next: LibraryRoot[] = [
        ...roots,
        { path, alias: '', includeSubfolders: true, photoCount: 0, isParent: false },
      ];
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
  // Mirror the OS fullscreen state (F11) so Esc can exit it first.
  useEffect(() => {
    window.win?.onFullScreenChange((fs) => useUIStore.setState({ fullscreen: fs }));
  }, []);
  // Trackpad pinch arrives as ctrl+wheel. The loupe consumes it for image zoom;
  // everywhere else it must NOT trigger Chromium's page/visual-viewport zoom.
  // React registers wheel listeners as passive, so preventDefault only bites from
  // a native non-passive listener.
  useEffect(() => {
    const block = (e: WheelEvent) => {
      if (e.ctrlKey) e.preventDefault();
    };
    window.addEventListener('wheel', block, { passive: false, capture: true });
    return () =>
      window.removeEventListener('wheel', block, { capture: true } as EventListenerOptions);
  }, []);
  const folderId = useUIStore((s) => s.folderId);
  const mode = useUIStore((s) => s.mode);
  const view = useUIStore((s) => s.view);
  const { roots, isLoading } = useLibraryRoots();
  const empty = roots.length === 0 && folderId == null;
  const structured = folderId == null || (mode === 'library' && view === 'grid');

  return (
    <div className="app-backdrop flex h-screen flex-col text-foreground">
      <UISettingsSync />
      {/* Cinema modes are edge-to-edge; their floating HUD replaces the bar. */}
      {structured && <TopBar />}
      <div className="flex min-h-0 flex-1">
        {structured && <ResizableLibraryRail />}
        {folderId != null ? (
          <Workspace folderId={folderId} />
        ) : isLoading ? null : empty ? (
          <EmptyLibrary />
        ) : (
          <Welcome />
        )}
      </div>
      <AddFolderDialog />
      <SettingsDialog />
      <WatermarkDialog />
      <CommandPalette />
      <ShortcutsOverlay />
      <TaskToasts />
      <Toaster position="bottom-right" />
    </div>
  );
}

// The library rail with a draggable right edge. Width follows the pointer
// live via the store (optimistic, no server chatter) and persists once on
// release; double-click snaps back to the design default.
function ResizableLibraryRail() {
  const client = useApiClient();
  const railWidth = useUIStore((s) => s.railWidth);

  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    e.preventDefault();
    const handle = e.currentTarget;
    const startX = e.clientX;
    const startWidth = useUIStore.getState().railWidth;
    handle.setPointerCapture(e.pointerId);
    const onMove = (ev: PointerEvent) =>
      useUIStore.setState({ railWidth: clampRailWidth(startWidth + ev.clientX - startX) });
    const onUp = () => {
      handle.removeEventListener('pointermove', onMove);
      handle.removeEventListener('pointerup', onUp);
      handle.removeEventListener('pointercancel', onUp);
      updateRailWidth(client, useUIStore.getState().railWidth);
    };
    handle.addEventListener('pointermove', onMove);
    handle.addEventListener('pointerup', onUp);
    handle.addEventListener('pointercancel', onUp);
  };

  return (
    <aside className="relative shrink-0 border-r" style={{ width: railWidth }}>
      <LibraryRail />
      <div
        className="absolute inset-y-0 -right-[3px] z-10 w-[6px] cursor-col-resize touch-none hover:bg-primary/35 active:bg-primary/55"
        onPointerDown={onPointerDown}
        onDoubleClick={() => updateRailWidth(client, RAIL_WIDTH_DEFAULT)}
      />
    </aside>
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

// The library has shoots but none is open: the mark, the build you're running,
// and where to click next.
function Welcome() {
  return (
    <main className="flex flex-1 items-center justify-center p-10">
      <div className="flex flex-col items-center gap-5 text-center">
        {/* Relative URL: the packaged shell loads index.html over file://. */}
        <img src="./icon.svg" alt="" className="size-[112px]" />
        <div className="flex flex-col gap-2">
          <h2 className="text-[23px] font-semibold tracking-[-.02em]">
            Welcome to marraw v{__APP_VERSION__}
          </h2>
          <p className="text-sm text-muted-foreground">Pick a shoot on the left to get started.</p>
        </div>
      </div>
    </main>
  );
}

function Workspace({ folderId }: { folderId: number }) {
  const view = useUIStore((s) => s.view);
  const mode = useUIStore((s) => s.mode);
  const folderPath = useUIStore((s) => s.folderPath);
  const showEditPanel = useUIStore((s) => s.showEditPanel);
  const { all, visible } = usePhotos(folderId);
  const picked = all.filter((p) => p.flag === 'pick').length;
  const scan = useFolderScan(folderPath);

  // ?loupe=1 jumps straight into loupe on the first photo (UI smoke test).
  useEffect(() => {
    const s = useUIStore.getState();
    if (visible.length > 0 && s.focusId == null && new URLSearchParams(window.location.search).has('loupe')) {
      s.focus(visible[0].id);
      s.setView('loupe');
    }
  }, [visible]);

  // Legacy view='loupe' inside Library opens the Develop cinema — the old
  // in-place loupe grew into that surface.
  const structured = mode === 'library' && view === 'grid';
  return (
    <>
      <main className="flex min-w-0 flex-1 flex-col">
        {mode === 'cull' ? (
          <CullView photos={visible} />
        ) : structured ? (
          <>
            <FilterBar />
            <GridView photos={visible} folderId={folderId} />
            <StatusBar shown={visible.length} total={all.length} picked={picked} scan={scan} />
          </>
        ) : (
          <DevelopView photos={visible} all={all} />
        )}
      </main>
      {structured && showEditPanel && (
        <aside className="w-[300px] shrink-0 border-l">
          <EditPanel photos={all} />
        </aside>
      )}
      <ExportDialog photos={visible} />
    </>
  );
}
