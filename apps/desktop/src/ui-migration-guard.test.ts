import { existsSync, readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const srcDirectory = fileURLToPath(new URL("./", import.meta.url));

/**
 * Pages migrated to the paper/hairline design system. Every file listed here is
 * held to the token scale; move a page onto the list as it is reworked so the
 * arbitrary values it used to carry cannot come back.
 */
const migratedSources = [
  "AccessTokenManager.tsx",
  "App.tsx",
  "AppErrorBoundary.tsx",
  "Overview.tsx",
  "AuditReviewer.tsx",
  "AutoRoutingShowcase.tsx",
  "PageHeader.tsx",
  "RequestRecords.tsx",
  "RequestTrajectory.tsx",
  "RouteManager.tsx",
  "SafetyPolicy.tsx",
  "ServiceManager.tsx",
  "ServiceModelsEditor.tsx",
  "SettingsCenter.tsx",
  "AgentDebugSettings.tsx",
  "components/AppShell.tsx",
  "components/ChoiceCard.tsx",
  "components/ConfirmDialog.tsx",
  "components/DataRow.tsx",
  "components/EmptyState.tsx",
  "components/Field.tsx",
  "components/FormMessage.tsx",
  "components/Panel.tsx",
  "components/SectionKicker.tsx",
  "components/StatusDot.tsx",
];

function readSource(relativePath: string): string {
  return readFileSync(`${srcDirectory}/${relativePath}`, "utf8");
}

function productionSources(): Array<[string, string]> {
  return readdirSync(srcDirectory)
    .filter(
      (name) =>
        (name.endsWith(".ts") || name.endsWith(".tsx")) &&
        !name.includes(".test."),
    )
    .map((name) => [name, readFileSync(`${srcDirectory}/${name}`, "utf8")]);
}

function componentizedPageSources(): Array<[string, string]> {
  return productionSources().filter(
    ([name]) => name.endsWith(".tsx") && name !== "WindowChrome.tsx",
  );
}

function uiPrimitiveSources(): Array<[string, string]> {
  return readdirSync(`${srcDirectory}/components/ui`)
    .filter((name) => name.endsWith(".tsx") && !name.includes(".test."))
    .map((name) => [
      `components/ui/${name}`,
      readFileSync(`${srcDirectory}/components/ui/${name}`, "utf8"),
    ]);
}

describe("shadcn migration guard", () => {
  it("keeps the deleted legacy stylesheet and BEM hooks out of production pages", () => {
    expect(existsSync(`${srcDirectory}/styles.css`)).toBe(false);

    for (const [name, source] of productionSources()) {
      expect(source, name).not.toMatch(
        /\b(?:btn-primary|btn-secondary|btn-danger|token-dialog|workspace-card|form-message--|dot--)/,
      );
    }
  });

  it("keeps dark-mode variants out until AstrLink defines a dark theme", () => {
    for (const [name, source] of productionSources()) {
      expect(source, name).not.toContain("dark:");
    }
  });

  it("keeps browser-blocking dialogs out of desktop production code", () => {
    for (const [name, source] of productionSources()) {
      expect(source, name).not.toMatch(
        /(?:\bwindow\.)?\b(?:confirm|alert|prompt)\s*\(/,
      );
    }
  });

  it("uses the component layer for page-level form controls and tables", () => {
    for (const [name, source] of componentizedPageSources()) {
      expect(source, name).not.toMatch(
        /<(?:button|input|textarea|select|option|label|table|thead|tbody|tr|th|td|datalist)\b/,
      );
    }
  });
});

describe("paper/hairline design system", () => {
  it("locks the AstrLink semantic colors into the Tailwind theme", () => {
    const globals = readSource("styles/globals.css");

    expect(globals).toContain("--background: #ffffff;");
    expect(globals).toContain("--foreground: #1a2a3d;");
    expect(globals).toContain("--primary: #1d4d87;");
    expect(globals).toContain("--primary-hover: #16345c;");
    expect(globals).toContain("--destructive: #c2384f;");
    expect(globals).toContain("--success: #1f9d6b;");
    expect(globals).toContain("--warning: #d2911a;");
    expect(globals).toContain("--violet: #2f6fe0;");
    expect(globals).toContain("--color-primary: var(--primary);");
    expect(globals).toContain("--color-success: var(--success);");
    expect(globals).toContain("--color-warning: var(--warning);");
    expect(globals).toContain("--color-violet: var(--violet);");
  });

  it("keeps the logo's own indigo/tide, the UI on a separate blue, and bans gradients", () => {
    const globals = readSource("styles/globals.css");
    const logo = readSource("assets/astrlink-logo.svg");

    // Logo art stays indigo / periwinkle. UI brand ink is an independent
    // pure blue and must not be retied to those logo stops.
    expect(logo).toContain("#26336F");
    expect(logo).toContain("#768DF6");
    expect(globals).toContain("--primary: #1d4d87;");
    expect(globals).toContain("--tide: #3b82f6;");
    expect(globals).toContain("--color-tide: var(--tide);");

    for (const [name, source] of [
      ["styles/globals.css", globals] as const,
      ...migratedSources.map(
        (path) => [path, readSource(path)] as [string, string],
      ),
    ]) {
      expect(source, `${name} paints a gradient`).not.toMatch(
        /linear-gradient|radial-gradient|conic-gradient|\bbg-gradient-/,
      );
    }
  });

  it("defines exactly one type scale and one radius scale", () => {
    const globals = readSource("styles/globals.css");

    for (const step of ["micro", "xs", "sm", "base", "lg", "xl", "2xl"]) {
      expect(globals, `--text-${step}`).toContain(`--text-${step}:`);
    }

    // Cards, dialogs and list rows must all land on the same 8px cap, which is
    // also what legacy rounded-xl/2xl usages collapse onto.
    expect(globals).toContain("--radius-sm: 0.25rem;");
    expect(globals).toContain("--radius-md: 0.375rem;");
    expect(globals).toContain("--radius-lg: 0.5rem;");
    expect(globals).toContain("--radius-xl: 0.5rem;");
    expect(globals).toContain("--radius-2xl: 0.5rem;");
  });

  it("keeps card elevation flat so hairlines carry the hierarchy", () => {
    const globals = readSource("styles/globals.css");

    expect(globals).toContain("--shadow-card: none;");
    expect(globals).not.toMatch(/--shadow-card:\s*0 /);
  });

  it("keeps ad-hoc sizes, radii and shadows out of migrated sources", () => {
    for (const name of migratedSources) {
      const source = readSource(name);

      expect(source, `${name} uses an arbitrary font size`).not.toMatch(
        /\btext-\[/,
      );
      expect(source, `${name} uses an arbitrary radius`).not.toMatch(
        /\brounded-\[(?!inherit\])/,
      );
      expect(source, `${name} uses a one-off shadow`).not.toMatch(
        /\bshadow-\[/,
      );
    }
  });

  it("keeps the shared primitives on the token scale", () => {
    for (const [name, source] of uiPrimitiveSources()) {
      expect(source, `${name} uses an arbitrary font size`).not.toMatch(
        /\btext-\[/,
      );
      expect(source, `${name} uses an arbitrary radius`).not.toMatch(
        /\brounded-\[(?!inherit\])/,
      );
      expect(source, `${name} uses a one-off shadow`).not.toMatch(
        /\bshadow-\[/,
      );
    }
  });
});
