// Rasterizes the plan logos for the macOS widget from the marks the app draws
// (`ServiceKindIcon` → `vendor-marks.ts`). Asset catalogs need Xcode, so the
// widget ships plain PNGs; Chrome renders them, as it does the app's SVGs.
//
//   bun macos-widget/render-logos.mjs   (from apps/desktop, needs Chrome)
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";

const here = path.dirname(new URL(import.meta.url).pathname);
const modules = path.join(here, "../node_modules/");
const require = createRequire(modules);
const React = require("react");
const { renderToStaticMarkup } = require("react-dom/server");

// `kindMarks` for the plan-backed kinds; Droid has no mark and is drawn in code.
const marks = {
  codex: "Codex/components/Color",
  claude: "Claude/components/Color",
  grok: "Grok/components/Mono",
  antigravity: "Antigravity/components/Color",
  copilot: "GithubCopilot/components/Mono",
  opencode: "OpenCode/components/Mono",
  kimi: "Kimi/components/Mono",
  zhipu: "Zhipu/components/Color",
  minimax: "Minimax/components/Color",
};
const size = 64;
const chrome =
  process.env.CHROME ??
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

const out = path.join(here, "Resources/Logos");
mkdirSync(out, { recursive: true });
const work = mkdtempSync(path.join(tmpdir(), "astrlink-logos-"));
try {
  for (const [name, component] of Object.entries(marks)) {
    const module = await import(`${modules}@lobehub/icons/es/${component}.js`);
    // Mono marks become template images tinted by the widget's text color.
    const svg = renderToStaticMarkup(
      React.createElement(module.default, { size, color: "#000" }),
    ).replaceAll("currentColor", "#000");
    const page = path.join(work, `${name}.html`);
    writeFileSync(
      page,
      `<!doctype html><style>html,body{margin:0;background:transparent}svg{display:block}</style>${svg}`,
    );
    const target = path.join(out, `${name}.png`);
    const result = spawnSync(
      chrome,
      [
        "--headless=new",
        "--disable-gpu",
        "--hide-scrollbars",
        "--default-background-color=00000000",
        `--window-size=${size},${size}`,
        `--screenshot=${target}`,
        `file://${page}`,
      ],
      { stdio: "ignore" },
    );
    if (result.status !== 0) throw new Error(`Chrome failed on ${name}`);
    console.log(target);
  }
} finally {
  rmSync(work, { recursive: true, force: true });
}
