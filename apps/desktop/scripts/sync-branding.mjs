import { createHash } from "node:crypto";
import { copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const rootDir = dirname(fileURLToPath(import.meta.url));
const desktopDir = join(rootDir, "..");
const brandingLogo = join(desktopDir, "../../assets/branding/astrlink-logo.svg");
const uiLogo = join(desktopDir, "src/assets/astrlink-logo.svg");
const iconsDir = join(desktopDir, "src-tauri/icons");
const iconSvg = join(iconsDir, "icon.svg");
const stampPath = join(iconsDir, ".branding-hash");

// The macOS status item uses an 18-point template with a 36-pixel Retina image.
const menuBarLogo = join(desktopDir, "../../assets/branding/astrlink-menubar.svg");
const trayDir = join(iconsDir, "tray");
const traySvg = readFileSync(menuBarLogo);
const trayHash = createHash("sha256").update(traySvg).digest("hex");
const trayStampPath = join(trayDir, ".branding-hash");
mkdirSync(trayDir, { recursive: true });
copyFileSync(menuBarLogo, join(trayDir, "icon.svg"));
const previousTrayHash = existsSync(trayStampPath)
  ? readFileSync(trayStampPath, "utf8").trim()
  : "";
if (
  previousTrayHash !== trayHash ||
  !existsSync(join(trayDir, "18x18.png")) ||
  !existsSync(join(trayDir, "36x36.png"))
) {
  const trayResult = spawnSync(
    "bun",
    ["run", "tauri", "icon", menuBarLogo, "--output", trayDir, "--png", "18", "--png", "36"],
    { cwd: desktopDir, stdio: "inherit" },
  );
  if (trayResult.status !== 0) {
    process.exit(trayResult.status ?? 1);
  }
  writeFileSync(trayStampPath, `${trayHash}\n`);
  console.log("Synced macOS menu bar template icons.");
}

const svg = readFileSync(brandingLogo);
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

const iconResult = spawnSync(
  "bun",
  ["run", "tauri", "icon", brandingLogo],
  { cwd: desktopDir, stdio: "inherit" },
);
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
