import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";

export const appleRetryDelaysMs = [10_000, 30_000, 60_000];

const desktopDirectory = fileURLToPath(new URL("../", import.meta.url));
const submissionPattern = /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i;
const productName = "AstrLink";

function run(command, args) {
  const result = spawnSync(command, args, {
    encoding: "utf8",
    maxBuffer: 16 * 1024 * 1024,
  });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    throw new Error(
      `${command} failed (${result.status}):\n${result.stdout ?? ""}${result.stderr ?? ""}`,
    );
  }
  return `${result.stdout ?? ""}${result.stderr ?? ""}`;
}

export function checkSignature(details, teamID, executable = false) {
  if (!details.includes("Authority=Developer ID Application:")) {
    throw new Error("A Developer ID Application signature is required.");
  }
  if (!details.split("\n").includes(`TeamIdentifier=${teamID}`)) {
    throw new Error(`Signing Team ID does not match ${teamID}.`);
  }
  if (!/^Timestamp=.+/m.test(details)) {
    throw new Error("The signature must have a secure timestamp.");
  }
  if (executable && !/^CodeDirectory .*flags=.*\bruntime\b/m.test(details)) {
    throw new Error("Hardened Runtime must be enabled for every executable.");
  }
}

export function notarizeMacOS({
  bundleDirectory,
  teamID,
  profile = "astrlink-notary",
  keychain,
  submissionID,
  timeout = "30m",
  runCommand = run,
  retryDelaysMs = appleRetryDelaysMs,
}) {
  if (!/^[A-Z0-9]{10}$/.test(teamID ?? "")) {
    throw new Error(
      "Set APPLE_TEAM_ID to the expected 10-character developer Team ID.",
    );
  }
  // `mygo build` writes the app and its disk image side by side.
  const app = path.join(bundleDirectory, `${productName}.app`);
  if (!existsSync(app)) throw new Error(`Missing app bundle: ${app}`);
  const images = readdirSync(bundleDirectory).filter((name) =>
    name.endsWith(".dmg"),
  );
  if (images.length !== 1)
    throw new Error(
      "Expected exactly one DMG; remove stale installers before building.",
    );
  const dmg = path.join(bundleDirectory, images[0]);
  const listed = (directory, keep = () => true) =>
    existsSync(directory)
      ? readdirSync(directory, { withFileTypes: true })
          .filter((entry) => !entry.name.startsWith(".") && keep(entry))
          .map((entry) => path.join(directory, entry.name))
          .sort()
      : [];
  const resources = path.join(app, "Contents/Resources");
  // Core, the CLI and the workers, with the ONNX Runtime libraries they
  // load (Apple silicon adds the Core ML one), then the widget's WidgetKit
  // bridge and anything in Frameworks.
  const helpers = listed(path.join(resources, "bin"), (entry) =>
    entry.isFile(),
  );
  const binaries = helpers.filter((file) => !file.endsWith(".dylib"));
  const frameworks = [
    ...helpers.filter((file) => file.endsWith(".dylib")),
    ...listed(resources, (entry) => entry.name.endsWith(".dylib")),
    ...listed(path.join(app, "Contents/Frameworks")),
  ];
  // The desktop widget (build-macos-widget.mjs), plus any other extension
  // the bundle holds.
  const plugIns = listed(path.join(app, "Contents/PlugIns"));
  const executables = [app, ...binaries, ...plugIns];
  console.log(
    "Checking Developer ID signatures, Team ID, timestamps and Hardened Runtime.",
  );
  for (const file of [app, ...binaries, ...plugIns, ...frameworks, dmg]) {
    runCommand("codesign", ["--verify", "--deep", "--strict", file]);
    checkSignature(
      runCommand("codesign", ["--display", "--verbose=4", file]),
      teamID,
      executables.includes(file),
    );
  }
  runCommand("hdiutil", ["verify", dmg]);

  // Connections from the CI runners to Apple's notary and ticket services
  // sometimes drop or time out. Retry network errors only; any other xcrun
  // failure stops the release at once.
  const xcrun = (...args) => {
    for (let attempt = 0; ; attempt += 1) {
      try {
        return runCommand("xcrun", args);
      } catch (error) {
        const delay = retryDelaysMs[attempt];
        if (delay === undefined || !/NSURLErrorDomain/.test(error.message))
          throw error;
        console.warn(
          `${args[0]} ${args[1]} lost its connection to Apple; retrying in ${delay / 1000} s.`,
        );
        Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, delay);
      }
    }
  };
  const credentials = [
    "--keychain-profile",
    profile,
    ...(keychain ? ["--keychain", keychain] : []),
  ];
  const notary = (...args) => xcrun("notarytool", ...args, ...credentials);
  const logs = path.join(bundleDirectory, "notarization");
  mkdirSync(logs, { recursive: true });
  const sha256 = createHash("sha256").update(readFileSync(dmg)).digest("hex");
  let id = submissionID;
  if (id) {
    if (!submissionPattern.test(id))
      throw new Error("Invalid notarization submission ID.");
    const receipt = JSON.parse(
      readFileSync(path.join(logs, `${id}.submission.json`), "utf8"),
    );
    if (receipt.sha256 !== sha256)
      throw new Error(
        "The DMG has changed since this notarization submission; build and submit again.",
      );
  } else {
    console.log(`Submitting ${path.basename(dmg)} to Apple for notarization.`);
    const receipt = JSON.parse(
      notary("submit", dmg, "--output-format", "json"),
    );
    id = receipt.id;
    if (!submissionPattern.test(id ?? ""))
      throw new Error("Apple did not return a valid submission ID.");
    writeFileSync(
      path.join(logs, `${id}.submission.json`),
      JSON.stringify({ ...receipt, sha256, dmg: path.basename(dmg) }, null, 2) +
        "\n",
    );
  }
  console.log(`Notarization submission: ${id}. Waiting up to ${timeout}.`);
  let result;
  try {
    result = JSON.parse(
      notary("wait", id, "--timeout", timeout, "--output-format", "json"),
    );
  } catch (error) {
    // A rejected submission can make notarytool exit nonzero. Preserve its
    // diagnostic log too; a pending submission may not have a log yet.
    try {
      writeFileSync(path.join(logs, `${id}.log.json`), notary("log", id));
    } catch {
      console.warn(`No notarization log available yet for ${id}.`);
    }
    throw new Error(
      `Notarization has not completed successfully. Resume with macos:notarize --submission-id ${id}.\n${error.message}`,
      { cause: error },
    );
  }
  writeFileSync(
    path.join(logs, `${id}.result.json`),
    JSON.stringify(result, null, 2) + "\n",
  );
  const log = notary("log", id);
  writeFileSync(path.join(logs, `${id}.log.json`), log);
  if (result.status !== "Accepted") {
    throw new Error(
      `Apple notarization status: ${result.status}. See ${path.join(logs, `${id}.log.json`)}.\n${log}`,
    );
  }
  const issues = JSON.parse(log).issues;
  if (issues?.length)
    console.warn("Apple notarization issues:", JSON.stringify(issues, null, 2));

  // A DMG submission also produces a ticket for the enclosed app. Staple the
  // standalone app before archiving it, and the DMG before calculating hashes.
  for (const file of [app, dmg]) {
    xcrun("stapler", "staple", file);
    xcrun("stapler", "validate", file);
    runCommand("codesign", ["--verify", "--deep", "--strict", file]);
  }
  runCommand("spctl", ["--assess", "--type", "execute", "--verbose=2", app]);
  runCommand("spctl", [
    "--assess",
    "--type",
    "open",
    "--context",
    "context:primary-signature",
    "--verbose=2",
    dmg,
  ]);
  console.log(
    `Notarization Accepted; app and DMG tickets, signatures and Gatekeeper checks passed. Logs: ${logs}`,
  );
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    if (process.platform !== "darwin")
      throw new Error("macOS notarization must run on a Mac.");
    const { values } = parseArgs({
      options: {
        "bundle-dir": {
          type: "string",
          default: path.join(
            desktopDirectory,
            "build",
            `darwin-${process.arch === "arm64" ? "arm64" : "amd64"}`,
          ),
        },
        "submission-id": { type: "string" },
        timeout: { type: "string", default: "30m" },
      },
    });
    notarizeMacOS({
      bundleDirectory: path.resolve(values["bundle-dir"]),
      teamID: process.env.APPLE_TEAM_ID,
      profile: process.env.APPLE_NOTARY_PROFILE || "astrlink-notary",
      keychain: process.env.APPLE_NOTARY_KEYCHAIN,
      submissionID: values["submission-id"],
      timeout: values.timeout,
    });
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
