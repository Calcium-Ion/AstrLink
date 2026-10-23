import { createHash } from "node:crypto";
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const rootDir = dirname(fileURLToPath(import.meta.url));
const desktopDir = join(rootDir, "..");
const brandingLogo = join(
  desktopDir,
  "../../assets/branding/astrlink-logo.svg",
);
const uiLogo = join(desktopDir, "src/assets/astrlink-logo.svg");
const uiDevLogo = join(desktopDir, "src/assets/astrlink-dev-logo.svg");
const iconsDir = join(desktopDir, "src-tauri/icons");

// The tray icon carries gateway state, so each source gets three variants:
// ready (the source itself), watched (an agent is reading through the CLI: a
// badge) and idle (dimmed). macOS uses the monochrome menu-bar template at
// 18pt (36px for Retina); Windows and Linux use the colour mark at 32px.
const menuBarLogo = join(
  desktopDir,
  "../../assets/branding/astrlink-menubar.svg",
);
const trayDir = join(iconsDir, "tray");
const traySvg = readFileSync(menuBarLogo);
const svg = readFileSync(brandingLogo);
// Only the macOS application icon uses the softly lit background. UI, web,
// tray, Windows and Linux assets continue to use the flat branding source.
const macosSvg = readFileSync(
  join(desktopDir, "../../assets/branding/astrlink-macos-icon.svg"),
);
// Bump when the variant derivation below changes so stale PNGs regenerate.
const TRAY_VARIANTS_VERSION = "9";
const trayHash = createHash("sha256")
  .update(traySvg)
  .update(svg)
  .update(TRAY_VARIANTS_VERSION)
  .digest("hex");
const trayStampPath = join(trayDir, ".branding-hash");
mkdirSync(trayDir, { recursive: true });

/**
 * Wraps the drawing of an SVG document in `open`…`close` and appends
 * `overlay` before the closing tag. Works on the raw markup so gradients,
 * masks and filters in the source keep their ids.
 */
function deriveSvg(source, { defs = "", open = "", close = "", overlay = "" }) {
  const text = source.toString("utf8");
  const rootEnd = text.indexOf(">", text.indexOf("<svg")) + 1;
  const closing = text.lastIndexOf("</svg>");
  return `${text.slice(0, rootEnd)}${defs}${open}${text.slice(rootEnd, closing)}${close}${overlay}</svg>\n`;
}

// Template images are alpha-only: white is ink too, so the badge separates
// itself from the glyph with a mask that cuts a ring, not with a white stroke.
const templateKnockout =
  '<mask id="tray-knockout" maskUnits="userSpaceOnUse" x="0" y="0" width="22" height="22">' +
  '<rect width="22" height="22" fill="#FFFFFF"/><circle cx="17.5" cy="17.5" r="5.2" fill="#000000"/></mask>';
// Idle reads as "off" on both light and dark bars: greyscale, then lifted
// into the mid-greys so the navy A does not sink into a dark taskbar.
const idleFilter =
  '<filter id="tray-idle"><feColorMatrix type="saturate" values="0"/>' +
  '<feComponentTransfer><feFuncR type="linear" slope="0.5" intercept="0.42"/>' +
  '<feFuncG type="linear" slope="0.5" intercept="0.42"/><feFuncB type="linear" slope="0.5" intercept="0.42"/>' +
  "</feComponentTransfer></filter>";

/**
 * The menu-bar glyph in a fixed ink colour instead of the template's alpha
 * mask. Only the drawing after `</defs>` is recoloured: the crossing mask in
 * the defs must keep its black knockout stroke.
 */
function inkedMenuBarSvg(ink) {
  const text = traySvg.toString("utf8");
  const defsEnd = text.indexOf("</defs>") + "</defs>".length;
  return Buffer.from(
    text.slice(0, defsEnd) + text.slice(defsEnd).replaceAll("#000000", ink),
    "utf8",
  );
}

// macOS states, per the operator's brief: a running gateway is the native
// template glyph (the menu bar tints it, off-white on a dark bar); a stopped
// one is a fixed black glyph; an agent reading through the CLI is the off-white
// glyph with a red badge. Red cannot live in a template, so the last two are
// plain images with the ink baked in.
const macRunningInk = "#F5F5F7";

const trayVariants = [
  { dir: "mac-ready", source: traySvg.toString("utf8"), sizes: ["18", "36"] },
  {
    dir: "mac-watched",
    source: deriveSvg(inkedMenuBarSvg(macRunningInk), {
      defs: templateKnockout,
      open: '<g mask="url(#tray-knockout)">',
      close: "</g>",
      overlay: '<circle cx="17.5" cy="17.5" r="3.6" fill="#E5484D"/>',
    }),
    sizes: ["18", "36"],
  },
  {
    dir: "mac-idle",
    source: inkedMenuBarSvg("#000000").toString("utf8"),
    sizes: ["18", "36"],
  },
  { dir: "color-ready", source: svg.toString("utf8"), sizes: ["32"] },
  {
    dir: "color-watched",
    source: deriveSvg(svg, {
      overlay:
        '<circle cx="404" cy="404" r="92" fill="#FFFFFF"/><circle cx="404" cy="404" r="68" fill="#E5484D"/>',
    }),
    sizes: ["32"],
  },
  {
    dir: "color-idle",
    source: deriveSvg(svg, {
      defs: idleFilter,
      open: '<g filter="url(#tray-idle)" opacity="0.9">',
      close: "</g>",
    }),
    sizes: ["32"],
  },
];

