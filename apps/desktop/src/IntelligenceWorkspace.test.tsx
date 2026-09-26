// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { Service } from "./service-model";
import type {
  IntelligenceChannel,
  IntelligenceRun,
} from "./intelligence-bridge";
const mocks = vi.hoisted(() => ({ invoke: vi.fn(), renderSVG: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke: mocks.invoke }));
vi.mock("./lib/question-image", () => ({
  renderQuestionSVG: mocks.renderSVG,
  readReferenceImage: vi.fn(),
}));
import {
  IntelligenceWorkspace,
  IntelligenceStartButton,
} from "./IntelligenceWorkspace";

const service: Service = {
  id: "service_iq",
  name: "DS",
  kind: "openai",
  enabled: true,
  models: ["deepseek-v4.1-flash"],
  capabilities: [{ protocol: "openai.chat", mode: "native", streaming: false }],
  http: { base_url: "https://example.com", auth: { scheme: "none" } },
  created_at: "",
  updated_at: "",
};
let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  mocks.invoke.mockReset();
  mocks.renderSVG.mockReset();
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

it("keeps a completed run in the history selector when the history request resolves after the terminal poll", async () => {
  vi.useFakeTimers();
  let finishHistory: (value: IntelligenceRun[]) => void = () => {};
  let historyCalls = 0;
  const run: IntelligenceRun = {
    id: "12c54b9cbf126f6ced65663760326f6a",
    service_id: service.id,
    started_at: "2026-09-25T12:00:00Z",
    status: "completed",
    items: [],
    judge_service_id: "",
    judge_model: "",
  };
  mocks.invoke.mockImplementation(
    async (_: string, args: { operation: string }) => {
      switch (args.operation) {
        case "settings":
          return {
            default_model: "gpt-6-astra",
            questions: [],
            default_question_ids: [],
          };
        case "channel":
          return { use_default: true, model: "", question_ids: [] };
        case "history":
          if (++historyCalls === 1) return [];
          return new Promise<IntelligenceRun[]>((resolve) => {
            finishHistory = resolve;
          });
        case "start":
          return { ...run, status: "running" };
        case "run":
          return run;
        default:
          return { ok: true };
      }
    },
  );
  await act(async () =>
    root.render(
      <IntelligenceWorkspace
        service={service}
        services={[service]}
        onClose={() => {}}
        onResult={() => {}}
      />,
    ),
  );
  await act(async () => button("开始测试").click());
  await act(async () => {
    await vi.advanceTimersByTimeAsync(150);
  });
  await act(async () => finishHistory([run]));
  expect(
    document.querySelector('[aria-label="测试历史"]')?.textContent,
  ).toContain(new Date(run.started_at).toLocaleString());
  expect(button("开始测试")).toBeDefined();
});
const button = (text: string) =>
  [...document.querySelectorAll<HTMLButtonElement>("button")].find(
    (el) => el.textContent === text,
  )!;
it("starts directly from the action icon without opening settings or overwriting the channel", async () => {
  mocks.invoke.mockImplementation(
    async (_: string, args: { operation: string }) => {
      if (args.operation === "history") return [];
      if (args.operation === "start")
        return {
          id: "direct",
          service_id: service.id,
          status: "completed",
          started_at: new Date().toISOString(),
          items: [],
          judge_service_id: "",
          judge_model: "",
        };
    },
  );
  await act(async () =>
    root.render(<IntelligenceStartButton service={service} disabled={false} />),
  );
  await act(async () =>
    document
      .querySelector<HTMLButtonElement>('[aria-label="智力测试 · DS"]')!
      .click(),
  );
  expect(mocks.invoke).toHaveBeenCalledWith(
    "intelligence",
    expect.objectContaining({
      operation: "start",
      serviceId: service.id,
      input: { mode: "all", count: 1 },
    }),
  );
  expect(
    mocks.invoke.mock.calls.some(
      ([, args]) => args.operation === "save_channel",
    ),
  ).toBe(false);
  expect(document.querySelector('[role="dialog"]')).toBeNull();
});
it("shows the missing-model error on click, then saves and uses this channel's selected model", async () => {
  let config: IntelligenceChannel = {
    use_default: true,
    model: "",
    question_ids: [],
  };
  const starts: string[] = [];
  const completed: IntelligenceRun = {
    id: "run1",
    service_id: service.id,
    started_at: "2026-09-25T12:00:00Z",
    status: "completed",
    judge_service_id: "",
    judge_model: "",
    items: [
      {
        question: {
          id: "candy",
          name: "糖果题",
          kind: "number",
          prompt: "题目",
          answer: "21",
          model: "",
        },
        model: service.models[0],
        status: "passed",
        output: "21",
        reason: "答案一致",
        duration_ms: 20,
      },
    ],
  };
  mocks.invoke.mockImplementation(
    async (_command: string, args: { operation: string; input?: unknown }) => {
      switch (args.operation) {
        case "settings":
          return {
            default_model: "gpt-6-astra",
            judge_service_id: "",
            judge_model: "",
            max_output_tokens: 16384,
            timeout_seconds: 120,
            questions: [completed.items[0].question],
            default_question_ids: ["candy"],
          };
        case "channel":
          return config;
        case "history":
          return [];
        case "save_channel":
          config = args.input as IntelligenceChannel;
          return { ok: true };
        case "start":
          starts.push(config.model);
          if (!config.model)
            throw new Error(
              'POST /control/v1/intelligence/channels/service_iq/runs returned 400 Bad Request: {"error":{"code":"intelligence_error","message":"没有指定模型"}}',
            );
          return completed;
      }
    },
  );
  await act(async () =>
    root.render(
      <IntelligenceWorkspace
        service={service}
        services={[service]}
        onClose={() => {}}
        onResult={() => {}}
      />,
    ),
  );
  await act(async () => button("开始测试").click());
  expect(document.querySelector('[role="alert"]')?.textContent).toContain(
    "没有指定模型",
  );
  expect(document.querySelector('[role="alert"]')?.textContent).not.toContain(
    "/control/",
  );
  await act(async () => {
    button("当前渠道").dispatchEvent(
      new MouseEvent("mousedown", { bubbles: true, button: 0 }),
    );
  });
  const input = document.querySelector<HTMLInputElement>(
    'input[aria-label="本渠道测试模型"]',
  )!;
  expect(input).not.toBeNull();
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!.call(input, service.models[0]);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(async () => button("保存渠道设置").click());
  expect(config.model).toBe(service.models[0]);
  await act(async () => button("开始测试").click());
  expect(starts).toEqual(["", service.models[0]]);
  expect(document.body.textContent).toContain("1/1 通过");
  expect(document.body.textContent).toContain("答案一致");
});

