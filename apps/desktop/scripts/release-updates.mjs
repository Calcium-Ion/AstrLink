// Packages a release's updates. Every release carries two sets:
//
// - MyGo's update files, which `mygo build` writes with
//   MYGO_UPDATER_PRIVATE_KEY: update-<platform>.json, the signed
//   <slug>-<version>-<platform>.tar.gz archives and their deltas. The app's
//   updater (internal/updates) reads them. Linux also ships an AppImage
//   (build-appimage.mjs), which updates itself from
//   update-<platform>-appimage.json, and the Debian package, which an
//   install of it updates from update-<platform>-deb.json; both are signed
//   here as MyGo signs archives.
// - latest.json, the manifest that releases before the MyGo app read,
//   signed with their Minisign key (TAURI_SIGNING_PRIVATE_KEY). It moves
//   those installs onto this app: macOS replaces the app bundle with
//   AstrLink-macOS-<arch>.app.tar.gz, Windows runs the new installer, which
//   removes the earlier install (installer/windows.nsh), and an AppImage
//   replaces its file with the new AppImage.
import {
  createHash,
  createPrivateKey,
  createPublicKey,
  scryptSync,
  sign,
  verify,
} from "node:crypto";
import { execFileSync } from "node:child_process";
import {
  cpSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  renameSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const desktop = fileURLToPath(new URL("..", import.meta.url));
const repository = "Calcium-Ion/AstrLink";
const slug = "astrlink";
export const targets = [
  "darwin-aarch64",
  "darwin-x86_64",
  "windows-x86_64",
  "linux-x86_64",
];
// Release publishes these together. The slower Intel macOS build runs in
// release-macos-x86_64.yml, which attaches it to the published release.
export const releaseTargets = targets.filter(
  (target) => target !== "darwin-x86_64",
);
/** MyGo's name of each target, as in build/<platform> and update-<platform>.json. */
export const mygoPlatforms = {
  "darwin-aarch64": "darwin-arm64",
  "darwin-x86_64": "darwin-amd64",
  "windows-x86_64": "windows-amd64",
  "linux-x86_64": "linux-amd64",
};
/**
 * The AppImage of a MyGo platform, null for none: the name the AppImages of
 * earlier releases had, which their updater replaces itself with.
 */
export function appImageName(platform, version) {
  return platform === "linux-amd64"
    ? `AstrLink_${version}_amd64.AppImage`
    : null;
}
/** The MyGo manifest an AppImage of the platform updates from. */
export const appImageManifestName = (platform) =>
  `update-${platform}-appimage.json`;
/**
 * The Debian package of a MyGo platform as the release ships it, null for
 * none: `mygo build` gives the package the Debian version, where "~" sorts
 * a pre-release before its release, and GitHub renames the "~" (assetName).
 */
export function debName(platform, version) {
  if (platform !== "linux-amd64") return null;
  const dash = version.indexOf("-");
  const debian = (
    dash < 0
      ? version
      : `${version.slice(0, dash)}~${version.slice(dash + 1).replaceAll("-", ".")}`
  ).replaceAll("+", "~");
  return assetName(`${slug}_${debian}_amd64.deb`);
}
/** The MyGo manifest an install of the platform's Debian package updates from. */
export const debManifestName = (platform) => `update-${platform}-deb.json`;
const readJSON = (file) => JSON.parse(readFileSync(file, "utf8"));
const writeJSON = (file, value) =>
  writeFileSync(file, JSON.stringify(value, null, 2) + "\n");
const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
function requireValue(value, message) {
  if (!value) throw new Error(message);
  return value;
}
const releaseURL = (tag, file) =>
  `https://github.com/${repository}/releases/download/${encodeURIComponent(tag)}/${encodeURIComponent(file)}`;

export function releaseVersion(tag) {
  const version = tag.replace(/^v/, "");
  const numeric = "(?:0|[1-9][0-9]*)";
  const id = "(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)";
  requireValue(
    new RegExp(
      `^${numeric}\\.${numeric}\\.${numeric}(?:-${id}(?:\\.${id})*)?(?:\\+[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?$`,
    ).test(version),
    "Release tag must be a SemVer version, optionally prefixed with v",
  );
  return { version, prerelease: version.split("+")[0].includes("-") };
}

/** Desktop stable checks read GitHub's latest release, so only the highest stable version may hold it. */
export function newestStable(tag, releases) {
  const { version, prerelease } = releaseVersion(tag);
  if (prerelease) return false;
  const core = (value) => value.split(/[-+]/)[0].split(".").map(Number);
  const newer = (a, b) => {
    const [x, y] = [core(a), core(b)];
    const index = x.findIndex((part, i) => part !== y[i]);
    return index >= 0 && x[index] > y[index];
  };
  return !releases.some((release) => {
    if (release.draft || release.prerelease) return false;
    try {
      const other = releaseVersion(release.tag_name);
      return !other.prerelease && newer(other.version, version);
    } catch {
      return false;
    }
  });
}

/** Stamps the release version into package.json, which mygo.config.ts and Core's build read. */
export function stampVersion(root, tag) {
  const { version } = releaseVersion(tag);
  const file = path.join(root, "package.json"),
    data = readJSON(file);
  data.version = version;
  writeJSON(file, data);
}

/** Verify base64-wrapped Minisign signatures, including the trusted comment. */
export function verifyUpdateSignature(bytes, signature, publicKey) {
  const lines = Buffer.from(signature.trim(), "base64")
    .toString("utf8")
    .trim()
    .split(/\r?\n/);
  const keyLines = Buffer.from(publicKey.trim(), "base64")
    .toString("utf8")
    .trim()
    .split(/\r?\n/);
  const key = Buffer.from(keyLines[1] ?? "", "base64"),
    sig = Buffer.from(lines[1] ?? "", "base64");
  requireValue(
    key.length === 42 &&
      sig.length === 74 &&
      lines[2]?.startsWith("trusted comment: "),
    "Invalid update signature encoding",
  );
  requireValue(
    key.subarray(2, 10).equals(sig.subarray(2, 10)),
    "Update signature key does not match embedded public key",
  );
  const algorithm = sig.subarray(0, 2).toString();
  requireValue(
    algorithm === "ED" || algorithm === "Ed",
    "Unsupported update signature algorithm",
  );
  const verifier = createPublicKey({
    key: Buffer.concat([
      Buffer.from("302a300506032b6570032100", "hex"),
      key.subarray(10),
    ]),
    format: "der",
    type: "spki",
  });
  const message =
    algorithm === "ED"
      ? createHash("blake2b512").update(bytes).digest()
      : bytes;
  requireValue(
    verify(null, message, verifier, sig.subarray(10)),
    "Update artifact signature mismatch",
  );
  requireValue(
    verify(
      null,
      Buffer.concat([sig.subarray(10), Buffer.from(lines[2].slice(17))]),
      verifier,
      Buffer.from(lines[3] ?? "", "base64"),
    ),
    "Update trusted comment signature mismatch",
  );
}

/** libsodium's scrypt parameters for an opslimit and memlimit (pickparams). */
export function scryptParameters(opslimit, memlimit) {
  const ops = Math.max(opslimit, 32768);
  const r = 8;
  let logN = 1;
  if (ops < memlimit / 32) {
    const maxN = Math.floor(ops / (r * 4));
    while (logN < 63 && 2 ** logN <= maxN / 2) logN += 1;
    return { N: 2 ** logN, r, p: 1 };
  }
  const maxN = Math.floor(memlimit / (r * 128));
  while (logN < 63 && 2 ** logN <= maxN / 2) logN += 1;
  const maxrp = Math.min(Math.floor(ops / 4 / 2 ** logN), 0x3fffffff);
  return { N: 2 ** logN, r, p: Math.max(Math.floor(maxrp / r), 1) };
}

/**
 * The Ed25519 key of TAURI_SIGNING_PRIVATE_KEY: a base64 Minisign secret key
 * file, encrypted with scrypt under its password (possibly empty).
 */
export function minisignSecretKey(encoded, password = "") {
  const text = Buffer.from(encoded.trim(), "base64").toString("utf8");
  const line = text
    .trim()
    .split(/\r?\n/)
    .find((entry) => !entry.startsWith("untrusted comment:"));
  const bytes = Buffer.from(line ?? "", "base64");
  requireValue(
    bytes.length === 158 && bytes.subarray(0, 2).toString() === "Ed",
    "TAURI_SIGNING_PRIVATE_KEY is not a Minisign secret key",
  );
  const kdf = bytes.subarray(2, 4).toString("latin1");
  let keynum = Buffer.from(bytes.subarray(54));
  if (kdf === "Sc") {
    const { N, r, p } = scryptParameters(
      Number(bytes.readBigUInt64LE(38)),
      Number(bytes.readBigUInt64LE(46)),
    );
    const stream = scryptSync(
      Buffer.from(password, "utf8"),
      bytes.subarray(6, 38),
      keynum.length,
      { N, r, p, maxmem: 256 * N * r * p + 64 * 1024 * 1024 },
    );
    for (let i = 0; i < keynum.length; i += 1) keynum[i] ^= stream[i];
  } else {
    requireValue(kdf === "\0\0", "Unsupported Minisign key derivation");
  }
  const id = keynum.subarray(0, 8),
    seed = keynum.subarray(8, 40),
    publicKey = keynum.subarray(40, 72);
  const privateKey = createPrivateKey({
    key: Buffer.concat([
      Buffer.from("302e020100300506032b657004220420", "hex"),
      seed,
    ]),
    format: "der",
    type: "pkcs8",
  });
  // A wrong password decrypts to another seed, whose public key differs.
  requireValue(
    createPublicKey(privateKey)
      .export({ type: "spki", format: "der" })
      .subarray(-32)
      .equals(publicKey),
    "Unable to decrypt TAURI_SIGNING_PRIVATE_KEY: wrong password",
  );
  return { id: Buffer.from(id), privateKey };
}

/** Signs a file as `tauri signer sign` does: prehashed, with its name and time trusted. */
export function signUpdate(bytes, fileName, key, now = new Date()) {
  const signature = sign(
    null,
    createHash("blake2b512").update(bytes).digest(),
    key.privateKey,
  );
  const comment = `timestamp:${Math.floor(now.getTime() / 1000)}\tfile:${fileName}`;
  const global = sign(
    null,
    Buffer.concat([signature, Buffer.from(comment)]),
    key.privateKey,
  );
  return Buffer.from(
    `untrusted comment: signature from tauri secret key\n${Buffer.concat([Buffer.from("ED"), key.id, signature]).toString("base64")}\ntrusted comment: ${comment}\n${global.toString("base64")}\n`,
  ).toString("base64");
}

/** Checks a MyGo update signature: Ed25519 over the SHA-256 of the file. */
export function verifyMyGoSignature(bytes, signature, publicKey) {
  const key = Buffer.from(publicKey.trim(), "base64");
  requireValue(key.length === 32, "Invalid MyGo update public key");
  const verifier = createPublicKey({
    key: Buffer.concat([Buffer.from("302a300506032b6570032100", "hex"), key]),
    format: "der",
    type: "spki",
  });
  requireValue(
    verify(
      null,
      createHash("sha256").update(bytes).digest(),
      verifier,
      Buffer.from(signature ?? "", "base64"),
    ),
    "MyGo update signature mismatch",
  );
}

/**
 * The key of MYGO_UPDATER_PRIVATE_KEY: base64 of the 64-byte Ed25519 key
 * `mygo keygen` writes, its seed then its public key, which must be
 * MYGO_UPDATER_PUBLIC_KEY.
 */
export function mygoSecretKey(encoded, publicKey) {
  const bytes = Buffer.from(encoded?.trim() ?? "", "base64");
  requireValue(
    bytes.length === 64,
    "MYGO_UPDATER_PRIVATE_KEY is not a MyGo update key",
  );
  const privateKey = createPrivateKey({
    key: Buffer.concat([
      Buffer.from("302e020100300506032b657004220420", "hex"),
      bytes.subarray(0, 32),
    ]),
    format: "der",
    type: "pkcs8",
  });
  // Go signs with the public half of the key, so it must be the seed's.
  const derived = createPublicKey(privateKey)
    .export({ type: "spki", format: "der" })
    .subarray(-32);
  requireValue(
    derived.equals(bytes.subarray(32)) &&
      derived.equals(Buffer.from(publicKey?.trim() ?? "", "base64")),
    "MYGO_UPDATER_PRIVATE_KEY does not match MYGO_UPDATER_PUBLIC_KEY",
  );
  return privateKey;
}

/** Signs a file as `mygo build` signs archives: Ed25519 over its SHA-256. */
export function signMyGoUpdate(bytes, key) {
  return sign(null, createHash("sha256").update(bytes).digest(), key).toString(
    "base64",
  );
}

export function signingEnvironment(env = process.env) {
  for (const [name, kind] of [
    ["MYGO_UPDATER_PUBLIC_KEY", "variable"],
    ["MYGO_UPDATER_PRIVATE_KEY", "secret"],
    ["TAURI_UPDATER_PUBLIC_KEY", "variable"],
    ["TAURI_SIGNING_PRIVATE_KEY", "secret"],
  ])
    requireValue(env[name]?.trim(), `Missing Actions ${kind} ${name}`);
}

/** The files of a MyGo build of one platform that a release ships. */
export function releaseFiles(names, platform, version) {
  const archive = `${slug}-${version}-${platform}.tar.gz`;
  return names.filter(
    (name) =>
      name === `update-${platform}.json` ||
      name === archive ||
      (name.startsWith(`${slug}-`) &&
        name.endsWith(`-to-${version}-${platform}.delta`)) ||
      name === appImageName(platform, version) ||
      name === "install.sh" ||
      name === "install.ps1" ||
      /\.(dmg|deb)$/.test(name) ||
      name.startsWith(`${slug}-setup-`),
  );
}

/**
 * Verify the MyGo manifests of the release, update-<platform>.json and
 * those of the AppImage and the Debian package, against their files and
 * key, and return the names of the files they list.
 */
export function verifyMyGoUpdate(directory, platform, tag, publicKey) {
  const { version } = releaseVersion(tag);
  // A file of this release, of the size and signature the manifest gives.
  const verified = (entry) => {
    // MyGo escapes as Go's url.PathEscape does, which keeps a "+".
    const url = new URL(entry.url),
      parts = url.pathname.split("/").map(decodeURIComponent),
      name = parts.at(-1);
    requireValue(
      url.origin === "https://github.com" &&
        parts.join("/") === `/${repository}/releases/download/${tag}/${name}`,
      `MyGo update URL is not a file of ${tag}: ${platform}`,
    );
    const bytes = readFileSync(path.join(directory, name));
    requireValue(
      bytes.length === entry.size,
      `MyGo update size mismatch: ${name}`,
    );
    verifyMyGoSignature(bytes, entry.signature, publicKey);
    return name;
  };
  const manifest = readJSON(path.join(directory, `update-${platform}.json`));
  requireValue(
    manifest.version === version,
    `MyGo manifest version mismatch: ${platform}`,
  );
  const names = [manifest, ...(manifest.deltas ?? [])].map(verified);
  requireValue(
    names[0] === `${slug}-${version}-${platform}.tar.gz`,
    `Unexpected MyGo update archive: ${platform}`,
  );
  const appImage = appImageName(platform, version);
  if (appImage) {
    const image = readJSON(
      path.join(directory, appImageManifestName(platform)),
    );
    requireValue(
      image.version === version,
      `AppImage manifest version mismatch: ${platform}`,
    );
    // The whole AppImage replaces the file, with no deltas.
    requireValue(
      verified(image) === appImage && image.deltas === undefined,
      `Unexpected AppImage update: ${platform}`,
    );
    names.push(appImage);
  }
  const deb = debName(platform, version);
  if (deb) {
    const update = readJSON(path.join(directory, debManifestName(platform)));
    requireValue(
      update.version === version,
      `Debian package manifest version mismatch: ${platform}`,
    );
    // The whole package installs over the old one, with no deltas.
    requireValue(
      verified(update) === deb && update.deltas === undefined,
      `Unexpected Debian package update: ${platform}`,
    );
    names.push(deb);
  }
  return names;
}

export function stageUpdate(
  root,
  target,
  tag,
  env = process.env,
  now = new Date(),
) {
  signingEnvironment(env);
  requireValue(targets.includes(target), "Unsupported update target");
  const { version } = releaseVersion(tag);
  requireValue(
    readJSON(path.join(root, "package.json")).version === version,
    "Build version does not match release tag",
  );
  const platform = mygoPlatforms[target];
  const build = path.join(root, "build", platform);
  const output = path.join(root, "package");
  mkdirSync(output, { recursive: true });
  const shipped = releaseFiles(readdirSync(build), platform, version);
  for (const name of shipped)
    cpSync(path.join(build, name), path.join(output, name));
  // The AppImage and the Debian package update whole, each from its own
  // manifest, written as MyGo writes update-<platform>.json; its dates are
  // in seconds.
  const wholeFile = (name, manifestName) => {
    requireValue(existsSync(path.join(output, name)), `Missing ${name}`);
    const bytes = readFileSync(path.join(output, name));
    writeJSON(path.join(output, manifestName), {
      version,
      date: now.toISOString().replace(/\.\d+Z$/, "Z"),
      url: releaseURL(tag, name),
      size: bytes.length,
      signature: signMyGoUpdate(
        bytes,
        mygoSecretKey(
          env.MYGO_UPDATER_PRIVATE_KEY,
          env.MYGO_UPDATER_PUBLIC_KEY,
        ),
      ),
    });
  };
  const appImage = appImageName(platform, version);
  if (appImage) wholeFile(appImage, appImageManifestName(platform));
  const deb = debName(platform, version);
  if (deb) {
    // Shipped under the name GitHub gives it, which its manifest links.
    const built = shipped.find(
      (name) => name.endsWith(".deb") && assetName(name) === deb,
    );
    requireValue(built, `Missing ${deb}`);
    if (built !== deb)
      renameSync(path.join(output, built), path.join(output, deb));
    wholeFile(deb, debManifestName(platform));
  }
  verifyMyGoUpdate(output, platform, tag, env.MYGO_UPDATER_PUBLIC_KEY);

  let file;
  if (target.startsWith("darwin-")) {
    const arch = target === "darwin-aarch64" ? "arm64" : "x86_64";
    // The app as `mygo build` left it, notarized and stapled by
    // notarize-macos.mjs before this runs.
    file = path.join(output, `AstrLink-macOS-${arch}.app.tar.gz`);
    requireValue(
      existsSync(path.join(build, "AstrLink.app")),
      "Missing AstrLink.app",
    );
    execFileSync("tar", ["-czf", file, "-C", build, "AstrLink.app"], {
      env: { ...env, COPYFILE_DISABLE: "1" },
    });
  } else if (appImage) {
    file = path.join(output, appImage);
  } else {
    const installers = shipped.filter((name) =>
      name.startsWith(`${slug}-setup-`),
    );
    requireValue(
      installers.length === 1,
      "Expected exactly one update installer",
    );
    file = path.join(output, installers[0]);
  }
  const bytes = readFileSync(file);
  const signature = signUpdate(
    bytes,
    path.basename(file),
    minisignSecretKey(
      env.TAURI_SIGNING_PRIVATE_KEY,
      env.TAURI_SIGNING_PRIVATE_KEY_PASSWORD ?? "",
    ),
  );
  verifyUpdateSignature(bytes, signature, env.TAURI_UPDATER_PUBLIC_KEY);
  writeFileSync(`${file}.sig`, signature);
  writeJSON(path.join(output, `${target}.json`), {
    tag,
    version,
    target,
    platform,
    file: path.basename(file),
    signature,
    sha256: sha256(bytes),
  });
}

function filesIn(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) =>
    entry.isDirectory()
      ? filesIn(path.join(directory, entry.name))
      : [path.join(directory, entry.name)],
  );
}

