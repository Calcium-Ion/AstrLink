import { describe, expect, it } from "vitest";

import {
  parseAuditContent,
  parseMemoryEntryDetail,
  parseProjectMemory,
  parsePurgeResult,
  parseRequestActivity,
  parseRequestContextTexts,
  parseRequestRecord,
  parseRequestRecordPage,
  parseRequestSession,
  parseRequestSessionDetail,
  displayRequestStatus,
  statusLabel,
  statusTone,
} from "./request-record-model";

const fullRecord = {
  id: "req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  started_at: "2026-07-25T10:00:00Z",
  completed_at: "2026-07-25T10:00:01Z",
  status: "succeeded",
  input_protocol: "openai.responses",
  requested_model: "gpt-4.1",
  streaming: true,
  route_id: "route_01",
  service_id: "service_01",
  local_access_token_id: "token_01",
  plan: { kind: "native" },
  http_status: 200,
  latency_ms: 120,
  usage: {
    input_tokens: 10,
    output_tokens: 20,
    total_tokens: 30,
    cache_read_tokens: 2,
  },
  error: null,
  audit: {
    request_body_captured: true,
    response_content_captured: false,
    request_body_truncated: false,
    response_content_truncated: false,
  },
  privacy_restore: {
    enabled: true,
    mapping_count: 4,
    restored_count: 5,
    visible_restored_count: 3,
    tool_argument_restored_count: 2,
    fallback_count: 0,
  },
  extensions: { note: "ignored" },
};

const fullSession = {
  id: "session_keep",
  title: "创建快捷方式",
  started_at: "2026-08-16T10:00:00Z",
  last_started_at: "2026-08-16T10:01:00Z",
  duration_ms: 120,
  running_since: null,
  active_request_starts: [],
  completed_at: "2026-08-16T10:01:30Z",
  turn_count: 2,
  call_count: 3,
  status: "succeeded",
  requested_model: "gpt-4.1",
  input_protocol: "openai.responses",
  service_id: "service_01",
  local_access_token_id: null,
};

const nullOptionalRecord = {
  id: "req_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  started_at: "2026-07-25T11:00:00Z",
  completed_at: null,
  status: "pending",
  input_protocol: "openai.chat",
  requested_model: null,
  streaming: false,
  route_id: null,
  service_id: null,
  local_access_token_id: null,
  http_status: null,
  latency_ms: null,
  usage: null,
  error: null,
  audit: {
    request_body_captured: false,
    response_content_captured: false,
    request_body_truncated: false,
    response_content_truncated: false,
  },
  privacy_restore: null,
};

describe("reasoning effort metadata", () => {
  it("validates recorded runtime, the running block and active request timestamps", () => {
    expect(
      parseRequestSession({
        ...fullSession,
        duration_ms: 5000,
        running_since: "2026-08-16T10:01:00Z",
        active_request_starts: ["2026-08-16T10:01:00Z"],
      }),
    ).toMatchObject({
      duration_ms: 5000,
      running_since: "2026-08-16T10:01:00Z",
      active_request_starts: ["2026-08-16T10:01:00Z"],
    });
    for (const duration_ms of [-1, 1.5, "12"]) {
      expect(() =>
        parseRequestSession({ ...fullSession, duration_ms }),
      ).toThrow(/duration_ms/);
    }
    for (const running_since of [undefined, "invalid", 42]) {
      expect(() =>
        parseRequestSession({ ...fullSession, running_since }),
      ).toThrow(/running_since/);
    }
    for (const active_request_starts of [null, "invalid", ["invalid"], [42]]) {
      expect(() =>
        parseRequestSession({ ...fullSession, active_request_starts }),
      ).toThrow(/active_request_starts/);
    }
  });

  it("accepts explicit values and older records without the field", () => {
    expect(
      parseRequestRecord({ ...fullRecord, reasoning_effort: "high" })
        .reasoning_effort,
    ).toBe("high");
    expect(
      parseRequestSession({ ...fullSession, reasoning_effort: "xhigh" })
        .reasoning_effort,
    ).toBe("xhigh");
    expect(parseRequestRecord(fullRecord).reasoning_effort).toBeNull();
    expect(parseRequestSession(fullSession).reasoning_effort).toBeNull();
    expect(() =>
      parseRequestRecord({ ...fullRecord, reasoning_effort: 42 }),
    ).toThrow();
  });
});

describe("privacy warning marker", () => {
  it("reads whether a session had a warned call, absent on older Cores", () => {
    expect(
      parseRequestSession({ ...fullSession, privacy_warned: true })
        .privacy_warned,
    ).toBe(true);
    expect(parseRequestSession(fullSession).privacy_warned).toBeUndefined();
    expect(() =>
      parseRequestSession({ ...fullSession, privacy_warned: "yes" }),
    ).toThrow(/privacy_warned/);
  });
});

