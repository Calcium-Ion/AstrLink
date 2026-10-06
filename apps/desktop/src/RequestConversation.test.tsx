// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  getRequestAuditContent: vi.fn(),
}));
vi.mock("./bridge", () => bridgeMocks);

import type { CopyFeedback } from "./copy-feedback";
import { RequestConversation } from "./RequestConversation";
import {
  emptyTrajectoryFields,
  type AuditContent,
  type RequestRecord,
  type RequestSession,
} from "./request-record-model";

const audit = {
  request_body_captured: true,
  response_content_captured: true,
  request_body_truncated: false,
  response_content_truncated: false,
  upstream_request_body_captured: false,
  upstream_response_content_captured: false,
  upstream_request_body_truncated: false,
  upstream_response_content_truncated: false,
};

function record(overrides: Partial<RequestRecord>): RequestRecord {
  return {
    id: "req_x",
    parent_request_id: null,
    attempt_index: 1,
    child_count: 0,
    started_at: "2026-10-03T01:00:00Z",
    completed_at: "2026-10-03T01:00:10Z",
    status: "succeeded",
    input_protocol: "anthropic.messages",
    requested_model: "claude-fable-5-1",
    streaming: false,
    route_id: null,
    service_id: "service_01",
    local_access_token_id: null,
    http_status: 200,
    latency_ms: 10_000,
    usage: { input_tokens: 1000, output_tokens: 50, total_tokens: 1050 },
    error: null,
    audit,
    privacy_restore: null,
    ...emptyTrajectoryFields,
    session_id: "session_1",
    client_type: "claude_code",
    ...overrides,
  };
}

const calls = [
  record({
    id: "req_1",
    turn_index: 1,
    input_preview: "修这四条",
    started_at: "2026-10-03T01:00:00Z",
    completed_at: "2026-10-03T01:00:10Z",
  }),
  record({
    id: "req_2",
    turn_index: 1,
    started_at: "2026-10-03T01:00:12Z",
    completed_at: "2026-10-03T01:00:20Z",
  }),
  record({
    id: "req_3",
    turn_index: 1,
    started_at: "2026-10-03T01:00:22Z",
    completed_at: "2026-10-03T01:00:30Z",
  }),
  record({
    id: "req_4",
    turn_index: 1,
    started_at: "2026-10-03T01:00:32Z",
    completed_at: "2026-10-03T01:00:40Z",
    streaming: true,
    first_token_ms: 1_200,
    first_answer_ms: 7_300,
  }),
  record({
    id: "req_5",
    turn_index: 2,
    input_preview: "再确认一下",
    started_at: "2026-10-03T01:05:00Z",
    completed_at: "2026-10-03T01:05:08Z",
  }),
];

const session: RequestSession = {
  id: "session_1",
  title: "修这四条",
  started_at: calls[0]!.started_at,
  last_started_at: calls[4]!.started_at,
  completed_at: calls[4]!.completed_at,
  duration_ms: 46_000,
  active_request_starts: [],
  turn_count: 2,
  call_count: 5,
  status: "succeeded",
  requested_model: "claude-fable-5-1",
  input_protocol: "anthropic.messages",
  service_id: "service_01",
  local_access_token_id: null,
  client_type: "claude_code",
};

function part(content: unknown) {
  return {
    media_type: "application/json",
    content: JSON.stringify(content),
    truncated: false,
    captured_bytes: 100,
  };
}

function anthropicRequest(
  user: string,
  results: Array<{ id: string; text: string; isError?: boolean }> = [],
) {
  return {
    messages: [
      { role: "user", content: user },
      ...(results.length
        ? [
            { role: "assistant", content: [] },
            {
              role: "user",
              content: results.map((result) => ({
                type: "tool_result",
                tool_use_id: result.id,
                content: result.text,
                is_error: result.isError ?? false,
              })),
            },
          ]
        : []),
    ],
  };
}

function anthropicResponse(blocks: unknown[]) {
  return { type: "message", role: "assistant", content: blocks };
}

