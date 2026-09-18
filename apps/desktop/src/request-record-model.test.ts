import { describe, expect, it } from "vitest";

import {
  parseAuditContent,
  parsePurgeResult,
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
  it("accepts explicit values and older records without the field", () => {
    expect(parseRequestRecord({ ...fullRecord, reasoning_effort: "high" }).reasoning_effort).toBe("high");
    expect(parseRequestSession({ ...fullSession, reasoning_effort: "xhigh" }).reasoning_effort).toBe("xhigh");
    expect(parseRequestRecord(fullRecord).reasoning_effort).toBeNull();
    expect(parseRequestSession(fullSession).reasoning_effort).toBeNull();
    expect(() => parseRequestRecord({ ...fullRecord, reasoning_effort: 42 })).toThrow();
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
        { kind: "fingerprint", direction: "out", value: "fp1_0123456789abcdef0123456789abcdef" },
      ],
    });
    expect(parsed.turn_index).toBe(2);
    expect(parsed.session_link).toEqual({ kind: "echo_id", value: "call_8f3kd92ls0a1Qz7" });
    expect(parsed.cursors).toHaveLength(2);
    expect(() =>
      parseRequestRecord({ ...fullRecord, turn_index: 0 }),
    ).toThrow(/turn_index/);
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
    expect(() =>
      parseRequestRecord({ ...fullRecord, events: "none" }),
    ).toThrow(/events/);
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
    expect(() =>
      parseRequestRecord({ ...fullRecord, status: "ok" }),
    ).toThrow("状态枚举无效");
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
    });
    expect(
      parsePurgeResult({ deleted_records: 3, deleted_audit_blobs: 1 }),
    ).toEqual({ deleted_records: 3, deleted_audit_blobs: 1 });
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
    expect(statusLabel("succeeded")).toBe("成功");
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
    ).toThrow("状态枚举无效");
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
