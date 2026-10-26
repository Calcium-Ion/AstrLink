// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  testService: vi.fn(),
  cancelServiceTest: vi.fn(),
}));
vi.mock("./bridge", () => ({
  testService: mocks.testService,
  cancelServiceTest: mocks.cancelServiceTest,
  SERVICE_TEST_CANCELLED_ERROR: "service_test_cancelled",
  SERVICE_TEST_ALREADY_RUNNING_ERROR: "service_test_already_running",
  SERVICE_TEST_INVALID_ID_ERROR: "service_test_invalid_id",
}));
import { ServiceTestDialog } from "./ServiceTestDialog";
import { settleClosedLayer } from "./lib/test-layer-settle";
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
    'input[aria-label="搜索或输入模型 ID"]',
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
  mocks.cancelServiceTest.mockReset().mockResolvedValue(true);
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});

it.each([false, true])(
  "tests and retries mixed Vertex models in their own native formats (stream=%s)",
  async (stream) => {
    const vertex: Service = {
      ...service,
      kind: "vertex",
      models: [
        "gemini-test",
        "claude-test@20250101",
        "meta/llama-test-maas",
        "unknown",
      ],
      capabilities: [
        {
          protocol: "openai.responses",
          mode: "native",
          streaming: true,
          convert_to: "google.generate_content",
        },
      ],
      model_protocols: [
        { model: "gemini-test", protocol: "google.generate_content" },
      ],
    };
    mocks.testService.mockImplementation(
      async (_id: string, input: ServiceTestInput) => ({
        ...result(input.model, !input.model.startsWith("claude-")),
        ...input,
      }),
    );
    await act(async () =>
      root.render(<ServiceTestDialog service={vertex} onClose={() => {}} />),
    );
    await act(async () => {
      if (!stream)
        document.querySelector<HTMLButtonElement>('[role="switch"]')!.click();
      const prompt = document.querySelector<HTMLTextAreaElement>("textarea")!;
      Object.getOwnPropertyDescriptor(
        HTMLTextAreaElement.prototype,
        "value",
      )!.set!.call(prompt, "keep my prompt");
      prompt.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => button("批量模型").click());
    await act(async () => button("测试配置").click());
    expect(document.body.textContent).toContain("逐模型选择原生协议");
    await act(async () =>
      document.activeElement!.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
      ),
    );
    await settleClosedLayer();
    await act(async () => button("测试 4 个模型").click());
    expect(
      mocks.testService.mock.calls.map(([, input]) => [
        input.model,
        input.protocol,
        input.stream,
        input.prompt,
      ]),
    ).toEqual([
      ["gemini-test", "google.generate_content", stream, "keep my prompt"],
      ["claude-test@20250101", "anthropic.messages", stream, "keep my prompt"],
      ["meta/llama-test-maas", "openai.chat", stream, "keep my prompt"],
    ]);
    expect(modelRow("unknown").textContent).toContain("失败");
    mocks.testService.mockImplementation(
      async (_id: string, input: ServiceTestInput) => ({
        ...result(input.model),
        ...input,
      }),
    );
    await act(async () => button("重试失败 (2)").click());
    expect(mocks.testService).toHaveBeenLastCalledWith(
      service.id,
      {
        model: "claude-test@20250101",
        protocol: "anthropic.messages",
        stream,
        prompt: "keep my prompt",
      },
      expect.any(String),
    );
    expect(mocks.testService).toHaveBeenCalledTimes(4);
  },
);

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

function pendingTests() {
  const pending = new Map<
    string,
    {
      id: string;
      resolve: (value: ServiceTestResult) => void;
      reject: (error: unknown) => void;
    }
  >();
  mocks.testService.mockImplementation(
    (_id: string, input: ServiceTestInput, id: string) =>
      new Promise<ServiceTestResult>((resolve, reject) =>
        pending.set(input.model, { id, resolve, reject }),
      ),
  );
  return pending;
}

it.each(["native", "web"])(
  "cancels active %s tests, skips the queue and lets the dialog close",
  async (transport) => {
    const pending = pendingTests();
    await open();
    await act(async () => button("测试 3 个模型").click());
    expect(mocks.testService).toHaveBeenCalledTimes(2);
    expect(button("单模型").disabled).toBe(true);
    expect(button("关闭").disabled).toBe(true);
    const ids = [...pending.values()].map((test) => test.id);
    expect(ids).toEqual([expect.any(String), expect.any(String)]);
    expect(new Set(ids).size).toBe(2);
    await act(async () => button("取消测试").click());
    expect(mocks.cancelServiceTest.mock.calls).toEqual(
      ids.map((id) => [service.id, id]),
    );
    expect(button("正在取消…").disabled).toBe(true);
    await act(async () => {
      for (const test of pending.values())
        test.reject(
          transport === "native"
            ? new Error("service_test_cancelled")
            : new DOMException("Aborted", "AbortError"),
        );
    });
    expect(mocks.testService).toHaveBeenCalledTimes(2);
    expect(modelRow("alpha").textContent).toContain("已取消");
    expect(modelRow("beta").textContent).toContain("已取消");
    expect(modelRow("gamma").textContent).toContain("未执行");
    expect(batch().textContent).toContain("已取消 2");
    expect(batch().textContent).toContain("失败 0");
    expect(button("关闭").disabled).toBe(false);
    expect(button("单模型").disabled).toBe(false);
    await act(async () => button("查看 alpha 的测试响应").click());
    expect(batch().textContent).toContain("测试已取消");
    await act(async () => button("返回模型列表").click());
    mocks.testService.mockImplementation(
      async (_id: string, input: ServiceTestInput) => result(input.model),
    );
    await act(async () => button("测试 3 个模型").click());
    const nextIds = mocks.testService.mock.calls
      .slice(2)
      .map((call) => call[2]);
    expect(new Set([...ids, ...nextIds]).size).toBe(5);
    expect(batch().textContent).toContain("成功 3");
  },
);

