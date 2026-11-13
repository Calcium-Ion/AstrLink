import { describe, expect, it } from "vitest";

import { foldDiff, lineDiff } from "./line-diff";

const kinds = (before: string, after: string, maxCells?: number) =>
  lineDiff(before, after, maxCells).map(
    (line) =>
      `${line.kind === "same" ? " " : line.kind === "added" ? "+" : "-"}${line.text}`,
  );

describe("line diff", () => {
  it("keeps the lines two versions share and marks the rest", () => {
    expect(
      kinds("# Rules\nRun gofmt.\nRun go vet.\n", "# Rules\nRun gofmt.\n"),
    ).toStrictEqual([" # Rules", " Run gofmt.", "-Run go vet."]);
    expect(kinds("a\nb\nc\nd", "a\nB\nc\nd\ne")).toStrictEqual([
      " a",
      "-b",
      "+B",
      " c",
      " d",
      "+e",
    ]);
    // Line endings and a final newline do not count as changes.
    expect(kinds("a\r\nb\r\n", "a\nb")).toStrictEqual([" a", " b"]);
    expect(kinds("", "a\n")).toStrictEqual(["+a"]);
    expect(kinds("a", "")).toStrictEqual(["-a"]);
  });

  it("reads a middle too large to compare as replaced", () => {
    expect(kinds("x\na\nb\ny", "x\nb\na\ny", 1)).toStrictEqual([
      " x",
      "-a",
      "-b",
      "+b",
      "+a",
      " y",
    ]);
    expect(kinds("x\na\nb\ny", "x\nb\na\ny")).toStrictEqual([
      " x",
      "-a",
      " b",
      "+a",
      " y",
    ]);
  });

  it("folds unchanged lines away from the changes", () => {
    const before = Array.from({ length: 12 }, (_, index) => `line ${index}`);
    const after = [...before];
    after[6] = "line six";
    expect(
      foldDiff(lineDiff(before.join("\n"), after.join("\n"))).map((line) =>
        line.kind === "fold" ? `…${line.count}` : `${line.kind}:${line.text}`,
      ),
    ).toStrictEqual([
      "…4",
      "same:line 4",
      "same:line 5",
      "removed:line 6",
      "added:line six",
      "same:line 7",
      "same:line 8",
      "…3",
    ]);
    expect(foldDiff(lineDiff("a\nb", "a\nb"))).toStrictEqual([
      { kind: "fold", count: 2 },
    ]);
  });
});
