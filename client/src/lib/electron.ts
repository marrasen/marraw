// Types for the Electron preload bridge (electron/preload.cjs). Absent when
// running in a plain browser tab — always feature-check window.marraw.

// The shell's remote-access preferences (preferences.json). Spawn flags, so a
// change reports restartRequired until the app relaunches.
export interface RemoteAccessPrefs {
  enabled: boolean;
  listen: string;
  port: number;
  restartRequired: boolean;
}

// A saved connection to another machine's library, stored in the shell's
// prefs (not the daemon's settings) so the list is the same in every window.
export interface RemoteConnection {
  id: string;
  name: string;
  /** Always host:port — the shell normalizes a bare host to the default port. */
  host: string;
  token: string;
}

/** One reachability probe of a remote daemon (GET /authz from the shell). */
export type RemoteProbe = { ok: true; version: string } | { ok: false; error: string };

/** The host is now showing `code`, and is waiting for someone to approve. */
export type PairRequestResult =
  | { ok: true; requestId: string; code: string; hostName: string }
  | { ok: false; error: string };

/** How a pairing attempt ended. `token` is present only when approved. */
export interface PairWaitResult {
  status: 'approved' | 'denied' | 'expired' | 'canceled' | 'pending' | 'error';
  token?: string;
  hostName?: string;
  error?: string;
}

/**
 * Where the updater stands right now, pushed from the shell on every change.
 * `available`/`downloading`/`downloaded` all carry the offered `version`;
 * `idle` with a non-zero `checkedAt` means "checked, nothing newer".
 */
export interface UpdateState {
  status: 'idle' | 'checking' | 'available' | 'downloading' | 'downloaded' | 'error';
  version: string;
  releaseNotes: string;
  releaseDate: string;
  /** 0–100 while downloading. */
  percent: number;
  transferred: number;
  total: number;
  bytesPerSecond: number;
  error: string;
  /** epoch ms of the last completed check; 0 = not checked this session. */
  checkedAt: number;
  /** Only present on the reply to getUpdateState, not on pushed updates. */
  supported?: boolean;
  currentVersion?: string;
}

/**
 * Which photo the pop-out viewer should show, pushed by whichever window is
 * driving it. The folder comes along because the viewer subscribes to the
 * folder's photo list itself — it shares no store with the main window.
 */
export interface ViewerPhoto {
  folderId: number;
  photoId: number;
}

/** getViewerPhoto's reply: nulls when nothing has been focused yet. */
export type ViewerPhotoState = { open: boolean } & (ViewerPhoto | { folderId: null; photoId: null });

declare global {
  interface Window {
    marraw?: {
      pickDirectory: () => Promise<string | null>;
      // Absent on builds predating the watermark editor — feature-check.
      pickImage?: () => Promise<string | null>;
      revealInExplorer: (path: string) => void;
      getPathForFile: (file: File) => string;
      isDirectory: (path: string) => Promise<boolean>;
      // Absent on builds predating clipboard export — feature-check.
      copyImageToClipboard?: (buf: ArrayBuffer) => Promise<boolean>;
      // Absent on builds predating the auto-update setting — feature-check.
      updatesSupported?: boolean;
      getAutoUpdate?: () => Promise<boolean>;
      setAutoUpdate?: (on: boolean) => Promise<boolean>;
      // Absent on builds predating the beta-channel setting — feature-check.
      getBetaChannel?: () => Promise<boolean>;
      setBetaChannel?: (on: boolean) => Promise<boolean>;
      // In-app update flow — absent on builds predating it; feature-check.
      getUpdateState?: () => Promise<UpdateState>;
      checkForUpdates?: () => Promise<boolean>;
      downloadUpdate?: () => Promise<boolean>;
      installUpdate?: () => Promise<boolean>;
      /** Subscribes to state pushes; returns its own unsubscribe. */
      onUpdateState?: (cb: (state: UpdateState) => void) => () => void;
      // Remote connections — absent on builds predating them; feature-check.
      listRemotes?: () => Promise<RemoteConnection[]>;
      saveRemote?: (conn: Partial<RemoteConnection>) => Promise<RemoteConnection[]>;
      deleteRemote?: (id: string) => Promise<RemoteConnection[]>;
      testRemote?: (host: string, token: string) => Promise<RemoteProbe>;
      openRemote?: (id: string) => Promise<boolean>;
      openLocal?: () => Promise<boolean>;
      // Pairing — absent on builds predating it; feature-check. Finding
      // machines is System.ScanForHosts on the daemon, not a bridge call.
      pairRemote?: (host: string) => Promise<PairRequestResult>;
      waitRemotePairing?: (host: string, requestId: string) => Promise<PairWaitResult>;
      cancelRemotePairing?: (host: string, requestId: string) => Promise<boolean>;
      getRemoteAccess?: () => Promise<RemoteAccessPrefs>;
      setRemoteAccess?: (patch: Partial<RemoteAccessPrefs>) => Promise<RemoteAccessPrefs>;
      relaunch?: () => Promise<void>;
    };
    win?: {
      minimize: () => void;
      toggleMax: () => void;
      close: () => void;
      toggleFullScreen: () => void;
      isMax: () => Promise<boolean>;
      openNewWindow: (folderPath?: string) => void;
      onMaxChange: (cb: (max: boolean) => void) => void;
      onFullScreenChange: (cb: (fs: boolean) => void) => void;
      // Pop-out photo window — absent on builds predating it; feature-check.
      toggleViewer?: () => void;
      setViewerPhoto?: (state: ViewerPhoto) => void;
      getViewerPhoto?: () => Promise<ViewerPhotoState>;
      /** Subscribes to photo pushes; returns its own unsubscribe. */
      onViewerPhoto?: (cb: (state: ViewerPhoto) => void) => () => void;
    };
  }
}

export {};
