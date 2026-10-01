// @vitest-environment happy-dom
import { expect, it } from "vitest";
import { staticQuestionSVG } from "./question-image";
it("extracts fenced SVG and bounds its dimensions without discarding the drawing", () => {
  const svg = staticQuestionSVG(
    '```svg\n<svg xmlns="http://www.w3.org/2000/svg" width="2048" height="1024"><circle cx="30" cy="30" r="20"/></svg>\n```',
  );
  expect(svg).toContain('width="1024"');
  expect(svg).toContain('height="512"');
  expect(svg).toContain("circle");
});
it.each([
  "<script>alert(1)</script>",
  "<foreignObject><p>test</p></foreignObject>",
  '<use href="https://example.com/x.svg#a"/>',
  '<g onclick="alert(1)"/>',
  '<style>@import "https://example.com/style.css";</style>',
  '<path style="fill:url(https://example.com/x)"/>',
])("rejects active or remote SVG content: %s", (child) => {
  expect(() =>
    staticQuestionSVG(`<svg xmlns="http://www.w3.org/2000/svg">${child}</svg>`),
  ).toThrow();
});
