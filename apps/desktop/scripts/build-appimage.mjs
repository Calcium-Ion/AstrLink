// Builds the AppImage of the Linux app after `mygo build`, on x86_64 Debian
// or Ubuntu with WebKitGTK 4.1 and its development files installed:
//
//   bun scripts/build-appimage.mjs   writes build/linux-amd64/AstrLink_<version>_amd64.AppImage
//
// The image holds the app as `mygo build` archives it for install.sh
// (<slug>-<version>-linux-amd64.tar.gz) in usr/lib/astrlink, beside the GTK
// 3 and WebKitGTK 4.1 of the build system and what they link, so that it
// runs where those are not installed, as the AppImages of the releases
// before the MyGo app did. MyGo opens these libraries by name when the app
// starts (no cgo), so linuxdeploy is told which to deploy. Tauri's
// linuxdeploy-plugin-gtk, which built those AppImages, adds GTK's modules
// and schemas with the hook that sets them up, and writes ././ over
// WebKitGTK's /usr paths: ../linux/AppRun starts the app in usr, from where
// they lead to the helper processes copied here. linuxdeploy runs before
// the app is added, so the app's files stay as `mygo build` wrote them.
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  chmodSync,
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  renameSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { downloadWithRetry } from "./download-with-retry.mjs";
import { appImageName } from "./release-updates.mjs";

const desktop = fileURLToPath(new URL("..", import.meta.url));
const platform = "linux-amd64";
const libraryDirectory = "/usr/lib/x86_64-linux-gnu";

/** The tools that build the image, pinned. */
export const tools = [
  {
    name: "linuxdeploy-x86_64.AppImage",
    url: "https://github.com/linuxdeploy/linuxdeploy/releases/download/1-alpha-20251107-1/linuxdeploy-x86_64.AppImage",
    sha256: "c20cd71e3a4e3b80c3483cef793cda3f4e990aca14014d23c544ca3ce1270b4d",
    size: 19_421_688,
  },
  {
    // linuxdeploy runs the plugins it finds beside it.
    name: "linuxdeploy-plugin-gtk.sh",
    url: "https://raw.githubusercontent.com/tauri-apps/linuxdeploy-plugin-gtk/dda522bce37387f1b853d9095713bfaa924c8423/linuxdeploy-plugin-gtk.sh",
    sha256: "7804c9eef13e59bf2783aad9882ef9db8f3f3f9e8d631874b1d348d550a3693f",
    size: 14_622,
  },
  {
    name: "appimagetool-x86_64.AppImage",
    url: "https://github.com/AppImage/appimagetool/releases/download/1.9.1/appimagetool-x86_64.AppImage",
    sha256: "ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0",
    size: 15_092_216,
  },
  {
    // The program that mounts and starts the image, which appimagetool
    // would otherwise download.
    name: "runtime-x86_64",
    url: "https://github.com/AppImage/type2-runtime/releases/download/20251108/runtime-x86_64",
    sha256: "2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d",
    size: 944_632,
  },
];

/**
 * The libraries MyGo opens by name (internal/linux of its module), deployed
 * with those they link, but for what every desktop has, such as libX11 and
 * libGL, which linuxdeploy's exclude list leaves to the system.
 */
export const libraries = [
  "libglib-2.0.so.0",
  "libgobject-2.0.so.0",
  "libgio-2.0.so.0",
  "libgtk-3.so.0",
  "libgdk-3.so.0",
  "libgdk_pixbuf-2.0.so.0",
  "libcairo.so.2",
  "libpango-1.0.so.0",
  "libatk-1.0.so.0",
  "libepoxy.so.0",
  "libwebkit2gtk-4.1.so.0",
  "libjavascriptcoregtk-4.1.so.0",
  "libsoup-3.0.so.0",
  "libsecret-1.so.0",
  // The tray icon.
  "libayatana-appindicator3.so.1",
  "libdbusmenu-glib.so.4",
  "libdbusmenu-gtk3.so.4",
];

/** WebKitGTK's helper processes, at the path it starts them from. */
export const webKitDirectory = "usr/lib/x86_64-linux-gnu/webkit2gtk-4.1";
export const webKitHelpers = [
  "WebKitNetworkProcess",
  "WebKitWebProcess",
  "injected-bundle/libwebkit2gtkinjectedbundle.so",
];

const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
function requireValue(value, message) {
  if (!value) throw new Error(message);
  return value;
}