it("preserves a result that finishes during cancellation", async () => {
  const pending = pendingTests();
  await open();
  await act(async () => button("测试 3 个模型").click());
  mocks.cancelServiceTest.mockImplementation(async (_serviceId, id) => {
    if (id === pending.get("alpha")!.id) {
      pending.get("alpha")!.resolve(result("alpha"));
      return false;
    }
    pending.get("beta")!.reject(new Error("service_test_cancelled"));
    return true;
  });
  await act(async () => button("取消测试").click());
  expect(modelRow("alpha").textContent).toContain("成功0.23 s1.25 s");
  expect(modelRow("beta").textContent).toContain("已取消");
  expect(modelRow("gamma").textContent).toContain("未执行");
  expect(document.body.textContent).not.toContain("无法取消测试");
  expect(mocks.testService).toHaveBeenCalledTimes(2);
});

it.each(["rejected", "missing"])(
  "allows retrying a %s cancellation while keeping the remaining request active",
  async (failure) => {
    const pending = pendingTests();
    await open();
    await act(async () => button("测试 3 个模型").click());
    mocks.cancelServiceTest.mockImplementation(async (_serviceId, id) => {
      if (id === pending.get("alpha")!.id) {
        if (failure === "rejected") throw new Error("offline");
        return false;
      }
      pending.get("beta")!.reject(new Error("service_test_cancelled"));
      return true;
    });
    await act(async () => button("取消测试").click());
    expect(document.body.textContent).toContain("无法取消测试。");
    expect(button("取消测试").disabled).toBe(false);
    expect(button("关闭").disabled).toBe(true);
    expect(modelRow("alpha").textContent).toContain("测试中");
    expect(modelRow("beta").textContent).toContain("已取消");
    mocks.cancelServiceTest.mockClear().mockImplementation(async () => {
      pending.get("alpha")!.reject(new Error("service_test_cancelled"));
      return true;
    });
    await act(async () => button("取消测试").click());
    expect(mocks.cancelServiceTest).toHaveBeenCalledExactlyOnceWith(
      service.id,
      pending.get("alpha")!.id,
    );
    expect(button("关闭").disabled).toBe(false);
    expect(document.body.textContent).not.toContain("无法取消测试");
  },
);

it("cancels active requests when the dialog unmounts", async () => {
  const pending = pendingTests();
  await open();
  await act(async () => button("测试 3 个模型").click());
  mocks.cancelServiceTest.mockImplementation(async (_serviceId, id) => {
    [...pending.values()]
      .find((test) => test.id === id)!
      .reject(new Error("service_test_cancelled"));
    return true;
  });
  await act(async () => root.render(null));
  expect(mocks.cancelServiceTest.mock.calls).toEqual(
    [...pending.values()].map((test) => [service.id, test.id]),
  );
  expect(mocks.testService).toHaveBeenCalledTimes(2);
});

it("ignores a cancellation response from a completed batch", async () => {
  const pending = pendingTests();
  let finishCancellation!: (value: boolean) => void;
  mocks.cancelServiceTest.mockImplementation(
    () =>
      new Promise<boolean>((resolve) => {
        finishCancellation = resolve;
      }),
  );
  await open(["alpha"]);
  await act(async () => button("测试 1 个模型").click());
  await act(async () => button("取消测试").click());
  await act(async () => pending.get("alpha")!.resolve(result("alpha")));
  await act(async () => button("测试 1 个模型").click());
  await act(async () => finishCancellation(false));
  expect(button("取消测试").disabled).toBe(false);
  expect(document.body.textContent).not.toContain("无法取消测试");
  await act(async () => pending.get("alpha")!.resolve(result("alpha")));
});

it("keeps completed results when cancelling the remaining batch", async () => {
  const pending = pendingTests();
  await open(["alpha", "beta", "gamma", "delta"]);
  await act(async () => button("测试 4 个模型").click());
  await act(async () => pending.get("alpha")!.resolve(result("alpha")));
  expect(pending.has("gamma")).toBe(true);
  mocks.cancelServiceTest.mockImplementation(async (_serviceId, id) => {
    [...pending.values()]
      .find((test) => test.id === id)!
      .reject(new Error("service_test_cancelled"));
    return true;
  });
  await act(async () => button("取消测试").click());
  expect(mocks.cancelServiceTest.mock.calls.map((call) => call[1])).toEqual([
    pending.get("beta")!.id,
    pending.get("gamma")!.id,
  ]);
  expect(modelRow("alpha").textContent).toContain("成功");
  expect(modelRow("beta").textContent).toContain("已取消");
  expect(modelRow("gamma").textContent).toContain("已取消");
  expect(modelRow("delta").textContent).toContain("未执行");
  expect(button("关闭").disabled).toBe(false);
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