describe("model redirect metadata", () => {
  const redirect = { from: "gpt-4.1", to: "claude-sonnet-4-5" };

  it("keeps the client model and adds the redirect on records and sessions", () => {
    const record = parseRequestRecord({
      ...fullRecord,
      model_redirect: redirect,
    });
    expect(record.requested_model).toBe(fullRecord.requested_model);
    expect(record.model_redirect).toStrictEqual(redirect);

    const session = parseRequestSession({
      ...fullSession,
      model_redirect: redirect,
    });
    expect(session.requested_model).toBe(fullSession.requested_model);
    expect(session.model_redirect).toStrictEqual(redirect);
  });

  it("leaves the key out when the call was not redirected", () => {
    for (const model_redirect of [null, undefined]) {
      expect(
        parseRequestRecord({ ...fullRecord, model_redirect }),
      ).not.toHaveProperty("model_redirect");
      expect(
        parseRequestSession({ ...fullSession, model_redirect }),
      ).not.toHaveProperty("model_redirect");
    }
    expect(parseRequestRecord(fullRecord)).not.toHaveProperty("model_redirect");
  });

  it("rejects a malformed redirect", () => {
    const long = "m".repeat(257);
    for (const model_redirect of [
      { from: "", to: "b" },
      { from: "a", to: "" },
      { from: long, to: "b" },
      { from: "a", to: long },
    ]) {
      expect(() =>
        parseRequestRecord({ ...fullRecord, model_redirect }),
      ).toThrow("model_redirect）：格式无效");
      expect(() =>
        parseRequestSession({ ...fullSession, model_redirect }),
      ).toThrow("model_redirect）：格式无效");
    }
    // 256 runes is the limit, counted as characters rather than UTF-16 units.
    const wide = "模".repeat(256);
    expect(
      parseRequestRecord({
        ...fullRecord,
        model_redirect: { from: wide, to: "b" },
      }).model_redirect?.from,
    ).toBe(wide);
    for (const model_redirect of ["a → b", ["a", "b"], { from: "a" }]) {
      expect(() =>
        parseRequestRecord({ ...fullRecord, model_redirect }),
      ).toThrow("model_redirect");
    }
  });

  it("accepts the model_redirect trajectory event", () => {
    const parsed = parseRequestRecord({
      ...fullRecord,
      model_redirect: redirect,
      events: [
        {
          kind: "model_redirect",
          started_at: fullRecord.started_at,
          ended_at: fullRecord.started_at,
          status: "succeeded",
          summary: "gpt-4.1 → claude-sonnet-4-5",
          attempt_index: 0,
        },
      ],
    });
    expect(parsed.events[0]).toMatchObject({
      kind: "model_redirect",
      summary: "gpt-4.1 → claude-sonnet-4-5",
    });
  });
});

describe("upstream error", () => {
  const failed = {
    ...fullRecord,
    status: "failed",
    http_status: 500,
    error: {
      category: "upstream",
      code: "upstream_http_error",
      message: "new_api_error: 模型 claude-haiku-4-5 的可用渠道不存在",
      retryable: true,
      upstream: {
        status: 500,
        content_type: "application/json",
        body: '{"error":{"type":"new_api_error","message":"模型 claude-haiku-4-5 的可用渠道不存在"}}',
        truncated: false,
      },
    },
  };

  it("keeps the provider's response verbatim", () => {
    expect(parseRequestRecord(failed).error).toStrictEqual(failed.error);
  });

  it("accepts older errors without it", () => {
    const { upstream: _upstream, ...older } = failed.error;
    expect(
      parseRequestRecord({ ...failed, error: older }).error,
    ).not.toHaveProperty("upstream");
  });

  it("rejects a malformed response", () => {
    expect(() =>
      parseRequestRecord({
        ...failed,
        error: { ...failed.error, upstream: { status: 500 } },
      }),
    ).toThrow();
  });
});

describe("routing decision metadata", () => {
  const decision = {
    selected: "failover",
    skipped: [
      { service_id: "service_mly", reason: "model_not_listed" },
      { service_id: "service_codex", reason: "disabled" },
    ],
  };

  it("keeps why routing chose the provider and what it skipped", () => {
    expect(
      parseRequestRecord({ ...fullRecord, routing_decision: decision })
        .routing_decision,
    ).toStrictEqual(decision);
    const pinned = { selected: "client_pin", skipped: [] };
    expect(
      parseRequestRecord({ ...fullRecord, routing_decision: pinned })
        .routing_decision,
    ).toStrictEqual(pinned);
    // No provider could serve the call: every exclusion, no selection.
    const unserved = { skipped: decision.skipped };
    expect(
      parseRequestRecord({
        ...nullOptionalRecord,
        status: "failed",
        routing_decision: unserved,
      }).routing_decision,
    ).toStrictEqual(unserved);
  });

  it("leaves the key out for records routing did not explain", () => {
    for (const routing_decision of [null, undefined]) {
      expect(
        parseRequestRecord({ ...fullRecord, routing_decision }),
      ).not.toHaveProperty("routing_decision");
    }
    expect(parseRequestRecord(fullRecord)).not.toHaveProperty(
      "routing_decision",
    );
  });

  it("rejects wire values it cannot explain", () => {
    for (const [routing_decision, message] of [
      ["priority", "应为对象"],
      [{ selected: "priority" }, "应为数组"],
      [{ selected: "cheapest", skipped: [] }, "selected）：未知原因"],
      [{ selected: null, skipped: [] }, "selected）：未知原因"],
      [
        { skipped: [{ service_id: "service_a", reason: "slow" }] },
        "reason）：未知原因",
      ],
      [{ skipped: [{ reason: "disabled" }] }, "应为字符串"],
      [{ skipped: ["service_a"] }, "应为对象"],
      [
        {
          skipped: Array.from({ length: 65 }, (_, index) => ({
            service_id: `service_${index}`,
            reason: "disabled",
          })),
        },
        "条目过多",
      ],
    ] as const) {
      expect(() =>
        parseRequestRecord({ ...fullRecord, routing_decision }),
      ).toThrow(message);
    }
  });
});

describe("conversion diagnostics", () => {
  const diagnostics = [
    {
      phase: "request",
      severity: "error",
      code: "unsupported_hosted_tool",
      path: "tools[0]",
      message:
        'OpenAI Chat Completions cannot represent hosted tool "local_shell"',
    },
    {
      phase: "response",
      severity: "warning",
      code: "hosted_tool_event_unrepresentable",
      message: "",
    },
  ];

  it("keeps what the conversion dropped or rewrote", () => {
    expect(
      parseRequestRecord({ ...fullRecord, conversion_diagnostics: diagnostics })
        .conversion_diagnostics,
    ).toStrictEqual(diagnostics);
  });

  it("leaves the key out when nothing was lost", () => {
    for (const conversion_diagnostics of [null, undefined, []]) {
      expect(
        parseRequestRecord({ ...fullRecord, conversion_diagnostics }),
      ).not.toHaveProperty("conversion_diagnostics");
    }
  });

  it("rejects diagnostics it cannot show", () => {
    const valid = diagnostics[0];
    for (const [conversion_diagnostics, message] of [
      [valid, "应为数组"],
      [["tools[0]"], "应为对象"],
      [[{ ...valid, phase: "routing" }], "phase）：取值无效"],
      [[{ ...valid, severity: "fatal" }], "severity）：取值无效"],
      [[{ ...valid, code: "" }], "不得为空"],
      [[{ ...valid, path: 0 }], "应为字符串"],
      [Array.from({ length: 65 }, () => valid), "条目过多"],
    ] as const) {
      expect(() =>
        parseRequestRecord({ ...fullRecord, conversion_diagnostics }),
      ).toThrow(message);
    }
  });
});