const contents: Record<string, AuditContent> = {
  req_1: content(
    "req_1",
    anthropicRequest("修这四条 <system-reminder>tick</system-reminder>"),
    anthropicResponse([
      { type: "text", text: "先看一下现状" },
      { type: "tool_use", id: "t1", name: "Read", input: { path: "a.ts" } },
    ]),
  ),
  req_2: content(
    "req_2",
    anthropicRequest("修这四条", [{ id: "t1", text: "file a" }]),
    anthropicResponse([
      { type: "tool_use", id: "t2", name: "Grep", input: { pattern: "x" } },
    ]),
  ),
  req_3: content(
    "req_3",
    anthropicRequest("修这四条", [{ id: "t2", text: "3 hits" }]),
    anthropicResponse([
      { type: "tool_use", id: "t3", name: "Bash", input: { command: "ls" } },
    ]),
  ),
  req_4: content(
    "req_4",
    anthropicRequest("修这四条", [
      { id: "t3", text: "Exit code 1\nTraceback", isError: true },
    ]),
    anthropicResponse([
      { type: "thinking", thinking: "最后核对一遍。" },
      { type: "text", text: "修完了，**两条**已提交。" },
    ]),
  ),
  req_5: content(
    "req_5",
    anthropicRequest("再确认一下"),
    anthropicResponse([
      { type: "thinking", thinking: "用户要确认改动是否都在。" },
      { type: "text", text: "都在。" },
    ]),
  ),
};

function content(
  requestId: string,
  request: unknown,
  response: unknown,
): AuditContent {
  return {
    request_id: requestId,
    request_body: part(request),
    response_content: part(response),
    upstream_request_body: null,
    upstream_response_content: null,
    http_meta: null,
    upstream_http_meta: null,
    withheld: {},
  } as unknown as AuditContent;
}

const copyFeedback: CopyFeedback = {
  activeKey: null,
  state: "idle",
  copy: vi.fn(),
} as unknown as CopyFeedback;

let container: HTMLDivElement;
let root: Root;

async function flush(times = 4) {
  for (let index = 0; index < times; index += 1) {
    await act(async () => await Promise.resolve());
  }
}

function render(
  overrides: Partial<Parameters<typeof RequestConversation>[0]> = {},
) {
  const onSelectRequest = vi.fn();
  act(() => {
    root.render(
      <RequestConversation
        auditContent={null}
        auditError={null}
        auditLoading={false}
        childrenByRoot={{}}
        copyFeedback={copyFeedback}
        onSelectRequest={onSelectRequest}
        outlineOpen
        rawSealing={null}
        selectedRequestId={null}
        services={{ service_01: { id: "service_01", name: "Anthropic 订阅" } }}
        session={session}
        turns={calls}
        {...overrides}
      />,
    );
  });
  return { onSelectRequest };
}

/** Older turns start folded; opening one reads every call it holds. */
async function openFirstTurn() {
  await act(async () => {
    container
      .querySelector<HTMLButtonElement>(
        '[data-testid="conversation-process"] button[aria-expanded="false"]',
      )!
      .click();
  });
}

