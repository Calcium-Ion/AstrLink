// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  testService: vi.fn(),
  failRendering: false,
}));
vi.mock("./bridge", () => ({ testService: mocks.testService }));
vi.mock("./components/MarkdownRenderer", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("./components/MarkdownRenderer")>();
  return {
    default: (props: { content: string }) => {
      if (mocks.failRendering)
        throw new Error("Simulated response parser failure");
      return <actual.default {...props} />;
    },
  };
});

import { AppErrorBoundary } from "./AppErrorBoundary";
import { ServiceTestDialog } from "./ServiceTestDialog";
import type { Service } from "./service-model";

const service: Service = {
  id: "service_test",
  name: "Test provider",
  kind: "openai",
  enabled: true,
  models: ["test-model"],
  capabilities: [{ protocol: "openai.chat", mode: "native", streaming: true }],
  http: { base_url: "https://example.com", auth: { scheme: "none" } },
  created_at: "2026-09-22T00:00:00Z",
  updated_at: "2026-09-22T00:00:00Z",
};
let root: Root;
let container: HTMLDivElement;

beforeEach(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
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
  return [
    ...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button'),
  ].find(
    (item) =>
      item.textContent === text ||
      (text === "开始测试" && item.textContent === "重新测试"),
  )!;
}

const modelInput = () =>
  document.querySelector<HTMLInputElement>('input[aria-label="测试模型"]')!;
const modelOptions = () => [
  ...document.querySelectorAll<HTMLElement>(
    '[role="listbox"][aria-label="测试模型"] [role="option"]',
  ),
];
async function typeModel(input: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
async function press(input: HTMLInputElement, key: string) {
  await act(async () =>
    input.dispatchEvent(
      new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }),
    ),
  );
}

it("opens the full model list with a preselected value and reopens it after choosing another model", async () => {
  const models = ["grok-4.5", "grok-4.1-fast", "grok-code-fast"];
  mocks.testService.mockResolvedValue({
    service_id: service.id,
    protocol: "openai.chat",
    model: models[1],
    stream: true,
    ok: true,
    status_code: 200,
    duration_ms: 100,
    output: "OK",
  });
  await act(async () =>
    root.render(
      <ServiceTestDialog service={{ ...service, models }} onClose={() => {}} />,
    ),
  );
  const input = modelInput();
  expect(input.value).toBe(models[0]);
  expect(document.querySelector("datalist")).toBeNull();
  await act(async () => input.click());
  expect(modelOptions().map((item) => item.title)).toEqual(models);
  expect(modelOptions()[0].getAttribute("aria-selected")).toBe("true");
  await act(async () => modelOptions()[1].click());
  expect(input.value).toBe(models[1]);
  expect(input.getAttribute("aria-expanded")).toBe("false");
  await act(async () =>
    document
      .querySelector<HTMLButtonElement>('button[aria-label="测试模型"]')!
      .click(),
  );
  expect(modelOptions().map((item) => item.title)).toEqual(models);
  await press(input, "Escape");
  await act(async () => button("开始测试").click());
  expect(mocks.testService).toHaveBeenCalledWith(service.id, {
    protocol: "openai.chat",
    model: models[1],
    stream: true,
  });
});

it("filters only while typing, preserves custom IDs, and clears the filter when reopened", async () => {
  await act(async () =>
    root.render(
      <ServiceTestDialog
        service={{ ...service, models: ["alpha", "beta"] }}
        onClose={() => {}}
      />,
    ),
  );
  const input = modelInput();
  await typeModel(input, "BETA");
  expect(modelOptions().map((item) => item.title)).toEqual(["beta"]);
  await press(input, "Escape");
  await act(async () => input.click());
  expect(modelOptions().map((item) => item.title)).toEqual(["alpha", "beta"]);
  expect(input.value).toBe("BETA");
  await typeModel(input, "custom-model");
  expect(modelOptions()).toHaveLength(0);
  expect(document.body.textContent).toContain(
    "没有匹配的模型，可直接使用输入的模型 ID。",
  );
  await press(input, "Enter");
  expect(input.value).toBe("custom-model");
  expect(input.getAttribute("aria-expanded")).toBe("false");
  await act(async () => button("开始测试").click());
  expect(mocks.testService).toHaveBeenCalledWith(service.id, {
    protocol: "openai.chat",
    model: "custom-model",
    stream: true,
  });
});

