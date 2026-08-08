import path from "path"
import { defineConfig } from "vitest/config"

// Separate from vite.config.ts on purpose: the app config carries the React and
// Tailwind plugins, and nothing under test needs either. What is tested here is
// the client's pure logic — functions over data — so the default environment is
// node and a test that wants a DOM asks for one per-file with
// `// @vitest-environment jsdom`.
export default defineConfig({
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
})