describe("request-record IPC contract", () => {
  it("round-trips a valid record and drops plan/extensions", () => {
    const parsed = parseRequestRecord(fullRecord);
    expect(parsed).toEqual({
      id: fullRecord.id,
      parent_request_id: null,
      attempt_index: 1,
      child_count: 0,
      session_id: null,
      previous_response_id: null,
      output_response_id: null,
      input_preview: null,
      turn_index: null,
      session_link: null,
      cursors: [],
      events: [],
      started_at: fullRecord.started_at,
      completed_at: fullRecord.completed_at,
      status: "succeeded",
      input_protocol: fullRecord.input_protocol,
      requested_model: fullRecord.requested_model,
      reasoning_effort: null,
      streaming: true,
      route_id: fullRecord.route_id,
      service_id: fullRecord.service_id,
      local_access_token_id: fullRecord.local_access_token_id,
      http_status: 200,
      latency_ms: 120,
      first_token_ms: null,
      first_answer_ms: null,
      usage: {
        input_tokens: 10,
        output_tokens: 20,
        total_tokens: 30,
        cache_read_tokens: 2,
      },
      error: null,
      audit: {
        ...fullRecord.audit,
        upstream_request_body_captured: false,
        upstream_response_content_captured: false,
        upstream_request_body_truncated: false,
        upstream_response_content_truncated: false,
      },
      privacy_restore: fullRecord.privacy_restore,
    });
    expect(parseRequestRecord(nullOptionalRecord)).toEqual({
      ...nullOptionalRecord,
      first_token_ms: null,
      first_answer_ms: null,
      reasoning_effort: null,
      parent_request_id: null,
      attempt_index: 1,
      child_count: 0,
      session_id: null,
      previous_response_id: null,
      output_response_id: null,
      input_preview: null,
      turn_index: null,
      session_link: null,
      cursors: [],
      events: [],
      audit: {
        ...nullOptionalRecord.audit,
        upstream_request_body_captured: false,
        upstream_response_content_captured: false,
        upstream_request_body_truncated: false,
        upstream_response_content_truncated: false,
      },
    });
  });

  it("parses conversation linking fields and rejects bad kinds", () => {
    const parsed = parseRequestRecord({
      ...fullRecord,
      turn_index: 2,
      session_link: { kind: "echo_id", value: "call_8f3kd92ls0a1Qz7" },
      cursors: [
        { kind: "explicit", direction: "out", value: "chatcmpl-1" },
        {
          kind: "fingerprint",
          direction: "out",
          value: "fp1_0123456789abcdef0123456789abcdef",
        },
      ],
    });
    expect(parsed.turn_index).toBe(2);
    expect(parsed.session_link).toEqual({
      kind: "echo_id",
      value: "call_8f3kd92ls0a1Qz7",
    });
    expect(parsed.cursors).toHaveLength(2);
    expect(() => parseRequestRecord({ ...fullRecord, turn_index: 0 })).toThrow(
      /turn_index/,
    );
    expect(() =>
      parseRequestRecord({
        ...fullRecord,
        session_link: { kind: "guess", value: "x" },
      }),
    ).toThrow(/session_link\.kind/);
    expect(() =>
      parseRequestRecord({
        ...fullRecord,
        cursors: [{ kind: "explicit", direction: "sideways", value: "x" }],
      }),
    ).toThrow(/cursors\[0\]\.direction/);
  });

  it("treats null trajectory arrays as empty", () => {
    // A record stored without a trajectory keeps nil Go slices, which reach the
    // desktop as `null`. Older records omit the keys entirely.
    const parsed = parseRequestRecord({
      ...fullRecord,
      cursors: null,
      events: null,
    });
    expect(parsed.cursors).toEqual([]);
    expect(parsed.events).toEqual([]);
    expect(() => parseRequestRecord({ ...fullRecord, events: "none" })).toThrow(
      /events/,
    );
  });

  it("parses request-time privacy hit counts", () => {
    const parsed = parseRequestRecord({
      ...fullRecord,
      privacy_restore: {
        ...fullRecord.privacy_restore,
        hits: [
          { kind: "email", count: 2 },
          { kind: "url", count: 1 },
        ],
      },
    });
    expect(parsed.privacy_restore?.hits).toEqual([
      { kind: "email", count: 2 },
      { kind: "url", count: 1 },
    ]);
  });

  it("parses model confidence and ignored matches", () => {
    const confidence = { min: 0.81, max: 0.99, threshold: 0.7 };
    const parsed = parseRequestRecord({
      ...fullRecord,
      privacy_findings: [
        { kind: "email", json_path: "/input/0/content", count: 2, confidence },
        { kind: "url", json_path: "/input/1/content", count: 1 },
      ],
      privacy_ignored: [
        { kind: "private_person", reason: "low_confidence", count: 2 },
        { kind: "email", reason: "allowlisted", count: 1, confidence },
        {
          kind: "common_secret",
          reason: "astrlink_identifier",
          count: 3,
          confidence,
        },
      ],
    });
    expect(parsed.privacy_findings).toEqual([
      { kind: "email", json_path: "/input/0/content", count: 2, confidence },
      { kind: "url", json_path: "/input/1/content", count: 1 },
    ]);
    expect(parsed.privacy_ignored).toEqual([
      { kind: "private_person", reason: "low_confidence", count: 2 },
      { kind: "email", reason: "allowlisted", count: 1, confidence },
      {
        kind: "common_secret",
        reason: "astrlink_identifier",
        count: 3,
        confidence,
      },
    ]);
    expect(parseRequestRecord(fullRecord)).not.toHaveProperty(
      "privacy_ignored",
    );

    const finding = (value: object) => ({
      ...fullRecord,
      privacy_findings: [
        { kind: "email", json_path: "/", count: 1, confidence: value },
      ],
    });
    expect(() =>
      parseRequestRecord(finding({ ...confidence, threshold: 1.5 })),
    ).toThrow("$.privacy_findings[0].confidence.threshold");
    expect(() =>
      parseRequestRecord(finding({ ...confidence, min: 0.995 })),
    ).toThrow("$.privacy_findings[0].confidence.min");
    expect(() =>
      parseRequestRecord({
        ...fullRecord,
        privacy_ignored: [{ kind: "email", reason: "placeholder", count: 1 }],
      }),
    ).toThrow("$.privacy_ignored[0].reason");
  });

  it("maps legacy cached_input_tokens to cache_read_tokens", () => {
    const parsed = parseRequestRecord({
      ...fullRecord,
      usage: {
        input_tokens: 10,
        output_tokens: 20,
        total_tokens: 30,
        cached_input_tokens: 7,
      },
    });
    expect(parsed.usage).toEqual({
      input_tokens: 10,
      output_tokens: 20,
      total_tokens: 30,
      cache_read_tokens: 7,
    });
  });

  it("parses a page with a cursor", () => {
    expect(
      parseRequestRecordPage({
        items: [nullOptionalRecord],
        next_cursor: "cursor-1",
      }),
    ).toEqual({
      items: [
        {
          ...nullOptionalRecord,
          first_token_ms: null,
          first_answer_ms: null,
          reasoning_effort: null,
          parent_request_id: null,
          attempt_index: 1,
          child_count: 0,
          session_id: null,
          previous_response_id: null,
          output_response_id: null,
          input_preview: null,
          turn_index: null,
          session_link: null,
          cursors: [],
          events: [],
          audit: {
            ...nullOptionalRecord.audit,
            upstream_request_body_captured: false,
            upstream_response_content_captured: false,
            upstream_request_body_truncated: false,
            upstream_response_content_truncated: false,
          },
        },
      ],
      next_cursor: "cursor-1",
    });
  });

  it("parses a session detail and defaults missing trajectory fields", () => {
    expect(
      parseRequestSession({
        id: "session_keep",
        title: "创建快捷方式",
        started_at: "2026-08-16T10:00:00Z",
        last_started_at: "2026-08-16T10:01:00Z",
        duration_ms: 120,
        running_since: null,
        active_request_starts: [],
        completed_at: "2026-08-16T10:01:30Z",
        turn_count: 2,
        call_count: 3,
        status: "succeeded",
        requested_model: "gpt-4.1",
        input_protocol: "openai.responses",
        service_id: "service_01",
        local_access_token_id: null,
      }),
    ).toEqual({
      id: "session_keep",
      title: "创建快捷方式",
      started_at: "2026-08-16T10:00:00Z",
      last_started_at: "2026-08-16T10:01:00Z",
      duration_ms: 120,
      running_since: null,
      tool_duration_ms: null,
      average_ttft_ms: null,
      output_tokens_per_second: null,
      active_request_starts: [],
      completed_at: "2026-08-16T10:01:30Z",
      turn_count: 2,
      call_count: 3,
      status: "succeeded",
      requested_model: "gpt-4.1",
      reasoning_effort: null,
      input_protocol: "openai.responses",
      service_id: "service_01",
      local_access_token_id: null,
    });
    const detail = parseRequestSessionDetail({
      id: "session_keep",
      title: "创建快捷方式",
      started_at: "2026-08-16T10:00:00Z",
      last_started_at: "2026-08-16T10:00:00Z",
      duration_ms: 120,
      running_since: null,
      active_request_starts: [],
      completed_at: "2026-08-16T10:00:01Z",
      turn_count: 1,
      call_count: 1,
      status: "succeeded",
      requested_model: "gpt-4.1",
      input_protocol: "openai.responses",
      service_id: null,
      local_access_token_id: null,
      turns: [fullRecord],
    });
    expect(detail.turns).toHaveLength(1);
    expect(detail.turns[0].events).toEqual([]);
    expect(detail.turns[0].session_id).toBeNull();
  });

  it("rejects missing id, bad status, and non-array items", () => {
    const { id: _id, ...missingId } = fullRecord;
    expect(() => parseRequestRecord(missingId)).toThrow("缺少字段");
    expect(() => parseRequestRecord({ ...fullRecord, status: "ok" })).toThrow(
      "status）：未知状态",
    );
    expect(() =>
      parseRequestRecordPage({ items: {}, next_cursor: null }),
    ).toThrow("应为数组");
  });

  it("parses audit content with null parts and purge results", () => {
    expect(
      parseAuditContent({
        request_id: fullRecord.id,
        request_body: null,
        response_content: {
          media_type: "text/plain",
          content: "hello",
          truncated: true,
          captured_bytes: 5,
        },
      }),
    ).toEqual({
      request_id: fullRecord.id,
      view: "full",
      // An older core sidecar that omits the key entirely maps to null.
      http_meta: null,
      request_body: null,
      response_content: {
        media_type: "text/plain",
        content: "hello",
        truncated: true,
        captured_bytes: 5,
      },
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
      withheld: {},
      privacy_findings: [],
    });
    expect(
      parsePurgeResult({ deleted_records: 3, deleted_audit_blobs: 1 }),
    ).toEqual({ deleted_records: 3, deleted_audit_blobs: 1 });
  });

  it("keeps withheld parts out of the readable body fields", () => {
    const parsed = parseAuditContent({
      request_id: fullRecord.id,
      view: "full",
      http_meta: null,
      request_body: {
        withheld: true,
        reason: "raw_locked",
        raw_available: false,
        media_type: "application/json",
        truncated: false,
        captured_bytes: 42,
      },
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: {
        media_type: "application/json",
        content: '{"content":"<EMAIL_1>"}',
        truncated: false,
        captured_bytes: 23,
        exposure: "shareable",
      },
      upstream_response_content: null,
      privacy_findings: [
        { kind: "email", json_path: "/messages/0/content", count: 1 },
      ],
    });
    expect(parsed.request_body).toBeNull();
    expect(parsed.withheld).toEqual({
      request_body: {
        reason: "raw_locked",
        raw_available: false,
        media_type: "application/json",
        truncated: false,
        captured_bytes: 42,
      },
    });
    expect(parsed.upstream_request_body?.exposure).toBe("shareable");
    expect(parsed.privacy_findings).toEqual([
      { kind: "email", json_path: "/messages/0/content", count: 1 },
    ]);

    const withheld = {
      withheld: true,
      reason: "privacy_redacted",
      raw_available: true,
      media_type: "application/json",
      truncated: false,
      captured_bytes: 1,
    };
    const parse = (request_body: unknown, extra: object = {}) =>
      parseAuditContent({ request_id: fullRecord.id, request_body, ...extra });
    // A placeholder that carries content could pass for a readable body.
    expect(() => parse({ ...withheld, content: "secret" })).toThrow(
      "$.request_body.content",
    );
    expect(() => parse({ ...withheld, reason: "because" })).toThrow(
      "$.request_body.reason",
    );
    expect(() => parse({ ...withheld, withheld: false })).toThrow(
      "$.request_body.withheld",
    );
    expect(() =>
      parse({
        media_type: "text/plain",
        content: "x",
        truncated: false,
        captured_bytes: 1,
        exposure: "public",
      }),
    ).toThrow("$.request_body.exposure");
    expect(() => parse(null, { view: "raw" })).toThrow("$.view");
    expect(() => parse(null, { privacy_findings: {} })).toThrow(
      "$.privacy_findings",
    );
  });

  it("parses http metadata with ordered redacted headers", () => {
    const meta = {
      method: "POST",
      url: "/v1/responses?key=<redacted>",
      http_version: "HTTP/1.1",
      request_headers: [
        {
          name: "authorization",
          value: "Bearer <redacted:51 chars>",
          redacted: true,
        },
        { name: "content-type", value: "application/json", redacted: false },
      ],
      response_status: 200,
      response_headers: [
        { name: "x-request-id", value: "req_1", redacted: false },
      ],
    };
    const parsed = parseAuditContent({
      request_id: fullRecord.id,
      http_meta: meta,
      request_body: null,
      response_content: null,
    });
    expect(parsed.http_meta).toEqual(meta);

    expect(
      parseAuditContent({
        request_id: fullRecord.id,
        http_meta: null,
        request_body: null,
        response_content: null,
      }).http_meta,
    ).toBeNull();

    expect(() =>
      parseAuditContent({
        request_id: fullRecord.id,
        http_meta: { ...meta, request_headers: "not-an-array" },
        request_body: null,
        response_content: null,
      }),
    ).toThrow("应为数组");
  });

  it("maps status labels and tones", () => {
    expect(statusLabel("pending")).toBe("进行中");
    expect(statusLabel("succeeded")).toBe("完成");
    expect(statusLabel("failed")).toBe("失败");
    expect(statusLabel("cancelled")).toBe("已取消");
    expect(statusLabel("blocked")).toBe("已拦截");
    expect(statusLabel("interrupted")).toBe("已中断");
    expect(statusTone("succeeded")).toBe("positive");
    expect(statusTone("failed")).toBe("negative");
    expect(statusTone("pending")).toBe("pending");
    expect(statusTone("cancelled")).toBe("pending");
    expect(statusTone("blocked")).toBe("blocked");
    expect(statusTone("interrupted")).toBe("pending");
  });

  it("accepts the session-only interrupted status", () => {
    expect(
      parseRequestSession({ ...fullSession, status: "interrupted" }).status,
    ).toBe("interrupted");
    expect(() =>
      parseRequestSession({ ...fullSession, status: "stopped" }),
    ).toThrow("status）：未知状态");
  });

  it("treats a completed HTTP error as failed", () => {
    expect(displayRequestStatus("succeeded", 200)).toBe("succeeded");
    expect(displayRequestStatus("succeeded", 502)).toBe("failed");
    expect(displayRequestStatus("succeeded", 403)).toBe("failed");
    expect(displayRequestStatus("blocked", 403)).toBe("blocked");
    expect(displayRequestStatus("failed", 502)).toBe("failed");
    expect(displayRequestStatus("succeeded", null)).toBe("succeeded");
  });
});

