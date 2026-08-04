import { createRequire } from 'node:module';
import path from 'path';
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

const { version } = createRequire(import.meta.url)('../package.json');

// The share page: a separate bundle from the desktop app, embedded into the
// daemon and served to a visitor's browser (internal/guestui).
//
// Separate rather than a second entry in the main config, because the two
// builds want opposite things. This one is loaded over HTTP from a URL, so it
// wants relative asset paths under /s/<token>/ and its own output directory;
// the app is loaded from disk by Electron. Keeping them apart also means the
// desktop bundle never ships guest code, and the guest bundle never ships the
// develop pipeline.
export default defineConfig({
  // Relative: the page lives at /s/<token>/, and its assets alongside it.
  base: './',
  define: { __APP_VERSION__: JSON.stringify(version) },
  plugins: [react(), tailwindcss()],
  build: {
    outDir: 'dist-guest',
    emptyOutDir: true,
    rollupOptions: { input: path.resolve(__dirname, 'guest.html') },
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
});
