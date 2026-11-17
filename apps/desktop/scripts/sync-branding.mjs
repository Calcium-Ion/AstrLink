import { createHash } from "node:crypto";
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
} from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { Resvg } from "@resvg/resvg-js";

const rootDir = dirname(fileURLToPath(import.meta.url));
const desktopDir = join(rootDir, "..");
const brandingLogo = join(
  desktopDir,
  "../../assets/branding/astrlink-logo.svg",
);
const uiLogo = join(desktopDir, "src/assets/astrlink-logo.svg");
const uiDevLogo = join(desktopDir, "src/assets/astrlink-dev-logo.svg");
const iconsDir = join(desktopDir, "icons");

// The tray icon carries gateway state, so each source gets three variants:
// ready (the source itself), watched (an agent is reading through the CLI: a
// badge) and idle (dimmed). macOS uses the monochrome menu-bar template at
// 18pt (36px for Retina); Windows and Linux use the colour mark at 32px.
const menuBarLogo = join(
  desktopDir,
  "../../assets/branding/astrlink-menubar.svg",
);
const trayDir = join(desktopDir, "internal/ui/icons");
const traySvg = readFileSync(menuBarLogo);
const svg = readFileSync(brandingLogo);
// Only the macOS application icon uses the softly lit background. UI, web,
// tray, Windows and Linux assets continue to use the flat branding source.
const macosSvg = readFileSync(
  join(desktopDir, "../../assets/branding/astrlink-macos-icon.svg"),
);
// Bump when the variant derivation below changes so stale PNGs regenerate.
const TRAY_VARIANTS_VERSION = "11";
const trayHash = createHash("sha256")
  .update(traySvg)
  .update(svg)
  .update(TRAY_VARIANTS_VERSION)
  .digest("hex");
const trayStampPath = join(trayDir, ".branding-hash");
mkdirSync(trayDir, { recursive: true });

// Bump when renderPng or icns change so the committed icons regenerate.
const ICON_RENDER_VERSION = "1";

/** Renders an SVG document into a size×size PNG. */
function renderPng(source, size) {
  return new Resvg(Buffer.isBuffer(source) ? source : Buffer.from(source), {
    fitTo: { mode: "width", value: size },
  })
    .render()
    .asPng();
}

/** An .icns of PNG images, the format macOS 10.7 and later reads. */
function icns(source) {
  const entries = [
    ["icp4", 16],
    ["icp5", 32],
    ["ic11", 32],
    ["icp6", 64],
    ["ic12", 64],
    ["ic07", 128],
    ["ic13", 256],
    ["ic08", 256],
    ["ic14", 512],
    ["ic09", 512],
    ["ic10", 1024],
  ].map(([type, size]) => {
    const png = renderPng(source, size);
    const header = Buffer.alloc(8);
    header.write(type, 0, "latin1");
    header.writeUInt32BE(png.length + 8, 4);
    return Buffer.concat([header, png]);
  });
  const header = Buffer.alloc(8);
  header.write("icns", 0, "latin1");
  header.writeUInt32BE(8 + entries.reduce((n, e) => n + e.length, 0), 4);
  return Buffer.concat([header, ...entries]);
}

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
  { dir: "mac-ready", source: traySvg.toString("utf8"), size: 36 },
  {
    dir: "mac-watched",
    source: deriveSvg(inkedMenuBarSvg(macRunningInk), {
      defs: templateKnockout,
      open: '<g mask="url(#tray-knockout)">',
      close: "</g>",
      overlay: '<circle cx="17.5" cy="17.5" r="3.6" fill="#E5484D"/>',
    }),
    size: 36,
  },
  {
    dir: "mac-idle",
    source: inkedMenuBarSvg("#000000").toString("utf8"),
    size: 36,
  },
  { dir: "color-ready", source: svg.toString("utf8"), size: 32 },
  {
    dir: "color-watched",
    source: deriveSvg(svg, {
      overlay:
        '<circle cx="404" cy="404" r="92" fill="#FFFFFF"/><circle cx="404" cy="404" r="68" fill="#E5484D"/>',
    }),
    size: 32,
  },
  {
    dir: "color-idle",
    source: deriveSvg(svg, {
      defs: idleFilter,
      open: '<g filter="url(#tray-idle)" opacity="0.9">',
      close: "</g>",
    }),
    size: 32,
  },
  // Notifications on Windows and Linux show the colour mark, large enough
  // for the balloon's large icon and the notification image to shrink, so
  // they never take the tray icon's current, possibly idle, state
  // (internal/ui/notices.go).
  { dir: "notification", source: svg.toString("utf8"), size: 256 },
];

const previousTrayHash = existsSync(trayStampPath)
  ? readFileSync(trayStampPath, "utf8").trim()
  : "";
// One image per state: the tray loads the 36px glyph as an 18pt template on
// macOS and the 32px colour mark elsewhere (internal/ui/tray.go). The
// notification image goes beside them.
const trayOutput = (variant) => join(trayDir, `${variant.dir}.png`);
if (
  previousTrayHash !== trayHash ||
  trayVariants.some((variant) => !existsSync(trayOutput(variant)))
) {
  for (const variant of trayVariants)
    writeFileSync(trayOutput(variant), renderPng(variant.source, variant.size));
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
// MyGo renders the Windows and Linux icons from icon.png; macOS gets the
// softly lit icon.icns in its place (mygo.config.ts).
const appIconNames = ["icon.svg", "icon.png", "icon.icns"];
mkdirSync(dirname(uiLogo), { recursive: true });
copyFileSync(brandingLogo, uiLogo);
writeFileSync(uiDevLogo, developmentIcon(svg));

for (const { directory, source, macosSource } of appVariants) {
  const stampPath = join(directory, ".branding-hash");
  const hash = createHash("sha256")
    .update(source)
    .update(macosSource)
    .update(ICON_RENDER_VERSION)
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
  writeFileSync(join(directory, "icon.svg"), source);
  writeFileSync(join(directory, "icon.png"), renderPng(source, 1024));
  writeFileSync(join(directory, "icon.icns"), icns(macosSource));
  writeFileSync(stampPath, `${hash}\n`);
}

console.log("Synced branding logo into desktop UI, app and development icons.");
