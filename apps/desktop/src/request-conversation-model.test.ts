import { describe, expect, it } from "vitest";

import {
  callReason,
  cancelledLabel,
  conversationTurns,
  extractLastUserText,
  extractToolResults,
  foldRoutineCalls,
  pairToolCalls,
  parseCallContent,
  stripClientWrappers,
  type ParsedCall,
} from "./request-conversation-model";
import {
  emptyTrajectoryFields,
  type AuditContent,
  type RequestRecord,
} from "./request-record-model";
import type { ResponseOutput } from "./response-preview-model";

const emptyAudit = {
  request_body_captured: true,
  response_content_captured: true,
  request_body_truncated: false,
  response_content_truncated: false,
  upstream_request_body_captured: false,
  upstream_response_content_captured: false,
  upstream_request_body_truncated: false,
  upstream_response_content_truncated: false,
};

function record(overrides: Partial<RequestRecord> = {}): RequestRecord {
  return {
    id: "req_a",
    parent_request_id: null,
    attempt_index: 1,
    child_count: 0,
    started_at: "2026-10-03T01:00:00Z",
    completed_at: "2026-10-03T01:00:10Z",
    status: "succeeded",
    input_protocol: "anthropic.messages",
    requested_model: "claude-fable-5-1",
    streaming: true,
    route_id: null,
    service_id: "service_01",
    local_access_token_id: null,
    http_status: 200,
    latency_ms: 10_000,
    usage: { input_tokens: 1000, output_tokens: 50, total_tokens: 1050 },
    error: null,
    audit: emptyAudit,
    privacy_restore: null,
    ...emptyTrajectoryFields,
    turn_index: 1,
    ...overrides,
  };
}

function parsed(
  requestId: string,
  overrides: Partial<ParsedCall> = {},
): [string, ParsedCall] {
  return [
    requestId,
    {
      requestId,
      user: null,
      toolResults: [],
      outputs: [],
      locked: false,
      captured: true,
      responseCaptured: true,
      ...overrides,
    },
  ];
}

describe("stripClientWrappers", () => {
  it("folds pasted content, system reminders and skill commands", () => {
    const text = [
      "以下问题存在吗？",
      '<pasted_content id="3cc6">' + "x".repeat(1284) + "</pasted_content>",
      "<system-reminder>be careful</system-reminder>",
      "<command-name>/review</command-name>",
      "<command-message>review</command-message>",
      "<command-args>--fix</command-args>",
      "逐条核对。",
    ].join("\n");
    const result = stripClientWrappers(text);
    expect(result.text).toBe("以下问题存在吗？\n\n逐条核对。");
    expect(result.wrappers).toEqual([
      { kind: "system", chars: "be careful".length },
      { kind: "pasted", chars: 1284 },
      { kind: "command", chars: "/review".length, name: "/review" },
    ]);
  });
});

describe("extractLastUserText", () => {
  it("skips tool-result-only user messages in an Anthropic agent loop", () => {
    const body = JSON.stringify({
      messages: [
        { role: "user", content: "修这四条" },
        {
          role: "assistant",
          content: [{ type: "tool_use", id: "t1", name: "Bash", input: {} }],
        },
        {
          role: "user",
          content: [
            { type: "tool_result", tool_use_id: "t1", content: "ok" },
            { type: "text", text: "<system-reminder>tick</system-reminder>" },
          ],
        },
      ],
    });
    expect(extractLastUserText("anthropic.messages", body)).toMatchObject({
      text: "修这四条",
      source: "body",
    });
  });

  it("reads Chat, Responses and Gemini shapes", () => {
    expect(
      extractLastUserText(
        "openai.chat",
        JSON.stringify({
          messages: [
            { role: "system", content: "sys" },
            { role: "user", content: [{ type: "text", text: "hello" }] },
            { role: "assistant", content: "hi" },
            { role: "tool", tool_call_id: "c1", content: "42" },
          ],
        }),
      )?.text,
    ).toBe("hello");
    expect(
      extractLastUserText(
        "openai.responses",
        JSON.stringify({
          input: [
            {
              type: "message",
              role: "user",
              content: [{ type: "input_text", text: "run it" }],
            },
            { type: "function_call_output", call_id: "c1", output: "done" },
          ],
        }),
      )?.text,
    ).toBe("run it");
    expect(
      extractLastUserText(
        "openai.responses",
        JSON.stringify({ input: "plain string input" }),
      )?.text,
    ).toBe("plain string input");
    expect(
      extractLastUserText(
        "google.generate_content",
        JSON.stringify({
          contents: [{ role: "user", parts: [{ text: "你好" }] }],
        }),
      )?.text,
    ).toBe("你好");
    expect(extractLastUserText("openai.models", "{}")).toBeNull();
    expect(extractLastUserText("openai.chat", "not json")).toBeNull();
  });
});

