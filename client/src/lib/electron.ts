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
      // Remote connections — absent on builds predating them; feature-check.
      listRemotes?: () => Promise<RemoteConnection[]>;
      saveRemote?: (conn: Partial<RemoteConnection>) => Promise<RemoteConnection[]>;
      deleteRemote?: (id: string) => Promise<RemoteConnection[]>;
      testRemote?: (host: string, token: string) => Promise<RemoteProbe>;
      openRemote?: (id: string) => Promise<boolean>;
      openLocal?: () => Promise<boolean>;
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
    };
  }
}

export {};
