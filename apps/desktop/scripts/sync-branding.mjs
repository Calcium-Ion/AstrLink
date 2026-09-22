import { createHash } from "node:crypto";
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
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
const iconsDir = join(desktopDir, "src-tauri/icons");
const iconSvg = join(iconsDir, "icon.svg");
const stampPath = join(iconsDir, ".branding-hash");

// The tray icon carries gateway state, so the app mark gets three variants:
// ready (the mark itself), watched (an agent is reading through MCP: red
// badge) and idle (greyed). macOS renders the 18-point item at 36px for
// Retina; Windows and Linux use 32px.
const trayDir = join(iconsDir, "tray");
const svg = readFileSync(brandingLogo);
// Bump when the variant derivation below changes so stale PNGs regenerate.
const TRAY_VARIANTS_VERSION = "6";
const trayHash = createHash("sha256")
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

/**
 * The app logo without its rounded tile, framed tightly on the mark, for a
 * full-colour macOS status item (18pt, rendered at 36px for Retina). Badges
 * are placed in this frame's bottom-right corner.
 */
function deriveMarkSvg(source, options = {}) {
  const text = source
    .toString("utf8")
    .replace(/\n?\s*<rect x="52" y="52"[^>]*\/>/, "")
    .replace(/viewBox="0 0 512 512"/, 'viewBox="76 51 380 380"')
    .replace(/width="512" height="512"/, 'width="18" height="18"');
  return deriveSvg(Buffer.from(text, "utf8"), options);
}

const markBadgeBase = '<circle cx="396" cy="371" r="62" fill="#FFFFFF"/>';
const watchedBadge = markBadgeBase + '<circle cx="396" cy="371" r="46" fill="#E5484D"/>';
// Idle reads as "off" on both light and dark bars: greyscale, then lifted
// into the mid-greys so the navy A does not sink into a dark menu bar.
const idleFilter =
  '<filter id="tray-idle"><feColorMatrix type="saturate" values="0"/>' +
  '<feComponentTransfer><feFuncR type="linear" slope="0.5" intercept="0.42"/>' +
  '<feFuncG type="linear" slope="0.5" intercept="0.42"/><feFuncB type="linear" slope="0.5" intercept="0.42"/>' +
  "</feComponentTransfer></filter>";

const trayVariants = [
  { dir: "mac-color-ready", source: deriveMarkSvg(svg), sizes: ["18", "36"] },
  { dir: "mac-color-watched", source: deriveMarkSvg(svg, { overlay: watchedBadge }), sizes: ["18", "36"] },
  {
    dir: "mac-color-idle",
    source: deriveMarkSvg(svg, { defs: idleFilter, open: '<g filter="url(#tray-idle)" opacity="0.9">', close: "</g>" }),
    sizes: ["18", "36"],
  },
  { dir: "color-ready", source: svg.toString("utf8"), sizes: ["32"] },
  {
    dir: "color-watched",
    source: deriveSvg(svg, {
      overlay: '<circle cx="404" cy="404" r="92" fill="#FFFFFF"/><circle cx="404" cy="404" r="68" fill="#E5484D"/>',
    }),
    sizes: ["32"],
  },
  {
    dir: "color-idle",
    source: deriveSvg(svg, { defs: idleFilter, open: '<g filter="url(#tray-idle)" opacity="0.9">', close: "</g>" }),
    sizes: ["32"],
  },
];

const previousTrayHash = existsSync(trayStampPath)
  ? readFileSync(trayStampPath, "utf8").trim()
  : "";
const trayOutputs = trayVariants.flatMap((variant) =>
  variant.sizes.map((size) => join(trayDir, variant.dir, `${size}x${size}.png`)),
);
if (previousTrayHash !== trayHash || trayOutputs.some((path) => !existsSync(path))) {
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

const hash = createHash("sha256").update(svg).digest("hex");

mkdirSync(dirname(uiLogo), { recursive: true });
copyFileSync(brandingLogo, uiLogo);

let previous = "";
try {
  previous = readFileSync(stampPath, "utf8").trim();
} catch {
  previous = "";
}

if (previous === hash) {
  console.log("Branding assets already up to date.");
  process.exit(0);
}

copyFileSync(brandingLogo, iconSvg);

const iconResult = spawnSync("bun", ["run", "tauri", "icon", brandingLogo], {
  cwd: desktopDir,
  stdio: "inherit",
});
if (iconResult.status !== 0) {
  process.exit(iconResult.status ?? 1);
}

// Keep the desktop icon set lean; tauri icon also emits mobile/Appx assets.
rmSync(join(iconsDir, "android"), { recursive: true, force: true });
rmSync(join(iconsDir, "ios"), { recursive: true, force: true });
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
  rmSync(join(iconsDir, name), { force: true });
}

copyFileSync(brandingLogo, iconSvg);
writeFileSync(stampPath, `${hash}\n`);
console.log("Synced branding logo into desktop UI and app icons.");
