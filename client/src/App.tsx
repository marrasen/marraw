import { useEffect, useMemo, useRef, useState } from 'react';
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
import { SubjectScanDialog } from '@/components/SubjectScanDialog';
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
import { burstMap } from '@/lib/bursts';
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
import { updateLastSeenVersion, updateRailWidth, UISettingsSync } from '@/lib/uiSettings';
import { entriesSince, type ChangelogEntry } from '@/lib/changelog';
import { openFolder, setFocus } from '@/api/library';
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
  // Fresh install: baseline lastSeenVersion silently so the first update
  // after this one shows only its own news, not the whole history. Runs
  // app-level because a first-run user may never land on Welcome.
  const client = useApiClient();
  const settingsLoaded = useUIStore((s) => s.settingsLoaded);
  useEffect(() => {
    if (settingsLoaded && useUIStore.getState().lastSeenVersion === '')
      updateLastSeenVersion(client, __APP_VERSION__);
  }, [settingsLoaded, client]);
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
// and where to click next. After an update, a "What's new" card lists every
// release since the version this machine last saw.
function Welcome() {
  const client = useApiClient();
  const settingsLoaded = useUIStore((s) => s.settingsLoaded);
  // Captured once when settings arrive: marking the version seen right after
  // must not blank the card mid-mount (the write is optimistic). The ref (not
  // the state) guards the capture — StrictMode re-runs the effect before the
  // setState lands, and the second run would re-read the already-bumped store.
  const [whatsNew, setWhatsNew] = useState<ChangelogEntry[] | null>(null);
  const captured = useRef(false);
  useEffect(() => {
    if (!settingsLoaded || captured.current) return;
    captured.current = true;
    const last = useUIStore.getState().lastSeenVersion;
    setWhatsNew(last === '' ? [] : entriesSince(last, __APP_VERSION__));
    if (last !== __APP_VERSION__) updateLastSeenVersion(client, __APP_VERSION__);
  }, [settingsLoaded, client]);

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
        {whatsNew != null && whatsNew.length > 0 && (
          <section className="mt-2 w-[480px] max-w-full rounded-xl border bg-card/50 text-left">
            <h3 className="border-b px-5 py-3 text-sm font-semibold">
              What's new in v{__APP_VERSION__}
            </h3>
            <div className="max-h-[40vh] overflow-y-auto px-5 py-4">
              {whatsNew.map((e, ei) => (
                <div key={e.version} className={ei > 0 ? 'mt-4' : undefined}>
                  {whatsNew.length > 1 && (
                    <div className="text-xs font-medium text-muted-foreground">
                      v{e.version}
                      {e.date && ` — ${e.date}`}
                    </div>
                  )}
                  <ul className="mt-1.5 flex flex-col gap-1 text-sm text-secondary-foreground">
                    {e.items.map((item, i) => (
                      <li key={i} className="flex gap-2">
                        <span className="text-faint">•</span>
                        <ChangeItem text={item} />
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            </div>
          </section>
        )}
      </div>
    </main>
  );
}

// Changelog bullets follow the commit style "Area: what changed" — set the
// area off in a stronger weight when present.
function ChangeItem({ text }: { text: string }) {
  const sep = text.indexOf(': ');
  if (sep <= 0) return <span>{text}</span>;
  return (
    <span>
      <span className="font-medium text-foreground">{text.slice(0, sep)}:</span>
      {text.slice(sep + 1)}
    </span>
  );
}

function Workspace({ folderId }: { folderId: number }) {
  const view = useUIStore((s) => s.view);
  const mode = useUIStore((s) => s.mode);
  const folderPath = useUIStore((s) => s.folderPath);
  const showEditPanel = useUIStore((s) => s.showEditPanel);
  const subjectScanOpen = useUIStore((s) => s.subjectScanOpen);
  const setSubjectScanOpen = useUIStore((s) => s.setSubjectScanOpen);
  const { all, visible, softBelow } = usePhotos(folderId);
  // Burst groups are derived over the WHOLE folder, not the filtered `visible`
  // list, so badge counts and the sharpest-frame pick describe the real group
  // even when a filter hides some members.
  const bursts = useMemo(() => burstMap(all), [all]);
  const picked = all.filter((p) => p.flag === 'pick').length;
  // Subject-analysis coverage over the whole folder, for the toolbar scan
  // control. Counts frames that have been analyzed — including ones with no
  // detectable subject (score-invisible) — so the indicator resolves instead of
  // forever flagging subjectless frames as "the rest" to scan.
  const subjectAnalyzed = all.filter((p) => p.subjectAnalyzed).length;
  const scan = useFolderScan(folderPath);
  const client = useApiClient();

  // Tell the backend which photo the viewport is centred on so its background
  // pre-render pass warms the loupe-ready rendition outward from here first.
  // Fire-and-forget; the pass re-reads focus on every claim, so this only needs
  // to keep the hint current as the user navigates.
  const focusId = useUIStore((s) => s.focusId);
  useEffect(() => {
    if (focusId == null) return;
    setFocus(client, folderId, focusId).catch(() => {});
  }, [client, folderId, focusId]);

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
          <CullView photos={visible} bursts={bursts} softBelow={softBelow} />
        ) : structured ? (
          <>
            <FilterBar softBelow={softBelow} subjectAnalyzed={subjectAnalyzed} photoCount={all.length} />
            <GridView photos={visible} folderId={folderId} bursts={bursts} softBelow={softBelow} />
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
      <SubjectScanDialog
        photos={all}
        open={subjectScanOpen}
        onOpenChange={setSubjectScanOpen}
      />
    </>
  );
}