it("keeps timing while waiting and renders multiple images after the dialog is closed", async () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-09-25T12:00:00Z"));
  let current: IntelligenceRun = {
    id: "background-run",
    service_id: service.id,
    started_at: new Date().toISOString(),
    status: "running",
    judge_service_id: "judge",
    judge_model: "vision",
    items: ["first", "second"].map((id) => ({
      question: {
        id,
        name: id,
        kind: "svg",
        prompt: id,
        answer: id,
        model: "",
      },
      model: service.models[0],
      status: "generating",
      output: "",
      reason: "",
      duration_ms: 0,
      started_at: new Date().toISOString(),
    })),
  };
  let history: IntelligenceRun[] = [];
  mocks.renderSVG.mockImplementation(async (svg: string) => `png:${svg}`);
  mocks.invoke.mockImplementation(
    async (_: string, args: { operation: string }) => {
      switch (args.operation) {
        case "settings":
          return {
            default_model: "gpt-6-astra",
            questions: [],
            default_question_ids: [],
          };
        case "channel":
          return {
            use_default: false,
            model: service.models[0],
            question_ids: [],
          };
        case "history":
          return history;
        case "start":
        case "run":
          return current;
        default:
          return { ok: true };
      }
    },
  );
  const show = () =>
    root.render(
      <IntelligenceWorkspace
        service={service}
        services={[service]}
        onClose={() => root.render(<div>另一个页面</div>)}
        onResult={() => {}}
      />,
    );
  await act(async () => show());
  await act(async () => button("开始测试").click());
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2500);
  });
  expect(document.body.textContent).toContain("2.5s");
  await act(async () => button("后台运行").click());
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  current = {
    ...current,
    items: current.items.map((item) => ({
      ...item,
      status: "rendering",
      output: item.question.id,
    })),
  };
  await act(async () => {
    await vi.advanceTimersByTimeAsync(800);
  });
  const renders = mocks.invoke.mock.calls
    .filter(([, args]) => args.operation === "render")
    .map(([, args]) => args.input);
  expect(renders).toEqual([
    { question_id: "first", png: "png:first" },
    { question_id: "second", png: "png:second" },
  ]);
  expect(
    mocks.invoke.mock.calls.some(([, args]) => args.operation === "cancel"),
  ).toBe(false);
  current = {
    ...current,
    status: "completed",
    items: current.items.map((item) => ({
      ...item,
      status: "failed",
      reason: "图片不符合要求",
      duration_ms: 3600,
    })),
  };
  history = [current];
  await act(async () => {
    await vi.advanceTimersByTimeAsync(800);
  });
  await act(async () => show());
  expect(document.body.textContent).toContain("0/2 通过");
  expect(document.body.textContent).toContain("图片不符合要求");
  expect(document.body.textContent).toContain("3.6s");
  await act(async () => {
    await vi.advanceTimersByTimeAsync(3000);
  });
  expect(document.body.textContent).toContain("3.6s");
});
