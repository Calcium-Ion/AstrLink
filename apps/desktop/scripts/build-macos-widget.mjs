// Builds and signs the macOS desktop widget (`macos-widget/`) with swiftc
// alone, so neither CI nor a local build needs Xcode.
//
//   bun scripts/build-macos-widget.mjs            build the appex (Tauri's
//                                                 beforeBundleCommand on macOS)
//   bun scripts/build-macos-widget.mjs --test     run the widget's model tests
//   bun scripts/build-macos-widget.mjs --render <dir>
//                                                 draw every size and state
//
// The appex lands in src-tauri/target/macos-widget, signed with the app's
// identity, and tauri.macos.conf.json copies it into Contents/PlugIns before
// Tauri signs the app around it.
import { spawnSync } from "node:child_process";
import {
  cpSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";

const desktop = fileURLToPath(new URL("../", import.meta.url));
const tauriDirectory = path.join(desktop, "src-tauri");
const widget = path.join(desktop, "macos-widget");
const output = path.join(tauriDirectory, "target/macos-widget");
const moduleName = "AstrLinkWidgets";
// WidgetKit's configurable widgets and containerBackground need macOS 14.
const minimumSystemVersion = "14.0";
// The widget's own strings; app copy comes from the snapshot's language.
const displayNames = {
  en: (product) => `${product} Widgets`,
  "zh-Hans": (product) => `${product} 小组件`,
  // The app shows every Chinese locale in zh-CN; the widget follows.
  "zh-Hant": (product) => `${product} 小组件`,
  ja: (product) => `${product} ウィジェット`,
};

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
    stdio: options.inherit ? "inherit" : "pipe",
  });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    throw new Error(
      `${command} ${args.join(" ")} failed (${result.status}):\n${result.stdout ?? ""}${result.stderr ?? ""}`,
    );
  }
  return (result.stdout ?? "").trim();
}

function swiftFiles(...directories) {
  return directories.flatMap((directory) =>
    readdirSync(path.join(widget, directory))
      .filter((name) => name.endsWith(".swift"))
      .sort()
      .map((name) => path.join(widget, directory, name)),
  );
}

function swiftc(sources, target, binary, extraArgs = []) {
  run("xcrun", [
    "swiftc",
    "-sdk",
    run("xcrun", ["--sdk", "macosx", "--show-sdk-path"]),
    "-target",
    `${target}-apple-macos${minimumSystemVersion}`,
    "-swift-version",
    "5",
    "-parse-as-library",
    "-module-name",
    moduleName,
    "-O",
    ...extraArgs,
    ...sources,
    "-o",
    binary,
  ]);
}

// JSON Merge Patch, as Tauri applies platform and --config files.
function merge(base, patch) {
  if (patch === null || typeof patch !== "object" || Array.isArray(patch))
    return patch;
  const merged =
    base && typeof base === "object" && !Array.isArray(base) ? { ...base } : {};
  for (const [key, value] of Object.entries(patch)) {
    if (value === null) delete merged[key];
    else merged[key] = merge(merged[key], value);
  }
  return merged;
}

/** The app config Tauri is bundling: the base file, the macOS file, then
 * every --config file, which the CLI passes to hooks as TAURI_CONFIG. */
export function bundleConfig(environment = process.env) {
  const read = (name) =>
    JSON.parse(readFileSync(path.join(tauriDirectory, name), "utf8"));
  let config = merge(read("tauri.conf.json"), read("tauri.macos.conf.json"));
  if (environment.TAURI_CONFIG)
    config = merge(config, JSON.parse(environment.TAURI_CONFIG));
  return config;
}

/** The App Group the app is signed with; the widget reads the same one. */
export function appGroup(entitlements) {
  const groups = [
    ...entitlements.matchAll(
      /<key>com\.apple\.security\.application-groups<\/key>\s*<array>([\s\S]*?)<\/array>/g,
    ),
  ].flatMap(([, array]) =>
    [...array.matchAll(/<string>([^<]+)<\/string>/g)].map(([, group]) =>
      group.trim(),
    ),
  );
  if (groups.length !== 1)
    throw new Error("The app entitlements must name exactly one App Group.");
  return groups[0];
}

/** swiftc targets for Tauri's target triple. */
export function architectures(triple = "") {
  if (triple.startsWith("universal")) return ["arm64", "x86_64"];
  if (triple.startsWith("aarch64")) return ["arm64"];
  if (triple.startsWith("x86_64")) return ["x86_64"];
  return [process.arch === "arm64" ? "arm64" : "x86_64"];
}