it.each([0, 1, 2])(
  "allows native model-list scrolling with wheel delta mode %i while keeping the background locked",
  async (deltaMode) => {
    await act(async () =>
      root.render(
        <ServiceTestDialog
          service={{
            ...service,
            models: Array.from({ length: 100 }, (_, i) => `model-${i}`),
          }}
          onClose={() => {}}
        />,
      ),
    );
    await act(async () => modelInput().click());
    const list = document.querySelector<HTMLElement>(
      '[role="listbox"][aria-label="测试模型"]',
    )!;
    // Portaled options sit outside the dialog's DOM scroll-lock boundary.
    expect(
      document.querySelector('[data-slot="dialog-content"]')!.contains(list),
    ).toBe(false);
    list.style.overflowY = "auto";
    Object.defineProperties(list, {
      clientHeight: { value: 256, configurable: true },
      scrollHeight: { value: 3200, configurable: true },
    });
    const wheel = () =>
      new WheelEvent("wheel", {
        bubbles: true,
        cancelable: true,
        deltaY: deltaMode === 0 ? 120 : 3,
        deltaMode,
      });
    const backgroundWheel = wheel();
    document.body.dispatchEvent(backgroundWheel);
    expect(backgroundWheel.defaultPrevented).toBe(true);

    const listWheel = wheel();
    modelOptions()[0].querySelector("span")!.dispatchEvent(listWheel);
    expect(listWheel.defaultPrevented).toBe(false);
    expect(modelInput().value).toBe("model-0");
    expect(modelInput().getAttribute("aria-expanded")).toBe("true");
  },
);

it("allows wheel and touch scrolling in nested settings and model popovers", async () => {
  await act(async () =>
    root.render(<ServiceTestDialog service={service} onClose={() => {}} />),
  );
  await act(async () => button("测试配置").click());
  const settings = document.querySelector<HTMLElement>(
    '[data-slot="popover-content"]',
  )!;
  const input = settings.querySelector<HTMLInputElement>(
    'input[aria-label="测试模型"]',
  )!;
  await act(async () => input.click());

  for (const target of [settings, modelOptions()[0]]) {
    const touch = new Touch({ identifier: 1, target, clientY: 120 });
    for (const event of [
      new WheelEvent("wheel", {
        bubbles: true,
        cancelable: true,
        deltaY: 120,
      }),
      new TouchEvent("touchmove", {
        bubbles: true,
        cancelable: true,
        touches: [touch],
        changedTouches: [touch],
      }),
    ]) {
      target.dispatchEvent(event);
      expect(event.defaultPrevented).toBe(false);
    }
  }
  expect(input.getAttribute("aria-expanded")).toBe("true");
});

it("supports keyboard selection and dismisses the model list without closing the containing dialog", async () => {
  const onClose = vi.fn();
  await act(async () =>
    root.render(
      <ServiceTestDialog
        service={{ ...service, models: ["alpha", "beta", "gamma"] }}
        onClose={onClose}
      />,
    ),
  );
  const input = modelInput();
  await press(input, "ArrowDown");
  await press(input, "ArrowDown");
  expect(
    document.getElementById(input.getAttribute("aria-activedescendant")!)
      ?.title,
  ).toBe("beta");
  expect(input.value).toBe("alpha");
  await press(input, "Enter");
  expect(input.value).toBe("beta");
  await press(input, "ArrowUp");
  await press(input, "Escape");
  expect(input.getAttribute("aria-expanded")).toBe("false");
  expect(onClose).not.toHaveBeenCalled();
  await press(input, "ArrowDown");
  await press(input, "Tab");
  expect(input.getAttribute("aria-expanded")).toBe("false");
  expect(input.value).toBe("beta");
});

it("accepts a manual model without saved models and disables selection while testing", async () => {
  let finish!: (value: unknown) => void;
  mocks.testService.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  await act(async () =>
    root.render(
      <ServiceTestDialog
        service={{ ...service, models: [] }}
        onClose={() => {}}
      />,
    ),
  );
  const input = modelInput();
  expect(button("开始测试").disabled).toBe(true);
  await typeModel(input, "custom-model");
  await act(async () => button("开始测试").click());
  expect(mocks.testService).toHaveBeenCalledWith(service.id, {
    protocol: "openai.chat",
    model: "custom-model",
    stream: true,
  });
  expect(input.disabled).toBe(true);
  expect(
    document.querySelector<HTMLButtonElement>('button[aria-label="测试模型"]')!
      .disabled,
  ).toBe(true);
  expect(input.getAttribute("aria-expanded")).toBe("false");
  await act(async () =>
    finish({
      service_id: service.id,
      protocol: "openai.chat",
      model: "custom-model",
      stream: true,
      ok: true,
      status_code: 200,
      duration_ms: 100,
      output: "OK",
    }),
  );
});