describe("extractToolResults", () => {
  it("takes only the trailing results of each protocol", () => {
    expect(
      extractToolResults(
        "anthropic.messages",
        JSON.stringify({
          messages: [
            {
              role: "user",
              content: [
                { type: "tool_result", tool_use_id: "old", content: "stale" },
              ],
            },
            { role: "assistant", content: [] },
            {
              role: "user",
              content: [
                {
                  type: "tool_result",
                  tool_use_id: "t1",
                  content: [{ type: "text", text: "Exit code 1" }],
                  is_error: true,
                },
                { type: "tool_result", tool_use_id: "t2", content: "ok" },
              ],
            },
          ],
        }),
      ),
    ).toEqual([
      { id: "t1", name: null, text: "Exit code 1", isError: true },
      { id: "t2", name: null, text: "ok", isError: false },
    ]);
    expect(
      extractToolResults(
        "openai.chat",
        JSON.stringify({
          messages: [
            { role: "user", content: "q" },
            { role: "assistant", tool_calls: [] },
            { role: "tool", tool_call_id: "c1", content: "1" },
            { role: "tool", tool_call_id: "c2", content: "2" },
          ],
        }),
      ).map((result) => result.id),
    ).toEqual(["c1", "c2"]);
    expect(
      extractToolResults(
        "openai.responses",
        JSON.stringify({
          input: [
            { role: "user", content: "q" },
            { type: "function_call_output", call_id: "c9", output: { a: 1 } },
          ],
        }),
      ),
    ).toEqual([
      { id: "c9", name: null, text: '{\n  "a": 1\n}', isError: false },
    ]);
    expect(
      extractToolResults(
        "google.generate_content",
        JSON.stringify({
          contents: [
            { role: "user", parts: [{ text: "q" }] },
            {
              role: "user",
              parts: [
                { functionResponse: { name: "lookup", response: { ok: 1 } } },
              ],
            },
          ],
        }),
      ),
    ).toEqual([
      { id: null, name: "lookup", text: '{\n  "ok": 1\n}', isError: false },
    ]);
  });
});

describe("pairToolCalls", () => {
  const outputs: ResponseOutput[] = [
    { kind: "text", text: "let me check" },
    { kind: "tool", name: "Bash", text: '{"command":"ls"}', id: "t1" },
    { kind: "tool", name: "Read", text: '{"path":"a"}', id: "t2" },
  ];

  it("pairs by id and leaves unanswered tools without a result", () => {
    expect(
      pairToolCalls(outputs, [
        { id: "t2", name: null, text: "file", isError: false },
      ]),
    ).toEqual([
      {
        id: "t1",
        name: "Bash",
        args: '{"command":"ls"}',
        result: null,
        isError: false,
      },
      {
        id: "t2",
        name: "Read",
        args: '{"path":"a"}',
        result: "file",
        isError: false,
      },
    ]);
  });

  it("pairs Gemini results by name and order when there is no id", () => {
    expect(
      pairToolCalls(
        [
          { kind: "tool", name: "lookup", text: "{}" },
          { kind: "tool", name: "lookup", text: "{}" },
        ],
        [
          { id: null, name: "lookup", text: "first", isError: false },
          { id: null, name: "lookup", text: "second", isError: false },
        ],
      ).map((tool) => tool.result),
    ).toEqual(["first", "second"]);
  });
});

