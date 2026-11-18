import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { deflateRawSync } from "node:zlib";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  fetchOnnxRuntimePackage,
  onnxRuntimePackageUrl,
  readZipEntry,
  withUnixLineEndings,
} from "./onnx-runtime-package.mjs";

// zip builds a small archive the way packagers lay it out: local headers,
// then the central directory and its end record.
function zip(entries: Array<{ name: string; data: Buffer; deflate: boolean }>) {
  const locals: Buffer[] = [];
  const centrals: Buffer[] = [];
  let offset = 0;
  for (const { name, data, deflate } of entries) {
    const stored = deflate ? deflateRawSync(data) : data;
    const nameBytes = Buffer.from(name);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(deflate ? 8 : 0, 8);
    local.writeUInt32LE(stored.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(nameBytes.length, 26);
    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0);
    central.writeUInt16LE(deflate ? 8 : 0, 10);
    central.writeUInt32LE(stored.length, 20);
    central.writeUInt32LE(data.length, 24);
    central.writeUInt16LE(nameBytes.length, 28);
    central.writeUInt32LE(offset, 42);
    locals.push(local, nameBytes, stored);
    centrals.push(central, nameBytes);
    offset += local.length + nameBytes.length + stored.length;
  }
  const directory = Buffer.concat(centrals);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(entries.length, 8);
  end.writeUInt16LE(entries.length, 10);
  end.writeUInt32LE(directory.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, directory, end]);
}

const library = Buffer.from("synthetic runtime library ".repeat(64));
const license = Buffer.from("MIT License\r\n\r\nCopyright\r\n");
const archive = zip([
  { name: "LICENSE", data: license, deflate: false },
  {
    name: "runtimes/linux-x64/native/libonnxruntime.so",
    data: library,
    deflate: true,
  },
]);
const pin = {
  sha256: createHash("sha256").update(archive).digest("hex"),
  size: archive.length,
};

let directory: string | undefined;
afterEach(() => {
  if (directory) rmSync(directory, { recursive: true, force: true });
  directory = undefined;
});

describe("ONNX Runtime NuGet package", () => {
  it("reads stored and deflated entries", () => {
    expect(readZipEntry(archive, "LICENSE")).toEqual(license);
    expect(
      readZipEntry(archive, "runtimes/linux-x64/native/libonnxruntime.so"),
    ).toEqual(library);
    expect(() => readZipEntry(archive, "runtimes/osx-x64")).toThrow(
      "Zip archive has no runtimes/osx-x64.",
    );
  });

  it("ships notices with the repository's line endings", () => {
    expect(withUnixLineEndings(license).toString()).toBe(
      "MIT License\n\nCopyright\n",
    );
    // A carriage return without a line feed is text, not a line ending.
    expect(withUnixLineEndings(Buffer.from("a\rb\r\n")).toString()).toBe(
      "a\rb\n",
    );
  });

  it("downloads the pinned package once and reuses the cached copy", async () => {
    directory = mkdtempSync(path.join(tmpdir(), "onnx-runtime-package-"));
    const download = vi.fn(async () => archive);
    const first = await fetchOnnxRuntimePackage("1.23.2", pin, directory, {
      download,
    });
    const second = await fetchOnnxRuntimePackage("1.23.2", pin, directory, {
      download,
    });
    expect(second).toBe(first);
    expect(readFileSync(first)).toEqual(archive);
    expect(download).toHaveBeenCalledTimes(1);
    expect(download).toHaveBeenCalledWith(
      onnxRuntimePackageUrl("1.23.2"),
      "pinned ONNX Runtime 1.23.2",
    );
  });

  it("replaces a damaged cached copy and refuses a mismatched download", async () => {
    directory = mkdtempSync(path.join(tmpdir(), "onnx-runtime-package-"));
    const cached = path.join(
      directory,
      "microsoft.ml.onnxruntime.1.23.2.nupkg",
    );
    writeFileSync(cached, "truncated");
    const download = vi.fn(async () => archive);
    await fetchOnnxRuntimePackage("1.23.2", pin, directory, { download });
    expect(readFileSync(cached)).toEqual(archive);

    rmSync(cached);
    await expect(
      fetchOnnxRuntimePackage("1.23.2", pin, directory, {
        download: async () => Buffer.from("tampered"),
      }),
    ).rejects.toThrow(
      "Pinned ONNX Runtime 1.23.2 package failed integrity verification.",
    );
  });

  it("points at Microsoft's NuGet feed", () => {
    expect(onnxRuntimePackageUrl("1.24.4")).toBe(
      "https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime/1.24.4/microsoft.ml.onnxruntime.1.24.4.nupkg",
    );
  });
});
