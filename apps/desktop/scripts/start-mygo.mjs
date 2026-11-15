// Builds the AstrLink app for this machine with `mygo build` and runs it:
// the installed identity, the production frontend, no file watching.
import { execFileSync, spawn } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { patchedMygo } from "./mygo.mjs";

const desktopDirectory = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);
const goos = { darwin: "darwin", linux: "linux", win32: "windows" }[
  process.platform
];
const goarch = { arm64: "arm64", x64: "amd64" }[process.arch];
if (!goos || !goarch) {
  throw new Error(`Unsupported platform ${process.platform}/${process.arch}.`);
}

const mygo = patchedMygo();
execFileSync(
  mygo,
  ["build", "-platform", `${goos}/${goarch}`, "-skip-dmg", "-skip-installer"],
  { cwd: desktopDirectory, stdio: "inherit" },
);

const output = path.join(desktopDirectory, "build", `${goos}-${goarch}`);
const executable = {
  darwin: path.join(output, "AstrLink.app", "Contents", "MacOS", "AstrLink"),
  linux: path.join(output, "astrlink"),
  windows: path.join(output, "AstrLink.exe"),
}[goos];
const app = spawn(executable, process.argv.slice(2), { stdio: "inherit" });
app.on("exit", (code, signal) => {
  process.exitCode = signal ? 1 : (code ?? 0);
});
