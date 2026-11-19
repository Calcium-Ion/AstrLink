import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  chmodSync,
  copyFileSync,
  existsSync,
  lstatSync,
  mkdirSync,
  readFileSync,
  realpathSync,
  renameSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import {
  fetchOnnxRuntimePackage,
  readZipEntry,
  withUnixLineEndings,
} from "./onnx-runtime-package.mjs";
import { stageWindowsVcRuntime } from "./stage-windows-vc-runtime.mjs";

const onnxRuntimeVersion = "1.23.2";
// Core ML models load their own ONNX Runtime: before 1.24.1 the Core ML
// provider refused every FP16 node on macOS. CPU models keep 1.23.2 so their
// results stay byte-identical. Microsoft publishes 1.24 for macOS on Apple
// silicon only, and Core ML variants are offered there only, so Intel Macs
// ship 1.23.2 alone.
const coreMLRuntimeVersion = "1.24.4";
// Windows workers link the ONNX Runtime build that ort downloads with
// DirectML, not a pinned Microsoft archive, so they ship that build's notices.
// ort-sys 2.0.0-rc.15 downloads ONNX Runtime 1.31.0; another ort-sys version
// fails the Windows build until both values are updated together.
const windowsRuntimeVersion = "1.31.0";
const windowsRuntimeOrtSysVersion = "2.0.0-rc.15";
const macOSRuntimeLibraryName = `libonnxruntime.${onnxRuntimeVersion}.dylib`;
const coreMLRuntimeLibraryName = `libonnxruntime.${coreMLRuntimeVersion}.dylib`;
const linuxRuntimeLibraryName = `libonnxruntime.so.${onnxRuntimeVersion}`;
const onnxRuntimeLicense = {
  source: "LICENSE",
  name: "LICENSE.txt",
  sha256: "2f07c72751aed99790b8a4869cf2311df85a860b22ded05fa22803587a48922c",
  size: 1_073,
};
const onnxRuntimeNotices = {
  [onnxRuntimeVersion]: [
    onnxRuntimeLicense,
    {
      source: "ThirdPartyNotices.txt",
      name: "ThirdPartyNotices.txt",
      sha256:
        "e9e90971a8e75a9a8ac0c6412e29c1202d079998389915aa485f46c816c3b4cc",
      size: 326_866,
    },
  ],
  [coreMLRuntimeVersion]: [
    onnxRuntimeLicense,
    {
      source: "ThirdPartyNotices.txt",
      name: "ThirdPartyNotices.txt",
      sha256:
        "0e07b95f3a8d6230037707c5c4a2b554d12c4cb67369669ac255635528ffcee2",
      size: 325_054,
    },
  ],
  [windowsRuntimeVersion]: [
    onnxRuntimeLicense,
    {
      source: "ThirdPartyNotices.txt",
      name: "ThirdPartyNotices.txt",
      sha256:
        "143764b952fdb1a7c69ce653bfba74a7744d6a8a573bfb73e235fba356c83de3",
      size: 338_088,
    },
  ],
};
// Microsoft.ML.OnnxRuntime on NuGet, one package per runtime version. Windows
// takes only the notices from its package.
const onnxRuntimePackages = {
  [onnxRuntimeVersion]: {
    sha256: "25fe172f7fcdf34f2b5b02c8b997f6b88abc282956849d4af4335af23d0c4e4a",
    size: 123_371_574,
  },
  [coreMLRuntimeVersion]: {
    sha256: "4b978d5065b85e7004b6c6f60ca494bd978fbe6836cbf0a0b52d82b61ab99638",
    size: 125_194_303,
  },
  [windowsRuntimeVersion]: {
    sha256: "7e35fdb4f5adb62f01e60798e2e5ed05108f5fbbe1cca03fbcc37903cf7c5e3b",
    size: 159_626_085,
  },
};
// Each target's library inside the package.
const macOSRuntimeAssets = {
  "aarch64-apple-darwin": "runtimes/osx-arm64/native/libonnxruntime.dylib",
  "x86_64-apple-darwin": "runtimes/osx-x64/native/libonnxruntime.dylib",
};
const coreMLRuntimeAssets = {
  "aarch64-apple-darwin": "runtimes/osx-arm64/native/libonnxruntime.dylib",
};
const linuxRuntimeAssets = {
  "x86_64-unknown-linux-gnu": "runtimes/linux-x64/native/libonnxruntime.so",
};

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
const desktopDirectory = path.resolve(scriptDirectory, "..");
const repositoryRoot = path.resolve(desktopDirectory, "../..");
const coreDirectory = path.join(repositoryRoot, "core");
const workerDirectory = path.join(repositoryRoot, "apps", "privacy-worker");
const classifierWorkerDirectory = path.join(
  repositoryRoot,
  "apps",
  "classifier-worker",
);