/**
 * Verify one staged platform: its MyGo updates and the package that moves
 * earlier releases onto it. Returns the platform's latest.json entry.
 */
function verifiedPlatform(files, target, tag, version, keys) {
  const fragments = files.filter(
    (file) => path.basename(file) === `${target}.json`,
  );
  requireValue(
    fragments.length === 1,
    `Missing or duplicate platform: ${target}`,
  );
  const fragment = readJSON(fragments[0]),
    directory = path.dirname(fragments[0]);
  requireValue(
    fragment.version === version &&
      fragment.tag === tag &&
      fragment.target === target &&
      fragment.platform === mygoPlatforms[target],
    `Version or target mismatch: ${target}`,
  );
  // Manifests link their files by the names they were built with.
  for (const name of verifyMyGoUpdate(
    directory,
    fragment.platform,
    tag,
    keys.mygo,
  ))
    requireValue(
      assetName(name) === name,
      `GitHub would rename a MyGo update file: ${name}`,
    );
  requireValue(
    typeof fragment.file === "string" &&
      path.basename(fragment.file) === fragment.file,
    "Invalid artifact filename",
  );
  const file = path.join(directory, fragment.file),
    bytes = readFileSync(file);
  requireValue(
    sha256(bytes) === fragment.sha256,
    `Artifact checksum mismatch: ${target}`,
  );
  requireValue(
    readFileSync(`${file}.sig`, "utf8").trim() === fragment.signature,
    `Signature sidecar mismatch: ${target}`,
  );
  verifyUpdateSignature(bytes, fragment.signature, keys.bridge);
  return { signature: fragment.signature, url: releaseURL(tag, fragment.file) };
}

