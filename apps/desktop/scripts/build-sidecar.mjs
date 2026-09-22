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
import { stageWindowsVcRuntime } from "./stage-windows-vc-runtime.mjs";

const onnxRuntimeVersion = "1.23.2";
const macOSRuntimeLibraryName = `libonnxruntime.${onnxRuntimeVersion}.dylib`;
const linuxRuntimeLibraryName = `libonnxruntime.so.${onnxRuntimeVersion}`;
const onnxRuntimeNotices = [
  {
    source: "LICENSE",
    destination: `onnxruntime-${onnxRuntimeVersion}-LICENSE.txt`,
    sha256: "2f07c72751aed99790b8a4869cf2311df85a860b22ded05fa22803587a48922c",
    size: 1_073,
  },
  {
    source: "ThirdPartyNotices.txt",
    destination: `onnxruntime-${onnxRuntimeVersion}-ThirdPartyNotices.txt`,
    sha256: "e9e90971a8e75a9a8ac0c6412e29c1202d079998389915aa485f46c816c3b4cc",
    size: 326_866,
  },
];
const macOSRuntimeAssets = {
  "aarch64-apple-darwin": {
    archive: `onnxruntime-osx-arm64-${onnxRuntimeVersion}.tgz`,
    directory: `onnxruntime-osx-arm64-${onnxRuntimeVersion}`,
    sha256: "b4d513ab2b26f088c66891dbbc1408166708773d7cc4163de7bdca0e9bbb7856",
    size: 9_999_931,
  },
  "x86_64-apple-darwin": {
    archive: `onnxruntime-osx-x86_64-${onnxRuntimeVersion}.tgz`,
    directory: `onnxruntime-osx-x86_64-${onnxRuntimeVersion}`,
    sha256: "d10359e16347b57d9959f7e80a225a5b4a66ed7d7e007274a15cae86836485a6",
    size: 11_676_322,
  },
};
const linuxRuntimeAssets = {
  "x86_64-unknown-linux-gnu": {
    archive: `onnxruntime-linux-x64-${onnxRuntimeVersion}.tgz`,
    directory: `onnxruntime-linux-x64-${onnxRuntimeVersion}`,
    sha256: "1fa4dcaef22f6f7d5cd81b28c2800414350c10116f5fdd46a2160082551c5f9b",
    size: 8_309_231,
  },
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
  throw new Error(
    "Unable to determine the Rust host target for the Tauri sidecar.",
  );
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
const binariesDirectory = path.join(desktopDirectory, "src-tauri", "binaries");
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
const mcpOutput = path.join(
  binariesDirectory,
  `astrlink-mcp-${target}${executableSuffix}`,
);

mkdirSync(binariesDirectory, { recursive: true });

async function stageOnnxRuntimeNotices() {
  const cacheDirectory = path.join(
    workerDirectory,
    "target",
    "onnxruntime",
    onnxRuntimeVersion,
    "notices",
  );
  mkdirSync(cacheDirectory, { recursive: true });

  for (const notice of onnxRuntimeNotices) {
    const cached = path.join(cacheDirectory, notice.source);
    if (
      existsSync(cached) &&
      !fileMatches(cached, notice.size, notice.sha256)
    ) {
      rmSync(cached, { force: true });
    }
    if (!existsSync(cached)) {
      const url =
        `https://raw.githubusercontent.com/microsoft/onnxruntime/` +
        `v${onnxRuntimeVersion}/${notice.source}`;
      const response = await fetch(url, { redirect: "follow" });
      if (!response.ok) {
        throw new Error(
          `Unable to download pinned ONNX Runtime notice: HTTP ${response.status}`,
        );
      }
      const bytes = Buffer.from(await response.arrayBuffer());
      if (bytes.byteLength !== notice.size || sha256(bytes) !== notice.sha256) {
        throw new Error(
          `Pinned ONNX Runtime ${notice.source} failed integrity verification.`,
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
    copyFileSync(cached, path.join(binariesDirectory, notice.destination));
  }
  console.log(
    `Staged pinned ONNX Runtime ${onnxRuntimeVersion} license notices.`,
  );
}

async function stageMacOSRuntime() {
  const asset = macOSRuntimeAssets[target];
  if (!asset) {
    if (target.endsWith("-apple-darwin")) {
      throw new Error(`Unsupported macOS target for ONNX Runtime: ${target}`);
    }
    return;
  }

  const cacheDirectory = path.join(
    workerDirectory,
    "target",
    "onnxruntime",
    onnxRuntimeVersion,
    target,
  );
  const archivePath = path.join(cacheDirectory, asset.archive);
  const extractionDirectory = path.join(cacheDirectory, "extracted");
  mkdirSync(cacheDirectory, { recursive: true });

  if (
    existsSync(archivePath) &&
    !fileMatches(archivePath, asset.size, asset.sha256)
  ) {
    rmSync(archivePath, { force: true });
  }
  if (!existsSync(archivePath)) {
    const url =
      `https://github.com/microsoft/onnxruntime/releases/download/` +
      `v${onnxRuntimeVersion}/${asset.archive}`;
    const response = await fetch(url, { redirect: "follow" });
    if (!response.ok) {
      throw new Error(
        `Unable to download pinned ONNX Runtime ${onnxRuntimeVersion}: HTTP ${response.status}`,
      );
    }
    const bytes = Buffer.from(await response.arrayBuffer());
    if (bytes.byteLength !== asset.size || sha256(bytes) !== asset.sha256) {
      throw new Error(
        `Pinned ONNX Runtime ${onnxRuntimeVersion} archive failed integrity verification.`,
      );
    }
    const temporaryArchive = `${archivePath}.download`;
    writeFileSync(temporaryArchive, bytes, { mode: 0o600 });
    renameSync(temporaryArchive, archivePath);
  }

  rmSync(extractionDirectory, { recursive: true, force: true });
  mkdirSync(extractionDirectory, { recursive: true });
  execFileSync("tar", ["-xzf", archivePath, "-C", extractionDirectory], {
    stdio: "inherit",
  });
  const runtimeSource = path.join(
    extractionDirectory,
    asset.directory,
    "lib",
    macOSRuntimeLibraryName,
  );
  if (!existsSync(runtimeSource)) {
    throw new Error(
      `Pinned ONNX Runtime archive is missing ${macOSRuntimeLibraryName}.`,
    );
  }

  const runtimeDestinations = [
    path.join(workerDirectory, "target", "release", macOSRuntimeLibraryName),
    path.join(
      classifierWorkerDirectory,
      "target",
      "release",
      macOSRuntimeLibraryName,
    ),
    path.join(binariesDirectory, macOSRuntimeLibraryName),
  ];
  return { runtimeSource, runtimeDestinations };
}

async function stageLinuxRuntime() {
  const asset = linuxRuntimeAssets[target];
  if (!asset) {
    if (target.includes("-linux-")) {
      throw new Error(`Unsupported Linux target for ONNX Runtime: ${target}`);
    }
    return;
  }

  const cacheDirectory = path.join(
    workerDirectory,
    "target",
    "onnxruntime",
    onnxRuntimeVersion,
    target,
  );
  const archivePath = path.join(cacheDirectory, asset.archive);
  const extractionDirectory = path.join(cacheDirectory, "extracted");
  mkdirSync(cacheDirectory, { recursive: true });

  if (
    existsSync(archivePath) &&
    !fileMatches(archivePath, asset.size, asset.sha256)
  ) {
    rmSync(archivePath, { force: true });
  }
  if (!existsSync(archivePath)) {
    const url =
      `https://github.com/microsoft/onnxruntime/releases/download/` +
      `v${onnxRuntimeVersion}/${asset.archive}`;
    const response = await fetch(url, { redirect: "follow" });
    if (!response.ok) {
      throw new Error(
        `Unable to download pinned ONNX Runtime ${onnxRuntimeVersion}: HTTP ${response.status}`,
      );
    }
    const bytes = Buffer.from(await response.arrayBuffer());
    if (bytes.byteLength !== asset.size || sha256(bytes) !== asset.sha256) {
      throw new Error(
        `Pinned ONNX Runtime ${onnxRuntimeVersion} archive failed integrity verification.`,
      );
    }
    const temporaryArchive = `${archivePath}.download`;
    writeFileSync(temporaryArchive, bytes, { mode: 0o600 });
    renameSync(temporaryArchive, archivePath);
  }

  rmSync(extractionDirectory, { recursive: true, force: true });
  mkdirSync(extractionDirectory, { recursive: true });
  execFileSync("tar", ["-xzf", archivePath, "-C", extractionDirectory], {
    stdio: "inherit",
  });
  const runtimeSource = path.join(
    extractionDirectory,
    asset.directory,
    "lib",
    linuxRuntimeLibraryName,
  );
  if (!existsSync(runtimeSource)) {
    throw new Error(
      `Pinned ONNX Runtime archive is missing ${linuxRuntimeLibraryName}.`,
    );
  }

  const runtimeDestinations = [
    path.join(workerDirectory, "target", "release", linuxRuntimeLibraryName),
    path.join(
      classifierWorkerDirectory,
      "target",
      "release",
      linuxRuntimeLibraryName,
    ),
    path.join(binariesDirectory, linuxRuntimeLibraryName),
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

function fileMatches(filePath, size, expectedSha256) {
  return (
    statSync(filePath).size === size &&
    sha256(readFileSync(filePath)) === expectedSha256
  );
}

function sha256(bytes) {
  return createHash("sha256").update(bytes).digest("hex");
}

await stageOnnxRuntimeNotices();
await stageWindowsVcRuntime({ target, binariesDirectory });

execFileSync(
  "go",
  ["build", "-trimpath", "-o", output, "./cmd/astrlink-core"],
  {
    cwd: coreDirectory,
    stdio: "inherit",
  },
);

console.log(`Staged astrlink-core for Tauri: ${output}`);

execFileSync(
  "go",
  ["build", "-trimpath", "-o", mcpOutput, "./cmd/astrlink-mcp"],
  {
    cwd: coreDirectory,
    stdio: "inherit",
  },
);
if (!target.includes("windows")) {
  chmodSync(mcpOutput, 0o755);
}
console.log(`Staged astrlink-mcp for Tauri: ${mcpOutput}`);

const macOSRuntime = await stageMacOSRuntime();
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

if (macOSRuntime) {
  for (const destination of macOSRuntime.runtimeDestinations) {
    mkdirSync(path.dirname(destination), { recursive: true });
    copyFileSync(macOSRuntime.runtimeSource, destination);
  }
  console.log(`Staged pinned ONNX Runtime ${onnxRuntimeVersion} for macOS.`);
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

console.log(`Staged astrlink-privacy-worker for Tauri: ${workerOutput}`);

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
  `Staged astrlink-classifier-worker for Tauri: ${classifierWorkerOutput}`,
);

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
  console.log(`Staged Windows ONNX Runtime dependency: ${runtimeName}`);
}
