// Run after `make web`. Uses an isolated headless Playwright browser and a
// disposable Core data directory. PLAYWRIGHT_MODULE can point to an installed
// playwright package when it is not a dependency of this workspace.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const directory = await mkdtemp(join(tmpdir(), "astrlink-console-test-"));
const listener = createServer();
await new Promise((resolve) => listener.listen(0, "127.0.0.1", resolve));
const port = listener.address().port;
await new Promise((resolve) => listener.close(resolve));
const origin = `http://127.0.0.1:${port}`;
// Deliberately omit deployment secrets, proxies, worker paths and reset flags.
const core = spawn(
  fileURLToPath(new URL("../../../core/bin/astrlink-core", import.meta.url)),
  ["serve"],
  {
    env: {
      PATH: process.env.PATH,
      ASTRLINK_DATA_DIR: directory,
      ASTRLINK_LISTEN: `127.0.0.1:${port}`,
    },
    stdio: ["ignore", "pipe", "pipe"],
  },
);
let logs = "";
core.stderr.on("data", (data) => {
  logs += data.toString();
});
let browser;
try {
  for (let n = 0; ; n++) {
    try {
      if ((await fetch(`${origin}/console/v1/status`)).ok) break;
    } catch {
      /* startup */
    }
    if (n > 100 || core.exitCode !== null)
      throw new Error(`Core did not start: ${logs}`);
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1280, height: 720 },
  });
  const page = await context.newPage();
  const errors = [];
  const calls = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(message.text());
  });
  await page.addInitScript(() => {
    window.consoleViolations = [];
    document.addEventListener("securitypolicyviolation", (event) =>
      window.consoleViolations.push(event.violatedDirective),
    );
  });
  // A regression must fail before it sends a probe or inference request.
  await page.route("**/*", async (route) => {
    const url = new URL(route.request().url());
    if (
      url.origin !== origin ||
      /\/(test|probe-models|service-model-probes|service-proxy-probes|authorization)$/.test(
        url.pathname,
      )
    ) {
      errors.push(`Unexpected upstream action: ${url.pathname}`);
      await route.abort();
      return;
    }
    if (url.pathname.startsWith("/control/")) calls.push(url.pathname);
    await route.continue();
  });
  await page.goto(origin);
  await page
    .getByLabel("Password", { exact: true })
    .fill("test-console-password-1");
  await page
    .getByLabel("Confirm password", { exact: true })
    .fill("test-console-password-1");
  await page
    .getByRole("button", { name: "Set console password", exact: true })
    .click();
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await page
    .getByLabel("Password", { exact: true })
    .fill("test-console-password-1");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("button", { name: "Sign out", exact: true }).waitFor();
  const rawStatus = await context.request.get(
    `${origin}/control/v1/audit/raw-sealing`,
  );
  assert.equal((await rawStatus.json()).unlocked, false);
  await page
    .getByRole("button", { name: "Access tokens", exact: true })
    .click();
  await page.getByRole("button", { name: "Create token", exact: true }).click();
  await page
    .getByLabel("Token name", { exact: true })
    .fill("Browser verification");
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await page.getByText("Browser verification", { exact: true }).waitFor();
  await page
    .getByRole("button", { name: /Configure a client for/ })
    .first()
    .click();
  assert.match(
    await page
      .getByRole("textbox", { name: "Connect a client", exact: true })
      .inputValue(),
    new RegExp(origin.replaceAll(".", "\\.")),
  );
  await page.getByRole("tab", { name: "Codex", exact: true }).click();
  assert.ok(
    (
      await page
        .getByRole("textbox", { name: "Connect a client", exact: true })
        .inputValue()
    ).includes(`${origin}/v1`),
  );
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await page
    .getByRole("button", { name: "API providers", exact: true })
    .click();
  const help = page.getByRole("button", { name: "Got it", exact: true });
  if (await help.isVisible()) await help.click();
  await page
    .getByRole("button", { name: "Add API provider", exact: true })
    .first()
    .click();
  await page.getByRole("button", { name: /Custom API Declare/ }).click();
  await page
    .getByLabel("API provider name", { exact: true })
    .fill("Browser test provider");
  await page
    .getByLabel("API address", { exact: true })
    .fill("http://127.0.0.1:1");
  await page
    .getByRole("combobox", { name: "Auth method", exact: true })
    .click();
  await page.getByRole("option", { name: "No auth", exact: true }).click();
  await page.getByTestId("service-editor-tab-protocols").click();
  await page.waitForTimeout(300);
  if (
    await page.getByRole("button", { name: "Got it", exact: true }).isVisible()
  )
    await page.getByRole("button", { name: "Got it", exact: true }).click();
  if (await page.getByRole("button", { name: "Skip", exact: true }).isVisible())
    await page.getByRole("button", { name: "Skip", exact: true }).click();
  await page
    .getByTestId("service-capability-row")
    .first()
    .getByRole("switch")
    .first()
    .click();
  const saved = page.waitForResponse(
    (response) =>
      response.url() === `${origin}/control/v1/services` &&
      response.request().method() === "POST",
  );
  await page
    .getByRole("button", { name: "Save API provider", exact: true })
    .click();
  const savedResponse = await saved;
  assert.ok(savedResponse.ok(), await savedResponse.text());
  const service = await savedResponse.json();
  await page
    .getByText("Browser test provider", { exact: true })
    .first()
    .waitFor();
  // Seed a long model list through the same authenticated control API.
  const seeded = await context.request.patch(
    `${origin}/control/v1/services/${service.id}`,
    {
      headers: {
        "X-AstrLink-Console": "1",
        "If-Match": savedResponse.headers().etag,
        "Content-Type": "application/merge-patch+json",
      },
      data: {
        models: Array.from(
          { length: 100 },
          (_, index) => `test-model-${index}`,
        ),
      },
    },
  );
  assert.ok(seeded.ok(), await seeded.text());
  await page.getByRole("button", { name: "Requests", exact: true }).click();
  await page.getByTestId("request-records-scroll").waitFor();
  await page.waitForTimeout(300);
  assert.ok(calls.includes("/control/v1/request-sessions"));
  const measurements = [];
  for (const [width, height] of [
    [1280, 720],
    [1024, 600],
    [390, 844],
  ]) {
    await page.setViewportSize({ width, height });
    await page.waitForTimeout(300);
    const region = await page
      .getByTestId("request-records-scroll")
      .boundingBox();
    const usable = await page
      .locator('[data-slot="workspace"]')
      .evaluate((el) => {
        const style = getComputedStyle(el);
        return (
          el.clientHeight -
          parseFloat(style.paddingTop) -
          parseFloat(style.paddingBottom)
        );
      });
    measurements.push({
      width,
      height,
      primary: region.height,
      usable,
      ratio: region.height / usable,
    });
    assert.ok(
      region.height / usable >= 0.6,
      JSON.stringify(measurements.at(-1)),
    );
    assert.equal(
      await page.evaluate(
        () => document.documentElement.scrollWidth > innerWidth,
      ),
      false,
    );
    if (process.env.ASTRLINK_TEST_SCREENSHOTS)
      await page.screenshot({
        path: join(
          process.env.ASTRLINK_TEST_SCREENSHOTS,
          `console-${width}x${height}.png`,
        ),
      });
  }
  await page.setViewportSize({ width: 1280, height: 720 });
  await page
    .getByRole("button", { name: "API providers", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Edit Browser test provider", exact: true })
    .click();
  await page.getByTestId("service-editor-tab-models").click();
  const expand = page.getByRole("button", { name: "Expand all", exact: true });
  if (await expand.isVisible()) await expand.click();
  await page.getByText("test-model-99", { exact: true }).waitFor();
  const panel = page
    .getByTestId("service-editor-tab-panel")
    .filter({ visible: true });
  await panel.evaluate((el) => {
    el.scrollTop = el.scrollHeight;
  });
  const workspaceTop = await page
    .locator('[data-slot="workspace"]')
    .evaluate((el) => el.scrollTop);
  await page.getByTestId("service-editor-tab-protocols").click();
  assert.equal(
    await page
      .locator('[data-slot="workspace"]')
      .evaluate((el) => el.scrollTop),
    workspaceTop,
  );
  await page
    .getByRole("button", { name: "Safety policy", exact: true })
    .click();
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  assert.equal(
    await page.getByText("Console settings", { exact: true }).count(),
    1,
  );
  assert.equal(
    await page
      .getByRole("button", { name: "Agent tools", exact: true })
      .count(),
    0,
  );
  assert.equal(
    await page.getByRole("button", { name: "About", exact: true }).count(),
    0,
  );
  assert.deepEqual(await page.evaluate(() => window.consoleViolations), []);
  assert.deepEqual(errors, []);
  await context.clearCookies();
  await page.getByRole("button", { name: "Sign in", exact: true }).waitFor();
  assert.equal(new URL(page.url()).pathname, "/");
  console.log(
    JSON.stringify(
      {
        result: "passed",
        flows: [
          "setup",
          "logout",
          "login",
          "create token",
          "client snippets",
          "create provider without credentials",
          "request records",
          "safety",
          "settings",
          "401 returns to login",
          "long model list tab switch",
        ],
        measurements,
        cspViolations: 0,
      },
      null,
      2,
    ),
  );
} finally {
  await browser?.close();
  core.kill("SIGINT");
  await new Promise((resolve) => {
    if (core.exitCode !== null) resolve();
    else core.once("exit", resolve);
  });
  await rm(directory, { recursive: true, force: true });
}
