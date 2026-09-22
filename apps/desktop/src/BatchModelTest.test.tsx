// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ testService: vi.fn() }));
vi.mock("./bridge", () => ({ testService: mocks.testService }));
import { ServiceTestDialog } from "./ServiceTestDialog";
import type { Service } from "./service-model";
import type { ServiceTestInput, ServiceTestResult } from "./service-test-model";

const service: Service = {
  id: "service_test",
  name: "Batch provider",
  kind: "openai",
  enabled: true,
  models: ["alpha", "beta", "gamma"],
  capabilities: [{ protocol: "openai.chat", mode: "native", streaming: true }],
  http: { base_url: "https://example.test", auth: { scheme: "none" } },
  created_at: "2026-09-22T00:00:00Z",
  updated_at: "2026-09-22T00:00:00Z",
};
let root: Root;
let container: HTMLDivElement;
const result = (model: string, ok = true): ServiceTestResult => ({
  service_id: service.id,
  protocol: "openai.chat",
  stream: true,
  model,
  ok,
  status_code: ok ? 200 : 429,
  duration_ms: 1250,
  response_headers_ms: 100,
  first_token_ms: ok ? 230 : null,
  output: ok ? "**OK**" : "",
  raw_response: ok
    ? 'data: {"text":"OK"}\n\ndata: [DONE]\n\n'
    : '{"error":{"message":"quota"}}',
  response_content_type: ok ? "text/event-stream" : "application/json",
  ...(ok ? {} : { message: "quota", error_code: "upstream_error" }),
});
const buttons = () => [
  ...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button'),
];
function button(text: string) {
  // CSS is not loaded in happy-dom; ignore the preserved inactive mode.
  const match = buttons().find(
    (item) =>
      (item.getAttribute("aria-label") ?? item.textContent) === text &&
      !item.closest(".hidden"),
  );
  if (!match) throw new Error(`Missing button ${text}`);
  return match;
}
const batch = () =>
  document.querySelector<HTMLElement>('[data-testid="batch-model-tests"]')!;
const modelRow = (model: string) =>
  [...batch().querySelectorAll("tbody tr")].find(
    (row) => row.querySelector("[title]")?.getAttribute("title") === model,
  )!;
async function open(models = service.models) {
  await act(async () =>
    root.render(
      <ServiceTestDialog service={{ ...service, models }} onClose={() => {}} />,
    ),
  );
  await act(async () => button("批量模型").click());
}
async function search(value: string) {
  const input = batch().querySelector<HTMLInputElement>(
    'input[type="search"]',
  )!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
beforeEach(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  mocks.testService.mockReset();
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});

it("runs selected models, shows original failures and retries only failed items", async () => {
  mocks.testService.mockImplementation(
    async (_id: string, input: ServiceTestInput) =>
      result(input.model, input.model !== "beta"),
  );
  await open();
  await act(async () =>
    modelRow("gamma")
      .querySelector<HTMLButtonElement>('[role="checkbox"]')!
      .click(),
  );
  await act(async () => button("测试 2 个模型").click());
  expect(mocks.testService.mock.calls.map(([, input]) => input.model)).toEqual([
    "alpha",
    "beta",
  ]);
  expect(modelRow("alpha").textContent).toContain("成功0.23 s1.25 s");
  expect(modelRow("beta").textContent).toContain("失败—1.25 s");
  await act(async () =>
    modelRow("beta")
      .querySelector<HTMLButtonElement>('[aria-label="查看 beta 的测试响应"]')!
      .click(),
  );
  await act(async () => button("原文").click());
  expect(batch().querySelector("pre")?.textContent).toBe(
    result("beta", false).raw_response,
  );
  await act(async () => button("返回模型列表").click());
  mocks.testService.mockImplementation(
    async (_id: string, input: ServiceTestInput) => result(input.model),
  );
  await act(async () => button("重试失败 (1)").click());
  expect(mocks.testService.mock.calls.map(([, input]) => input.model)).toEqual([
    "alpha",
    "beta",
    "beta",
  ]);
  expect(batch().textContent).toContain("成功 2");
  expect(modelRow("alpha").textContent).toContain("成功");
  await act(async () => button("单模型").click());
  await act(async () => button("批量模型").click());
  expect(modelRow("beta").textContent).toContain("成功");
});

it("blocks mode switches and close until active tests finish, and stops pending models", async () => {
  const pending = new Map<string, (value: ServiceTestResult) => void>();
  mocks.testService.mockImplementation(
    (_id: string, input: ServiceTestInput) =>
      new Promise<ServiceTestResult>((resolve) =>
        pending.set(input.model, resolve),
      ),
  );
  await open();
  await act(async () => button("测试 3 个模型").click());
  expect(mocks.testService).toHaveBeenCalledTimes(2);
  expect(button("单模型").disabled).toBe(true);
  expect(button("关闭").disabled).toBe(true);
  await act(async () => button("停止后续").click());
  expect(button("正在停止…").disabled).toBe(true);
  await act(async () => {
    pending.get("alpha")!(result("alpha"));
    pending.get("beta")!(result("beta"));
  });
  expect(mocks.testService).toHaveBeenCalledTimes(2);
  expect(modelRow("gamma").textContent).toContain("未执行");
  expect(button("关闭").disabled).toBe(false);
  expect(button("单模型").disabled).toBe(false);
});

it("selects matching models and adds test-only models without changing the provider", async () => {
  mocks.testService.mockImplementation(
    async (_id: string, input: ServiceTestInput) => result(input.model),
  );
  await open();
  await act(async () => button("清空选择").click());
  await search("alpha");
  await act(async () =>
    batch()
      .querySelector<HTMLButtonElement>('[aria-label="选择全部匹配模型"]')!
      .click(),
  );
  await search("custom-model");
  await act(async () => button("添加模型").click());
  await act(async () => button("测试 2 个模型").click());
  expect(mocks.testService.mock.calls.map(([, input]) => input.model)).toEqual([
    "alpha",
    "custom-model",
  ]);
  expect(service.models).toEqual(["alpha", "beta", "gamma"]);
});

it("limits the initial selection to 100 models and accepts unusual upstream IDs", async () => {
  const models = [
    "toString",
    "__proto__",
    ...Array.from({ length: 99 }, (_, index) => `model-${index}`),
  ];
  await open(models);
  expect(button("测试 100 个模型").disabled).toBe(false);
  expect(
    modelRow("model-98").querySelector<HTMLButtonElement>('[role="checkbox"]')!
      .disabled,
  ).toBe(true);
  expect(modelRow("toString").textContent).toContain("未测试");
});