const rustVersion = execFileSync("rustc", ["-vV"], { encoding: "utf8" });
const target = rustVersion.match(/^host:\s+(.+)$/m)?.[1]?.trim();

if (!target) {
  throw new Error("Unable to determine the Rust host target for the workers.");
}

const executableSuffix = target.includes("windows") ? ".exe" : "";
let reuseWindowsWorkers = process.env.ASTRLINK_REUSE_WINDOWS_WORKERS === "1";
if (reuseWindowsWorkers && target !== "x86_64-pc-windows-msvc") {
  throw new Error("Prebuilt worker reuse is only supported for Windows x64.");
}
if (reuseWindowsWorkers) {
  // CI enables this only after an exact source/toolchain cache hit. A cache
  // that still holds DirectML.dll as a symlink (see
  // materializeWindowsWorkerRuntime) is unusable on a fresh runner, so fall
  // back to a full build rather than staging a broken worker.
  const gaps = windowsWorkerCacheGaps();
  if (gaps.length > 0) {
    console.warn(
      `Incomplete Windows worker cache, rebuilding workers:\n  ${gaps.join("\n  ")}`,
    );
    reuseWindowsWorkers = false;
  }
}
// Intermediate outputs: the programs under their target names, the pinned
// ONNX Runtime and the Windows VC++ runtime the installer embeds
// (installer/windows.nsh).
const binariesDirectory = path.join(desktopDirectory, "build", "staging");
// The MyGo app ships resources/<os>-<arch> merged into its resource
// directory: the programs under bin/, the Linux ONNX Runtime under
// onnxruntime/ and the license notices under notices/. Each program keeps
// its plain name, so Core finds its workers beside it.
const mygoPlatform = {
  "aarch64-apple-darwin": "darwin-arm64",
  "x86_64-apple-darwin": "darwin-amd64",
  "x86_64-unknown-linux-gnu": "linux-amd64",
  "aarch64-unknown-linux-gnu": "linux-arm64",
  "x86_64-pc-windows-msvc": "windows-amd64",
  "aarch64-pc-windows-msvc": "windows-arm64",
}[target];
if (!mygoPlatform) {
  throw new Error(`No MyGo platform directory for the Rust target ${target}.`);
}
const resourcesDirectory = path.join(
  desktopDirectory,
  "resources",
  mygoPlatform,
);
const resourcesBinDirectory = path.join(resourcesDirectory, "bin");
const output = path.join(
  binariesDirectory,
  `astrlink-core-${target}${executableSuffix}`,
);
const workerOutput = path.join(
  binariesDirectory,
  `astrlink-privacy-worker-${target}${executableSuffix}`,
);
const classifierWorkerOutput = path.join(
  binariesDirectory,
  `astrlink-classifier-worker-${target}${executableSuffix}`,
);
const cliOutput = path.join(
  binariesDirectory,
  `astrlink-cli-${target}${executableSuffix}`,
);
mkdirSync(binariesDirectory, { recursive: true });
mkdirSync(resourcesBinDirectory, { recursive: true });