it("parses performance samples and rejects invalid timing and rates", () => {
  expect(
    parseRequestRecord({ ...fullRecord, first_token_ms: 0 }).first_token_ms,
  ).toBe(0);
  expect(
    parseRequestSession({
      ...fullSession,
      tool_duration_ms: 0,
      average_ttft_ms: 2200.5,
      output_tokens_per_second: 131.25,
    }),
  ).toMatchObject({
    tool_duration_ms: 0,
    average_ttft_ms: 2200.5,
    output_tokens_per_second: 131.25,
  });
  for (const key of [
    "tool_duration_ms",
    "average_ttft_ms",
    "output_tokens_per_second",
  ]) {
    expect(
      parseRequestSession(fullSession)[key as "tool_duration_ms"],
    ).toBeNull();
    for (const value of [-1, Infinity, NaN, "12"]) {
      expect(() =>
        parseRequestSession({ ...fullSession, [key]: value }),
      ).toThrow(key);
    }
  }
  expect(() =>
    parseRequestSession({ ...fullSession, tool_duration_ms: 1.5 }),
  ).toThrow("tool_duration_ms");
  expect(() =>
    parseRequestRecord({ ...fullRecord, first_token_ms: -1 }),
  ).toThrow("first_token_ms");
  expect(
    parseRequestRecord({
      ...fullRecord,
      first_token_ms: 400,
      first_answer_ms: 2600,
    }).first_answer_ms,
  ).toBe(2600);
  expect(() =>
    parseRequestRecord({ ...fullRecord, first_answer_ms: 1.5 }),
  ).toThrow("first_answer_ms");
});

