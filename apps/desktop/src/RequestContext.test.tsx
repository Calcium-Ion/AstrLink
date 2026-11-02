// @vitest-environment happy-dom

import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { settleClosedLayer } from "./lib/test-layer-settle";
import { RequestContext, SessionContextMeter } from "./RequestContext";
import {
  emptyTrajectoryFields,
  type ContextItem,
  type ContextParts,
  type RequestRecord,
  type RequestSession,
  type SessionContext,
} from "./request-record-model";

function root(id: string, turn: number): RequestRecord {
  return {
    id,
    parent_request_id: null,
    attempt_index: 1,
    child_count: 0,
    started_at: "2026-10-20T09:00:00Z",
    completed_at: "2026-10-20T09:00:05Z",
    status: "succeeded",
    input_protocol: "anthropic.messages",
    requested_model: "claude-opus",
    streaming: true,
    route_id: null,
    service_id: null,
    local_access_token_id: null,
    http_status: 200,
    latency_ms: 5000,
    usage: null,
    error: null,
    audit: {
      request_body_captured: false,
      response_content_captured: false,
      request_body_truncated: false,
      response_content_truncated: false,
      upstream_request_body_captured: false,
      upstream_response_content_captured: false,
      upstream_request_body_truncated: false,
      upstream_response_content_truncated: false,
    },
    privacy_restore: null,
    ...emptyTrajectoryFields,
    session_id: "session_ctx",
    turn_index: turn,
  };
}

const turns = [
  root("req_1", 1),
  root("req_2", 1),
  root("req_3", 1),
  root("req_4", 2),
  root("req_5", 2),
  root("req_6", 2),
];

const context: SessionContext = {
  calls: [
    { request_id: "req_1", tokens: 100_000 },
    { request_id: "req_2", tokens: 200_000 },
    { request_id: "req_3", tokens: 380_000 },
    { request_id: "req_4", tokens: 40_000 },
    { request_id: "req_5", tokens: 150_000 },
    { request_id: "req_6", tokens: 312_000 },
  ],
  compactions: [
    {
      request_id: "req_4",
      before_tokens: 380_000,
      after_tokens: 40_000,
      marked: false,
    },
  ],
  cache_misses: [
    {
      request_id: "req_5",
      reason: "model_changed",
      rewritten_tokens: 100_000,
      extra_usd: "0.345000000",
    },
  ],
};

const session: RequestSession = {
  id: "session_ctx",
  title: "重构 ingress",
  started_at: "2026-10-20T09:00:00Z",
  last_started_at: "2026-10-20T09:10:00Z",
  completed_at: "2026-10-20T09:10:05Z",
  duration_ms: 30_000,
  running_since: null,
  active_request_starts: [],
  turn_count: 2,
  call_count: 6,
  status: "succeeded",
  requested_model: "claude-opus",
  input_protocol: "anthropic.messages",
  service_id: null,
  local_access_token_id: null,
  context_tokens: 312_000,
  compaction_point_tokens: 380_000,
  context_window_tokens: 1_000_000,
  compaction_count: 1,
  usage: {
    input_tokens: 1_182_000,
    output_tokens: 6_000,
    cache_read_tokens: 1_040_000,
    cache_write_tokens: 142_000,
    cache_hit_ratio: 0.88,
  },
  cost: { amount_usd: "1.234000000", priced: 6, unpriced: 0, pending: 0 },
};

let container: HTMLDivElement;
let root_: Root;

function render(overrides: Partial<Parameters<typeof RequestContext>[0]> = {}) {
  const onSelectRequest = vi.fn();
  const onOpenCall = vi.fn();
  act(() => {
    root_.render(
      <RequestContext
        context={context}
        onOpenCall={onOpenCall}
        onSelectRequest={onSelectRequest}
        selectedRequestId="req_6"
        session={session}
        turns={turns}
        {...overrides}
      />,
    );
  });
  return { onSelectRequest, onOpenCall };
}

const query = (testId: string) =>
  container.querySelector<HTMLElement>(`[data-testid="${testId}"]`);

