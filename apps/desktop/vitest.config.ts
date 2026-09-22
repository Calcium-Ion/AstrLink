import { fileURLToPath } from "node:url";

import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  test: {
    setupFiles: ["./src/i18n/test-setup.ts"],
    environment: "node",
    // Package workflows also run this suite on hosted macOS Intel runners,
    // where the heaviest React suites exceed vitest's 5 s default budget.
    testTimeout: 30_000,
    hookTimeout: 30_000,
    server: {
      deps: {
        // @lobehub/ui reaches emoji-mart's data through a bare JSON import.
        // Node refuses that without an import attribute, which fails every
        // suite that renders a brand icon. Let Vite transform it instead.
        inline: [/@lobehub[\\/]ui/],
      },
    },
    // The desktop test suite is small and several files exercise the same
    // process-level Tauri/browser shims. Keeping one worker makes `bun run
    // check` deterministic in constrained CI and local sandboxes.
    maxWorkers: 1,
  },
});