describe("subagent links", () => {
  const sessionFields = {
    id: "session_child",
    title: "find the router files",
    started_at: "2026-10-24T10:00:00Z",
    last_started_at: "2026-10-24T10:00:05Z",
    duration_ms: 120,
    running_since: null,
    active_request_starts: [],
    completed_at: "2026-10-24T10:00:06Z",
    turn_count: 1,
    call_count: 2,
    status: "succeeded",
    requested_model: "gpt-4.1",
    input_protocol: "anthropic.messages",
    service_id: null,
    local_access_token_id: null,
  };

  it("reads the parent and the subagents a conversation started", () => {
    const child = parseRequestSession({
      ...sessionFields,
      parent_session_id: "session_parent",
    });
    expect(child.parent_session_id).toBe("session_parent");
    expect(child.subagent_count).toBeUndefined();
    const parent = parseRequestSessionDetail({
      ...sessionFields,
      id: "session_parent",
      subagent_count: 1,
      turns: [{ ...fullRecord, parent_session_id: "session_grand" }],
      subagents: [{ ...sessionFields, parent_session_id: "session_parent" }],
      with_subagents: { call_count: 4, input_tokens: 900, output_tokens: 40 },
    });
    expect(parent.subagent_count).toBe(1);
    expect(parent.subagents?.map((item) => item.id)).toEqual(["session_child"]);
    expect(parent.with_subagents).toEqual({
      call_count: 4,
      input_tokens: 900,
      output_tokens: 40,
    });
    expect(parent.turns[0].parent_session_id).toBe("session_grand");
    const activity = parseRequestActivity({
      now: "2026-10-24T10:01:00Z",
      calls: [
        {
          session_id: "session_child",
          parent_session_id: "session_parent",
          started_at: "2026-10-24T10:00:00Z",
          completed_at: null,
          status: "pending",
        },
      ],
    });
    expect(activity.calls[0].parent_session_id).toBe("session_parent");
  });

  it("rejects malformed subagent fields", () => {
    expect(() =>
      parseRequestSession({ ...sessionFields, subagent_count: -1 }),
    ).toThrow();
    expect(() =>
      parseRequestSession({ ...sessionFields, parent_session_id: 7 }),
    ).toThrow();
    expect(() =>
      parseRequestSessionDetail({ ...sessionFields, turns: [], subagents: {} }),
    ).toThrow();
    expect(() =>
      parseRequestSessionDetail({
        ...sessionFields,
        turns: [],
        with_subagents: { call_count: 1, input_tokens: -1, output_tokens: 0 },
      }),
    ).toThrow();
  });
});

