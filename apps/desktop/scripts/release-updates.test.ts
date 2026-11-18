import { execFileSync } from "node:child_process";
import {
  createHash,
  createPublicKey,
  generateKeyPairSync,
  randomBytes,
  scryptSync,
  sign,
  verify,
} from "node:crypto";
import {
  cpSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import {
  appImageManifestName,
  appImageName,
  debManifestName,
  debName,
  assetName,
  attachRelease,
  collectRelease,
  extendRelease,
  minisignSecretKey,
  mygoPlatforms,
  mygoSecretKey,
  newestStable,
  publishRelease,
  releaseTargets,
  releaseVersion,
  scryptParameters,
  signMyGoUpdate,
  signUpdate,
  signingEnvironment,
  stageUpdate,
  stampVersion,
  targets,
  verifyMyGoUpdate,
  verifyUpdateSignature,
} from "./release-updates.mjs";

type Target = keyof typeof mygoPlatforms;

const sha256 = (bytes: Buffer) =>
  createHash("sha256").update(bytes).digest("hex");
const rawKey = (key: { export: (options: object) => Buffer }, type: string) =>
  key.export({ type, format: "der" }).subarray(-32);

/** A Minisign key pair as `tauri signer generate` writes it. */
function minisignKeys(password = "") {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const id = randomBytes(8),
    pk = rawKey(publicKey, "spki");
  // Key id, secret key (seed and public key) and a checksum, which the
  // release script does not need.
  const keynum = Buffer.concat([
    id,
    rawKey(privateKey, "pkcs8"),
    pk,
    randomBytes(32),
  ]);
  const salt = randomBytes(32),
    limits = Buffer.alloc(16);
  limits.writeBigUInt64LE(1048576n, 0);
  limits.writeBigUInt64LE(33554432n, 8);
  const stream = scryptSync(password, salt, keynum.length, {
    N: 32768,
    r: 8,
    p: 1,
    maxmem: 64 * 1024 * 1024,
  });
  for (let i = 0; i < keynum.length; i += 1) keynum[i] ^= stream[i];
  const blob = Buffer.concat([Buffer.from("EdScB2"), salt, limits, keynum]);
  return {
    secret: Buffer.from(
      `untrusted comment: rsign encrypted secret key\n${blob.toString("base64")}\n`,
    ).toString("base64"),
    publicKey: Buffer.from(
      `untrusted comment: minisign public key: ${id.toString("hex")}\n${Buffer.concat([Buffer.from("Ed"), id, pk]).toString("base64")}\n`,
    ).toString("base64"),
  };
}

/** A MyGo key pair as `mygo keygen` writes it: the seed and public key. */
function mygoKeys() {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const pk = rawKey(publicKey, "spki");
  return {
    privateKey,
    publicKey: pk.toString("base64"),
    secret: Buffer.concat([rawKey(privateKey, "pkcs8"), pk]).toString("base64"),
  };
}

/** Both release keys, as Actions passes them to release-updates.mjs. */
function releaseKeys() {
  const bridge = minisignKeys();
  const mygo = mygoKeys();
  return {
    mygo: mygo.privateKey,
    env: {
      MYGO_UPDATER_PUBLIC_KEY: mygo.publicKey,
      MYGO_UPDATER_PRIVATE_KEY: mygo.secret,
      TAURI_UPDATER_PUBLIC_KEY: bridge.publicKey,
      TAURI_SIGNING_PRIVATE_KEY: bridge.secret,
    },
  };
}
type Keys = ReturnType<typeof releaseKeys>;

const download = (tag: string, name: string) =>
  `https://github.com/Calcium-Ion/AstrLink/releases/download/${tag}/${name}`;

/** What `mygo build` leaves in build/<platform> with updates. */
function mygoBuild(
  root: string,
  target: Target,
  keys: Keys,
  tag = "v1.0.0",
  version = "1.0.0",
) {
  const platform = mygoPlatforms[target],
    arch = platform.split("-")[1];
  const build = path.join(root, "build", platform);
  mkdirSync(build, { recursive: true });
  const write = (name: string, text: string) => {
    writeFileSync(path.join(build, name), text);
    return readFileSync(path.join(build, name));
  };
  const signed = (name: string, text: string) => {
    const bytes = write(name, text);
    return {
      url: download(tag, name),
      size: bytes.length,
      signature: sign(
        null,
        createHash("sha256").update(bytes).digest(),
        keys.mygo,
      ).toString("base64"),
    };
  };
  const archive = signed(
    `astrlink-${version}-${platform}.tar.gz`,
    `archive ${platform}`,
  );
  const delta = signed(
    `astrlink-0.9.0-to-${version}-${platform}.delta`,
    `delta ${platform}`,
  );
  write(
    `update-${platform}.json`,
    JSON.stringify({
      version,
      date: "2026-11-13T00:00:00Z",
      ...archive,
      deltas: [{ from: "0.9.0", ...delta }],
      previous: [
        {
          version: "0.9.0",
          url: download("v0.9.0", `astrlink-0.9.0-${platform}.tar.gz`),
          size: 1,
          signature: "earlier",
        },
      ],
    }),
  );
  // MyGo's own checksums of the platform; the release writes one for all.
  write("SHA256SUMS", "mygo checksums\n");
  if (target.startsWith("darwin-")) {
    mkdirSync(path.join(build, "AstrLink.app/Contents"), { recursive: true });
    write("AstrLink.app/Contents/Info.plist", "app bundle");
    write(`astrlink-${version}-${arch}.dmg`, `disk image ${arch}`);
    write("install.sh", "install script");
  } else if (target.startsWith("windows-")) {
    write("AstrLink.exe", "unpackaged app");
    write(`astrlink-setup-${version}-${arch}.exe`, `installer ${arch}`);
    write("install.ps1", "install script");
  } else {
    // MyGo gives the package its Debian version: "~" before a pre-release.
    write(
      `astrlink_${version.replace("-", "~")}_${arch}.deb`,
      `package ${arch}`,
    );
    write("install.sh", "install script");
    // build-appimage.mjs, after `mygo build`.
    write(`AstrLink_${version}_${arch}.AppImage`, `AppImage ${arch}`);
  }
}

/** Builds and stages targets as their release jobs do; returns `package`. */
function staged(keys: Keys, list: readonly Target[], tag = "v1.0.0") {
  const root = directory();
  writeFileSync(
    path.join(root, "package.json"),
    JSON.stringify({ version: releaseVersion(tag).version }),
  );
  for (const target of list)
    mygoBuild(root, target, keys, tag, releaseVersion(tag).version);
  for (const target of list) stageUpdate(root, target, tag, keys.env);
  return path.join(root, "package");
}

/** Answers the gh calls of a Release publish and keeps what it uploads. */
function fakeGitHub(release?: object) {
  const calls: string[][] = [];
  const uploaded = new Map<string, number>();
  const gh = (args: string[]) => {
    calls.push(args);
    const [command, action] = args;
    if (command === "api")
      return JSON.stringify(
        action.endsWith("/generate-notes")
          ? { body: "Generated notes" }
          : [release ? [release] : []],
      );
    if (action === "upload")
      for (const file of args.slice(6))
        uploaded.set(path.basename(file), readFileSync(file).length);
    if (action === "view")
      return JSON.stringify({
        assets: [...uploaded].map(([name, size]) => ({ name, size })),
      });
    return "";
  };
  const uploads = () =>
    calls
      .filter(([, action]) => action === "upload")
      .map((args) => args.slice(6).map((file) => path.basename(file)));
  return { gh, calls, uploads };
}
const releaseEnv = (keys: Keys) => ({
  ...keys.env,
  GITHUB_REPOSITORY: "Calcium-Ion/AstrLink",
  GITHUB_SHA: "0123456789abcdef",
});
const publicKeys = (keys: Keys) => ({
  bridge: keys.env.TAURI_UPDATER_PUBLIC_KEY,
  mygo: keys.env.MYGO_UPDATER_PUBLIC_KEY,
});
const manifestNotes = (output: string) =>
  JSON.parse(readFileSync(path.join(output, "latest.json"), "utf8")).notes;
const temporary: string[] = [];
const directory = () => {
  const dir = mkdtempSync(path.join(os.tmpdir(), "astrlink-update-test-"));
  temporary.push(dir);
  return dir;
};
afterEach(() => {
  for (const dir of temporary.splice(0))
    rmSync(dir, { recursive: true, force: true });
});

describe("release updates", () => {
  it("ships MyGo updates and moves earlier installs onto them", () => {
    const keys = releaseKeys(),
      packages = staged(keys, targets as Target[]);
    const files = readdirSync(packages);
    // The app bundle as one archive for earlier macOS releases, the
    // installer for earlier Windows ones and the AppImage for AppImages.
    for (const name of [
      "AstrLink-macOS-arm64.app.tar.gz.sig",
      "AstrLink-macOS-x86_64.app.tar.gz.sig",
      "astrlink-setup-1.0.0-amd64.exe.sig",
      "AstrLink_1.0.0_amd64.AppImage",
      "AstrLink_1.0.0_amd64.AppImage.sig",
      "update-linux-amd64.json",
      "update-linux-amd64-appimage.json",
      "update-linux-amd64-deb.json",
      "astrlink_1.0.0_amd64.deb",
      "astrlink-0.9.0-to-1.0.0-darwin-arm64.delta",
    ])
      expect(files).toContain(name);
    expect(files).not.toContain("AstrLink.exe");
    expect(files).not.toContain("SHA256SUMS");
    expect(files.filter((name) => name.includes("appimage"))).toEqual([
      "update-linux-amd64-appimage.json",
    ]);
    expect(
      JSON.parse(readFileSync(path.join(packages, "linux-x86_64.json"), "utf8"))
        .file,
    ).toBe("AstrLink_1.0.0_amd64.AppImage");
    expect(
      execFileSync(
        "tar",
        ["-tzf", path.join(packages, "AstrLink-macOS-arm64.app.tar.gz")],
        { encoding: "utf8" },
      ),
    ).toContain("AstrLink.app/Contents/Info.plist");

    // The AppImage updates itself from its own MyGo manifest, signed as
    // MyGo signs archives: Ed25519 over the file's SHA-256.
    const appImage = readFileSync(
      path.join(packages, "AstrLink_1.0.0_amd64.AppImage"),
    );
    const update = JSON.parse(
      readFileSync(path.join(packages, appImageManifestName("linux-amd64")), {
        encoding: "utf8",
      }),
    );
    expect(Object.keys(update)).toEqual([
      "version",
      "date",
      "url",
      "size",
      "signature",
    ]);
    expect(update).toMatchObject({
      version: "1.0.0",
      url: download("v1.0.0", "AstrLink_1.0.0_amd64.AppImage"),
      size: appImage.length,
    });
    expect(update.date).toMatch(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/);
    expect(
      verify(
        null,
        createHash("sha256").update(appImage).digest(),
        createPublicKey(keys.mygo),
        Buffer.from(update.signature, "base64"),
      ),
    ).toBe(true);
    expect(
      verifyMyGoUpdate(
        packages,
        "linux-amd64",
        "v1.0.0",
        keys.env.MYGO_UPDATER_PUBLIC_KEY,
      ),
    ).toEqual([
      "astrlink-1.0.0-linux-amd64.tar.gz",
      "astrlink-0.9.0-to-1.0.0-linux-amd64.delta",
      "AstrLink_1.0.0_amd64.AppImage",
      "astrlink_1.0.0_amd64.deb",
    ]);
    // The Debian package installs whole from its own manifest, signed the
    // same way.
    const deb = readFileSync(path.join(packages, "astrlink_1.0.0_amd64.deb"));
    const debUpdate = JSON.parse(
      readFileSync(path.join(packages, debManifestName("linux-amd64")), {
        encoding: "utf8",
      }),
    );
    expect(debUpdate).toMatchObject({
      version: "1.0.0",
      url: download("v1.0.0", "astrlink_1.0.0_amd64.deb"),
      size: deb.length,
    });
    expect(debUpdate.deltas).toBeUndefined();
    expect(
      verify(
        null,
        createHash("sha256").update(deb).digest(),
        createPublicKey(keys.mygo),
        Buffer.from(debUpdate.signature, "base64"),
      ),
    ).toBe(true);

    const release = path.join(directory(), "release");
    const manifest = collectRelease(
      packages,
      release,
      "v1.0.0",
      publicKeys(keys),
      "Notes",
    );
    expect(Object.keys(manifest.platforms)).toEqual(releaseTargets);
    expect(manifest.platforms["windows-x86_64"].url).toBe(
      download("v1.0.0", "astrlink-setup-1.0.0-amd64.exe"),
    );
    // Earlier AppImages replace themselves with the new one.
    const linux = manifest.platforms["linux-x86_64"];
    expect(linux.url).toBe(download("v1.0.0", "AstrLink_1.0.0_amd64.AppImage"));
    expect(() =>
      verifyUpdateSignature(
        appImage,
        linux.signature,
        keys.env.TAURI_UPDATER_PUBLIC_KEY,
      ),
    ).not.toThrow();
    expect(Buffer.from(linux.signature, "base64").toString()).toContain(
      "\tfile:AstrLink_1.0.0_amd64.AppImage\n",
    );
    const published = readdirSync(release);
    for (const name of [
      "update-darwin-arm64.json",
      "update-windows-amd64.json",
      "update-linux-amd64.json",
      "update-linux-amd64-appimage.json",
      "update-linux-amd64-deb.json",
      "AstrLink_1.0.0_amd64.AppImage",
      "AstrLink_1.0.0_amd64.AppImage.sig",
    ])
      expect(published).toContain(name);
    const sums = readFileSync(path.join(release, "SHA256SUMS"), "utf8");
    for (const name of published.filter((name) => name !== "SHA256SUMS"))
      expect(sums).toContain(
        `${sha256(readFileSync(path.join(release, name)))}  ${name}\n`,
      );

    const extended = extendRelease(
      packages,
      release,
      path.join(directory(), "attach"),
      "v1.0.0",
      "darwin-x86_64",
      publicKeys(keys),
    );
    expect(Object.keys(extended.platforms).sort()).toEqual([...targets].sort());
  });

  it("links a pre-release Debian package by the name GitHub gives it", () => {
    const keys = releaseKeys(),
      packages = staged(keys, ["linux-x86_64"], "v1.0.0-rc.1");
    const update = JSON.parse(
      readFileSync(path.join(packages, debManifestName("linux-amd64")), "utf8"),
    );
    expect(update.url).toBe(
      download("v1.0.0-rc.1", "astrlink_1.0.0.rc.1_amd64.deb"),
    );
    expect(readdirSync(packages)).toContain("astrlink_1.0.0.rc.1_amd64.deb");
    expect(readdirSync(packages)).not.toContain(
      "astrlink_1.0.0~rc.1_amd64.deb",
    );
  });

  it("ships files under the names GitHub gives them", () => {
    expect(assetName("astrlink_1.0.0~rc.1_amd64.deb")).toBe(
      "astrlink_1.0.0.rc.1_amd64.deb",
    );
    expect(assetName("astrlink-setup-1.0.0-rc.1-amd64.exe")).toBe(
      "astrlink-setup-1.0.0-rc.1-amd64.exe",
    );
    const keys = releaseKeys(),
      packages = staged(keys, releaseTargets as Target[], "v1.0.0-rc.1");
    const release = path.join(directory(), "release");
    collectRelease(packages, release, "v1.0.0-rc.1", publicKeys(keys));
    expect(readdirSync(release)).toContain("astrlink_1.0.0.rc.1_amd64.deb");
    expect(readFileSync(path.join(release, "SHA256SUMS"), "utf8")).toContain(
      "  astrlink_1.0.0.rc.1_amd64.deb\n",
    );
  });

  it("decrypts the Tauri signing key and signs as its signer does", () => {
    expect(scryptParameters(1048576, 33554432)).toEqual({
      N: 32768,
      r: 8,
      p: 1,
    });
    // Minisign's own (sensitive) limits.
    expect(scryptParameters(33554432, 1073741824)).toEqual({
      N: 1048576,
      r: 8,
      p: 1,
    });
    const keys = minisignKeys("passphrase"),
      bytes = Buffer.from("signed installer");
    expect(() => minisignSecretKey(keys.secret, "")).toThrow("wrong password");
    expect(() => minisignSecretKey("bm90IGEga2V5")).toThrow(
      "not a Minisign secret key",
    );
    const signature = signUpdate(
      bytes,
      "AstrLink-macOS-arm64.app.tar.gz",
      minisignSecretKey(keys.secret, "passphrase"),
      new Date(1_700_000_000_000),
    );
    expect(() =>
      verifyUpdateSignature(bytes, signature, keys.publicKey),
    ).not.toThrow();
    expect(Buffer.from(signature, "base64").toString()).toContain(
      "trusted comment: timestamp:1700000000\tfile:AstrLink-macOS-arm64.app.tar.gz\n",
    );
    expect(() =>
      verifyUpdateSignature(bytes, signature, minisignKeys().publicKey),
    ).toThrow("does not match");
  });

  it("stages nothing a release would not verify", () => {
    const keys = releaseKeys(),
      root = directory();
    writeFileSync(
      path.join(root, "package.json"),
      JSON.stringify({ version: "1.0.0" }),
    );
    expect(() =>
      stageUpdate(root, "darwin-aarch64", "v1.0.1", keys.env),
    ).toThrow("does not match release tag");
    // Signed by another key, or for another release.
    mygoBuild(root, "windows-x86_64", { ...releaseKeys(), env: keys.env });
    expect(() =>
      stageUpdate(root, "windows-x86_64", "v1.0.0", keys.env),
    ).toThrow("MyGo update signature mismatch");
    mygoBuild(root, "linux-x86_64", keys, "v0.9.0");
    expect(() => stageUpdate(root, "linux-x86_64", "v1.0.0", keys.env)).toThrow(
      "not a file of v1.0.0",
    );
    expect(() =>
      stageUpdate(root, "darwin-aarch64", "v1.0.0", {
        ...keys.env,
        TAURI_SIGNING_PRIVATE_KEY: "",
      }),
    ).toThrow("TAURI_SIGNING_PRIVATE_KEY");

    // The AppImage, which the Linux build must hold, is signed with the
    // MyGo key of the public key.
    const linux = () => {
      const dir = directory();
      writeFileSync(
        path.join(dir, "package.json"),
        JSON.stringify({ version: "1.0.0" }),
      );
      mygoBuild(dir, "linux-x86_64", keys);
      return dir;
    };
    expect(() =>
      stageUpdate(linux(), "linux-x86_64", "v1.0.0", {
        ...keys.env,
        MYGO_UPDATER_PRIVATE_KEY: releaseKeys().env.MYGO_UPDATER_PRIVATE_KEY,
      }),
    ).toThrow("does not match MYGO_UPDATER_PUBLIC_KEY");
    const missing = linux();
    rmSync(
      path.join(missing, "build/linux-amd64/AstrLink_1.0.0_amd64.AppImage"),
    );
    expect(() =>
      stageUpdate(missing, "linux-x86_64", "v1.0.0", keys.env),
    ).toThrow("Missing AstrLink_1.0.0_amd64.AppImage");

    const packages = staged(keys, ["linux-x86_64"]),
      manifest = path.join(packages, appImageManifestName("linux-amd64")),
      written = readFileSync(manifest, "utf8");
    const tampered = (
      change: (update: Record<string, unknown>) => void,
      failure: string,
    ) => {
      const update = JSON.parse(written);
      change(update);
      writeFileSync(manifest, JSON.stringify(update));
      expect(() =>
        verifyMyGoUpdate(
          packages,
          "linux-amd64",
          "v1.0.0",
          keys.env.MYGO_UPDATER_PUBLIC_KEY,
        ),
      ).toThrow(failure);
    };
    tampered((update) => {
      update.version = "0.9.0";
    }, "AppImage manifest version mismatch");
    tampered((update) => {
      update.deltas = [];
    }, "Unexpected AppImage update");
    tampered((update) => {
      update.signature = signMyGoUpdate(Buffer.from("other"), keys.mygo);
    }, "MyGo update signature mismatch");

    // The Debian package, which the Linux build must hold too.
    const noDeb = linux();
    rmSync(path.join(noDeb, "build/linux-amd64/astrlink_1.0.0_amd64.deb"));
    expect(() =>
      stageUpdate(noDeb, "linux-x86_64", "v1.0.0", keys.env),
    ).toThrow("Missing astrlink_1.0.0_amd64.deb");
    const debManifest = path.join(packages, debManifestName("linux-amd64")),
      debWritten = readFileSync(debManifest, "utf8");
    writeFileSync(manifest, written);
    for (const [change, failure] of [
      [
        (u: Record<string, unknown>) => (u.version = "0.9.0"),
        "Debian package manifest version mismatch",
      ],
      [
        (u: Record<string, unknown>) => (u.deltas = []),
        "Unexpected Debian package update",
      ],
    ] as const) {
      const update = JSON.parse(debWritten);
      change(update);
      writeFileSync(debManifest, JSON.stringify(update));
      expect(() =>
        verifyMyGoUpdate(
          packages,
          "linux-amd64",
          "v1.0.0",
          keys.env.MYGO_UPDATER_PUBLIC_KEY,
        ),
      ).toThrow(failure);
    }
  });

  it("names the Debian package as mygo build and GitHub do", () => {
    expect(debName("linux-amd64", "1.0.0")).toBe("astrlink_1.0.0_amd64.deb");
    expect(debName("linux-amd64", "1.0.0-rc.1")).toBe(
      "astrlink_1.0.0.rc.1_amd64.deb",
    );
    expect(debName("linux-amd64", "1.0.0-rc-1+build.2")).toBe(
      "astrlink_1.0.0.rc.1.build.2_amd64.deb",
    );
    expect(debName("darwin-arm64", "1.0.0")).toBeNull();
  });

  it("signs AppImage updates as mygo build does, with the key of mygo keygen", () => {
    expect(appImageName("linux-amd64", "1.0.0-rc.1")).toBe(
      "AstrLink_1.0.0-rc.1_amd64.AppImage",
    );
    expect(appImageName("windows-amd64", "1.0.0")).toBeNull();
    const keys = mygoKeys(),
      other = mygoKeys(),
      bytes = Buffer.from("an AppImage");
    // Ed25519 is deterministic: the signature Go's ed25519.Sign gives.
    expect(
      signMyGoUpdate(bytes, mygoSecretKey(keys.secret, keys.publicKey)),
    ).toBe(
      sign(
        null,
        createHash("sha256").update(bytes).digest(),
        keys.privateKey,
      ).toString("base64"),
    );
    expect(() => mygoSecretKey(keys.secret, other.publicKey)).toThrow(
      "does not match",
    );
    // Go signs with the key's public half, which must be its seed's.
    const mixed = Buffer.concat([
      Buffer.from(keys.secret, "base64").subarray(0, 32),
      Buffer.from(other.publicKey, "base64"),
    ]).toString("base64");
    expect(() => mygoSecretKey(mixed, other.publicKey)).toThrow(
      "does not match",
    );
    expect(() => mygoSecretKey(keys.publicKey, keys.publicKey)).toThrow(
      "not a MyGo update key",
    );
  });

  it("validates release tags and identifies preview channels", () => {
    expect(releaseVersion("v1.2.3")).toEqual({
      version: "1.2.3",
      prerelease: false,
    });
    expect(releaseVersion("1.2.3-rc.2+build.4").prerelease).toBe(true);
    for (const tag of [
      "latest",
      "v01.2.3",
      "1.2",
      "1.2.3-01",
      "1.2.3+",
      "1.2.3/evil",
    ])
      expect(() => releaseVersion(tag)).toThrow();
  });
  it("marks only the highest stable version as GitHub's latest release", () => {
    const published = (tag_name: string, extra = {}) => ({
      tag_name,
      draft: false,
      prerelease: false,
      ...extra,
    });
    const releases = [
      published("v0.9.0"),
      published("v0.10.0"),
      published("v1.0.0-rc.1", { prerelease: true }),
      published("v2.0.0", { draft: true }),
      published("v3.0.0", { prerelease: true }),
      published("latest"),
    ];
    expect(newestStable("v0.10.1", releases)).toBe(true);
    expect(newestStable("v1.0.0", releases)).toBe(true);
    expect(newestStable("v0.10.0", releases)).toBe(true);
    // A patch for an older line must not move stable clients backwards.
    expect(newestStable("v0.9.1", releases)).toBe(false);
    expect(newestStable("v1.1.0-beta.1", releases)).toBe(false);
    expect(newestStable("v1.0.0", [])).toBe(true);
  });

  it("stamps the version into package.json only", () => {
    const dir = directory();
    writeFileSync(
      path.join(dir, "package.json"),
      '{"version":"0.1.0","name":"desktop","dependencies":{"other":"1.0.0"}}',
    );
    stampVersion(dir, "v2.0.0-beta.1");
    expect(
      JSON.parse(readFileSync(path.join(dir, "package.json"), "utf8")),
    ).toEqual({
      version: "2.0.0-beta.1",
      name: "desktop",
      dependencies: { other: "1.0.0" },
    });
    expect(readdirSync(dir)).toEqual(["package.json"]);
  });
  it("verifies both artifact bytes and the trusted comment", () => {
    const keys = minisignKeys(),
      bytes = Buffer.from("a signed test artifact");
    const signature = signUpdate(
      bytes,
      "test",
      minisignSecretKey(keys.secret),
      new Date(1000),
    );
    expect(() =>
      verifyUpdateSignature(bytes, signature, keys.publicKey),
    ).not.toThrow();
    expect(() =>
      verifyUpdateSignature(Buffer.from("tampered"), signature, keys.publicKey),
    ).toThrow("signature mismatch");
    const changed = Buffer.from(
      Buffer.from(signature, "base64")
        .toString()
        .replace("timestamp:1", "timestamp:2"),
    ).toString("base64");
    expect(() => verifyUpdateSignature(bytes, changed, keys.publicKey)).toThrow(
      "trusted comment",
    );
  });
  it("requires both signing keys for update releases", () => {
    const { env } = releaseKeys();
    expect(() => signingEnvironment(env)).not.toThrow();
    for (const name of Object.keys(env))
      expect(() => signingEnvironment({ ...env, [name]: " " })).toThrow(name);
  });
  it("publishes a manifest only after every platform is present and verified", () => {
    const keys = releaseKeys(),
      input = directory(),
      output = path.join(directory(), "release");
    const partial = staged(keys, releaseTargets.slice(0, -1) as Target[]);
    cpSync(partial, path.join(input, "partial"), { recursive: true });
    expect(() =>
      collectRelease(input, output, "v1.0.0", publicKeys(keys)),
    ).toThrow("Missing or duplicate");
    expect(existsSync(output)).toBe(false);
    cpSync(
      staged(keys, [releaseTargets.at(-1)] as Target[]),
      path.join(input, "last"),
      { recursive: true },
    );
    const manifest = collectRelease(
      input,
      output,
      "v1.0.0",
      publicKeys(keys),
      "Release notes",
    );
    expect(manifest.notes).toBe("Release notes");
    expect(manifest.platforms["darwin-aarch64"].url).toContain(
      "/releases/download/v1.0.0/",
    );
    expect(() =>
      collectRelease(
        input,
        path.join(directory(), "bad"),
        "v2.0.0",
        publicKeys(keys),
      ),
    ).toThrow("Version");
    const corrupt = (folder: string, name: string, failure: string) => {
      writeFileSync(path.join(input, folder, name), "corrupted");
      expect(() =>
        collectRelease(
          input,
          path.join(directory(), "bad"),
          "v1.0.0",
          publicKeys(keys),
        ),
      ).toThrow(failure);
    };
    corrupt("last", "astrlink-1.0.0-linux-amd64.tar.gz", "size mismatch");
    corrupt("last", "AstrLink_1.0.0_amd64.AppImage", "size mismatch");
    corrupt("partial", "AstrLink-macOS-arm64.app.tar.gz", "checksum");
  });
  it("adds a late platform to a published release without replacing a listed package", () => {
    const keys = releaseKeys(),
      input = staged(keys, releaseTargets as Target[]),
      late = staged(keys, ["darwin-x86_64"]),
      published = path.join(directory(), "release"),
      output = path.join(directory(), "attach");
    collectRelease(input, published, "v1.0.0", publicKeys(keys), "Notes");
    const extend = (from: string, to: string, tag = "v1.0.0") =>
      extendRelease(late, from, to, tag, "darwin-x86_64", publicKeys(keys));

    const manifest = extend(published, output);
    expect(Object.keys(manifest.platforms)).toEqual([
      "darwin-aarch64",
      "windows-x86_64",
      "linux-x86_64",
      "darwin-x86_64",
    ]);
    expect(manifest.notes).toBe("Notes");
    // The new packages, the platform's MyGo update and the rewritten
    // manifest and checksums.
    expect(readdirSync(output).sort()).toEqual([
      "AstrLink-macOS-x86_64.app.tar.gz",
      "AstrLink-macOS-x86_64.app.tar.gz.sig",
      "SHA256SUMS",
      "astrlink-0.9.0-to-1.0.0-darwin-amd64.delta",
      "astrlink-1.0.0-amd64.dmg",
      "astrlink-1.0.0-darwin-amd64.tar.gz",
      "install.sh",
      "latest.json",
      "update-darwin-amd64.json",
    ]);
    const lines = (dir: string) =>
      readFileSync(path.join(dir, "SHA256SUMS"), "utf8")
        .split("\n")
        .filter(Boolean);
    // Release's lines stay and the six new files join them.
    expect(lines(output)).toHaveLength(lines(published).length + 6);
    expect(lines(output)).toContain(
      `${sha256(readFileSync(path.join(output, "latest.json")))}  latest.json`,
    );

    // Re-running with the same package succeeds, e.g. after a failed upload.
    expect(() => extend(output, path.join(directory(), "retry"))).not.toThrow();

    const refused = path.join(directory(), "refused");
    expect(() => extend(published, refused, "v2.0.0")).toThrow(
      "Published manifest version",
    );
    const listed = JSON.parse(
      readFileSync(path.join(output, "latest.json"), "utf8"),
    );
    listed.platforms["darwin-x86_64"].url += "-other";
    writeFileSync(path.join(output, "latest.json"), JSON.stringify(listed));
    expect(() => extend(output, refused)).toThrow(
      "different darwin-x86_64 package",
    );
    writeFileSync(
      path.join(published, "SHA256SUMS"),
      readFileSync(path.join(published, "SHA256SUMS"), "utf8") +
        `${"0".repeat(64)}  astrlink-1.0.0-amd64.dmg\n`,
    );
    expect(() => extend(published, refused)).toThrow(
      "different astrlink-1.0.0-amd64.dmg",
    );
    expect(existsSync(refused)).toBe(false);
  });
  it("adds the packages to a release written ahead of CI and keeps its notes", () => {
    const keys = releaseKeys(),
      input = staged(keys, releaseTargets as Target[]);
    // Saved as a draft, or already published as a pre-release.
    for (const draft of [true, false]) {
      const output = path.join(directory(), "release");
      const github = fakeGitHub({
        tag_name: "v1.0.0",
        draft,
        prerelease: !draft,
        body: "Written notes",
        assets: [],
      });
      publishRelease(input, output, "v1.0.0", releaseEnv(keys), github.gh);
      // No new release and no generated notes; the manifests go up after
      // the packages, latest.json last.
      expect(github.calls.map((args) => args.slice(0, 2).join(" "))).toEqual([
        "api repos/Calcium-Ion/AstrLink/releases",
        "release upload",
        "release view",
        "release upload",
        "release view",
        "release upload",
        "release view",
        "release edit",
      ]);
      expect(github.uploads()[1].sort()).toEqual([
        "update-darwin-arm64.json",
        "update-linux-amd64-appimage.json",
        "update-linux-amd64-deb.json",
        "update-linux-amd64.json",
        "update-windows-amd64.json",
      ]);
      expect(github.uploads()[2]).toEqual(["latest.json"]);
      expect(github.uploads().flat().sort()).toEqual(
        readdirSync(output)
          .filter((name) => name !== "release-notes.txt")
          .sort(),
      );
      const edit = github.calls.at(-1)!;
      expect(edit).toEqual(
        expect.arrayContaining([
          "--draft=false",
          "--prerelease=false",
          "--latest=true",
        ]),
      );
      expect(edit).not.toContain("--notes-file");
      expect(manifestNotes(output)).toBe("Written notes");
    }
  });
  it("drafts a new release with generated notes and never replaces a complete one", () => {
    const keys = releaseKeys(),
      input = staged(keys, releaseTargets as Target[]),
      env = releaseEnv(keys);

    const fresh = fakeGitHub(),
      output = path.join(directory(), "release");
    publishRelease(input, output, "v1.0.0", env, fresh.gh);
    const create = fresh.calls.find(([, action]) => action === "create")!;
    expect(create).toContain("--draft");
    const notes = "Generated notes";
    expect(
      readFileSync(create[create.indexOf("--notes-file") + 1], "utf8"),
    ).toBe(notes);
    expect(fresh.calls.at(-1)).toContain("--notes-file");
    expect(manifestNotes(output)).toBe(notes);

    const published = fakeGitHub({
      tag_name: "v1.0.0",
      draft: false,
      prerelease: false,
      body: "Written notes",
      assets: [{ name: "latest.json" }],
    });
    const refused = path.join(directory(), "refused");
    expect(() =>
      publishRelease(input, refused, "v1.0.0", env, published.gh),
    ).toThrow("Refusing to overwrite");
    expect(published.uploads()).toEqual([]);
    expect(existsSync(refused)).toBe(false);

    // The Intel macOS package waits for the same point.
    for (const release of [
      { isDraft: true, assets: [{ name: "latest.json" }] },
      { isDraft: false, assets: [] },
    ])
      expect(() =>
        attachRelease(input, directory(), "v1.0.0", "darwin-x86_64", env, () =>
          JSON.stringify(release),
        ),
      ).toThrow("not published yet");
  });
});
