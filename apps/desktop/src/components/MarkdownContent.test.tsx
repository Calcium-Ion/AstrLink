// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { MarkdownContent } from "./MarkdownContent";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

async function render(content: string) {
  await act(async () => root.render(<MarkdownContent content={content} />));
  await act(async () => { await vi.dynamicImportSettled(); });
}

it("renders model replies as Markdown with shared tables and readable code", async () => {
  await render("# Reply\n\n**OK** with `inline code`.\n\n- first\n- second\n\n```json\n{\"ok\":true}\n```\n\n| Model | Status |\n| --- | --- |\n| test | OK |\n\n- [x] complete");
  expect(container.querySelector("h3")?.textContent).toBe("Reply");
  expect(container.querySelector("strong")?.textContent).toBe("OK");
  expect(container.querySelector("ul li")?.textContent).toBe("first");
  expect(container.querySelector("pre code")?.textContent).toBe('{"ok":true}\n');
  expect(container.querySelector('[data-slot="table"] td')?.textContent).toBe("test");
  expect(container.querySelector('[role="checkbox"]')?.getAttribute("aria-checked")).toBe("true");
});

it("keeps untrusted HTML and URLs inert and does not fetch model-provided images", async () => {
  await render('<script>alert("bad")</script>\n\n[unsafe](javascript:alert%281%29)\n\n![preview](https://example.com/tracker.png)\n\n[safe](https://example.com/docs)');
  expect(container.querySelector("script, iframe, img")).toBeNull();
  expect(container.querySelector('a[href^="javascript:"]')).toBeNull();
  expect(container.textContent).toContain('<script>alert("bad")</script>');
  expect(container.textContent).toContain("preview");
  expect(container.querySelector('a[href="https://example.com/docs"]')?.getAttribute("rel")).toBe("noopener noreferrer");
});

it("accepts plain text and truncated Markdown without losing the response", async () => {
  await render("OK");
  expect(container.querySelector("p")?.textContent).toBe("OK");
  await render('```json\n{"unfinished":');
  expect(container.querySelector("pre code")?.textContent).toContain('{"unfinished":');
});