describe("request activity", () => {
  it("reads the output tokens of ended calls", () => {
    const ended = {
      session_id: "session_a",
      started_at: "2026-10-24T10:00:00Z",
      completed_at: "2026-10-24T10:00:20Z",
      status: "succeeded",
    };
    const activity = parseRequestActivity({
      now: "2026-10-24T10:01:00Z",
      calls: [{ ...ended, output_tokens: 420 }, ended],
    });
    expect(activity.calls[0].output_tokens).toBe(420);
    expect(activity.calls[1]).not.toHaveProperty("output_tokens");
    for (const output_tokens of [-1, 1.5, "420"]) {
      expect(() =>
        parseRequestActivity({
          now: "2026-10-24T10:01:00Z",
          calls: [{ ...ended, output_tokens }],
        }),
      ).toThrow();
    }
  });

  it("reads the model each call ran with", () => {
    const call = {
      session_id: "session_a",
      started_at: "2026-10-24T10:00:00Z",
      completed_at: null,
      status: "pending",
    };
    const activity = parseRequestActivity({
      now: "2026-10-24T10:01:00Z",
      calls: [{ ...call, model: "kimi-k3" }, call],
    });
    expect(activity.calls[0].model).toBe("kimi-k3");
    expect(activity.calls[1]).not.toHaveProperty("model");
    expect(() =>
      parseRequestActivity({
        now: "2026-10-24T10:01:00Z",
        calls: [{ ...call, model: 7 }],
      }),
    ).toThrow();
  });
});

describe("client attribution", () => {
  it.each([
    "codex",
    "claude_code",
    "cursor",
    "grok_cli",
    "gemini_cli",
    "opencode",
    "openclaw",
    "cline",
    "pi",
    "deepseek_harness",
    "codewhale",
    "reasonix",
    "qwen_code",
    "kimi_code",
    "codebuddy",
    "copilot",
    "droid",
    "crush",
    "kilo_code",
    "roo_code",
    "mistral_vibe",
    "zed",
    "cherry_studio",
    "unknown",
  ])("keeps %s on records and summaries", (client_type) => {
    expect(parseRequestRecord({ ...fullRecord, client_type }).client_type).toBe(
      client_type,
    );
    expect(
      parseRequestSession({ ...fullSession, client_type }).client_type,
    ).toBe(client_type);
  });
  it("accepts historical records and degrades future labels without guessing from the model", () => {
    expect(parseRequestRecord(fullRecord).client_type).toBeUndefined();
    expect(parseRequestSession(fullSession).client_type).toBeUndefined();
    expect(
      parseRequestRecord({ ...fullRecord, client_type: "future_client" })
        .client_type,
    ).toBe("unknown");
    expect(
      parseRequestSession({ ...fullSession, client_type: "future_client" })
        .client_type,
    ).toBe("unknown");
  });
});

