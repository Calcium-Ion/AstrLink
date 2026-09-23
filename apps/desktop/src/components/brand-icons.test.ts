import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const srcDirectory = fileURLToPath(new URL("../", import.meta.url));

describe("brand icons", () => {
  it("keeps production sources off the @lobehub/icons barrel and brand indexes", () => {
    const offenders = readdirSync(srcDirectory, { recursive: true })
      .map(String)
      .filter((name) => /\.tsx?$/.test(name) && !name.includes(".test."))
      .filter((name) =>
        /from "@lobehub\/icons(\/es\/[^/"]+)?"/.test(
          readFileSync(`${srcDirectory}/${name}`, "utf8"),
        ),
      );

    expect(offenders).toEqual([]);
  });
});
