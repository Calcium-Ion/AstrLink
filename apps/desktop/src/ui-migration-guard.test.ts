import { existsSync, readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const srcDirectory = fileURLToPath(new URL("./", import.meta.url));

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

  it("locks the AstrLink semantic colors into the Tailwind theme", () => {
    const globals = readFileSync(
      `${srcDirectory}/styles/globals.css`,
      "utf8",
    );

    expect(globals).toContain("--background: #fafbfc;");
    expect(globals).toContain("--foreground: #141a24;");
    expect(globals).toContain("--primary: #1a5fd4;");
    expect(globals).toContain("--primary-hover: #1549a0;");
    expect(globals).toContain("--destructive: #c0344a;");
    expect(globals).toContain("--success: #1fad6f;");
    expect(globals).toContain("--warning: #e6a317;");
    expect(globals).toContain("--violet: #5b4bd6;");
    expect(globals).toContain("--color-primary: var(--primary);");
    expect(globals).toContain("--color-success: var(--success);");
    expect(globals).toContain("--color-warning: var(--warning);");
    expect(globals).toContain("--color-violet: var(--violet);");
  });
});
