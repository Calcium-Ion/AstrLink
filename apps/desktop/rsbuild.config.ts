import { defineConfig, type RsbuildPlugin } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
import tailwindcss from "@tailwindcss/postcss";

const host = process.env.TAURI_DEV_HOST || "127.0.0.1";
let buildGeneration = 0;

const tauriDevReloadPlugin: RsbuildPlugin = {
  name: "astrlink-tauri-dev-reload",
  setup(api) {
    api.onAfterDevCompile(({ isFirstCompile, stats }) => {
      if (isFirstCompile) return;
      // Failed compiles report here too. Bumping the generation on a failure
      // reloads the webview onto a broken bundle, and the poller that drives
      // this reload lives inside that bundle -- so one syntax error would
      // silently kill auto-reload until the dev server is restarted by hand.
      if (stats.hasErrors()) return;
      buildGeneration += 1;
    });
  },
};

export default defineConfig({
  plugins: [pluginReact(), tauriDevReloadPlugin],
  source: {
    entry: {
      index: "./src/main.tsx",
    },
  },
  html: {
    template: "./index.html",
    favicon: "./src/assets/astrlink-logo.svg",
  },
  output: {
    distPath: {
      root: "dist",
    },
  },
  server: {
    host,
    port: 1420,
    strictPort: true,
    headers: {
      "Cache-Control": "no-store",
    },
  },
  dev: {
    // WKWebView Fast Refresh often applies without repainting. Live reload
    // plus the /__astrlink_build poller in the Tauri window keep the UI current.
    hmr: false,
    liveReload: true,
    client: {
      protocol: "ws",
      host,
      port: 1420,
    },
    setupMiddlewares: [
      (middlewares) => {
        middlewares.unshift((req, res, next) => {
          if (req.url?.split("?")[0] !== "/__astrlink_build") {
            next();
            return;
          }
          res.statusCode = 200;
          res.setHeader("Content-Type", "text/plain; charset=utf-8");
          res.setHeader("Cache-Control", "no-store");
          res.end(String(buildGeneration));
        });
      },
    ],
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