// stageResource copies a staged file into the MyGo platform resources.
function stageResource(source, relativePath) {
  const destination = path.join(resourcesDirectory, relativePath);
  mkdirSync(path.dirname(destination), { recursive: true });
  copyFileSync(source, destination);
  if (relativePath.startsWith("bin/") && !target.includes("windows")) {
    chmodSync(destination, 0o755);
  }
}

// stageProgram copies a built program into bin/ under its plain name.
function stageProgram(sidecar, name) {
  stageResource(sidecar, `bin/${name}${executableSuffix}`);
  console.log(`Staged ${name} for MyGo: ${resourcesBinDirectory}`);
}

// onnxRuntimePackage returns the pinned NuGet package of a runtime version,
// kept under the privacy worker's target directory between builds.
function onnxRuntimePackage(version) {
  return fetchOnnxRuntimePackage(
    version,
    onnxRuntimePackages[version],
    path.join(workerDirectory, "target", "onnxruntime", version),
  );
}

// extractOnnxRuntimeLibrary writes this target's library from the package
// under libraryName, the name the workers load, and returns its path.
async function extractOnnxRuntimeLibrary(version, entry, libraryName) {
  const archive = readFileSync(await onnxRuntimePackage(version));
  const directory = path.join(
    workerDirectory,
    "target",
    "onnxruntime",
    version,
    target,
  );
  rmSync(directory, { recursive: true, force: true });
  mkdirSync(directory, { recursive: true });
  const runtimeSource = path.join(directory, libraryName);
  writeFileSync(runtimeSource, readZipEntry(archive, entry), { mode: 0o755 });
  return runtimeSource;
}

async function stageOnnxRuntimeNotices(version) {
  const cacheDirectory = path.join(
    workerDirectory,
    "target",
    "onnxruntime",
    version,
    "notices",
  );
  mkdirSync(cacheDirectory, { recursive: true });

  let archive;
  for (const notice of onnxRuntimeNotices[version]) {
    const cached = path.join(cacheDirectory, notice.source);
    if (
      existsSync(cached) &&
      !fileMatches(cached, notice.size, notice.sha256)
    ) {
      rmSync(cached, { force: true });
    }
    if (!existsSync(cached)) {
      archive ??= readFileSync(await onnxRuntimePackage(version));
      const bytes = withUnixLineEndings(readZipEntry(archive, notice.source));
      if (bytes.byteLength !== notice.size || sha256(bytes) !== notice.sha256) {
        throw new Error(
          `Pinned ONNX Runtime ${version} ${notice.source} failed integrity verification.`,
        );
      }
      const temporary = `${cached}.${Date.now()}.download`;
      writeFileSync(temporary, bytes, { mode: 0o600 });
      try {
        renameSync(temporary, cached);
      } finally {
        rmSync(temporary, { force: true });
      }
    }
    copyFileSync(
      cached,
      path.join(binariesDirectory, `onnxruntime-${version}-${notice.name}`),
    );
    stageResource(cached, `notices/onnxruntime-${version}/${notice.name}`);
  }
  console.log(`Staged pinned ONNX Runtime ${version} license notices.`);
}

async function stageMacOSRuntime(
  version = onnxRuntimeVersion,
  assets = macOSRuntimeAssets,
  libraryName = macOSRuntimeLibraryName,
  workerDirectories = [workerDirectory, classifierWorkerDirectory],
) {
  const entry = assets[target];
  if (!entry) {
    if (target.endsWith("-apple-darwin") && assets === macOSRuntimeAssets) {
      throw new Error(`Unsupported macOS target for ONNX Runtime: ${target}`);
    }
    return;
  }

  const runtimeSource = await extractOnnxRuntimeLibrary(
    version,
    entry,
    libraryName,
  );
  const runtimeDestinations = [
    ...workerDirectories.map((directory) =>
      path.join(directory, "target", "release", libraryName),
    ),
    path.join(binariesDirectory, libraryName),
    // The workers load it from beside their executable.
    path.join(resourcesBinDirectory, libraryName),
  ];
  return { runtimeSource, runtimeDestinations, version };
}

