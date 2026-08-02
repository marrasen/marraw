import { useEffect } from 'react';
import { create } from 'zustand';
import type { UpdateState } from '@/lib/electron';

// The updater lives in the Electron shell, not the daemon: the launch check
// runs before marrawd is up, and an update outlives any one library. The shell
// pushes a whole state object on every phase change, which is what makes an
// update something the user can look up at any time rather than a notification
// they had to be watching for.

const IDLE: UpdateState = {
  status: 'idle',
  version: '',
  releaseNotes: '',
  releaseDate: '',
  percent: 0,
  transferred: 0,
  total: 0,
  bytesPerSecond: 0,
  error: '',
  checkedAt: 0,
};

interface Store {
  state: UpdateState;
  /** The running version, from the shell (app.getVersion()); '' until read. */
  currentVersion: string;
  /** State has been read at least once — tells "up to date" from "not yet". */
  loaded: boolean;
}

export const useUpdateStore = create<Store>(() => ({
  state: IDLE,
  currentVersion: '',
  loaded: false,
}));

/**
 * Updating needs the Electron shell, and a packaging that can replace itself:
 * Windows and the Linux AppImage. A .deb is the package manager's to update
 * and an unsigned macOS bundle can't update at all, so the UI stays hidden
 * there rather than offering a button that can only fail.
 */
export const updatesSupported = (): boolean =>
  !!window.marraw?.getUpdateState && !!window.marraw.updatesSupported;

export function checkForUpdates(): void {
  void window.marraw?.checkForUpdates?.();
}

export function downloadUpdate(): void {
  void window.marraw?.downloadUpdate?.();
}

/** Quits and relaunches into the downloaded version. Nothing returns. */
export function installUpdate(): void {
  void window.marraw?.installUpdate?.();
}

// One subscription and one initial read shared by every consumer (the rail
// badge and the Settings pane are both mounted most of the time).
let consumers = 0;
let unsubscribe: (() => void) | null = null;

/** Subscribes to updater state for as long as the caller is mounted. */
export function useUpdates(): Store {
  useEffect(() => {
    if (!updatesSupported()) return;
    consumers++;
    if (consumers === 1) {
      unsubscribe =
        window.marraw?.onUpdateState?.((state) => useUpdateStore.setState({ state })) ?? null;
      void window.marraw?.getUpdateState?.().then(({ supported, currentVersion, ...state }) => {
        void supported; // constant per install; updatesSupported() already covers it
        useUpdateStore.setState({ state, currentVersion: currentVersion ?? '', loaded: true });
      });
    }
    return () => {
      consumers--;
      if (consumers === 0) {
        unsubscribe?.();
        unsubscribe = null;
      }
    };
  }, []);
  return useUpdateStore();
}

/** One line of status, worded the same in the rail and in Settings. */
export function updateStatusText(s: UpdateState): string {
  switch (s.status) {
    case 'checking':
      return 'Checking for updates…';
    case 'available':
      return `Version ${s.version} is available`;
    case 'downloading':
      return `Downloading ${s.version}… ${Math.round(s.percent)}%`;
    case 'downloaded':
      return `Version ${s.version} is ready to install`;
    case 'error':
      return s.error || 'Update check failed';
    default:
      return s.checkedAt ? 'marraw is up to date' : 'Not checked yet';
  }
}
