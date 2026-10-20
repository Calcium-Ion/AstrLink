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
const config = JSON.parse(
  readFileSync(
    path.join(desktopDirectory, "src-tauri/tauri.conf.json"),
    "utf8",
  ),
);

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
  const app = path.join(bundleDirectory, "macos", `${config.productName}.app`);
  if (!existsSync(app)) throw new Error(`Missing app bundle: ${app}`);
  const images = readdirSync(path.join(bundleDirectory, "dmg")).filter((name) =>
    name.endsWith(".dmg"),
  );
  if (images.length !== 1)
    throw new Error(
      "Expected exactly one DMG; remove stale installers before building.",
    );
  const dmg = path.join(bundleDirectory, "dmg", images[0]);
  const binaries = (config.bundle.externalBin ?? []).map((binary) =>
    path.join(app, "Contents/MacOS", path.basename(binary)),
  );
  // Apple silicon bundles add the Core ML runtime through a generated config
  // (see build-sidecar.mjs), so check what the bundle holds as well as what
  // tauri.conf.json lists.
  const frameworksDirectory = path.join(app, "Contents/Frameworks");
  const frameworks = [
    ...new Set([
      ...(config.bundle.macOS.frameworks ?? []).map((framework) =>
        path.basename(framework),
      ),
      ...(existsSync(frameworksDirectory)
        ? readdirSync(frameworksDirectory)
        : []),
    ]),
  ].map((name) => path.join(frameworksDirectory, name));
  console.log(
    "Checking Developer ID signatures, Team ID, timestamps and Hardened Runtime.",
  );
  for (const file of [app, ...binaries, ...frameworks, dmg]) {
    runCommand("codesign", ["--verify", "--deep", "--strict", file]);
    checkSignature(
      runCommand("codesign", ["--display", "--verbose=4", file]),
      teamID,
      file === app || binaries.includes(file),
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
            "src-tauri/target/release/bundle",
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
