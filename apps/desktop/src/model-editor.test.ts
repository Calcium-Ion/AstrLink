import { describe, expect, it } from "vitest";

import { decodeModelEditorValue, encodeModelEditorValue } from "./model-editor";

describe("model editor escaping", () => {
  it.each([
    ["carriage return", "model\rrevision"],
    ["CRLF", "model\r\nrevision"],
    ["line feed", "model\nrevision"],
    ["literal escape text", String.raw`model\r\nrevision`],
    ["controls and supplementary Unicode", "\u0000\t😀\u007f"],
  ])("round-trips %s without browser newline normalization", (_name, value) => {
    expect(decodeModelEditorValue(encodeModelEditorValue(value))).toBe(value);
  });

  it("uses a canonical visible representation for control characters", () => {
    expect(encodeModelEditorValue("a\r\nb\tc")).toBe(String.raw`a\r\nb\tc`);
    expect(decodeModelEditorValue(String.raw`a\\r`)).toBe(String.raw`a\r`);
  });
});
