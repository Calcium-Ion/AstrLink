import { fileURLToPath } from "node:url";

import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  test: {
    environment: "node",
    // The desktop test suite is small and several files exercise the same
    // process-level Tauri/browser shims. Keeping one worker makes `bun run
    // check` deterministic in constrained CI and local sandboxes.
    maxWorkers: 1,
  },
});