function escape(text) {
  return String(text)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function plist(entries) {
  const value = (item) => {
    if (typeof item === "boolean") return item ? "<true/>" : "<false/>";
    if (Array.isArray(item))
      return `<array>${item.map(value).join("")}</array>`;
    if (typeof item === "object")
      return `<dict>${Object.entries(item)
        .map(([key, nested]) => `<key>${escape(key)}</key>${value(nested)}`)
        .join("")}</dict>`;
    return `<string>${escape(item)}</string>`;
  };
  return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">${value(entries)}</plist>
`;
}

/** `Type` in module `AstrLinkWidgets`, as Swift mangles a struct name. */
function mangled(type) {
  return `${moduleName.length}${moduleName}${type.length}${type}V`;
}

/** What Xcode's App Intents metadata processor would extract for the small
 * widget's plan picker. The system reads it to show "Edit Widget". */
export function intentsMetadata(sdkBuild) {
  const everywhere = { LNPlatformNameWildcard: { introducedVersion: "*" } };
  const text = (key) => ({ alternatives: [], key });
  const visible = { assistantOnly: false, isDiscoverable: true };
  const plan = { entity: { wrapper: { typeName: "PlanEntity" } } };
  const widgetProtocol = "com.apple.link.systemProtocol.WidgetConfiguration";
  return {
    actions: {
      SelectPlanIntent: {
        assistantDefinedSchemas: [],
        assistantDefinedSchemaTraits: [],
        authenticationPolicy: 0,
        availabilityAnnotations: everywhere,
        descriptionMetadata: {
          descriptionText: text("Choose the plan this widget shows."),
          searchKeywords: [],
        },
        effectiveBundleIdentifiers: [],
        fullyQualifiedTypeName: `${moduleName}.SelectPlanIntent`,
        identifier: "SelectPlanIntent",
        isAuthPolExplicit: false,
        isDiscoverable: false,
        mangledTypeName: mangled("SelectPlanIntent"),
        mangledTypeNameByBundleIdentifier: {},
        mangledTypeNameByBundleIdentifierV2: {},
        mangledTypeNameV2: mangled("SelectPlanIntent"),
        openAppWhenRun: false,
        outputFlags: 8,
        parameters: [
          {
            capabilities: 0,
            dynamicOptionsSupport: 2,
            inputConnectionBehavior: 0,
            isInput: false,
            isOptional: true,
            name: "plan",
            resolvableInputTypes: [],
            title: text("Plan"),
            typeSpecificMetadata: [],
            valueType: plan,
          },
        ],
        presentationStyle: 0,
        requiredCapabilities: [],
        supportedModes: 1,
        systemProtocolMetadata: [widgetProtocol, { empty: {} }],
        systemProtocolMetadataV2: [widgetProtocol, { empty: {} }],
        systemProtocols: [widgetProtocol],
        title: text("Choose Plan"),
        typeSpecificMetadata: [],
        visibilityMetadata: visible,
      },
    },
    assistantEntities: [],
    assistantIntentNegativePhrases: [],
    assistantIntents: [],
    autoShortcuts: [],
    entities: {
      PlanEntity: {
        assistantDefinedSchemas: [],
        availabilityAnnotations: everywhere,
        defaultQueryIdentifier: `${moduleName}.PlanQuery`,
        displayTypeName: text("Plan"),
        effectiveBundleIdentifiers: [],
        entitySyncMetadata: { entitySyncType: 0 },
        fullyQualifiedTypeName: `${moduleName}.PlanEntity`,
        mangledTypeName: mangled("PlanEntity"),
        mangledTypeNameByBundleIdentifier: {},
        numericFormatTypeName: text("Plan"),
        properties: [],
        requiredCapabilities: [],
        systemProtocolMetadata: [],
        systemProtocolMetadataV2: [],
        transient: false,
        typeName: "PlanEntity",
        typeSpecificMetadata: [],
        visibilityMetadata: visible,
      },
    },
    enums: [],
    generator: { name: "xcode-tools", version: sdkBuild },
    negativePhrases: [],
    queries: {
      PlanQuery: {
        availabilityAnnotations: everywhere,
        capabilities: 70,
        defaultQueryForEntity: true,
        effectiveBundleIdentifiers: [],
        entityType: "PlanEntity",
        fullyQualifiedIdentifier: `${moduleName}.PlanQuery`,
        identifier: "PlanQuery",
        mangledTypeName: mangled("PlanQuery"),
        mangledTypeNameByBundleIdentifier: {},
        parameters: [],
        queryType: "PlanQuery",
        resultValueType: plan,
        sortingOptions: [],
        visibilityMetadata: visible,
      },
    },
    shortcutTileColor: 14,
    version: 1,
  };
}

function buildAppex() {
  const config = bundleConfig();
  const macOS = config.bundle?.macOS ?? {};
  if (!macOS.entitlements)
    throw new Error("bundle.macOS.entitlements must name the app's App Group.");
  const group = appGroup(
    readFileSync(path.resolve(tauriDirectory, macOS.entitlements), "utf8"),
  );
  const product = config.productName;
  const version = config.version;

  rmSync(output, { recursive: true, force: true });
  const build = path.join(output, "build");
  const appex = path.join(output, `${moduleName}.appex`);
  const contents = path.join(appex, "Contents");
  const resources = path.join(contents, "Resources");
  mkdirSync(build, { recursive: true });
  mkdirSync(path.join(contents, "MacOS"), { recursive: true });

  const sources = swiftFiles("Sources", "Main");
  const slices = architectures(process.env.TAURI_ENV_TARGET_TRIPLE).map(
    (arch) => {
      const binary = path.join(build, `${moduleName}-${arch}`);
      swiftc(sources, arch, binary, ["-application-extension"]);
      return binary;
    },
  );
  const executable = path.join(contents, "MacOS", moduleName);
  run("lipo", ["-create", ...slices, "-output", executable]);
  // The metadata names Swift types; a mismatch would only show as a widget
  // that cannot be edited, so stop the build here instead.
  const symbols = run("nm", ["-U", executable]);
  for (const type of ["SelectPlanIntent", "PlanEntity", "PlanQuery"]) {
    if (!symbols.includes(`_$s${mangled(type)}Mn`))
      throw new Error(`${type} is not mangled as ${mangled(type)}.`);
  }

  writeFileSync(
    path.join(contents, "Info.plist"),
    plist({
      CFBundleDevelopmentRegion: "en",
      CFBundleDisplayName: displayNames.en(product),
      CFBundleExecutable: moduleName,
      CFBundleIdentifier: `${config.identifier}.widgets`,
      CFBundleInfoDictionaryVersion: "6.0",
      CFBundleLocalizations: Object.keys(displayNames),
      CFBundleName: moduleName,
      CFBundlePackageType: "XPC!",
      CFBundleShortVersionString: version,
      CFBundleSupportedPlatforms: ["MacOSX"],
      CFBundleVersion: macOS.bundleVersion ?? version,
      LSMinimumSystemVersion: minimumSystemVersion,
      NSExtension: {
        NSExtensionPointIdentifier: "com.apple.widgetkit-extension",
      },
      // QuotaSnapshot.load() reads the host's snapshot from this group.
      AstrLinkAppGroup: group,
    }),
  );
  cpSync(path.join(widget, "Resources"), resources, { recursive: true });
  for (const [language, name] of Object.entries(displayNames)) {
    const directory = path.join(resources, `${language}.lproj`);
    mkdirSync(directory, { recursive: true });
    writeFileSync(
      path.join(directory, "InfoPlist.strings"),
      `"CFBundleDisplayName" = "${name(product)}";\n`,
    );
  }
  const sdkBuild = run("xcrun", [
    "--sdk",
    "macosx",
    "--show-sdk-build-version",
  ]);
  const metadata = path.join(resources, "Metadata.appintents");
  mkdirSync(metadata);
  writeFileSync(
    path.join(metadata, "extract.actionsdata"),
    JSON.stringify(intentsMetadata(sdkBuild)),
  );
  writeFileSync(
    path.join(metadata, "version.json"),
    JSON.stringify({ toolsVersion: sdkBuild, version: "3.0" }),
  );

  const entitlements = path.join(build, "widget.entitlements");
  writeFileSync(
    entitlements,
    plist({
      "com.apple.security.app-sandbox": true,
      "com.apple.security.application-groups": [group],
    }),
  );
  // The app's identity: the same variable Tauri signs with, then the config.
  const identity =
    process.env.APPLE_SIGNING_IDENTITY || macOS.signingIdentity || "-";
  run("codesign", [
    "--force",
    "--sign",
    identity,
    ...(identity === "-" ? [] : ["--options", "runtime", "--timestamp"]),
    "--entitlements",
    entitlements,
    appex,
  ]);
  run("codesign", ["--verify", "--strict", appex]);
  rmSync(build, { recursive: true, force: true });
  console.log(
    `Built ${path.relative(desktop, appex)} for ${group}, signed by ${identity === "-" ? "an ad-hoc signature" : identity}.`,
  );
}

function runTests() {
  mkdirSync(output, { recursive: true });
  const binary = path.join(output, "quota-model-tests");
  swiftc(
    [
      ...swiftFiles("Sources"),
      path.join(widget, "Tests/QuotaModelTests.swift"),
    ],
    architectures()[0],
    binary,
  );
  run(binary, [], { inherit: true });
}

function renderPreviews(directory) {
  mkdirSync(output, { recursive: true });
  const binary = path.join(output, "render-previews");
  swiftc(
    [
      ...swiftFiles("Sources"),
      path.join(widget, "Preview/RenderPreviews.swift"),
    ],
    architectures()[0],
    binary,
  );
  run(binary, [path.resolve(directory), path.join(widget, "Resources")], {
    inherit: true,
  });
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const { values } = parseArgs({
    options: { test: { type: "boolean" }, render: { type: "string" } },
  });
  if (process.platform !== "darwin")
    throw new Error("The macOS widget builds on macOS only.");
  if (values.test) runTests();
  else if (values.render) renderPreviews(values.render);
  else buildAppex();
}
