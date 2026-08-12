import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
import tailwindcss from "@tailwindcss/postcss";

const host = process.env.TAURI_DEV_HOST;

export default defineConfig({
  plugins: [pluginReact()],
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
    host: host || false,
    port: 1420,
    strictPort: true,
  },
  dev: {
    client: host
      ? {
          protocol: "ws",
          host,
          port: 1420,
        }
      : undefined,
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
