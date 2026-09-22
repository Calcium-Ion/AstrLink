import { describe, expect, it } from "vitest";
import {
  dryRunFindingSpan,
  dryRunSampleText,
  dryRunTextAtPath,
} from "./privacy-dry-run-model";
import type { PrivacyDryRunFinding } from "./privacy-policy-model";

describe("dry-run original text", () => {
  const text = '  中文😀："alice@example.com"\n再次 alice@example.com  ';
  const body = JSON.stringify({ messages: [{ role: "user", content: text }] });
  const value = "alice@example.com";
  const start = text.lastIndexOf(value);
  const finding: PrivacyDryRunFinding = {
    kind: "email",
    path: "/messages/0/content",
    confidence: 1,
    start: new TextEncoder().encode(text.slice(0, start)).length,
    end: new TextEncoder().encode(text.slice(0, start + value.length)).length,
  };

  it("converts byte offsets after Chinese, emoji, quotes and newlines and locates repeated values", () => {
    expect(dryRunFindingSpan(body, finding)).toEqual({
      text,
      value,
      start,
      end: start + value.length,
    });
  });

  it("does not invent a snippet for invalid ranges or paths", () => {
    for (const range of [
      { start: 3, end: 5 },
      { start: -1, end: 4 },
      { start: 0, end: 999 },
      { start: 2, end: 2 },
    ]) {
      expect(dryRunFindingSpan(body, { ...finding, ...range })).toBeNull();
    }
    expect(
      dryRunFindingSpan(body, { ...finding, path: "/missing" }),
    ).toBeNull();
    expect(dryRunTextAtPath("invalid", "/input")).toBeNull();
    expect(dryRunTextAtPath('{"a/b":{"~text":"value"}}', "/a~1b/~0text")).toBe(
      "value",
    );
  });

  it("reads plain text from every supported protocol without including other request fields", () => {
    expect(dryRunSampleText(body, "openai.chat")).toBe(text);
    expect(dryRunSampleText(body, "anthropic.messages")).toBe(text);
    expect(
      dryRunSampleText(JSON.stringify({ prompt: text }), "openai.completions"),
    ).toBe(text);
    for (const protocol of [
      "openai.responses",
      "openai.responses.compact",
    ] as const) {
      expect(
        dryRunSampleText(
          JSON.stringify({ input: text, instructions: "system instruction" }),
          protocol,
        ),
      ).toBe(text);
    }
    expect(
      dryRunSampleText(
        JSON.stringify({ contents: [{ parts: [{ text }] }] }),
        "google.generate_content",
      ),
    ).toBe(text);
  });
});
