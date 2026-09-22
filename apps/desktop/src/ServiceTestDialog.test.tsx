// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ testService: vi.fn(), failRendering: false }));
vi.mock("./bridge", () => ({ testService: mocks.testService }));
vi.mock("./components/MarkdownRenderer", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./components/MarkdownRenderer")>();
  return {
    default: (props: { content: string }) => {
      if (mocks.failRendering) throw new Error("Simulated response parser failure");
      return <actual.default {...props} />;
    },
  };
});

import { AppErrorBoundary } from "./AppErrorBoundary";
import { ServiceTestDialog } from "./ServiceTestDialog";
import type { Service } from "./service-model";

const service: Service = {
  id: "service_test", name: "Test provider", kind: "openai", enabled: true,
  models: ["test-model"], capabilities: [{ protocol: "openai.chat", mode: "native", streaming: true }],
  http: { base_url: "https://example.com", auth: { scheme: "none" } },
  created_at: "2026-09-22T00:00:00Z", updated_at: "2026-09-22T00:00:00Z",
};
let root: Root;
let container: HTMLDivElement;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  mocks.testService.mockReset();
  mocks.failRendering = false;
  vi.spyOn(console, "error").mockImplementation(() => undefined);
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});

function button(text: string) {
  return [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find(item => item.textContent === text || (text === "开始测试" && item.textContent === "重新测试"))!;
}

it("shows header, post-header, first-text and complete timings without treating missing data as zero", async () => {
  mocks.testService.mockResolvedValue({ service_id: service.id, protocol: "openai.chat", model: "test-model", stream: true, ok: true, status_code: 200, duration_ms: 2665, response_headers_ms: 120, first_token_ms: 840, output: "OK" });
  await act(async () => root.render(<ServiceTestDialog service={service} onClose={() => {}} />));
  await act(async () => button("开始测试").click());
  const dialog = document.querySelector('[role="dialog"]')!;
  for (const value of ["HTTP 响应0.12 s", "首字等待0.72 s", "首字总延迟0.84 s", "完整耗时2.67 s"]) {
    expect(dialog.textContent).toContain(value);
  }
  await act(async () => document.querySelector<HTMLButtonElement>('button[aria-label="耗时说明"]')!.click());
  expect(document.body.textContent).toContain("并非纯网络延迟");
  expect(document.body.textContent).toContain("首字总延迟 = HTTP 响应 + 首字等待");
});

it("keeps missing and non-streaming first-text timings unavailable", async () => {
  mocks.testService.mockResolvedValue({ service_id: service.id, protocol: "openai.chat", model: "test-model", stream: false, ok: true, status_code: 200, duration_ms: 500, response_headers_ms: 100, first_token_ms: null, output: "OK" });
  await act(async () => root.render(<ServiceTestDialog service={service} onClose={() => {}} />));
  await act(async () => button("开始测试").click());
  const dialog = document.querySelector('[role="dialog"]')!;
  expect(dialog.textContent).toContain("首字等待—");
  expect(dialog.textContent).toContain("首字总延迟—");
  await act(async () => document.querySelector<HTMLButtonElement>('button[aria-label="耗时说明"]')!.click());
  expect(document.body.textContent).toContain("非流式响应无法测量首字延迟");
  mocks.testService.mockResolvedValue({ service_id: service.id, protocol: "openai.chat", model: "test-model", stream: true, ok: false, status_code: 0, duration_ms: 60000, response_headers_ms: null, first_token_ms: null, output: "", error_code: "timeout", message: "Timed out" });
  await act(async () => button("开始测试").click());
  expect(dialog.textContent).toContain("HTTP 响应—");
  expect(dialog.textContent).toContain("完整耗时60.00 s");
  expect(dialog.textContent).toContain("测试失败");
});

it("contains parser failures within the response, preserves raw text, and allows retry and close", async () => {
  const onClose = vi.fn();
  mocks.failRendering = true;
  mocks.testService.mockResolvedValue({ service_id: service.id, protocol: "openai.chat", model: "test-model", stream: true, ok: true, status_code: 200, duration_ms: 120, output: '**OK**\n<script>bad()</script>' });
  await act(async () => root.render(<AppErrorBoundary><ServiceTestDialog service={service} onClose={onClose} /></AppErrorBoundary>));
  await act(async () => button("开始测试").click());
  await act(async () => { await vi.dynamicImportSettled(); });
  const dialog = document.querySelector('[role="dialog"]')!;
  expect(dialog.textContent).toContain("内容解析失败，已显示原始文本。");
  expect(dialog.querySelector("pre")?.textContent).toBe('**OK**\n<script>bad()</script>');
  expect(dialog.querySelector("script")).toBeNull();
  expect(document.body.textContent).not.toContain("AstrLink 界面遇到问题");
  expect(dialog.textContent).toContain("测试成功");
  expect(button("开始测试").disabled).toBe(false);
  expect(button("关闭").disabled).toBe(false);

  // A later response must get a fresh rendering attempt, not a stuck fallback.
  mocks.failRendering = false;
  mocks.testService.mockResolvedValue({ service_id: service.id, protocol: "openai.chat", model: "test-model", stream: true, ok: true, status_code: 200, duration_ms: 100, output: "**Recovered**" });
  await act(async () => button("开始测试").click());
  expect(dialog.querySelector("strong")?.textContent).toBe("Recovered");
  expect(dialog.textContent).not.toContain("内容解析失败");
  await act(async () => button("关闭").click());
  expect(onClose).toHaveBeenCalledOnce();
});

it("shows the failure reason alongside partial output from an interrupted stream", async () => {
  mocks.testService.mockResolvedValue({ service_id: service.id, protocol: "openai.chat", model: "test-model", stream: true, ok: false, status_code: 200, duration_ms: 100, output: "Partial reply", error_code: "interrupted", message: "Provider response was interrupted." });
  await act(async () => root.render(<ServiceTestDialog service={service} onClose={() => {}} />));
  await act(async () => button("开始测试").click());
  await act(async () => { await vi.dynamicImportSettled(); });
  const dialog = document.querySelector('[role="dialog"]')!;
  expect(dialog.textContent).toContain("Partial reply");
  expect(dialog.textContent).toContain("Provider response was interrupted.");
});
