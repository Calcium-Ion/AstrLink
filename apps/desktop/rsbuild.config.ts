import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
import tailwindcss from "@tailwindcss/postcss";

import {
  BuildGeneration,
  createBuildGenerationMiddleware,
  createDevReloadPlugin,
} from "./dev-build-generation";

// devUrl in mygo.config.ts.
const host = "127.0.0.1";
const buildGeneration = new BuildGeneration();

export default defineConfig(({ env }) => ({
  plugins: [pluginReact(), createDevReloadPlugin(buildGeneration)],
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
    favicon:
      env === "development"
        ? "./src/assets/astrlink-dev-logo.svg"
        : "./src/assets/astrlink-logo.svg",
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
    // WKWebView Fast Refresh often applies without repainting. The debug host
    // (internal/devreload) polls /__astrlink_build and reloads the pages
    // instead.
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
        // `mygo dev` and `mygo build` output.
        ignored: ["**/build/**", "**/.mygo/**"],
      },
    },
  },
}));
