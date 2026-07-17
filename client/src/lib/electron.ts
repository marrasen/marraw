// Types for the Electron preload bridge (electron/preload.cjs). Absent when
// running in a plain browser tab — always feature-check window.marraw.
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
