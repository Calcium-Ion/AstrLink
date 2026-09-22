// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ResponseViewer } from "./ResponseViewer";

let root: Root;
let container: HTMLDivElement;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});

it("switches between rendered text and upstream JSON and copies the active view", async () => {
  const copy = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();
  const content = "**OK**\n\n```json\n{\"ok\": true}\n```";
  const raw = JSON.stringify({ choices: [{ message: { content } }] });
  await act(async () => root.render(<ResponseViewer content={content} rawContent={raw} contentType="application/json" label="回复" />));
  await act(async () => { await vi.dynamicImportSettled(); });
  expect(container.querySelector("strong")?.textContent).toBe("OK");
  const button = (label: string) => [...container.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent === label)!;
  await act(async () => button("原文").click());
  expect(container.querySelector("pre")?.textContent).toBe(raw);
  expect(container.querySelector("strong")).toBeNull();
  await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="复制原始响应"]')!.click());
  expect(copy).toHaveBeenLastCalledWith(raw);
  expect(container.textContent).toContain("已复制");
  await act(async () => button("预览").click());
  expect(container.querySelector("strong")?.textContent).toBe("OK");
  await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="复制回复"]')!.click());
  expect(copy).toHaveBeenLastCalledWith(content);
});

it("keeps malformed upstream bodies inert and explains truncation", async () => {
  const raw = 'event: error\r\ndata: <script>bad()</script>\r\n\r\n';
  await act(async () => root.render(<ResponseViewer content="" rawContent={raw} rawTruncated contentType="text/event-stream" label="回复" />));
  await act(async () => [...container.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent === "原文")!.click());
  expect(container.querySelector("pre")?.textContent).toBe(raw);
  expect(container.querySelector("script")).toBeNull();
  expect(container.textContent).toContain("text/event-stream");
  expect(container.textContent).toContain("已截断");
});

it("never substitutes extracted text when an older Core omits the raw response", async () => {
  await act(async () => root.render(<ResponseViewer content="OK" label="回复" />));
  await act(async () => [...container.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent === "原文")!.click());
  expect(container.textContent).toContain("原始响应不可用");
  expect(container.querySelector("pre")).toBeNull();
  expect(container.querySelector<HTMLButtonElement>('[aria-label="复制原始响应"]')!.disabled).toBe(true);
  await act(async () => root.render(<ResponseViewer content="" rawContent="" label="回复" />));
  expect(container.textContent).toContain("未收到响应正文");
});

it("keeps the response visible when clipboard access fails", async () => {
  vi.spyOn(navigator.clipboard, "writeText").mockRejectedValue(new Error("clipboard unavailable"));
  await act(async () => root.render(<ResponseViewer content="OK" label="回复" />));
  await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="复制回复"]')!.click());
  expect(container.textContent).toContain("复制失败");
  expect(container.querySelector('[data-slot="response-content"]')?.textContent).toBe("OK");
});