describe("RequestContext", () => {
  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.appendChild(container);
    root_ = createRoot(container);
  });

  afterEach(() => {
    act(() => root_.unmount());
    container.remove();
  });

  it("draws the context, the compaction and the cache miss", () => {
    render();
    const summary = query("context-summary")?.textContent ?? "";
    expect(summary).toContain("现在 31.2万");
    expect(summary).toContain("模型窗口 100万");
    expect(summary).toContain("压缩过 1 次");
    expect(container.textContent).toContain("缓存没命中");
    expect(container.textContent).toContain("客户端约在 38万 时压缩");
    expect(
      container.querySelector('[data-testid="context-compaction-point"]'),
    ).not.toBeNull();
    expect(
      container.querySelectorAll('[data-testid="context-drop"]'),
    ).toHaveLength(1);
    expect(
      container.querySelectorAll('[data-testid="context-miss"]'),
    ).toHaveLength(1);
    expect(query("context-latest")?.textContent).toBe("31.2万");
    const curve = query("context-curve")?.textContent ?? "";
    expect(curve).toContain("第 1 次调用");
    expect(curve).toContain("第 6 次");
    const plot = query("context-plot")!;
    expect(plot.getAttribute("role")).toBe("slider");
    expect(plot.getAttribute("aria-valuenow")).toBe("6");
    expect(plot.getAttribute("aria-valuetext")).toBe("第 6 次调用 · 31.2万");
  });

  it("sums up the cost and lists why the cache missed", async () => {
    const { onOpenCall } = render();
    const line = query("context-cost")?.textContent ?? "";
    expect(line).toContain("折算 $1.23");
    expect(line).toContain("缓存命中 88%");
    expect(line).toContain("没命中 1 次，多花 $0.35");
    await act(async () => query("context-miss-reasons")!.click());
    const reasons = document.querySelectorAll<HTMLButtonElement>(
      '[data-testid="context-miss-reason"]',
    );
    expect(reasons).toHaveLength(1);
    expect(reasons[0]!.textContent).toContain("第 2 轮第 2 次调用");
    expect(reasons[0]!.textContent).toContain("换了模型");
    expect(reasons[0]!.textContent).toContain("多花 $0.35");
    await act(async () => reasons[0]!.click());
    await settleClosedLayer();
    expect(onOpenCall).toHaveBeenCalledWith("req_5");
    expect(
      document.querySelector('[data-testid="context-miss-reason"]'),
    ).toBeNull();
  });

  it("points the detail at the call clicked or reached by the keys", () => {
    const { onSelectRequest } = render({ selectedRequestId: "req_3" });
    const plot = query("context-plot")!;
    // The selected call carries its own dot and guide.
    expect(query("context-focus")).not.toBeNull();
    plot.getBoundingClientRect = () =>
      ({ left: 100, width: 500, top: 0, height: 192 }) as DOMRect;
    act(() => {
      plot.dispatchEvent(
        new MouseEvent("click", { bubbles: true, clientX: 100 + 0.62 * 500 }),
      );
    });
    // Six calls sit at fifths of the width; 62% is nearest the fourth.
    expect(onSelectRequest).toHaveBeenLastCalledWith("req_4");
    act(() => {
      plot.dispatchEvent(
        new KeyboardEvent("keydown", { bubbles: true, key: "ArrowLeft" }),
      );
    });
    expect(onSelectRequest).toHaveBeenLastCalledWith("req_2");
    act(() => {
      plot.dispatchEvent(
        new KeyboardEvent("keydown", { bubbles: true, key: "End" }),
      );
    });
    expect(onSelectRequest).toHaveBeenLastCalledWith("req_6");
  });

  it("leaves out what it does not know", () => {
    render({
      context: { ...context, cache_misses: [] },
      session: {
        ...session,
        compaction_point_tokens: undefined,
        context_window_tokens: undefined,
        cost: undefined,
        usage: undefined,
      },
    });
    expect(query("context-summary")?.textContent).not.toContain("模型窗口");
    expect(
      container.querySelector('[data-testid="context-compaction-point"]'),
    ).toBeNull();
    expect(query("context-cost")).toBeNull();
    expect(container.textContent).not.toContain("缓存没命中");
  });

  it("explains an empty context", () => {
    render({ context: undefined });
    expect(query("context-empty")?.textContent).toContain("还没有调用上报用量");
    expect(query("context-curve")).toBeNull();
  });
});

