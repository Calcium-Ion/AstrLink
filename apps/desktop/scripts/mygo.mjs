// Runs the mygo CLI that packages AstrLink: MyGo's release, with the patches
// in ../patches applied. The first run fetches the module from the Go module
// proxy (`go mod download`, checked against the checksum database), applies
// the patches with `git apply` and builds cmd/mygo into the user cache; later
// runs reuse that binary until the version or a patch changes.
//
//   bun scripts/mygo.mjs build -platform darwin/arm64   run the patched CLI
//   bun scripts/mygo.mjs --print-path                   build it, print its path
//
// ASTRLINK_MYGO_CACHE moves the cache directory. With MYGO_UPDATER_PUBLIC_KEY,
// the app's own updater (internal/updates) is linked with the key too.
import { execFileSync, spawn } from "node:child_process";
import { createHash } from "node:crypto";
import {
  chmodSync,
  cpSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  renameSync,
  rmSync,
  statSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const desktop = fileURLToPath(new URL("../", import.meta.url));
export const mygoModule = "github.com/egoist/mygo";
export const mygoVersion = "v0.9.0";
const patchDirectory = path.join(desktop, "patches");

/** The patches for mygoVersion, in the order they apply. */
export function patchFiles(directory = patchDirectory) {
  return readdirSync(directory)
    .filter(
      (name) =>
        name.startsWith(`mygo-${mygoVersion}-`) && name.endsWith(".patch"),
    )
    .sort()
    .map((name) => path.join(directory, name));
}

/** Names the build of mygoVersion with these patches for this platform. */
export function cacheKey(
  patches,
  platform = process.platform,
  arch = process.arch,
) {
  const hash = createHash("sha256").update(mygoVersion);
  for (const patch of patches)
    hash.update(path.basename(patch)).update("\0").update(readFileSync(patch));
  return `${mygoVersion}-${hash.digest("hex").slice(0, 16)}-${platform}-${arch}`;
}

/** The user cache directory, like Go's os.UserCacheDir. */
export function cacheDirectory(env = process.env, platform = process.platform) {
  if (env.ASTRLINK_MYGO_CACHE) return path.resolve(env.ASTRLINK_MYGO_CACHE);
  const home = os.homedir();
  const base =
    platform === "win32"
      ? env.LOCALAPPDATA || path.join(home, "AppData", "Local")
      : platform === "darwin"
        ? path.join(home, "Library", "Caches")
        : env.XDG_CACHE_HOME || path.join(home, ".cache");
  return path.join(base, "astrlink", "mygo-cli");
}

const publicKeyFlag =
  "github.com/QuantumNous/astrlink/desktop/internal/updates.publicKey";

/**
 * The environment of a mygo run: MYGO_LDFLAGS links updates.publicKey of
 * mygo.config.ts into internal/updates, beside MyGo's own copy.
 */
export function mygoEnvironment(env = process.env, binary = "") {
  const next = { ...env };
  if (binary) next.MYGO_CLI_BINARY = binary;
  const key = env.MYGO_UPDATER_PUBLIC_KEY?.trim();
  if (key) {
    if (!/^[A-Za-z0-9+/]+=*$/.test(key))
      throw new Error("MYGO_UPDATER_PUBLIC_KEY is not a base64 key.");
    next.MYGO_LDFLAGS = [env.MYGO_LDFLAGS?.trim(), `-X ${publicKeyFlag}=${key}`]
      .filter(Boolean)
      .join(" ");
  }
  return next;
}

function makeWritable(directory) {
  chmodSync(directory, 0o755);
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const file = path.join(directory, entry.name);
    if (entry.isDirectory()) makeWritable(file);
    else if (entry.isFile()) chmodSync(file, statSync(file).mode | 0o200);
  }
}

/** Builds the patched CLI unless the cache holds it, and returns its path. */
export function patchedMygo({ log = console.error } = {}) {
  const patches = patchFiles();
  const root = path.join(cacheDirectory(), cacheKey(patches));
  const binary = path.join(
    root,
    process.platform === "win32" ? "mygo.exe" : "mygo",
  );
  if (statSync(binary, { throwIfNoEntry: false })?.isFile()) return binary;

  mkdirSync(path.dirname(root), { recursive: true });
  const work = mkdtempSync(path.join(path.dirname(root), ".build-"));
  // The module proxy and the checksum database, outside any module, so no
  // go.mod or go.work of the repository is touched.
  const env = { ...process.env, GOWORK: "off", GOFLAGS: "-mod=mod" };
  try {
    log(`Fetching ${mygoModule}@${mygoVersion} for the patched mygo CLI.`);
    const { Dir: source } = JSON.parse(
      execFileSync(
        "go",
        ["mod", "download", "-json", `${mygoModule}@${mygoVersion}`],
        { cwd: work, env, encoding: "utf8" },
      ),
    );
    if (!source || !existsSync(source))
      throw new Error(`go mod download did not provide ${mygoModule}.`);
    const tree = path.join(work, "mygo");
    cpSync(source, tree, { recursive: true });
    // The module cache is read-only.
    makeWritable(tree);
    // Its own repository, so git apply resolves paths from the module root
    // wherever the cache lives.
    execFileSync("git", ["init", "--quiet"], { cwd: tree });
    for (const patch of patches) {
      log(`Applying ${path.basename(patch)}.`);
      execFileSync("git", ["apply", "--whitespace=nowarn", patch], {
        cwd: tree,
        stdio: ["ignore", "inherit", "inherit"],
      });
    }
    // Beside the source tree, which is named mygo too.
    const built = path.join(work, "bin", path.basename(binary));
    mkdirSync(path.dirname(built));
    execFileSync("go", ["build", "-trimpath", "-o", built, "./cmd/mygo"], {
      cwd: tree,
      env,
      stdio: ["ignore", "inherit", "inherit"],
    });
    // An earlier run may have left something else at the binary's path.
    rmSync(root, { recursive: true, force: true });
    mkdirSync(root, { recursive: true });
    renameSync(built, binary);
    log(`Built the patched mygo CLI: ${binary}`);
    return binary;
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const args = process.argv.slice(2);
  let binary, env;
  try {
    binary = patchedMygo();
    env = mygoEnvironment(process.env, binary);
  } catch (error) {
    console.error(error.message);
    process.exit(1);
  }
  if (args[0] === "--print-path") {
    console.log(binary);
  } else {
    const child = spawn(binary, args, {
      cwd: desktop,
      stdio: "inherit",
      // Scripts that run mygo-cli's `mygo` reach the same binary.
      env,
    });
    process.on("SIGINT", () => {});
    process.on("SIGTERM", () => child.kill("SIGTERM"));
    child.on("exit", (code, signal) => {
      process.exitCode = signal ? 1 : (code ?? 1);
    });
  }
}
