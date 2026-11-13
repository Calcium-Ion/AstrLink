// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { installHighlighterWorker } from "@/lib/test-highlighter-worker";
import { MarkdownContent } from "./MarkdownContent";

const mocks = vi.hoisted(() => ({
  native: false,
  openExternalURL: vi.fn(async (_url: string) => {}),
}));
vi.mock("@tauri-apps/api/core", () => ({ isTauri: () => mocks.native }));
vi.mock("@/bridge", () => ({ openExternalURL: mocks.openExternalURL }));

const worker = installHighlighterWorker();

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  mocks.native = false;
  mocks.openExternalURL.mockClear();
});

async function render(content: string) {
  await act(async () => root.render(<MarkdownContent content={content} />));
  await act(async () => {
    await vi.dynamicImportSettled();
  });
}

it("renders model replies as Markdown with shared tables and readable code", async () => {
  await render(
    '# Reply\n\n**OK** with `inline code`.\n\n- first\n- second\n\n```json\n{"ok":true}\n```\n\n| Model | Status |\n| --- | --- |\n| test | OK |\n\n- [x] complete',
  );
  expect(container.querySelector("h3")?.textContent).toBe("Reply");
  expect(container.querySelector("strong")?.textContent).toBe("OK");
  expect(container.querySelector("ul li")?.textContent).toBe("first");
  // A fence is a code block, one line per line, without the closing newline.
  expect(
    container.querySelector('[data-slot="code-block"] pre code')?.textContent,
  ).toBe('{"ok":true}');
  expect(container.querySelector('[data-slot="table"] td')?.textContent).toBe(
    "test",
  );
  expect(
    container.querySelector('[role="checkbox"]')?.getAttribute("aria-checked"),
  ).toBe("true");
});

it("keeps untrusted HTML and URLs inert and does not fetch model-provided images", async () => {
  await render(
    '<script>alert("bad")</script>\n\n[unsafe](javascript:alert%281%29)\n\n![preview](https://example.com/tracker.png)\n\n[safe](https://example.com/docs)',
  );
  expect(container.querySelector("script, iframe, img")).toBeNull();
  expect(container.querySelector('a[href^="javascript:"]')).toBeNull();
  expect(container.textContent).toContain('<script>alert("bad")</script>');
  // An image is a link to it, opened only when clicked.
  const image = container.querySelector<HTMLAnchorElement>(
    'a[href="https://example.com/tracker.png"]',
  );
  expect(image?.textContent).toBe("preview");
  expect(image?.getAttribute("rel")).toBe("noopener noreferrer");
  expect(
    container
      .querySelector('a[href="https://example.com/docs"]')
      ?.getAttribute("rel"),
  ).toBe("noopener noreferrer");
});

it("accepts plain text and truncated Markdown without losing the response", async () => {
  await render("OK");
  expect(container.querySelector("p")?.textContent).toBe("OK");
  await render('```json\n{"unfinished":');
  expect(container.querySelector("pre code")?.textContent).toContain(
    '{"unfinished":',
  );
});

it("highlights a fence by its language, keeps an unknown one plain and copies either", async () => {
  const copy = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();
  await render(
    "```ts\nconst answer: number = 42;\n```\n\n```brainfuck\n+[>+<-]\n```\n\nInline `code` stays inline.",
  );
  const blocks = container.querySelectorAll('[data-slot="code-block"]');
  expect(blocks).toHaveLength(2);
  expect(worker.requests.map((request) => request.language)).toEqual(
    expect.arrayContaining(["ts", "brainfuck"]),
  );
  await act(async () => {
    await worker.answered();
  });
  await vi.waitFor(() =>
    expect(
      blocks[0]!.querySelectorAll("code span[style]").length,
    ).toBeGreaterThan(0),
  );
  expect(blocks[0]!.querySelector("code")?.textContent).toBe(
    "const answer: number = 42;",
  );
  expect(blocks[1]!.querySelectorAll("code span[style]")).toHaveLength(0);
  expect(blocks[1]!.querySelector("code")?.textContent).toBe("+[>+<-]");
  expect(container.querySelector("p code")?.textContent).toBe("code");

  await act(async () =>
    blocks[1]!
      .querySelector<HTMLButtonElement>('[data-slot="code-block-copy-button"]')!
      .click(),
  );
  expect(copy).toHaveBeenCalledWith("+[>+<-]");
  copy.mockRestore();
});

it("renders GitHub alerts without the marker and keeps plain quotes", async () => {
  await render(
    "> [!IMPORTANT]\r\n> First release with [Guard](https://example.com/guard).\r\n\r\n> [!warning]\n>\n> Back up first.\n\n> [!NOTE] inline text stays a quote",
  );
  const paragraphs = (kind: string) =>
    Array.from(
      container.querySelectorAll(`[data-alert="${kind}"] > p`),
      (p) => p.textContent,
    );
  expect(paragraphs("important")).toEqual([
    "重要",
    "First release with Guard.",
  ]);
  expect(
    container.querySelector('[data-alert="important"] a')?.getAttribute("href"),
  ).toBe("https://example.com/guard");
  expect(paragraphs("warning")).toEqual(["警告", "Back up first."]);
  expect(container.querySelector("blockquote")?.textContent?.trim()).toBe(
    "[!NOTE] inline text stays a quote",
  );
});

it("opens links in the system browser inside the desktop shell", async () => {
  mocks.native = true;
  await render("[docs](https://example.com/docs)");
  const link = container.querySelector("a")!;
  const click = new MouseEvent("click", { bubbles: true, cancelable: true });
  await act(async () => link.dispatchEvent(click));
  expect(click.defaultPrevented).toBe(true);
  expect(mocks.openExternalURL).toHaveBeenCalledWith(
    "https://example.com/docs",
  );
});
