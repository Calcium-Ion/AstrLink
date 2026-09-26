import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { checkSignature, notarizeMacOS } from "./notarize-macos.mjs";

const teamID = "TESTTEAM01";
const submissionID = "01234567-89ab-cdef-0123-456789abcdef";
const signature = [
  "Authority=Developer ID Application: Test Team (TESTTEAM01)",
  "TeamIdentifier=TESTTEAM01",
  "Timestamp=Sep 26, 2026 at 12:00:00",
  "CodeDirectory v=20500 size=100 flags=0x10000(runtime) hashes=1+7 location=embedded",
].join("\n");
const directories: string[] = [];

function fixture(status = "Accepted") {
  const bundleDirectory = mkdtempSync(
    path.join(tmpdir(), "astrlink-notarize-"),
  );
  directories.push(bundleDirectory);
  mkdirSync(path.join(bundleDirectory, "macos/AstrLink.app"), {
    recursive: true,
  });
  mkdirSync(path.join(bundleDirectory, "dmg"));
  writeFileSync(
    path.join(bundleDirectory, "dmg/AstrLink.dmg"),
    "synthetic disk image",
  );
  const runCommand = vi.fn((command: string, args: string[]) => {
    if (command === "codesign" && args[0] === "--display") return signature;
    if (command === "xcrun" && args[0] === "notarytool") {
      if (args[1] === "submit") return JSON.stringify({ id: submissionID });
      if (args[1] === "wait")
        return JSON.stringify({ id: submissionID, status });
      if (args[1] === "log") return JSON.stringify({ status, issues: null });
    }
    return "";
  });
  return { bundleDirectory, teamID, profile: "test-notary", runCommand };
}

afterEach(() => {
  vi.restoreAllMocks();
  for (const directory of directories.splice(0))
    rmSync(directory, { recursive: true, force: true });
});

describe("macOS release signing gates", () => {
  it("rejects ad-hoc, wrong-team and untimestamped signatures", () => {
    expect(() => checkSignature("Signature=adhoc", teamID)).toThrow(
      "Developer ID Application",
    );
    expect(() => checkSignature(signature, "OTHERTEAM1")).toThrow("Team ID");
    expect(() =>
      checkSignature(signature.replace(/^Timestamp=.*\n/m, ""), teamID),
    ).toThrow("timestamp");
  });

  it("requires Hardened Runtime on executables, without requiring it on a DMG", () => {
    const details = signature.replace("0x10000(runtime)", "0x0(none)");
    expect(() => checkSignature(details, teamID, true)).toThrow(
      "Hardened Runtime",
    );
    expect(() => checkSignature(details, teamID)).not.toThrow();
  });

  it("does not upload when a nested sidecar has an invalid signature", () => {
    const options = fixture();
    const normal = options.runCommand.getMockImplementation()!;
    options.runCommand.mockImplementation((command, args) => {
      if (
        command === "codesign" &&
        args[0] === "--verify" &&
        args.at(-1)?.endsWith("astrlink-core")
      ) {
        throw new Error("invalid sidecar signature");
      }
      return normal(command, args);
    });
    expect(() => notarizeMacOS(options)).toThrow("invalid sidecar signature");
    expect(
      options.runCommand.mock.calls.some(
        ([, args]) => args[0] === "notarytool",
      ),
    ).toBe(false);
  });

  it.each(["Invalid", "Rejected", "In Progress"])(
    "never staples or passes Gatekeeper on Apple status %s",
    (status) => {
      const options = fixture(status);
      expect(() => notarizeMacOS(options)).toThrow(`status: ${status}`);
      expect(
        options.runCommand.mock.calls.some(
          ([command, args]) => command === "spctl" || args[0] === "stapler",
        ),
      ).toBe(false);
      expect(
        readFileSync(
          path.join(
            options.bundleDirectory,
            "notarization",
            `${submissionID}.log.json`,
          ),
          "utf8",
        ),
      ).toContain(status);
    },
  );

  it("preserves a timed-out submission and resumes without uploading again", () => {
    const options = fixture();
    const normal = options.runCommand.getMockImplementation()!;
    options.runCommand.mockImplementation((command, args) => {
      if (args[0] === "notarytool" && args[1] === "wait")
        throw new Error("timeout");
      return normal(command, args);
    });
    expect(() => notarizeMacOS(options)).toThrow(
      `--submission-id ${submissionID}`,
    );
    options.runCommand.mockReset().mockImplementation(normal);
    notarizeMacOS({ ...options, submissionID });
    expect(
      options.runCommand.mock.calls.some(([, args]) => args[1] === "submit"),
    ).toBe(false);
    expect(
      options.runCommand.mock.calls.filter(
        ([, args]) => args[0] === "stapler" && args[1] === "validate",
      ),
    ).toHaveLength(2);
    expect(
      options.runCommand.mock.calls.filter(([command]) => command === "spctl"),
    ).toHaveLength(2);
    writeFileSync(
      path.join(options.bundleDirectory, "dmg/AstrLink.dmg"),
      "replaced disk image",
    );
    expect(() => notarizeMacOS({ ...options, submissionID })).toThrow(
      "DMG has changed",
    );
  });
});
