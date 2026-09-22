import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import * as path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stageWindowsVcRuntime, verifyWindowsVcRuntime } from "./stage-windows-vc-runtime.mjs";

// Windows builds check the real Authenticode signature; unit fixtures are not PE files.
vi.mock("node:child_process", () => ({ execFileSync: vi.fn() }));

const bytes = Buffer.from("synthetic redistributable download");
const asset = {
  version: "14.44.35211.0",
  url: "https://example.invalid/vc_redist.x64.exe",
  sha256: createHash("sha256").update(bytes).digest("hex"),
  size: bytes.length,
};
const directories: string[] = [];
function options() {
  const binariesDirectory = mkdtempSync(path.join(tmpdir(), "astrlink-vc-runtime-"));
  directories.push(binariesDirectory);
  return {
    target: "x86_64-pc-windows-msvc",
    binariesDirectory,
    asset,
    fetchAsset: vi.fn(async () => new Response(bytes)),
  };
}
afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  vi.clearAllMocks();
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true });
});

describe("Windows model runtime staging", () => {
  it("lets Windows PowerShell rebuild its module path when launched through Bun", async () => {
    const config = options();
    vi.stubEnv("PSModulePath", "C:\\Program Files\\PowerShell\\7\\Modules");
    vi.stubGlobal("process", { ...process, platform: "win32" });
    await stageWindowsVcRuntime(config);
    const [command, args, spawnOptions] = vi.mocked(execFileSync).mock.calls[0];
    expect(command).toBe("powershell.exe");
    expect(args?.join(" ")).toContain("Get-AuthenticodeSignature");
    expect(Object.keys(spawnOptions?.env ?? {}).some(key => key.toUpperCase() === "PSMODULEPATH")).toBe(false);
    expect(spawnOptions?.env?.ASTRLINK_VC_REDIST_FILE)
      .toBe(path.join(config.binariesDirectory, "vc_redist.x64.exe"));
    expect(process.env.PSModulePath).toBe("C:\\Program Files\\PowerShell\\7\\Modules");
  });

  it("rejects both truncated and same-size tampered downloads", () => {
    expect(() => verifyWindowsVcRuntime(bytes.subarray(1), asset)).toThrow("integrity");
    expect(() => verifyWindowsVcRuntime(Buffer.alloc(bytes.length), asset)).toThrow("integrity");
  });

  it("stages a verified offline installer and reuses it without a network request", async () => {
    const config = options();
    await stageWindowsVcRuntime(config);
    expect(readFileSync(path.join(config.binariesDirectory, "vc_redist.x64.exe"))).toEqual(bytes);
    expect(readFileSync(path.join(config.binariesDirectory, "vc_redist.x64.nsh"), "utf8"))
      .toContain(`!define ASTRLINK_VC_RUNTIME_VERSION "${asset.version}"`);
    await stageWindowsVcRuntime(config);
    expect(config.fetchAsset).toHaveBeenCalledTimes(1);
  });

  it("replaces a corrupt cache instead of embedding it", async () => {
    const config = options();
    writeFileSync(path.join(config.binariesDirectory, "vc_redist.x64.exe"), Buffer.alloc(bytes.length));
    await stageWindowsVcRuntime(config);
    expect(config.fetchAsset).toHaveBeenCalledTimes(1);
    expect(readFileSync(path.join(config.binariesDirectory, "vc_redist.x64.exe"))).toEqual(bytes);
  });

  it("fails the build when a downloaded payload is corrupt or unavailable", async () => {
    const config = options();
    config.fetchAsset.mockResolvedValueOnce(new Response(Buffer.alloc(bytes.length)));
    await expect(stageWindowsVcRuntime(config)).rejects.toThrow("integrity");
    expect(() => readFileSync(path.join(config.binariesDirectory, "vc_redist.x64.exe"))).toThrow();
    config.fetchAsset.mockResolvedValueOnce(new Response(null, { status: 503 }));
    await expect(stageWindowsVcRuntime(config)).rejects.toThrow("HTTP 503");
  });

  it("does not ship an x64 installer to other architectures or operating systems", async () => {
    const config = options();
    await expect(stageWindowsVcRuntime({ ...config, target: "aarch64-pc-windows-msvc" }))
      .rejects.toThrow("Unsupported Windows target");
    await stageWindowsVcRuntime({ ...config, target: "aarch64-apple-darwin" });
    await stageWindowsVcRuntime({ ...config, target: "x86_64-unknown-linux-gnu" });
    expect(config.fetchAsset).not.toHaveBeenCalled();
  });
});
