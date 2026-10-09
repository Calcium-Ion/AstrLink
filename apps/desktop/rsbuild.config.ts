import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
import tailwindcss from "@tailwindcss/postcss";

import {
  BuildGeneration,
  createBuildGenerationMiddleware,
  createTauriDevReloadPlugin,
} from "./dev-build-generation";

const host = process.env.TAURI_DEV_HOST || "127.0.0.1";
const buildGeneration = new BuildGeneration();

export default defineConfig({
  plugins: [pluginReact(), createTauriDevReloadPlugin(buildGeneration)],
  source: {
    define: {
      "process.env.PUBLIC_ASTRLINK_EDITION": JSON.stringify(
        process.env.PUBLIC_ASTRLINK_EDITION === "web" ? "web" : "desktop",
      ),
    },
    entry: {
      index: "./src/main.tsx",
    },
  },
  html: {
    template: "./index.html",
    favicon: "./src/assets/astrlink-logo.svg",
  },
  output: {
    ...(process.env.PUBLIC_ASTRLINK_EDITION === "web"
      ? { dataUriLimit: 0 }
      : {}),
    distPath: {
      root: process.env.PUBLIC_ASTRLINK_EDITION === "web" ? "dist-web" : "dist",
    },
  },
  server: {
    host,
    port: 1420,
    strictPort: true,
    headers: {
      "Cache-Control": "no-store",
    },
    setup: ({ server }) => {
      server.middlewares.use(createBuildGenerationMiddleware(buildGeneration));
    },
  },
  dev: {
    // WKWebView Fast Refresh often applies without repainting. The debug Rust
    // host polls /__astrlink_build and calls webview.reload() instead.
    hmr: false,
    liveReload: false,
    client: {
      protocol: "ws",
      host,
      port: 1420,
    },
  },
  tools: {
    postcss: (_config, { addPlugins }) => {
      addPlugins(tailwindcss());
    },
    rspack: {
      watchOptions: {
        ignored: ["**/src-tauri/**"],
      },
    },
  },
});