describe("what a call carried", () => {
  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.appendChild(container);
    root_ = createRoot(container);
  });

  afterEach(() => {
    act(() => root_.unmount());
    container.remove();
  });

  const globalRules: ContextItem = {
    kind: "rules",
    name: "CLAUDE.md",
    path: "~/.claude/CLAUDE.md",
    scope: "global",
    tokens: 1_200,
    hash: "fp1_global",
  };
  const projectRules = (hash: string): ContextItem => ({
    kind: "rules",
    name: "AGENTS.md",
    path: "~/proj/AGENTS.md",
    scope: "project",
    tokens: 4_100,
    hash,
  });
  const memoryIndex = (count: number, hash: string): ContextItem => ({
    kind: "memory",
    name: "MEMORY.md",
    path: "~/.claude/projects/~-proj/memory/MEMORY.md",
    count,
    tokens: 2_400,
    hash,
  });
  const skills: ContextItem = {
    kind: "skills",
    count: 24,
    tokens: 2_300,
    hash: "fp1_skills",
  };
  const parts = (history: number): ContextParts => ({
    history,
    tool_results: 60_000,
    tools: 18_000,
    system: 3_000,
    memory: 10_000,
    other: 1_000,
  });
  const carriedTurns: RequestRecord[] = turns.map((record, position) => ({
    ...record,
    context: {
      parts: parts(position === 2 ? 288_000 : 200_000),
      items:
        position < 5
          ? [
              globalRules,
              projectRules("fp1_a"),
              memoryIndex(60, "fp1_m1"),
              skills,
            ]
          : [
              globalRules,
              projectRules("fp1_b"),
              memoryIndex(63, "fp1_m2"),
              skills,
            ],
      memory_writes: [],
    },
  }));

  function Harness({ records }: { records: RequestRecord[] }) {
    const [selected, setSelected] = useState<string | null>("req_6");
    return (
      <RequestContext
        context={context}
        onOpenCall={vi.fn()}
        onSelectRequest={setSelected}
        selectedRequestId={selected}
        session={session}
        turns={records}
      />
    );
  }

  const lines = (testId: string) =>
    [
      ...query(testId)!.querySelectorAll<HTMLElement>(
        '[data-slot="breakdown-line"]',
      ),
    ].map((line) =>
      [...line.children].map((cell) => cell.textContent).filter(Boolean),
    );

  it("breaks the latest call down and lists the files it carried", () => {
    act(() => root_.render(<Harness records={carriedTurns} />));
    expect(query("context-composition")?.textContent).toContain(
      "最近一次调用带了什么",
    );
    expect(lines("context-composition")).toStrictEqual([
      ["历史对话", "20万"],
      ["工具结果", "6万"],
      ["工具定义", "1.8万"],
      ["规则与记忆", "1万"],
      ["系统提示", "3,000"],
      ["其他", "1,000"],
    ]);
    expect(lines("context-carried")).toStrictEqual([
      ["CLAUDE.md全局规则", "1,200", "没变过"],
      ["AGENTS.md项目规则", "4,100", "第 6 次调用起变了"],
      ["MEMORY.md自动记忆 63 条", "2,400", "本对话新增 3 条"],
      ["Skills 目录24 个", "2,300", "没变过"],
    ]);
    expect(
      query("context-carried")!
        .querySelector('[data-slot="breakdown-line"] [title]')
        ?.getAttribute("title"),
    ).toBe("~/.claude/CLAUDE.md");
  });

  it("follows the call picked on the curve", () => {
    act(() => root_.render(<Harness records={carriedTurns} />));
    const plot = query("context-plot")!;
    plot.getBoundingClientRect = () =>
      ({ left: 0, width: 500, top: 0, height: 192 }) as DOMRect;
    act(() => {
      plot.dispatchEvent(
        new MouseEvent("click", { bubbles: true, clientX: 0.4 * 500 }),
      );
    });
    expect(query("context-composition")?.textContent).toContain(
      "第 3 次调用带了什么",
    );
    expect(lines("context-composition")[0]).toStrictEqual([
      "历史对话",
      "28.8万",
    ]);
    expect(lines("context-carried")[1]).toStrictEqual([
      "AGENTS.md项目规则",
      "4,100",
      "没变过",
    ]);
  });

  it("says when a call kept no composition or nothing at all", () => {
    const stateful = carriedTurns.map((record) =>
      record.id === "req_6"
        ? { ...record, context: { items: [], memory_writes: [] } }
        : record,
    );
    act(() => root_.render(<Harness records={stateful} />));
    expect(query("context-composition")?.textContent).toContain(
      "这次调用只发了新增的部分，看不出组成",
    );
    expect(query("context-carried")?.textContent).toContain(
      "这次调用没带规则或记忆文件",
    );
    act(() => root_.render(<Harness records={turns} />));
    expect(query("context-composition")?.textContent).toContain(
      "这次调用没有记下带了什么",
    );
    expect(query("context-carried")?.textContent).toContain(
      "这次调用没有记下带了什么",
    );
  });
});

describe("SessionContextMeter", () => {
  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.appendChild(container);
    root_ = createRoot(container);
  });

  afterEach(() => {
    act(() => root_.unmount());
    container.remove();
  });

  const renderMeter = (overrides: Partial<RequestSession>) =>
    act(() => {
      root_.render(
        <SessionContextMeter session={{ ...session, ...overrides }} />,
      );
    });

  it("warns once the context nears the compaction point", () => {
    renderMeter({ context_tokens: 353_400 });
    const meter = container.querySelector('[role="meter"]')!;
    expect(meter.getAttribute("aria-valuenow")).toBe("93");
    expect(container.textContent).toBe("上下文93%");
    expect(
      container.querySelector(".text-warning-foreground")?.textContent,
    ).toBe("93%");
    expect(
      container
        .querySelector('[data-slot="usage-meter-inline"]')
        ?.getAttribute("title"),
    ).toBe("上下文 35.34万，客户端约在 38万 时压缩");
  });

  it("measures against the model window before any compaction", () => {
    renderMeter({
      context_tokens: 410_000,
      compaction_point_tokens: undefined,
    });
    expect(container.textContent).toBe("上下文41%");
    expect(container.querySelector(".text-warning-foreground")).toBeNull();
  });

  it("stays hidden without a limit or a context", () => {
    renderMeter({
      compaction_point_tokens: undefined,
      context_window_tokens: undefined,
    });
    expect(container.innerHTML).toBe("");
    renderMeter({ context_tokens: undefined });
    expect(container.innerHTML).toBe("");
  });
});