function stageCoreMLRuntime() {
  return stageMacOSRuntime(
    coreMLRuntimeVersion,
    coreMLRuntimeAssets,
    coreMLRuntimeLibraryName,
    [workerDirectory],
  );
}

async function stageLinuxRuntime() {
  const entry = linuxRuntimeAssets[target];
  if (!entry) {
    if (target.includes("-linux-")) {
      throw new Error(`Unsupported Linux target for ONNX Runtime: ${target}`);
    }
    return;
  }

  const runtimeSource = await extractOnnxRuntimeLibrary(
    onnxRuntimeVersion,
    entry,
    linuxRuntimeLibraryName,
  );
  const runtimeDestinations = [
    path.join(workerDirectory, "target", "release", linuxRuntimeLibraryName),
    path.join(
      classifierWorkerDirectory,
      "target",
      "release",
      linuxRuntimeLibraryName,
    ),
    path.join(binariesDirectory, linuxRuntimeLibraryName),
    // The desktop hands Core this path (ASTRLINK_ONNX_RUNTIME_PATH).
    path.join(resourcesDirectory, "onnxruntime", linuxRuntimeLibraryName),
  ];
  return { runtimeSource, runtimeDestinations };
}

function isRegularNonEmptyFile(filePath) {
  try {
    const info = lstatSync(filePath);
    return info.isFile() && info.size > 0;
  } catch {
    return false;
  }
}

function windowsWorkerCacheGaps() {
  const gaps = [];
  for (const [directory, executable] of [
    [workerDirectory, "astrlink-privacy-worker.exe"],
    [classifierWorkerDirectory, "astrlink-classifier-worker.exe"],
  ]) {
    for (const name of [executable, "DirectML.dll"]) {
      const cached = path.join(directory, "target", "release", name);
      if (!isRegularNonEmptyFile(cached)) gaps.push(cached);
    }
  }
  return gaps;
}

function materializeWindowsWorkerRuntime(directory) {
  // ort-sys (`copy-dylibs`) places DirectML.dll next to the executable as a
  // symlink into the user-level ONNX Runtime download cache. CI never caches
  // that directory, so a restored target/ would only hold a dangling link.
  // Replace the link with a real copy before anything archives it.
  const runtime = path.join(directory, "target", "release", "DirectML.dll");
  let info;
  try {
    info = lstatSync(runtime);
  } catch {
    throw new Error(`Windows worker build did not produce ${runtime}`);
  }
  if (!info.isSymbolicLink()) return;
  let source;
  try {
    source = realpathSync(runtime);
  } catch {
    throw new Error(
      `${runtime} is a dangling symlink; clear the Rust build cache and rebuild.`,
    );
  }
  const temporary = `${runtime}.${process.pid}.copy`;
  copyFileSync(source, temporary);
  rmSync(runtime, { force: true });
  renameSync(temporary, runtime);
  console.log(`Materialized ${runtime} from ${source}`);
}

function assertWindowsRuntimeVersion() {
  const locked = /^name = "ort-sys"\r?\nversion = "([^"]+)"/m;
  for (const directory of [workerDirectory, classifierWorkerDirectory]) {
    const lockfile = path.join(directory, "Cargo.lock");
    const version = readFileSync(lockfile, "utf8").match(locked)?.[1];
    if (version !== windowsRuntimeOrtSysVersion) {
      throw new Error(
        `${lockfile} locks ort-sys ${version ?? "(missing)"}, but the Windows ` +
          `ONNX Runtime ${windowsRuntimeVersion} notices belong to ort-sys ` +
          `${windowsRuntimeOrtSysVersion}.`,
      );
    }
  }
}