describe("parseCallContent", () => {
  it("keeps what the view needs and flags locked parts", () => {
    const content: AuditContent = {
      request_id: "req_a",
      request_body: {
        media_type: "application/json",
        content: JSON.stringify({
          messages: [
            {
              role: "user",
              content: "hi " + "<system-reminder>x</system-reminder>",
            },
          ],
        }),
        truncated: false,
        captured_bytes: 10,
      },
      response_content: null,
      upstream_request_body: null,
      upstream_response_content: null,
      http_meta: null,
      upstream_http_meta: null,
      withheld: {
        response_content: {
          reason: "raw_locked",
          raw_available: true,
          media_type: "text/event-stream",
          truncated: false,
          captured_bytes: 5,
        },
      },
    } as unknown as AuditContent;
    const result = parseCallContent(record(), content, [
      { kind: "tool", name: "Bash", text: "x".repeat(20_000), id: "t1" },
    ]);
    expect(result.user?.text).toBe("hi");
    expect(result.user?.wrappers).toEqual([{ kind: "system", chars: 1 }]);
    expect(result.locked).toBe(true);
    expect(result.captured).toBe(true);
    expect(result.responseCaptured).toBe(false);
    expect(result.outputs[0].text.length).toBe(16 * 1024);
  });
});

describe("callReason", () => {
  it("explains a retry by the earlier attempt's error", () => {
    const root = record({ child_count: 1 });
    const child = record({
      id: "req_child",
      parent_request_id: root.id,
      status: "failed",
      http_status: 529,
      error: {
        category: "upstream",
        code: "overloaded_error",
        message: "Overloaded",
        retryable: true,
        upstream: { status: 529, body: "{}", truncated: false },
      },
    });
    expect(callReason(root, [child])).toBe(
      "上游 HTTP 529 · overloaded_error · Overloaded，重试 1 次后成功",
    );
    expect(callReason(child, [])).toBe(
      "上游 HTTP 529 · overloaded_error · Overloaded",
    );
    expect(callReason(root, [])).toBeNull();
  });
});