/**
 * The name GitHub gives an uploaded file: it renames characters such as the
 * "~" of a pre-release Debian package (astrlink_1.0.0~rc.1_amd64.deb).
 */
export const assetName = (name) => name.replace(/[^A-Za-z0-9._-]/g, ".");

/** The release's files by asset name; the same file built twice ships once. */
function releaseAssets(files) {
  const assets = new Map();
  for (const file of files.filter((file) =>
    /\.(dmg|deb|exe|AppImage|tar\.gz|sig|delta)$|(^|[\\/])(update-[^\\/]+\.json|install\.sh|install\.ps1)$/.test(
      file,
    ),
  )) {
    const name = assetName(path.basename(file));
    const listed = assets.get(name);
    requireValue(
      !listed || sha256(readFileSync(listed)) === sha256(readFileSync(file)),
      `Duplicate release filename: ${name}`,
    );
    assets.set(name, file);
  }
  return assets;
}

/** Write assets, latest.json and a SHA256SUMS that adds both to `sums`. */
function writeRelease(output, assets, manifest, sums = new Map()) {
  mkdirSync(output, { recursive: true });
  for (const [name, file] of assets) cpSync(file, path.join(output, name));
  writeJSON(path.join(output, "latest.json"), manifest);
  for (const name of [...assets.keys(), "latest.json"])
    sums.set(name, sha256(readFileSync(path.join(output, name))));
  writeFileSync(
    path.join(output, "SHA256SUMS"),
    [...sums.keys()]
      .sort()
      .map((name) => `${sums.get(name)}  ${name}\n`)
      .join(""),
  );
}