describe("session context and cost", () => {
  const context = {
    calls: [
      { request_id: "req_1", tokens: 120_000 },
      { request_id: "req_2", tokens: 30_000 },
    ],
    compactions: [
      {
        request_id: "req_2",
        summary_request_id: "req_1",
        before_tokens: 120_000,
        after_tokens: 30_000,
        marked: true,
      },
    ],
    cache_misses: [
      {
        request_id: "req_1",
        reason: "expired",
        idle_ms: 600_000,
        ttl_ms: 300_000,
        rewritten_tokens: 100_000,
        extra_usd: "0.345000000",
      },
    ],
  };

  it("reads the context fields of a summary and a detail", () => {
    const session = parseRequestSession({
      ...fullSession,
      context_tokens: 30_000,
      compaction_point_tokens: 120_000,
      context_window_tokens: 200_000,
      compaction_count: 1,
      usage: {
        input_tokens: 150_000,
        output_tokens: 2_000,
        cache_read_tokens: 20_000,
        cache_write_tokens: 130_000,
        cache_hit_ratio: 0.13,
      },
    });
    expect(session).toMatchObject({
      context_tokens: 30_000,
      compaction_point_tokens: 120_000,
      context_window_tokens: 200_000,
      compaction_count: 1,
      usage: { cache_hit_ratio: 0.13 },
    });
    expect(session.cost).toBeUndefined();
    const plain = parseRequestSession(fullSession);
    expect(plain.context_tokens).toBeUndefined();
    expect(plain.usage).toBeUndefined();

    const detail = parseRequestSessionDetail({
      ...fullSession,
      turns: [],
      cost: { amount_usd: "1.250000000", priced: 2, unpriced: 0, pending: 1 },
      context,
    });
    expect(detail.cost).toStrictEqual({
      amount_usd: "1.250000000",
      priced: 2,
      unpriced: 0,
      pending: 1,
    });
    expect(detail.context).toStrictEqual(context);
    expect(
      parseRequestSessionDetail({ ...fullSession, turns: [] }).context,
    ).toBeUndefined();
  });

  it("rejects malformed amounts, ratios and reasons", () => {
    expect(() =>
      parseRequestSession({
        ...fullSession,
        cost: { amount_usd: "-1", priced: 1, unpriced: 0, pending: 0 },
      }),
    ).toThrow(/amount_usd/);
    expect(() =>
      parseRequestSession({
        ...fullSession,
        usage: {
          input_tokens: 1,
          output_tokens: 1,
          cache_read_tokens: 0,
          cache_write_tokens: 0,
          cache_hit_ratio: 1.5,
        },
      }),
    ).toThrow(/cache_hit_ratio/);
    expect(() =>
      parseRequestSession({ ...fullSession, context_tokens: -1 }),
    ).toThrow(/context_tokens/);
    expect(() =>
      parseRequestSessionDetail({
        ...fullSession,
        turns: [],
        context: {
          ...context,
          cache_misses: [{ ...context.cache_misses[0], reason: "cosmic" }],
        },
      }),
    ).toThrow(/reason/);
    expect(() =>
      parseRequestSessionDetail({
        ...fullSession,
        turns: [],
        context: { calls: [], compactions: [] },
      }),
    ).toThrow(/cache_misses/);
  });
});

describe("call context", () => {
  const callContext = {
    parts: {
      history: 9_000,
      tool_results: 4_000,
      tools: 3_000,
      system: 2_000,
      memory: 1_500,
      other: 500,
    },
    items: [
      {
        kind: "rules",
        name: "CLAUDE.md",
        path: "~/.claude/CLAUDE.md",
        scope: "global",
        tokens: 600,
        hash: "fp1_rules",
      },
      {
        kind: "memory",
        name: "MEMORY.md",
        path: "~/.claude/projects/~-proj/memory/MEMORY.md",
        count: 63,
        tokens: 900,
        hash: "fp1_memory",
      },
      {
        kind: "mcp",
        count: 38,
        servers: ["paseo"],
        tokens: 2_400,
        hash: "fp1_mcp",
      },
      // A kind a later Core may add.
      { kind: "hooks", tokens: 10 },
    ],
    system_hash: "fp1_system",
    tools_hash: "fp1_tools",
    summary: true,
    compacted: "fp1_summary",
    memory_writes: [
      {
        kind: "memory",
        name: "MEMORY.md",
        path: "~/.claude/projects/~-proj/memory/MEMORY.md",
        title: "Reply in Chinese",
        entry: "reply-in-chinese.md",
        hook: "回复一律用中文",
      },
      { kind: "hooks", name: "hooks.json" },
    ],
    memory_reads: [
      {
        name: "reply-in-chinese.md",
        path: "~/.claude/projects/~-proj/memory/reply-in-chinese.md",
      },
    ],
    skills_used: ["humanizer"],
    failed_repeats: { tool: "Bash", count: 3, command: true },
  };

  it("reads what a call carried and leaves out kinds it does not know", () => {
    const record = parseRequestRecord({
      ...fullRecord,
      context: callContext,
      project: "~/proj",
    });
    expect(record.project).toBe("~/proj");
    expect(record.context).toStrictEqual({
      parts: callContext.parts,
      items: callContext.items.slice(0, 3),
      system_hash: "fp1_system",
      tools_hash: "fp1_tools",
      summary: true,
      compacted: "fp1_summary",
      memory_writes: callContext.memory_writes.slice(0, 1),
      memory_reads: callContext.memory_reads,
      skills_used: ["humanizer"],
      failed_repeats: { tool: "Bash", count: 3, command: true },
    });
    // A call that sent only what was new has no parts.
    expect(
      parseRequestRecord({ ...fullRecord, context: { items: [] } }).context,
    ).toStrictEqual({ items: [], memory_writes: [] });
    expect(parseRequestRecord(fullRecord).context).toBeUndefined();
    expect(parseRequestRecord(fullRecord).project).toBeUndefined();
  });

  it("reads the failing repeats of a conversation", () => {
    expect(
      parseRequestSession({
        ...fullSession,
        failed_repeats: { tool: "shell_command", count: 4 },
      }).failed_repeats,
    ).toStrictEqual({ tool: "shell_command", count: 4 });
    expect(parseRequestSession(fullSession).failed_repeats).toBeUndefined();
  });

  it("rejects a malformed context", () => {
    expect(() =>
      parseRequestRecord({
        ...fullRecord,
        context: {
          ...callContext,
          items: [{ kind: "rules", scope: "team", tokens: 1 }],
        },
      }),
    ).toThrow(/items\[0\]\.scope/);
    expect(() =>
      parseRequestRecord({
        ...fullRecord,
        context: {
          ...callContext,
          parts: { ...callContext.parts, history: -1 },
        },
      }),
    ).toThrow(/parts\.history/);
    expect(() =>
      parseRequestRecord({
        ...fullRecord,
        context: { ...callContext, memory_writes: [{ kind: "memory" }] },
      }),
    ).toThrow(/memory_writes\[0\]\.name/);
    expect(() =>
      parseRequestSession({ ...fullSession, failed_repeats: { tool: "Bash" } }),
    ).toThrow(/failed_repeats\.count/);
  });
});