describe("conversationTurns", () => {
  const first = record({
    id: "req_1",
    started_at: "2026-10-03T01:00:00Z",
    completed_at: "2026-10-03T01:00:10Z",
    input_preview: "修这四条",
  });
  const second = record({
    id: "req_2",
    started_at: "2026-10-03T01:00:13Z",
    completed_at: "2026-10-03T01:00:20Z",
  });
  const third = record({
    id: "req_3",
    started_at: "2026-10-03T01:00:21Z",
    completed_at: "2026-10-03T01:00:30Z",
  });
  const fourth = record({
    id: "req_4",
    started_at: "2026-10-03T01:00:31Z",
    completed_at: "2026-10-03T01:00:40Z",
    usage: { input_tokens: 2000, output_tokens: 300, total_tokens: 2300 },
  });

  it("uses the preview when nothing has been read and counts the calls", () => {
    const [turn] = conversationTurns([first, second], {}, new Map());
    expect(turn.user).toEqual({
      text: "修这四条",
      wrappers: [],
      source: "preview",
    });
    expect(turn.stats).toMatchObject({
      calls: 2,
      toolCalls: null,
      failed: 0,
      durationMs: 20_000,
      tokens: { input: 2000, output: 100 },
      pending: false,
    });
    expect(turn.reply.text).toBeNull();
  });

  it("pairs tools across calls, folds the routine run and keeps the reply out of the narration", () => {
    const content = new Map<string, ParsedCall>([
      parsed("req_1", {
        user: { text: "修这四条", wrappers: [], source: "body" },
        outputs: [
          { kind: "text", text: "先看一下" },
          { kind: "tool", name: "Read", text: "{}", id: "t1" },
        ],
      }),
      parsed("req_2", {
        toolResults: [{ id: "t1", name: null, text: "file", isError: false }],
        outputs: [{ kind: "tool", name: "Grep", text: "{}", id: "t2" }],
      }),
      parsed("req_3", {
        toolResults: [{ id: "t2", name: null, text: "hits", isError: false }],
        outputs: [{ kind: "tool", name: "Bash", text: "{}", id: "t3" }],
      }),
      parsed("req_4", {
        toolResults: [
          { id: "t3", name: null, text: "Exit code 1", isError: true },
        ],
        outputs: [{ kind: "text", text: "修完了。" }],
      }),
    ]);
    const [turn] = conversationTurns(
      [first, second, third, fourth],
      {},
      content,
    );
    expect(turn.calls[0].toolCalls[0]).toMatchObject({
      name: "Read",
      result: "file",
    });
    expect(turn.calls[0].narration).toEqual(["先看一下"]);
    expect(turn.calls[0].gapMs).toBe(3000);
    // The Bash failure arrives with the fourth request, so the third call is
    // the anomaly, not the fourth.
    expect(turn.calls[2].anomaly).toBe(true);
    expect(turn.calls[2].toolCalls[0]).toMatchObject({
      isError: true,
      result: "Exit code 1",
    });
    expect(turn.reply.text).toBe("修完了。");
    expect(turn.calls[3].narration).toEqual([]);
    expect(turn.stats.toolCalls).toBe(3);
    expect(turn.stats.toolCallsPartial).toBe(false);
    expect(turn.segments.map((segment) => segment.kind)).toEqual([
      "run",
      "call",
      "call",
    ]);
    const run = turn.segments[0];
    if (run.kind !== "run") throw new Error("expected a run");
    expect(run.toolCounts).toEqual([
      ["Read", 1],
      ["Grep", 1],
    ]);
    expect(run.toolTotal).toBe(2);
  });

  it("gives a lone call the reply like any other call", () => {
    const content = new Map<string, ParsedCall>([
      parsed("req_1", { outputs: [{ kind: "text", text: "答案" }] }),
    ]);
    const [turn] = conversationTurns([first], {}, content);
    expect(turn.reply.text).toBe("答案");
    expect(turn.segments).toEqual([
      { kind: "call", call: expect.objectContaining({ reply: true }) },
    ]);
  });

  it("reports a locked turn and a retried call as anomalies", () => {
    const retried = record({
      id: "req_r",
      child_count: 1,
      started_at: "2026-10-03T01:00:41Z",
      completed_at: "2026-10-03T01:00:50Z",
    });
    const child = record({
      id: "req_r_child",
      parent_request_id: "req_r",
      status: "failed",
      http_status: 529,
      error: {
        category: "upstream",
        code: "overloaded_error",
        message: "Overloaded",
        retryable: true,
      },
    });
    const content = new Map<string, ParsedCall>([
      parsed("req_1", { locked: true }),
      parsed("req_r", { locked: true }),
    ]);
    const [turn] = conversationTurns(
      [first, retried],
      { req_r: [child] },
      content,
    );
    expect(turn.locked).toBe(true);
    expect(turn.calls[1].anomaly).toBe(true);
    expect(turn.calls[1].reason).toContain("重试 1 次后成功");
    expect(turn.stats.failed).toBe(1);
    expect(turn.stats.calls).toBe(3);
  });

  it("counts the calls a client hung up on so a folded turn still shows its outcome", () => {
    const hungUp = (id: string, startedAt: string, completedAt: string) =>
      record({
        id,
        status: "cancelled",
        http_status: 200,
        error: null,
        started_at: startedAt,
        completed_at: completedAt,
      });
    const [turn] = conversationTurns(
      [
        hungUp("req_c1", "2026-10-03T01:00:00Z", "2026-10-03T01:04:54Z"),
        hungUp("req_c2", "2026-10-03T01:04:56Z", "2026-10-03T01:09:55Z"),
      ],
      {},
      new Map(),
    );
    expect(turn.stats).toMatchObject({
      calls: 2,
      failed: 0,
      cancelled: 2,
      disconnected: 2,
    });
    expect(cancelledLabel(turn.stats)).toBe("2 次客户端断开");
    const gatewayCancel = record({
      id: "req_c3",
      status: "cancelled",
      recovery: { delay_ms: 0, stop_reason: "timeout" },
    });
    const [mixed] = conversationTurns([gatewayCancel], {}, new Map());
    expect(mixed.stats).toMatchObject({ cancelled: 1, disconnected: 0 });
    expect(cancelledLabel(mixed.stats)).toBe("1 次已取消");
    expect(cancelledLabel({ ...turn.stats, cancelled: 0 })).toBeNull();
  });

  it("never folds a pending call and keeps the last call on its own row", () => {
    const pending = record({
      id: "req_p",
      status: "pending",
      completed_at: null,
      http_status: null,
      latency_ms: null,
      started_at: "2026-10-03T01:00:41Z",
    });
    const calls = conversationTurns(
      [first, second, third, pending],
      {},
      new Map(),
      Date.parse("2026-10-03T01:00:50Z"),
    )[0].calls;
    const segments = foldRoutineCalls(
      calls,
      Date.parse("2026-10-03T01:00:50Z"),
    );
    expect(segments.map((segment) => segment.kind)).toEqual(["run", "call"]);
    expect(calls[3].tone).toBe("pending");
    expect(calls[3].durationMs).toBe(9000);
  });
});