export function collectRelease(
  input,
  output,
  tag,
  keys,
  notes = "",
  now = new Date(),
) {
  const { version } = releaseVersion(tag),
    files = filesIn(input);
  const platforms = {};
  for (const target of releaseTargets)
    platforms[target] = verifiedPlatform(files, target, tag, version, keys);
  // All validation precedes creating the publish directory or touching GitHub.
  const assets = releaseAssets(files);
  const manifest = { version, notes, pub_date: now.toISOString(), platforms };
  writeRelease(output, assets, manifest);
  return manifest;
}

/**
 * Add a platform built outside Release to the manifest and checksums that
 * Release published. Retrying with the same package is safe; replacing a
 * package that the release already lists is refused.
 */
export function extendRelease(input, published, output, tag, target, keys) {
  requireValue(targets.includes(target), "Unsupported update target");
  const { version } = releaseVersion(tag),
    files = filesIn(input);
  const manifest = readJSON(path.join(published, "latest.json"));
  requireValue(
    manifest.version === version,
    "Published manifest version does not match release tag",
  );
  const platform = verifiedPlatform(files, target, tag, version, keys),
    listed = manifest.platforms[target];
  requireValue(
    !listed ||
      (listed.signature === platform.signature && listed.url === platform.url),
    `Release already lists a different ${target} package`,
  );
  const sums = new Map();
  for (const line of readFileSync(path.join(published, "SHA256SUMS"), "utf8")
    .split("\n")
    .filter(Boolean)) {
    const [, sum, name] = requireValue(
      /^([0-9a-f]{64}) {2}(.+)$/.exec(line),
      "Invalid published SHA256SUMS",
    );
    sums.set(name, sum);
  }
  const assets = releaseAssets(files);
  for (const [name, file] of assets)
    requireValue(
      !sums.has(name) || sums.get(name) === sha256(readFileSync(file)),
      `Release already has a different ${name}`,
    );
  manifest.platforms[target] = platform;
  writeRelease(output, assets, manifest, sums);
  return manifest;
}