function fileMatches(filePath, size, expectedSha256) {
  return (
    statSync(filePath).size === size &&
    sha256(readFileSync(filePath)) === expectedSha256
  );
}

function sha256(bytes) {
  return createHash("sha256").update(bytes).digest("hex");
}

if (process.argv.includes("--test-runtime-only")) {
  // Worker tests load the runtime beside their test executables. They do not
  // need packaged sidecars, license resources, or the Windows VC installer.
  // On Windows, ort-sys stages its runtime dependencies during cargo test.
  const runtime = (await stageMacOSRuntime()) ?? (await stageLinuxRuntime());
  if (runtime) {
    for (const directory of [workerDirectory, classifierWorkerDirectory]) {
      const destination = path.join(
        directory,
        "target",
        "debug",
        "deps",
        path.basename(runtime.runtimeSource),
      );
      mkdirSync(path.dirname(destination), { recursive: true });
      copyFileSync(runtime.runtimeSource, destination);
    }
  }
  console.log("Prepared worker test runtime without building sidecars.");
  process.exit(0);
}

// The licenses the app ships, as the About page lists them, with those of
// moejs, the plugin runtime Core embeds, and the Node.js modules it bundles.
for (const [source, destination] of [
  ["LICENSE", "licenses/LICENSE"],
  ["LICENSING.md", "licenses/LICENSING.md"],
  ["LICENSES/AGPL-3.0.txt", "licenses/LICENSES/AGPL-3.0.txt"],
  ["LICENSES/dsh-ptc-moejs-MIT.txt", "licenses/LICENSES/dsh-ptc-moejs-MIT.txt"],
  ["LICENSES/events-MIT.txt", "licenses/LICENSES/events-MIT.txt"],
  ["LICENSES/nodejs-MIT.txt", "licenses/LICENSES/nodejs-MIT.txt"],
  ["LICENSES/punycode-MIT.txt", "licenses/LICENSES/punycode-MIT.txt"],
  [
    "LICENSES/readable-stream-MIT.txt",
    "licenses/LICENSES/readable-stream-MIT.txt",
  ],
  [
    "LICENSES/string_decoder-MIT.txt",
    "licenses/LICENSES/string_decoder-MIT.txt",
  ],
  [
    "LICENSES/moejs-THIRD_PARTY_NOTICES.txt",
    "notices/moejs/THIRD_PARTY_NOTICES.txt",
  ],
]) {
  stageResource(path.join(repositoryRoot, source), destination);
}
if (target.includes("windows")) {
  assertWindowsRuntimeVersion();
  await stageOnnxRuntimeNotices(windowsRuntimeVersion);
} else {
  await stageOnnxRuntimeNotices(onnxRuntimeVersion);
}
if (coreMLRuntimeAssets[target]) {
  await stageOnnxRuntimeNotices(coreMLRuntimeVersion);
}
await stageWindowsVcRuntime({ target, binariesDirectory });

// Release jobs stamp the tag into package.json before this script runs, so
// Core reports the same version as the desktop app.
const { version: appVersion } = JSON.parse(
  readFileSync(path.join(desktopDirectory, "package.json"), "utf8"),
);
execFileSync(
  "go",
  [
    "build",
    "-trimpath",
    "-ldflags",
    `-X github.com/QuantumNous/astrlink/core/internal/buildinfo.Version=${appVersion}`,
    "-o",
    output,
    "./cmd/astrlink-core",
  ],
  {
    cwd: coreDirectory,
    stdio: "inherit",
  },
);

console.log(`Staged astrlink-core for MyGo: ${output}`);
stageProgram(output, "astrlink-core");

