import { lstat, readdir } from "node:fs/promises";
import { isAbsolute, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../", import.meta.url));
const arguments_ = process.argv.slice(2);
const packaged = arguments_.includes("--packaged");
const requestedRoots = arguments_.filter(
  (argument) => argument !== "--packaged",
);
const scanRoots =
  requestedRoots.length === 0
    ? [root]
    : requestedRoots.map((path) =>
        isAbsolute(path) ? path : resolve(process.cwd(), path),
      );
const ignoredDirectories = new Set([".git", "dist", "node_modules", "target"]);
const modelPatterns = [
  /(?:^|\/)pytorch_model[^/]*\.bin$/i,
  /(?:^|\/)tf_model[^/]*\.h5$/i,
  /(?:^|\/)flax_model[^/]*\.msgpack$/i,
  /\.ckpt$/i,
  /\.gguf$/i,
  /\.ggml$/i,
  /\.onnx$/i,
  /\.onnx_data(?:_\d+)?$/i,
  /\.onnx\.data$/i,
  /\.pt$/i,
  /\.pth$/i,
  /\.safetensors$/i,
];
const maximumFixtureBytes = 1024 * 1024;
const violations = [];

function displayPath(path) {
  const workspacePath = relative(root, path);
  if (
    workspacePath !== "" &&
    workspacePath !== ".." &&
    !workspacePath.startsWith(`..${sep}`)
  ) {
    return workspacePath.split(sep).join("/");
  }
  return path;
}

async function inspectFile(path) {
  const normalized = displayPath(path);
  if (!modelPatterns.some((pattern) => pattern.test(normalized))) return;

  const metadata = await lstat(path);
  const isFixture =
    !packaged &&
    normalized.includes("/testdata/") &&
    metadata.size <= maximumFixtureBytes;
  if (!isFixture) {
    violations.push(`${normalized} (${metadata.size} bytes)`);
  }
}

async function visit(path, useSourceIgnores) {
  const metadata = await lstat(path);
  if (metadata.isFile() || metadata.isSymbolicLink()) {
    await inspectFile(path);
    return;
  }
  if (!metadata.isDirectory()) return;

  for (const entry of await readdir(path, { withFileTypes: true })) {
    if (
      useSourceIgnores &&
      entry.isDirectory() &&
      ignoredDirectories.has(entry.name)
    ) {
      continue;
    }
    await visit(join(path, entry.name), useSourceIgnores);
  }
}

for (const scanRoot of scanRoots) {
  await visit(scanRoot, requestedRoots.length === 0);
}

if (violations.length > 0) {
  throw new Error(
    [
      "Production model artifacts must not be committed or packaged:",
      ...violations.map((item) => `- ${item}`),
    ].join("\n"),
  );
}

console.log(
  packaged
    ? "verified that no production model artifacts are present in packaged application contents"
    : "verified that no production model artifacts are present",
);
