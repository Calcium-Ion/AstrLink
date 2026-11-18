import { createHash } from "node:crypto";
import {
  existsSync,
  mkdirSync,
  readFileSync,
  renameSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { inflateRawSync } from "node:zlib";
import { downloadWithRetry } from "./download-with-retry.mjs";

// Microsoft publishes every ONNX Runtime release as the NuGet package
// Microsoft.ML.OnnxRuntime: one zip with the native library for each platform,
// LICENSE and ThirdPartyNotices.txt. Its libraries are the release builds the
// GitHub archives carried; some macOS ones also carry Microsoft's signature,
// which packaging replaces with the app's.
export function onnxRuntimePackageUrl(version) {
  return (
    "https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime/" +
    `${version}/microsoft.ml.onnxruntime.${version}.nupkg`
  );
}

// fetchOnnxRuntimePackage returns the path of the pinned package in
// cacheDirectory, downloading it when the cached copy is missing or differs.
export async function fetchOnnxRuntimePackage(
  version,
  pin,
  cacheDirectory,
  { download = downloadWithRetry } = {},
) {
  if (!pin) {
    throw new Error(`No pinned package for ONNX Runtime ${version}.`);
  }
  mkdirSync(cacheDirectory, { recursive: true });
  const packagePath = path.join(
    cacheDirectory,
    `microsoft.ml.onnxruntime.${version}.nupkg`,
  );
  if (
    existsSync(packagePath) &&
    !(
      statSync(packagePath).size === pin.size &&
      sha256(readFileSync(packagePath)) === pin.sha256
    )
  ) {
    rmSync(packagePath, { force: true });
  }
  if (!existsSync(packagePath)) {
    const bytes = await download(
      onnxRuntimePackageUrl(version),
      `pinned ONNX Runtime ${version}`,
    );
    if (bytes.byteLength !== pin.size || sha256(bytes) !== pin.sha256) {
      throw new Error(
        `Pinned ONNX Runtime ${version} package failed integrity verification.`,
      );
    }
    const temporary = `${packagePath}.${process.pid}.download`;
    writeFileSync(temporary, bytes, { mode: 0o600 });
    renameSync(temporary, packagePath);
  }
  return packagePath;
}

// readZipEntry returns one file of a zip archive. The packages are plain zips
// well under 4 GiB, so ZIP64 records are refused rather than read.
export function readZipEntry(archive, name) {
  const end = archive.lastIndexOf(Buffer.from([0x50, 0x4b, 0x05, 0x06]));
  if (end < 0 || end + 22 > archive.length) {
    throw new Error("Not a zip archive.");
  }
  const count = archive.readUInt16LE(end + 10);
  let offset = archive.readUInt32LE(end + 16);
  if (count === 0xffff || offset === 0xffffffff) {
    throw new Error("ZIP64 archives are not supported.");
  }
  for (let index = 0; index < count; index += 1) {
    if (archive.readUInt32LE(offset) !== 0x02014b50) {
      throw new Error("Corrupt zip central directory.");
    }
    const method = archive.readUInt16LE(offset + 10);
    const compressedSize = archive.readUInt32LE(offset + 20);
    const size = archive.readUInt32LE(offset + 24);
    const nameLength = archive.readUInt16LE(offset + 28);
    const extraLength = archive.readUInt16LE(offset + 30);
    const commentLength = archive.readUInt16LE(offset + 32);
    const localOffset = archive.readUInt32LE(offset + 42);
    const entryName = archive.toString(
      "utf8",
      offset + 46,
      offset + 46 + nameLength,
    );
    offset += 46 + nameLength + extraLength + commentLength;
    if (entryName !== name) continue;

    if (archive.readUInt32LE(localOffset) !== 0x04034b50) {
      throw new Error(`Corrupt zip entry ${name}.`);
    }
    const dataStart =
      localOffset +
      30 +
      archive.readUInt16LE(localOffset + 26) +
      archive.readUInt16LE(localOffset + 28);
    const data = archive.subarray(dataStart, dataStart + compressedSize);
    let bytes;
    if (method === 0) bytes = Buffer.from(data);
    else if (method === 8) bytes = inflateRawSync(data);
    else throw new Error(`Unsupported compression ${method} for ${name}.`);
    if (bytes.length !== size) {
      throw new Error(`Zip entry ${name} has the wrong size.`);
    }
    return bytes;
  }
  throw new Error(`Zip archive has no ${name}.`);
}

// withUnixLineEndings drops the carriage return of each CRLF. The package
// carries its notices with Windows line endings; the app ships them with the
// line endings of the ONNX Runtime repository.
export function withUnixLineEndings(bytes) {
  const out = Buffer.alloc(bytes.length);
  let length = 0;
  for (let index = 0; index < bytes.length; index += 1) {
    if (bytes[index] === 0x0d && bytes[index + 1] === 0x0a) continue;
    out[length] = bytes[index];
    length += 1;
  }
  return out.subarray(0, length);
}

function sha256(bytes) {
  return createHash("sha256").update(bytes).digest("hex");
}