execFileSync(
  "go",
  ["build", "-trimpath", "-o", cliOutput, "./cmd/astrlink-cli"],
  {
    cwd: coreDirectory,
    stdio: "inherit",
  },
);
if (!target.includes("windows")) {
  chmodSync(cliOutput, 0o755);
}
console.log(`Staged astrlink-cli for MyGo: ${cliOutput}`);
stageProgram(cliOutput, "astrlink-cli");

const macOSRuntime = await stageMacOSRuntime();
const coreMLRuntime = await stageCoreMLRuntime();
const linuxRuntime = await stageLinuxRuntime();

if (!reuseWindowsWorkers) {
  execFileSync(
    "cargo",
    [
      "build",
      "--locked",
      "--release",
      "--manifest-path",
      path.join(workerDirectory, "Cargo.toml"),
    ],
    {
      cwd: workerDirectory,
      stdio: "inherit",
    },
  );
  if (target.includes("windows")) {
    materializeWindowsWorkerRuntime(workerDirectory);
  }
}

for (const runtime of [macOSRuntime, coreMLRuntime]) {
  if (!runtime) continue;
  for (const destination of runtime.runtimeDestinations) {
    mkdirSync(path.dirname(destination), { recursive: true });
    copyFileSync(runtime.runtimeSource, destination);
  }
  console.log(`Staged pinned ONNX Runtime ${runtime.version} for macOS.`);
}

if (linuxRuntime) {
  for (const destination of linuxRuntime.runtimeDestinations) {
    mkdirSync(path.dirname(destination), { recursive: true });
    copyFileSync(linuxRuntime.runtimeSource, destination);
  }
  console.log(
    `Staged pinned ONNX Runtime ${onnxRuntimeVersion} for Linux x64.`,
  );
}

const builtWorker = path.join(
  workerDirectory,
  "target",
  "release",
  `astrlink-privacy-worker${executableSuffix}`,
);
copyFileSync(builtWorker, workerOutput);
if (!target.includes("windows")) {
  chmodSync(workerOutput, 0o755);
}

console.log(`Staged astrlink-privacy-worker for MyGo: ${workerOutput}`);
stageProgram(workerOutput, "astrlink-privacy-worker");

if (!reuseWindowsWorkers) {
  execFileSync(
    "cargo",
    [
      "build",
      "--locked",
      "--release",
      "--manifest-path",
      path.join(classifierWorkerDirectory, "Cargo.toml"),
    ],
    {
      cwd: classifierWorkerDirectory,
      stdio: "inherit",
    },
  );
  if (target.includes("windows")) {
    materializeWindowsWorkerRuntime(classifierWorkerDirectory);
  }
}

const builtClassifierWorker = path.join(
  classifierWorkerDirectory,
  "target",
  "release",
  `astrlink-classifier-worker${executableSuffix}`,
);
copyFileSync(builtClassifierWorker, classifierWorkerOutput);
if (!target.includes("windows")) {
  chmodSync(classifierWorkerOutput, 0o755);
}

console.log(
  `Staged astrlink-classifier-worker for MyGo: ${classifierWorkerOutput}`,
);
stageProgram(classifierWorkerOutput, "astrlink-classifier-worker");

if (target.includes("windows")) {
  const runtimeName = "DirectML.dll";
  const runtimeSource = path.join(
    workerDirectory,
    "target",
    "release",
    runtimeName,
  );
  const classifierRuntime = path.join(
    classifierWorkerDirectory,
    "target",
    "release",
    runtimeName,
  );
  if (
    !existsSync(runtimeSource) ||
    !existsSync(classifierRuntime) ||
    sha256(readFileSync(runtimeSource)) !==
      sha256(readFileSync(classifierRuntime))
  ) {
    throw new Error("Windows workers require the same bundled DirectML.dll.");
  }
  copyFileSync(runtimeSource, path.join(binariesDirectory, runtimeName));
  stageResource(runtimeSource, `bin/${runtimeName}`);
  console.log(`Staged Windows ONNX Runtime dependency: ${runtimeName}`);
}
