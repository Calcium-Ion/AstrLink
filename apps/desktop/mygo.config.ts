import { existsSync, readFileSync } from "node:fs";
import { defineConfig } from "mygo-cli";

// Release jobs stamp the tag into package.json (release-updates.mjs stamp).
const { version } = JSON.parse(
  readFileSync(new URL("./package.json", import.meta.url), "utf8"),
);

// The macOS desktop widget, which scripts/build-macos-widget.mjs builds and
// signs before `mygo build` ships it in Contents/PlugIns (macos.plugIns, a
// patched MyGo: see patches/ and scripts/mygo.mjs).
const widget = "build/macos-widget/AstrLinkWidgets.appex";

// `mygo dev` runs the development app beside the installed one. Its data,
// port and link scheme come from the isolated identity the earlier desktop
// shell used (internal/instance), so the developer's data stays where it
// was; the link scheme, icon and App Group are what the bundle declares.
export default defineConfig(({ command }) => {
  const dev = command === "dev";
  // Release builds get the public key of MYGO_UPDATER_PRIVATE_KEY; without
  // it the app reports updates as unconfigured and links to the releases.
  const publicKey = process.env.MYGO_UPDATER_PUBLIC_KEY?.trim();
  return {
    name: "AstrLink",
    slug: "astrlink",
    identifier: "com.astrlink.desktop",
    version,
    icon: dev ? "icons/dev/icon.png" : "icons/icon.png",

    devUrl: "http://127.0.0.1:1420",
    devCommand: "bun run dev",
    buildCommand: "bun run build",
    frontendDist: "dist",
    bindings: "src/mygo.ts",
    // dist is the frontend's.
    out: "build",

    urlSchemes: [dev ? "astrlink-isolated" : "astrlink"],
    macos: {
      icon: dev ? "icons/dev/icon.icns" : "icons/icon.icns",
      minimumSystemVersion: "13.4",
      // The App Group the widget shares; the development bundle names its
      // own, so it never feeds the release widget.
      entitlements: dev
        ? "macos/Entitlements.dev.plist"
        : "macos/Entitlements.plist",
      signingIdentity: process.env.APPLE_SIGNING_IDENTITY?.trim() || undefined,
      infoPlist: {
        // Shown the first time AstrLink asks Terminal or iTerm2 to open a
        // window for an API provider.
        NSAppleEventsUsageDescription:
          "AstrLink opens a Terminal or iTerm2 window for the API provider you choose.",
      },
      plugIns: !dev && existsSync(widget) ? [{ path: widget }] : undefined,
      // The disk image's window: the picture, with an arrow from the app
      // to Applications (patched MyGo, see patches/).
      dmg: {
        background: "installer/macos-dmg-background.tiff",
        width: 660,
        height: 432,
        appX: 170,
        appY: 190,
        applicationsX: 490,
        applicationsY: 190,
      },
      // Notarization runs after the build: scripts/notarize-macos.mjs
      // submits the disk image, staples it and the app, and checks every
      // signature first.
    },
    windows: {
      // The Visual C++ runtime check, and the removal of an install of an
      // earlier release.
      installerInclude: "installer/windows.nsh",
    },
    linux: {
      formats: ["deb"],
      depends: {
        deb: [
          "libayatana-appindicator3-1",
          "libc6 (>= 2.36)",
          "libstdc++6 (>= 12)",
          "libgcc-s1",
        ],
      },
    },
    updates: publicKey
      ? { publicKey, github: "Calcium-Ion/AstrLink" }
      : undefined,
  };
});