function runGh(args) {
  return execFileSync("gh", args, {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
    // Every release with its notes, from `releases --paginate`, outgrew
    // the default 1 MiB.
    maxBuffer: 256 << 20,
  });
}
function uploadAssets(tag, assets, gh) {
  gh(["release", "upload", tag, "--repo", repository, "--clobber", ...assets]);
  const remote = JSON.parse(
    gh(["release", "view", tag, "--repo", repository, "--json", "assets"]),
  );
  for (const asset of assets)
    requireValue(
      remote.assets.some(
        (a) =>
          a.name === path.basename(asset) &&
          a.size === readFileSync(asset).length,
      ),
      `Release upload incomplete: ${path.basename(asset)}`,
    );
}
/**
 * Release uploads latest.json after every package it lists, so a public
 * release that lists latest.json is complete, even one published before
 * Release ran.
 */
function releaseComplete(draft, assets) {
  return !draft && assets.some((asset) => asset.name === "latest.json");
}
function releaseKeys(env) {
  return {
    bridge: requireValue(
      env.TAURI_UPDATER_PUBLIC_KEY,
      "Missing update public key",
    ),
    mygo: requireValue(
      env.MYGO_UPDATER_PUBLIC_KEY,
      "Missing MyGo update public key",
    ),
  };
}
/** The manifests update clients read, uploaded after the files they list. */
const manifestNames = (output) =>
  readdirSync(output).filter(
    (name) => name === "latest.json" || /^update-.+\.json$/.test(name),
  );