const previousTrayHash = existsSync(trayStampPath)
  ? readFileSync(trayStampPath, "utf8").trim()
  : "";
const trayOutputs = trayVariants.flatMap((variant) =>
  variant.sizes.map((size) =>
    join(trayDir, variant.dir, `${size}x${size}.png`),
  ),
);
if (
  previousTrayHash !== trayHash ||
  trayOutputs.some((path) => !existsSync(path))
) {
  const renderTray = (sourcePath, outputDir, sizes) => {
    const result = spawnSync(
      "bun",
      [
        "run",
        "tauri",
        "icon",
        sourcePath,
        "--output",
        outputDir,
        ...sizes.flatMap((size) => ["--png", size]),
      ],
      { cwd: desktopDir, stdio: "inherit" },
    );
    if (result.status !== 0) {
      process.exit(result.status ?? 1);
    }
  };
  for (const variant of trayVariants) {
    const outputDir = join(trayDir, variant.dir);
    mkdirSync(outputDir, { recursive: true });
    const sourcePath = join(outputDir, "source.svg");
    writeFileSync(sourcePath, variant.source);
    renderTray(sourcePath, outputDir, variant.sizes);
    rmSync(sourcePath, { force: true });
  }
  writeFileSync(trayStampPath, `${trayHash}\n`);
  console.log("Synced tray icons and their state variants.");
}

// Keep the original mark and colours, adding only a development badge.
// Outlined DEV lettering needs no system font.
function developmentIcon(source) {
  return deriveSvg(
    source
      .toString("utf8")
      .replace("<title>AstrLink 星澜</title>", "<title>AstrLink Dev</title>"),
    {
      overlay:
        '<rect x="152" y="384" width="208" height="68" rx="22" fill="#FFB454"/>' +
        '<path d="M203 401H214C234 401 234 435 214 435H203Z M267 401H247V435H267 M247 418H264 M283 401L296 435L309 401" ' +
        'fill="none" stroke="#182444" stroke-width="9" stroke-linecap="square" stroke-linejoin="round"/>',
    },
  );
}
const appVariants = [
  { directory: iconsDir, source: svg, macosSource: macosSvg },
  {
    directory: join(iconsDir, "dev"),
    source: developmentIcon(svg),
    macosSource: developmentIcon(macosSvg),
  },
];
const appIconNames = [
  "icon.svg",
  "32x32.png",
  "64x64.png",
  "128x128.png",
  "128x128@2x.png",
  "icon.png",
  "icon.icns",
  "icon.ico",
];
mkdirSync(dirname(uiLogo), { recursive: true });
copyFileSync(brandingLogo, uiLogo);
writeFileSync(uiDevLogo, developmentIcon(svg));

function renderAppIcons(sourcePath, directory) {
  const result = spawnSync(
    "bun",
    ["run", "tauri", "icon", sourcePath, "--output", directory],
    { cwd: desktopDir, stdio: "inherit" },
  );
  if (result.error || result.status !== 0) {
    throw new Error(`Unable to generate icons from ${sourcePath}`, {
      cause: result.error ?? result.status,
    });
  }
}

for (const { directory, source, macosSource } of appVariants) {
  const stampPath = join(directory, ".branding-hash");
  const hash = createHash("sha256")
    .update(source)
    .update(macosSource)
    .digest("hex");
  const previous = existsSync(stampPath)
    ? readFileSync(stampPath, "utf8").trim()
    : "";

  if (
    previous === hash &&
    appIconNames.every((name) => existsSync(join(directory, name)))
  ) {
    continue;
  }

  mkdirSync(directory, { recursive: true });
  const iconSvg = join(directory, "icon.svg");
  writeFileSync(iconSvg, source);
  renderAppIcons(iconSvg, directory);

  // Tauri emits every platform format together. Keep only the macOS ICNS
  // from this render so the shared PNGs and Windows ICO remain flat.
  const macosDirectory = mkdtempSync(join(tmpdir(), "astrlink-macos-icon-"));
  try {
    const macosSourcePath = join(macosDirectory, "icon.svg");
    writeFileSync(macosSourcePath, macosSource);
    renderAppIcons(macosSourcePath, macosDirectory);
    copyFileSync(
      join(macosDirectory, "icon.icns"),
      join(directory, "icon.icns"),
    );
  } finally {
    rmSync(macosDirectory, { recursive: true, force: true });
  }

  // Keep the desktop icon set lean; tauri icon also emits mobile/Appx assets.
  rmSync(join(directory, "android"), { recursive: true, force: true });
  rmSync(join(directory, "ios"), { recursive: true, force: true });
  for (const name of [
    "StoreLogo.png",
    "Square30x30Logo.png",
    "Square44x44Logo.png",
    "Square71x71Logo.png",
    "Square89x89Logo.png",
    "Square107x107Logo.png",
    "Square142x142Logo.png",
    "Square150x150Logo.png",
    "Square284x284Logo.png",
    "Square310x310Logo.png",
  ]) {
    rmSync(join(directory, name), { force: true });
  }
  writeFileSync(stampPath, `${hash}\n`);
}

console.log("Synced branding logo into desktop UI, app and development icons.");
