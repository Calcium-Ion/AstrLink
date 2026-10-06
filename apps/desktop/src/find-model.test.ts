// @vitest-environment happy-dom

import { describe, expect, it } from "vitest";

import {
  findMatcher,
  findMatches,
  findOffsets,
  findStepShortcut,
  isFindShortcut,
  stepFind,
} from "./find-model";

describe("find model", () => {
  it("asks for nothing when the query is blank", () => {
    expect(findMatcher("")).toBeNull();
    expect(findMatcher("   ")).toBeNull();
  });

  it("matches case-insensitively and takes the query literally", () => {
    const matcher = findMatcher(" Content ")!;
    expect(findOffsets('{"content":"CONTENT"}', matcher)).toEqual([2, 12]);

    // Pattern characters in a query are text, not a pattern.
    const literal = findMatcher("a.b(c)")!;
    expect(findOffsets("axb(c) a.b(c)", literal)).toEqual([7]);
    expect(findMatches("a.b(c)", literal)).toBe(true);
    expect(findMatches("axbc", literal)).toBe(false);
  });

  it("finds hits without overlaps and stops at the limit", () => {
    const matcher = findMatcher("aa")!;
    expect(findOffsets("aaaaa", matcher)).toEqual([0, 2]);
    expect(findOffsets("aa aa aa", matcher, 2)).toEqual([0, 3]);
    // The shared pattern is left ready for the next text.
    expect(findOffsets("aa", matcher)).toEqual([0]);
  });

  it("wraps when stepping past either end", () => {
    expect(stepFind(2, 3, 1)).toBe(0);
    expect(stepFind(0, 3, -1)).toBe(2);
    expect(stepFind(1, 3, 1)).toBe(2);
    expect(stepFind(4, 0, 1)).toBe(0);
  });

  it("recognizes the find and step shortcuts", () => {
    const key = (init: KeyboardEventInit) => new KeyboardEvent("keydown", init);
    expect(isFindShortcut(key({ key: "f", metaKey: true }))).toBe(true);
    expect(isFindShortcut(key({ key: "F", ctrlKey: true }))).toBe(true);
    expect(isFindShortcut(key({ key: "f" }))).toBe(false);
    expect(
      isFindShortcut(key({ key: "f", metaKey: true, shiftKey: true })),
    ).toBe(false);
    expect(findStepShortcut(key({ key: "g", metaKey: true }))).toBe(1);
    expect(
      findStepShortcut(key({ key: "G", metaKey: true, shiftKey: true })),
    ).toBe(-1);
    expect(findStepShortcut(key({ key: "g" }))).toBeNull();
  });
});
