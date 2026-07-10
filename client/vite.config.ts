import { createRequire } from "node:module"
import path from "path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

// The root manifest is the one electron-builder ships; client/package.json is an
// unused Vite-starter leftover with its own stale version.
const { version } = createRequire(import.meta.url)("../package.json")

// https://vite.dev/config/
export default defineConfig({
  // Relative asset paths: the Electron shell loads dist/index.html via file://.
  base: './',
  define: { __APP_VERSION__: JSON.stringify(version) },
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
})