export function publishRelease(
  input,
  output,
  tag,
  env = process.env,
  gh = runGh,
) {
  requireValue(
    env.GITHUB_REPOSITORY === repository,
    "Release publishing is restricted to the project repository",
  );
  const { prerelease } = releaseVersion(tag);
  const releases = JSON.parse(
    gh(["api", `repos/${repository}/releases`, "--paginate", "--slurp"]),
  ).flat();
  // A release written ahead of CI, as a draft or published, keeps its title
  // and notes and gets the packages. One listing latest.json is never replaced.
  const existing = releases.find((release) => release.tag_name === tag);
  requireValue(
    !existing || !releaseComplete(existing.draft, existing.assets),
    "Refusing to overwrite an already published release",
  );
  const written = existing?.body?.trim() ? existing.body : undefined;
  const generated = () =>
    JSON.parse(
      gh([
        "api",
        `repos/${repository}/releases/generate-notes`,
        "-f",
        `tag_name=${tag}`,
        "-f",
        `target_commitish=${env.GITHUB_SHA}`,
      ]),
    ).body;
  const notes = written ?? generated();
  collectRelease(input, output, tag, releaseKeys(env), notes);
  const notesFile = path.join(output, "release-notes.txt");
  writeFileSync(notesFile, notes);
  if (!existing)
    gh([
      "release",
      "create",
      tag,
      "--repo",
      repository,
      "--draft",
      "--verify-tag",
      "--title",
      tag,
      "--notes-file",
      notesFile,
    ]);
  // A temporary draft keeps incomplete uploads invisible. No human approval is required.
  // A release published ahead of CI is public already, so the manifests go
  // up after the packages they list, latest.json last.
  const manifests = manifestNames(output);
  uploadAssets(
    tag,
    readdirSync(output)
      .filter(
        (name) => !manifests.includes(name) && name !== "release-notes.txt",
      )
      .map((name) => path.join(output, name)),
    gh,
  );
  uploadAssets(
    tag,
    manifests
      .filter((name) => name !== "latest.json")
      .map((name) => path.join(output, name)),
    gh,
  );
  uploadAssets(tag, [path.join(output, "latest.json")], gh);
  gh([
    "release",
    "edit",
    tag,
    "--repo",
    repository,
    "--draft=false",
    `--prerelease=${prerelease}`,
    `--latest=${newestStable(tag, releases)}`,
    ...(written ? [] : ["--notes-file", notesFile]),
  ]);
}

