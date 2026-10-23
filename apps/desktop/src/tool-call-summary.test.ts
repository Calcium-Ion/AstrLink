import { describe, expect, it } from "vitest";

import { toolCallSummary } from "./tool-call-summary";

describe("toolCallSummary", () => {
  it.each([
    ["Read", { file_path: "src/main.ts", limit: 200 }, "file", "src/main.ts"],
    ["mcp__filesystem__read_file", { path: "notes.txt" }, "file", "notes.txt"],
    [
      "functions.exec_command",
      { cmd: "bun run test", yield_time_ms: 1000 },
      "command",
      "bun run test",
    ],
    ["Bash", { command: "git\n status" }, "command", "git status"],
    ["Grep", { pattern: "useState", path: "src" }, "search", "useState"],
    ["Glob", { pattern: "**/*.tsx" }, "search", "**/*.tsx"],
    ["custom.lookup", { path: "src" }, "generic", '{"path":"src"}'],
    ["Read", { path: 3 }, "generic", '{"path":3}'],
  ])(
    "extracts a preview for %s without changing its input",
    (name, value, kind, text) => {
      const args = JSON.stringify(value);
      expect(toolCallSummary(name, args)).toEqual({ kind, text });
      expect(JSON.parse(args)).toEqual(value);
    },
  );

  it("keeps malformed, truncated, and non-object input readable", () => {
    expect(toolCallSummary("Read", '{ "path": "truncated')).toEqual({
      kind: "generic",
      text: '{ "path": "truncated',
    });
    expect(toolCallSummary("Read", "null").text).toBe("null");
    expect(toolCallSummary("Read", '["a","b"]').kind).toBe("generic");
    expect(toolCallSummary("Read", "").text).toBe("");
  });

  it("bounds long summaries, including known commands", () => {
    const args = JSON.stringify({ command: "x".repeat(20_000) });
    const summary = toolCallSummary("Bash", args);
    expect(summary.text.length).toBe(180);
    expect(summary.text.endsWith("…")).toBe(true);
  });
});