describe("project memory", () => {
  const entry = {
    file: "no-jitter.md",
    path: "~/.claude/projects/p/memory/no-jitter.md",
    title: "No jitter-sensitive tests",
    hook: "wait on done channels",
    noted_at: "2026-10-31T07:05:00Z",
    reads: [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 2],
    total_reads: 3,
  };
  const memory = {
    projects: [
      {
        project: "~/GolandProjects/astrlink",
        client_types: ["claude_code", "future_client"],
        last_seen_at: "2026-10-31T07:05:00Z",
      },
      { client_types: ["codex"], last_seen_at: "2026-10-30T07:05:00Z" },
    ],
    project: "~/GolandProjects/astrlink",
    client_type: "claude_code",
    day_start: "2026-10-31T16:00:00Z",
    recent: 1,
    entries: [
      entry,
      { file: "old.md", reads: Array(14).fill(0), total_reads: 0 },
    ],
    stories: [
      {
        file: "no-jitter.md",
        title: "No jitter-sensitive tests",
        at: "2026-10-31T07:05:00Z",
        request_id: "req_new",
        session_id: "session_2",
        prompt: "把 failover 的单测补完",
      },
    ],
    rules: [
      {
        name: "AGENTS.md",
        path: "~/GolandProjects/astrlink/AGENTS.md",
        scope: "project",
        first_seen_at: "2026-10-21T02:00:00Z",
        changed_at: "2026-10-31T06:58:00Z",
        request_id: "req_rules",
        before_request_id: "req_old",
        captured: true,
      },
    ],
    toolbox: {
      skills: [
        {
          name: "humanizer",
          description: "Rewrite text in plain words",
          uses: 4,
          carried: true,
        },
        { name: "deploy", uses: 0, carried: true },
      ],
      mcp: [
        {
          server: "paseo",
          tools: [{ name: "create_agent", uses: 12, carried: true }],
        },
      ],
      idle_tokens: 13_000,
    },
    has_memory: true,
    has_rules: true,
  };

  it("parses what one client's agents remembered in a project", () => {
    const parsed = parseProjectMemory(memory);
    expect(parsed).toStrictEqual({
      ...memory,
      projects: [
        { ...memory.projects[0], client_types: ["claude_code", "unknown"] },
        memory.projects[1],
      ],
    });
    // A project no call named has only the list.
    const bare = {
      projects: [],
      day_start: "2026-10-31T16:00:00Z",
      recent: 0,
      entries: [],
      stories: [],
      rules: [],
      toolbox: { skills: [], mcp: [], idle_tokens: 0 },
      has_memory: false,
      has_rules: false,
    };
    expect(parseProjectMemory(bare)).toStrictEqual(bare);
  });

  it("rejects a malformed project memory", () => {
    const { stories: _stories, ...withoutStories } = memory;
    expect(() => parseProjectMemory(withoutStories)).toThrow(/stories/);
    expect(() =>
      parseProjectMemory({
        ...memory,
        entries: [{ ...entry, reads: [1, -1] }],
      }),
    ).toThrow(/entries\[0\]\.reads\[1\]/);
    expect(() =>
      parseProjectMemory({
        ...memory,
        rules: [{ name: "AGENTS.md", scope: "team" }],
      }),
    ).toThrow(/rules\[0\]\.scope/);
    expect(() =>
      parseProjectMemory({
        ...memory,
        toolbox: { ...memory.toolbox, idle_tokens: "many" },
      }),
    ).toThrow(/toolbox\.idle_tokens/);
    expect(() =>
      parseProjectMemory({
        ...memory,
        toolbox: { ...memory.toolbox, mcp: [{ server: "paseo", tools: {} }] },
      }),
    ).toThrow(/toolbox\.mcp\[0\]\.tools/);
  });

  it("parses one memory and what happened to it", () => {
    const detail = {
      entry,
      events: [
        {
          kind: "noted",
          at: "2026-10-31T07:05:00Z",
          request_id: "req_new",
          session_id: "session_2",
          prompt: "把 failover 的单测补完",
          captured: true,
        },
        { kind: "read", at: "2026-11-01T02:00:00Z", request_id: "req_read" },
        // A kind a later Core may add.
        { kind: "moved", at: "2026-11-01T03:00:00Z", request_id: "req_x" },
      ],
    };
    expect(parseMemoryEntryDetail(detail)).toStrictEqual({
      ...detail,
      events: detail.events.slice(0, 2),
    });
    expect(() =>
      parseMemoryEntryDetail({ entry, events: [{ kind: "read" }] }),
    ).toThrow(/events\[0\]\.at/);
    expect(() => parseMemoryEntryDetail({ events: [] })).toThrow(/entry/);
  });

  it("parses the texts a captured call carried", () => {
    const texts = {
      request_id: "req_new",
      truncated: true,
      items: [
        {
          kind: "rules",
          name: "AGENTS.md",
          path: "~/proj/AGENTS.md",
          text: "Run gofmt.",
        },
        { kind: "plugins", name: "x", text: "y" },
      ],
      summary: "The user asked to fix the retry order.",
      writes: [
        {
          kind: "memory",
          name: "no-jitter.md",
          content: "Wait on done channels.",
        },
      ],
      reads: [
        {
          name: "no-jitter.md",
          path: "~/.claude/projects/p/memory/no-jitter.md",
          content: "Tests must not depend on deadlines.",
        },
      ],
    };
    expect(parseRequestContextTexts(texts)).toStrictEqual({
      ...texts,
      items: [texts.items[0]],
    });
    expect(
      parseRequestContextTexts({
        request_id: "req_locked",
        withheld: true,
        reason: "raw_locked",
        items: [],
        writes: [],
        reads: [],
      }),
    ).toStrictEqual({
      request_id: "req_locked",
      withheld: true,
      reason: "raw_locked",
      items: [],
      writes: [],
      reads: [],
    });
    expect(() =>
      parseRequestContextTexts({ ...texts, reason: "lost" }),
    ).toThrow(/reason/);
    expect(() =>
      parseRequestContextTexts({ request_id: "req", items: [] }),
    ).toThrow(/writes/);
    expect(() =>
      parseRequestContextTexts({
        ...texts,
        writes: [{ kind: "memory", name: "a.md" }],
      }),
    ).toThrow(/writes\[0\]\.content/);
    expect(() =>
      parseRequestContextTexts({ ...texts, reads: [{ name: "a.md" }] }),
    ).toThrow(/reads\[0\]\.content/);
  });
});