export function attachRelease(
  input,
  output,
  tag,
  target,
  env = process.env,
  gh = runGh,
) {
  requireValue(
    env.GITHUB_REPOSITORY === repository,
    "Release publishing is restricted to the project repository",
  );
  // Release may still be uploading, to its draft or to a release published
  // ahead of CI.
  const { isDraft, assets } = JSON.parse(
    gh([
      "release",
      "view",
      tag,
      "--repo",
      repository,
      "--json",
      "isDraft,assets",
    ]),
  );
  requireValue(
    releaseComplete(isDraft, assets),
    "Release is not published yet",
  );
  const published = mkdtempSync(path.join(os.tmpdir(), "astrlink-release-"));
  gh([
    "release",
    "download",
    tag,
    "--repo",
    repository,
    "--dir",
    published,
    "--pattern",
    "latest.json",
    "--pattern",
    "SHA256SUMS",
  ]);
  extendRelease(input, published, output, tag, target, releaseKeys(env));
  // Upload the packages before the manifests point update clients at them.
  const manifests = [...manifestNames(output), "SHA256SUMS"];
  uploadAssets(
    tag,
    readdirSync(output)
      .filter((name) => !manifests.includes(name))
      .map((name) => path.join(output, name)),
    gh,
  );
  uploadAssets(
    tag,
    manifests.map((name) => path.join(output, name)),
    gh,
  );
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const [command, ...args] = process.argv.slice(2);
  try {
    if (command === "version") {
      const data = releaseVersion(args[0]);
      if (process.env.GITHUB_OUTPUT)
        writeFileSync(
          process.env.GITHUB_OUTPUT,
          `version=${data.version}\nprerelease=${data.prerelease}\n`,
          { flag: "a" },
        );
    } else if (command === "stamp") stampVersion(desktop, args[0]);
    else if (command === "preflight") signingEnvironment();
    else if (command === "stage") stageUpdate(desktop, args[0], args[1]);
    else if (command === "publish") publishRelease(args[0], args[1], args[2]);
    else if (command === "attach")
      attachRelease(args[0], args[1], args[2], args[3]);
    else
      throw new Error(
        "Expected version, stamp, preflight, stage, publish or attach command",
      );
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