describe("RequestConversation", () => {
  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    bridgeMocks.getRequestAuditContent.mockReset();
    bridgeMocks.getRequestAuditContent.mockImplementation(
      async (id: string) => {
        const found = contents[id];
        if (!found) throw new Error(`no content for ${id}`);
        return found;
      },
    );
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
  });

  it("reads the turns, folds the routine calls and opens the tool failure", async () => {
    render();
    await vi.waitFor(() => {
      expect(container.textContent).toContain("都在。");
    });
    // A folded turn reads only its first and last call.
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(3);
    await openFirstTurn();
    await vi.waitFor(() => {
      expect(container.textContent).toContain("3 次工具调用");
    });
    const turns = container.querySelectorAll(
      '[data-testid="conversation-turn"]',
    );
    expect(turns.length).toBe(2);
    // The user text comes from the body with the client's reminder folded.
    const user = turns[0]!.querySelector('[data-testid="conversation-user"]');
    expect(user?.textContent).toContain("修这四条");
    expect(user?.textContent).not.toContain("system-reminder");
    expect(user?.textContent).toContain("客户端附加 1 段系统提示");
    // The process line counts calls and tools; the reply is Markdown.
    const process = turns[0]!.querySelector(
      '[data-testid="conversation-process"]',
    );
    expect(process?.textContent).toContain("4 次调用");
    expect(process?.textContent).toContain("3 次工具调用");
    // Markdown rendering loads lazily; the reply arrives rich once it has.
    await vi.waitFor(() => {
      expect(
        turns[0]!.querySelector('[data-testid="conversation-reply"] strong')
          ?.textContent,
      ).toBe("两条");
    });
    // Calls 1–2 fold into a run; call 3 stays open for its failed tool.
    const run = turns[0]!.querySelector('[data-testid="conversation-run"]');
    expect(run?.textContent).toContain("第 1–2 次调用");
    expect(run?.textContent).toContain("Read ×1");
    const tool = turns[0]!.querySelector('[data-testid="conversation-tool"]');
    expect(tool?.textContent).toContain("Bash");
    expect(tool?.textContent).toContain("工具失败");
    expect(tool?.textContent).toContain("Exit code 1");
    expect(
      turns[0]!.querySelector('[data-testid="conversation-narration"]'),
    ).toBeNull();
    // A turn of one call reads like the longer loop: the same process line,
    // with the reply beneath the row of the call that wrote it.
    const single = turns[1]!.querySelector(
      '[data-testid="conversation-process"]',
    );
    expect(single?.textContent).toContain("1 次调用");
    expect(
      single?.querySelector(
        '[data-testid="conversation-call"] [data-testid="conversation-reply"]',
      )?.textContent,
    ).toContain("都在。");
    // Bodies were read once per call, nothing more.
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(5);
  });

  it("marks the latest turn while following and the turn in view after scrolling up", async () => {
    render();
    await vi.waitFor(() => {
      expect(container.textContent).toContain("都在。");
    });
    const current = () =>
      container.querySelector(
        '[data-testid="conversation-outline"] [aria-current="true"]',
      )?.textContent;
    // Nothing overflows yet, so the reader is following the end.
    expect(current()).toContain("第 2 轮");
    // Lay the turns out: the second starts below the viewport's top line
    // even when the reader has scrolled to the end.
    const turnIds = [
      ...container.querySelectorAll<HTMLElement>(
        '[data-testid="conversation-turn"]',
      ),
    ].map((turn) => turn.dataset.turnId);
    const offsets = new Map(turnIds.map((id, index) => [id, index * 400]));
    const offsetTop = Object.getOwnPropertyDescriptor(
      HTMLElement.prototype,
      "offsetTop",
    );
    Object.defineProperty(HTMLElement.prototype, "offsetTop", {
      configurable: true,
      get(this: HTMLElement) {
        return offsets.get(this.dataset.turnId) ?? 0;
      },
    });
    try {
      const scroller = container.querySelector<HTMLElement>(
        '[data-testid="conversation-stream"]',
      )!;
      let scrollTop = 300;
      Object.defineProperties(scroller, {
        scrollHeight: { configurable: true, get: () => 800 },
        clientHeight: { configurable: true, get: () => 500 },
        scrollTop: {
          configurable: true,
          get: () => scrollTop,
          set: (value: number) => {
            scrollTop = value;
          },
        },
      });
      const scrollTo = async (top: number) => {
        scroller.scrollTop = top;
        await act(async () => {
          scroller.dispatchEvent(new Event("scroll"));
        });
      };
      await scrollTo(300);
      expect(current()).toContain("第 2 轮");
      await scrollTo(0);
      expect(current()).toContain("第 1 轮");
      await scrollTo(300);
      expect(current()).toContain("第 2 轮");
    } finally {
      if (offsetTop) {
        Object.defineProperty(HTMLElement.prototype, "offsetTop", offsetTop);
      } else {
        delete (HTMLElement.prototype as { offsetTop?: number }).offsetTop;
      }
    }
  });

  it("keeps a reader who scrolled up in place while the conversation grows", async () => {
    render();
    await vi.waitFor(() => {
      expect(container.textContent).toContain("都在。");
    });
    const scroller = container.querySelector<HTMLElement>(
      '[data-testid="conversation-stream"]',
    )!;
    let scrollTop = 300;
    Object.defineProperties(scroller, {
      scrollHeight: { configurable: true, get: () => 800 },
      clientHeight: { configurable: true, get: () => 500 },
      scrollTop: {
        configurable: true,
        get: () => scrollTop,
        set: (value: number) => {
          scrollTop = value;
        },
      },
    });
    const scrollTo = async (top: number) => {
      scroller.scrollTop = top;
      await act(async () => {
        scroller.dispatchEvent(new Event("scroll"));
      });
    };
    // Following the end: the model rebuilt by a live tick keeps the end in view.
    await scrollTo(300);
    render({ session: { ...session, status: "pending" } });
    await flush();
    expect(scrollTop).toBe(800);
    // Scrolled up: neither a tick nor a new call may move the reader.
    await scrollTo(0);
    const grown = [
      ...calls,
      record({
        id: "req_6",
        turn_index: 2,
        status: "pending",
        completed_at: null,
        started_at: "2026-10-03T01:06:00Z",
      }),
    ];
    render({ session: { ...session, status: "pending" }, turns: grown });
    await flush();
    expect(scrollTop).toBe(0);
    // A whole new turn is announced instead of being scrolled to.
    render({
      session: { ...session, status: "pending", turn_count: 3 },
      turns: [
        ...grown,
        record({
          id: "req_7",
          turn_index: 3,
          input_preview: "再看看",
          status: "pending",
          completed_at: null,
          started_at: "2026-10-03T01:07:00Z",
        }),
      ],
    });
    await flush();
    expect(scrollTop).toBe(0);
    expect(container.textContent).toContain("1 轮新内容");
  });

  it("hangs the reply beneath the call that wrote it and folds it there", async () => {
    const { onSelectRequest } = render();
    await vi.waitFor(() => {
      expect(container.textContent).toContain("都在。");
    });
    const turns = container.querySelectorAll(
      '[data-testid="conversation-turn"]',
    );
    // The older turn's process is folded, every row with it; opening it
    // shows the routine calls and the row of the call that wrote the reply.
    expect(
      turns[0]!.querySelector('[data-testid="conversation-call"]'),
    ).toBeNull();
    expect(
      turns[0]!.querySelector('[data-testid="conversation-reply"]'),
    ).toBeNull();
    await openFirstTurn();
    await vi.waitFor(() => {
      expect(
        turns[0]!.querySelector('[data-testid="conversation-run"]'),
      ).not.toBeNull();
    });
    const replyCall = turns[0]!.querySelector(
      '[data-testid="conversation-call"]:has([data-testid="conversation-reply"])',
    )!;
    expect(replyCall).not.toBeNull();
    expect(replyCall.textContent).toContain("第 4 次调用");
    expect(replyCall.textContent).toContain("修完了");
    // The row is a disclosure, like a run of calls: one click folds the text.
    const row = replyCall.querySelector<HTMLButtonElement>(
      'button[aria-expanded="true"]',
    )!;
    await act(async () => {
      row.click();
    });
    expect(
      replyCall.querySelector('[data-testid="conversation-reply"]'),
    ).toBeNull();
    expect(row.getAttribute("aria-expanded")).toBe("false");
    expect(onSelectRequest).not.toHaveBeenCalled();
    await act(async () => {
      row.click();
    });
    expect(
      replyCall.querySelector('[data-testid="conversation-reply"]'),
    ).not.toBeNull();
    // Inspecting the call is its own control on every row.
    await act(async () => {
      replyCall
        .querySelector<HTMLButtonElement>(
          '[data-testid="conversation-inspect"]',
        )!
        .click();
    });
    expect(onSelectRequest).toHaveBeenCalledWith("req_4");
    expect(
      turns[0]!.querySelectorAll('[data-testid="conversation-reply"]'),
    ).toHaveLength(1);
    // What the model thought is one folded line beneath the call that wrote
    // the reply, timed from the first streamed token to the first answer
    // token; the text itself waits for a click.
    const thought = replyCall.querySelector<HTMLElement>(
      '[data-testid="conversation-reasoning"]',
    )!;
    expect(thought.textContent).toContain("思考过程 · 6.1 s");
    expect(thought.textContent).not.toContain("最后核对一遍");
    await act(async () => {
      thought.querySelector("button")!.click();
    });
    expect(thought.textContent).toContain("最后核对一遍。");
    // A turn of one call folds its thinking and answer from that call's row,
    // as a longer loop does. Without stream timing the line has no duration.
    expect(
      turns[1]!.querySelector('[data-testid="conversation-reasoning"] button')!
        .textContent,
    ).toBe("思考过程");
    expect(turns[1]!.textContent).not.toContain("用户要确认改动是否都在");
    const singleRow = turns[1]!.querySelector<HTMLButtonElement>(
      '[data-testid="conversation-call"] button[aria-expanded="true"]',
    )!;
    expect(singleRow.textContent).toContain("第 1 次调用");
    expect(turns[1]!.textContent).toContain("都在。");
    await act(async () => {
      singleRow.click();
    });
    expect(turns[1]!.textContent).not.toContain("都在。");
    expect(turns[1]!.textContent).not.toContain("思考过程");
  });

  it("shows the narration of a folded run once it is expanded", async () => {
    render();
    await vi.waitFor(() => {
      expect(container.textContent).toContain("都在。");
    });
    await openFirstTurn();
    await vi.waitFor(() => {
      expect(container.textContent).toContain("第 1–2 次调用");
    });
    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          '[data-testid="conversation-run"] > div > button',
        )!
        .click();
    });
    expect(
      container.querySelector('[data-testid="conversation-narration"]')
        ?.textContent,
    ).toContain("先看一下现状");
    expect(container.textContent).toContain("≈ 2.0 s 后发起下一次调用");
  });

  it("selects a call and docks the inspector in place of the outline", async () => {
    const { onSelectRequest } = render();
    await vi.waitFor(() => {
      expect(container.textContent).toContain("都在。");
    });
    expect(
      container.querySelector('[data-testid="conversation-outline"]'),
    ).not.toBeNull();
    const outline = container.querySelector(
      '[data-testid="conversation-outline"]',
    );
    expect(outline?.textContent).toContain("2 轮");
    expect(outline?.textContent).toContain("再确认一下");
    await openFirstTurn();
    await vi.waitFor(() => {
      expect(
        container.querySelector('[data-request-id="req_3"]'),
      ).not.toBeNull();
    });
    await act(async () => {
      container
        .querySelector<HTMLButtonElement>('[data-request-id="req_3"]')!
        .click();
    });
    expect(onSelectRequest).toHaveBeenCalledWith("req_3");
    render({ selectedRequestId: "req_3" });
    await flush();
    expect(
      container.querySelector('[data-testid="trajectory-inspector"]'),
    ).not.toBeNull();
    expect(
      container.querySelector('[data-testid="conversation-outline"]'),
    ).toBeNull();
    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          '[data-testid="trajectory-inspector-close"]',
        )!
        .click();
    });
    expect(
      container.querySelector('[data-testid="conversation-outline"]'),
    ).not.toBeNull();
  });

  it("falls back to the preview and says the bodies are locked", async () => {
    bridgeMocks.getRequestAuditContent.mockImplementation(
      async (id: string) =>
        ({
          request_id: id,
          request_body: null,
          response_content: null,
          upstream_request_body: null,
          upstream_response_content: null,
          http_meta: null,
          upstream_http_meta: null,
          withheld: {
            request_body: {
              reason: "raw_locked",
              raw_available: true,
              media_type: "application/json",
              truncated: false,
              captured_bytes: 10,
            },
          },
        }) as unknown as AuditContent,
    );
    const onUnlockRaw = vi.fn();
    render({ onUnlockRaw });
    await vi.waitFor(() => {
      expect(
        container.querySelector('[data-testid="conversation-locked"]'),
      ).not.toBeNull();
    });
    const user = container.querySelector('[data-testid="conversation-user"]');
    expect(user?.textContent).toContain("修这四条");
    // The notice at the top says it once; nothing below repeats it.
    const stream = container.querySelector(
      '[data-testid="conversation-stream"]',
    )!;
    const notice = stream.querySelector('[data-testid="conversation-locked"]')!;
    expect(
      (stream.textContent ?? "").replace(notice.textContent ?? "", ""),
    ).not.toContain("锁定");
    // The response is unreadable, so the tool count is unknown, not zero.
    expect(container.textContent).not.toContain("次工具调用");
    expect(
      container.querySelector('[data-testid="conversation-reply"]'),
    ).toBeNull();
    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          '[data-testid="conversation-locked"] button',
        )!
        .click();
    });
    expect(onUnlockRaw).toHaveBeenCalled();
  });

  it("explains missing capture without reading anything twice", async () => {
    const uncaptured = calls.map((call) => ({
      ...call,
      audit: {
        ...audit,
        request_body_captured: false,
        response_content_captured: false,
      },
    }));
    bridgeMocks.getRequestAuditContent.mockImplementation(
      async (id: string) =>
        ({
          request_id: id,
          request_body: null,
          response_content: null,
          upstream_request_body: null,
          upstream_response_content: null,
          http_meta: null,
          upstream_http_meta: null,
          withheld: {},
        }) as unknown as AuditContent,
    );
    render({ turns: uncaptured });
    await flush();
    expect(
      container.querySelector('[data-testid="conversation-uncaptured"]'),
    ).not.toBeNull();
    await vi.waitFor(() => {
      expect(container.textContent).toContain("4 次调用");
    });
    // Only the notice at the top mentions the missing bodies.
    expect(container.textContent?.split("未捕获").length).toBe(2);
    expect(container.textContent).not.toContain("次工具调用");
  });
});
