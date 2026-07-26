import { describe, expect, it } from "vitest";

import {
  parseAuditContent,
  parsePurgeResult,
  parseRequestRecord,
  parseRequestRecordPage,
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
  endpoint_id: "endpoint_01",
  local_access_token_id: "token_01",
  plan: { kind: "native" },
  http_status: 200,
  latency_ms: 120,
  usage: {
    input_tokens: 10,
    output_tokens: 20,
    total_tokens: 30,
    cached_input_tokens: 2,
  },
  error: null,
  audit: {
    request_body_captured: true,
    response_content_captured: false,
    request_body_truncated: false,
    response_content_truncated: false,
  },
  extensions: { note: "ignored" },
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
  endpoint_id: null,
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
};

describe("request-record IPC contract", () => {
  it("round-trips a valid record and drops plan/extensions", () => {
    const parsed = parseRequestRecord(fullRecord);
    expect(parsed).toEqual({
      id: fullRecord.id,
      started_at: fullRecord.started_at,
      completed_at: fullRecord.completed_at,
      status: "succeeded",
      input_protocol: fullRecord.input_protocol,
      requested_model: fullRecord.requested_model,
      streaming: true,
      route_id: fullRecord.route_id,
      endpoint_id: fullRecord.endpoint_id,
      local_access_token_id: fullRecord.local_access_token_id,
      http_status: 200,
      latency_ms: 120,
      usage: {
        input_tokens: 10,
        output_tokens: 20,
        total_tokens: 30,
        cached_input_tokens: 2,
      },
      error: null,
      audit: fullRecord.audit,
    });
    expect(parseRequestRecord(nullOptionalRecord)).toEqual(nullOptionalRecord);
  });

  it("parses a page with a cursor", () => {
    expect(
      parseRequestRecordPage({
        items: [nullOptionalRecord],
        next_cursor: "cursor-1",
      }),
    ).toEqual({
      items: [nullOptionalRecord],
      next_cursor: "cursor-1",
    });
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
      request_body: null,
      response_content: {
        media_type: "text/plain",
        content: "hello",
        truncated: true,
        captured_bytes: 5,
      },
    });
    expect(
      parsePurgeResult({ deleted_records: 3, deleted_audit_blobs: 1 }),
    ).toEqual({ deleted_records: 3, deleted_audit_blobs: 1 });
  });

  it("maps status labels and tones", () => {
    expect(statusLabel("pending")).toBe("进行中");
    expect(statusLabel("succeeded")).toBe("成功");
    expect(statusLabel("failed")).toBe("失败");
    expect(statusLabel("cancelled")).toBe("已取消");
    expect(statusLabel("blocked")).toBe("已拦截");
    expect(statusTone("succeeded")).toBe("positive");
    expect(statusTone("failed")).toBe("negative");
    expect(statusTone("pending")).toBe("pending");
    expect(statusTone("cancelled")).toBe("pending");
    expect(statusTone("blocked")).toBe("pending");
  });
});
