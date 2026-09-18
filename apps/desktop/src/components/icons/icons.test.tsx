import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import * as icons from "./index";

describe("animated icon catalog", () => {
  it.each(Object.entries(icons))(
    "renders %s as one accessible SVG without layout wrappers",
    (_name, Icon) => {
      const markup = renderToStaticMarkup(
        <Icon size={16} className="size-4" />,
      );
      expect(markup.startsWith("<svg")).toBe(true);
      expect(markup.match(/<svg\b/g)).toHaveLength(1);
      expect(markup).toContain('width="16"');
      expect(markup).toContain('height="16"');
      expect(markup).toContain('aria-hidden="true"');
      expect(markup).toContain('viewBox="0 0 24 24"');
      expect(markup).toMatch(/<(?:path|circle|rect|line|polyline|polygon)\b/);
      expect(markup).not.toMatch(/<(?:div|span)\b/);
    },
  );

  it("keeps static Lucide imports out of the desktop source", () => {
    const root = fileURLToPath(new URL("../../", import.meta.url));
    for (const file of readdirSync(root, { recursive: true }) as string[]) {
      if (!/\.tsx?$/.test(file) || file.includes(".test.")) continue;
      expect(readFileSync(`${root}/${file}`, "utf8"), file).not.toMatch(
        /from\s+["']lucide-react["']/,
      );
    }
  });
});
