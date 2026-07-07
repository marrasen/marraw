// Types for the Electron preload bridge (electron/preload.cjs). Absent when
// running in a plain browser tab — always feature-check window.marraw.
declare global {
  interface Window {
    marraw?: {
      pickDirectory: () => Promise<string | null>;
      revealInExplorer: (path: string) => void;
      getPathForFile: (file: File) => string;
      isDirectory: (path: string) => Promise<boolean>;
    };
    win?: {
      minimize: () => void;
      toggleMax: () => void;
      close: () => void;
      toggleFullScreen: () => void;
      isMax: () => Promise<boolean>;
      onMaxChange: (cb: (max: boolean) => void) => void;
      onFullScreenChange: (cb: (fs: boolean) => void) => void;
    };
  }
}

export {};