/** Downloads the tools into directory, once, and returns their paths. */
export async function fetchTools(directory, download = downloadWithRetry) {
  mkdirSync(directory, { recursive: true });
  const paths = {};
  for (const tool of tools) {
    const file = path.join(directory, tool.name);
    const cached = existsSync(file) && readFileSync(file);
    if (
      !cached ||
      cached.length !== tool.size ||
      sha256(cached) !== tool.sha256
    ) {
      const bytes = await download(tool.url, tool.name);
      requireValue(
        bytes.length === tool.size && sha256(bytes) === tool.sha256,
        `${tool.name} failed integrity verification`,
      );
      writeFileSync(`${file}.download`, bytes);
      renameSync(`${file}.download`, file);
    }
    chmodSync(file, 0o755);
    paths[tool.name] = file;
  }
  return paths;
}

/**
 * Adds the app of archive to appDir, in usr/lib/astrlink, with the AppRun
 * and, at the root, the desktop entry and icon `mygo build` wrote for it.
 */
export function addApp(appDir, archive, root = desktop) {
  const app = path.join(appDir, "usr/lib/astrlink");
  mkdirSync(app, { recursive: true });
  execFileSync("tar", ["-xzf", archive, "-C", app]);
  requireValue(
    existsSync(path.join(app, "astrlink")),
    `${path.basename(archive)} holds no astrlink executable`,
  );
  copyFileSync(path.join(root, "linux/AppRun"), path.join(appDir, "AppRun"));
  chmodSync(path.join(appDir, "AppRun"), 0o755);
  for (const name of ["astrlink.desktop", "astrlink.png"])
    copyFileSync(path.join(app, name), path.join(appDir, name));
  symlinkSync("astrlink.png", path.join(appDir, ".DirIcon"));
}

export async function buildAppImage(root = desktop) {
  requireValue(
    process.platform === "linux" && process.arch === "x64",
    "The AppImage is built on x86_64 Linux",
  );
  const { version } = JSON.parse(
    readFileSync(path.join(root, "package.json"), "utf8"),
  );
  const build = path.join(root, "build", platform);
  const archive = path.join(build, `astrlink-${version}-${platform}.tar.gz`);
  requireValue(
    existsSync(archive),
    `Missing ${path.basename(archive)}: run mygo build first`,
  );
  const work = path.join(root, "build", "appimage");
  const appDir = path.join(work, "AstrLink.AppDir");
  rmSync(appDir, { recursive: true, force: true });
  mkdirSync(appDir, { recursive: true });
  const toolDirectory = path.join(work, "tools");
  const tool = await fetchTools(toolDirectory);
  const env = {
    ...process.env,
    // The tools are AppImages too, which then need no FUSE.
    APPIMAGE_EXTRACT_AND_RUN: "1",
    ARCH: "x86_64",
    // The system's libraries come stripped.
    NO_STRIP: "1",
    PATH: `${toolDirectory}:${process.env.PATH}`,
  };

  for (const helper of webKitHelpers) {
    const source = path.join("/", webKitDirectory, helper);
    requireValue(existsSync(source), `Missing ${source}`);
    const target = path.join(appDir, webKitDirectory, helper);
    mkdirSync(path.dirname(target), { recursive: true });
    copyFileSync(source, target);
    chmodSync(target, statSync(source).mode);
  }
  const deployed = libraries.map((name) => {
    const file = path.join(libraryDirectory, name);
    requireValue(existsSync(file), `Missing ${file}`);
    return `--library=${file}`;
  });
  execFileSync(
    tool["linuxdeploy-x86_64.AppImage"],
    ["--appdir", appDir, ...deployed, "--plugin", "gtk"],
    { env, stdio: "inherit" },
  );

  addApp(appDir, archive, root);
  const output = path.join(build, appImageName(platform, version));
  rmSync(output, { force: true });
  execFileSync(
    tool["appimagetool-x86_64.AppImage"],
    [
      "--no-appstream",
      "--runtime-file",
      tool["runtime-x86_64"],
      appDir,
      output,
    ],
    { env, stdio: "inherit" },
  );
  const header = readFileSync(output).subarray(0, 11);
  requireValue(
    header.subarray(0, 4).equals(Buffer.from("\x7fELF", "latin1")) &&
      header.subarray(8, 11).equals(Buffer.from("AI\x02", "latin1")),
    `${path.basename(output)} is not an AppImage`,
  );
  console.log(`Built ${path.relative(root, output)}`);
  return output;
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    await buildAppImage();
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
