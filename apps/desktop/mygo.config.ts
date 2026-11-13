import { defineConfig } from "mygo-cli";

// `mygo dev` runs the development app beside the installed one. Its data,
// port and link scheme come from the isolated identity the Tauri shell used
// (internal/instance), so the developer's data stays where it was; the link
// scheme is the one value the bundle itself declares.
export default defineConfig(({ command }) => ({
  name: "AstrLink",
  slug: "astrlink",
  identifier: "com.astrlink.desktop",
  version: "0.1.0-dev.1",
  icon: "src-tauri/icons/icon.png",

  devUrl: "http://127.0.0.1:1420",
  devCommand: "bun run dev",
  buildCommand: "bun run build",
  frontendDist: "dist",
  bindings: "src/mygo.ts",
  // dist is the frontend's.
  out: "build",

  urlSchemes: [command === "dev" ? "astrlink-isolated" : "astrlink"],
  macos: {
    minimumSystemVersion: "13.4",
  },
}));