it("shows header, post-header, first-text and complete timings without treating missing data as zero", async () => {
  mocks.testService.mockResolvedValue({
    service_id: service.id,
    protocol: "openai.chat",
    model: "test-model",
    stream: true,
    ok: true,
    status_code: 200,
    duration_ms: 2665,
    response_headers_ms: 120,
    first_token_ms: 840,
    output: "OK",
  });
  await act(async () =>
    root.render(<ServiceTestDialog service={service} onClose={() => {}} />),
  );
  await act(async () => button("开始测试").click());
  const dialog = document.querySelector('[role="dialog"]')!;
  for (const value of [
    "HTTP 响应0.12 s",
    "首字等待0.72 s",
    "首字总延迟0.84 s",
    "完整耗时2.67 s",
  ]) {
    expect(dialog.textContent).toContain(value);
  }
  await act(async () =>
    document
      .querySelector<HTMLButtonElement>('button[aria-label="耗时说明"]')!
      .click(),
  );
  expect(document.body.textContent).toContain("并非纯网络延迟");
  expect(document.body.textContent).toContain(
    "首字总延迟 = HTTP 响应 + 首字等待",
  );
});

it("keeps missing and non-streaming first-text timings unavailable", async () => {
  mocks.testService.mockResolvedValue({
    service_id: service.id,
    protocol: "openai.chat",
    model: "test-model",
    stream: false,
    ok: true,
    status_code: 200,
    duration_ms: 500,
    response_headers_ms: 100,
    first_token_ms: null,
    output: "OK",
  });
  await act(async () =>
    root.render(<ServiceTestDialog service={service} onClose={() => {}} />),
  );
  await act(async () => button("开始测试").click());
  const dialog = document.querySelector('[role="dialog"]')!;
  expect(dialog.textContent).toContain("首字等待—");
  expect(dialog.textContent).toContain("首字总延迟—");
  await act(async () =>
    document
      .querySelector<HTMLButtonElement>('button[aria-label="耗时说明"]')!
      .click(),
  );
  expect(document.body.textContent).toContain("非流式响应无法测量首字延迟");
  mocks.testService.mockResolvedValue({
    service_id: service.id,
    protocol: "openai.chat",
    model: "test-model",
    stream: true,
    ok: false,
    status_code: 0,
    duration_ms: 60000,
    response_headers_ms: null,
    first_token_ms: null,
    output: "",
    error_code: "timeout",
    message: "Timed out",
  });
  await act(async () => button("开始测试").click());
  expect(dialog.textContent).toContain("HTTP 响应—");
  expect(dialog.textContent).toContain("完整耗时60.00 s");
  expect(dialog.textContent).toContain("测试失败");
});

it("contains parser failures within the response, preserves raw text, and allows retry and close", async () => {
  const onClose = vi.fn();
  mocks.failRendering = true;
  mocks.testService.mockResolvedValue({
    service_id: service.id,
    protocol: "openai.chat",
    model: "test-model",
    stream: true,
    ok: true,
    status_code: 200,
    duration_ms: 120,
    output: "**OK**\n<script>bad()</script>",
  });
  await act(async () =>
    root.render(
      <AppErrorBoundary>
        <ServiceTestDialog service={service} onClose={onClose} />
      </AppErrorBoundary>,
    ),
  );
  await act(async () => button("开始测试").click());
  await act(async () => {
    await vi.dynamicImportSettled();
  });
  const dialog = document.querySelector('[role="dialog"]')!;
  expect(dialog.textContent).toContain("内容解析失败，已显示原始文本。");
  expect(dialog.querySelector("pre")?.textContent).toBe(
    "**OK**\n<script>bad()</script>",
  );
  expect(dialog.querySelector("script")).toBeNull();
  expect(document.body.textContent).not.toContain("AstrLink 界面遇到问题");
  expect(dialog.textContent).toContain("测试成功");
  expect(button("开始测试").disabled).toBe(false);
  expect(button("关闭").disabled).toBe(false);

  // A later response must get a fresh rendering attempt, not a stuck fallback.
  mocks.failRendering = false;
  mocks.testService.mockResolvedValue({
    service_id: service.id,
    protocol: "openai.chat",
    model: "test-model",
    stream: true,
    ok: true,
    status_code: 200,
    duration_ms: 100,
    output: "**Recovered**",
  });
  await act(async () => button("开始测试").click());
  expect(dialog.querySelector("strong")?.textContent).toBe("Recovered");
  expect(dialog.textContent).not.toContain("内容解析失败");
  await act(async () => button("关闭").click());
  expect(onClose).toHaveBeenCalledOnce();
});

it("shows the failure reason alongside partial output from an interrupted stream", async () => {
  mocks.testService.mockResolvedValue({
    service_id: service.id,
    protocol: "openai.chat",
    model: "test-model",
    stream: true,
    ok: false,
    status_code: 200,
    duration_ms: 100,
    output: "Partial reply",
    error_code: "interrupted",
    message: "Provider response was interrupted.",
  });
  await act(async () =>
    root.render(<ServiceTestDialog service={service} onClose={() => {}} />),
  );
  await act(async () => button("开始测试").click());
  await act(async () => {
    await vi.dynamicImportSettled();
  });
  const dialog = document.querySelector('[role="dialog"]')!;
  expect(dialog.textContent).toContain("Partial reply");
  expect(dialog.textContent).toContain("Provider response was interrupted.");
});
