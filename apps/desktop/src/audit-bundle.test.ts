import { describe, expect, it } from "vitest";

import {
  buildHeadersText,
  buildRecordBundle,
  bundleFilename,
  fence,
} from "./audit-bundle";
import {
  emptyTrajectoryFields,
  type AuditContent,
  type RequestRecord,
} from "./request-record-model";

const record: RequestRecord = {
  id: "req_bundle_test",
  parent_request_id: null,
  attempt_index: 1,
  child_count: 0,
  started_at: "2026-07-25T10:00:00Z",
  completed_at: "2026-07-25T10:00:01Z",
  status: "succeeded",
  input_protocol: "openai.responses",
  requested_model: "gpt-4.1",
  streaming: true,
  route_id: "route_primary",
  service_id: "service_01",
  local_access_token_id: "token_01",
  http_status: 200,
  latency_ms: 120,
  usage: {
    input_tokens: 10,
    output_tokens: 20,
    total_tokens: 30,
    cache_read_tokens: 4,
    cache_write_tokens: 1,
  },
  error: null,
  audit: {
    request_body_captured: true,
    response_content_captured: true,
    request_body_truncated: false,
    response_content_truncated: false,
    upstream_request_body_captured: true,
    upstream_response_content_captured: true,
    upstream_request_body_truncated: false,
    upstream_response_content_truncated: false,
  },
  privacy_restore: {
    enabled: true,
    mapping_count: 4,
    restored_count: 5,
    visible_restored_count: 3,
    tool_argument_restored_count: 2,
    fallback_count: 0,
  },
  ...emptyTrajectoryFields,
};

const content: AuditContent = {
  request_id: record.id,
  http_meta: {
    method: "POST",
    url: "/v1/responses?stream=true",
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
      { name: "x-request-id", value: "req_up_1", redacted: false },
    ],
  },
  request_body: {
    media_type: "application/json",
    content: '{"model":"gpt-4.1"}',
    truncated: false,
    captured_bytes: 19,
  },
  response_content: {
    media_type: "text/event-stream",
    content: 'data: {"type":"done"}\n\n',
    truncated: true,
    captured_bytes: 23,
  },
  upstream_http_meta: {
    method: "POST",
    url: "/prefix/v1/responses?stream=true",
    http_version: "HTTP/1.1",
    request_headers: [
      {
        name: "authorization",
        value: "Bearer <redacted:14 chars>",
        redacted: true,
      },
    ],
    response_status: 200,
    response_headers: [],
  },
  upstream_request_body: {
    media_type: "application/json",
    content: '{"model":"upstream-model"}',
    truncated: false,
    captured_bytes: 26,
  },
  upstream_response_content: {
    media_type: "text/event-stream",
    content: 'data: {"type":"raw"}\n\n',
    truncated: false,
    captured_bytes: 22,
  },
};

describe("fence", () => {
  it("survives content containing backtick fences", () => {
    const body = 'text\n```json\n{"a":1}\n```\nmore ````raw````';
    const wrapped = fence(body);
    // The delimiter must be longer than any run inside the content.
    expect(wrapped.startsWith("`````\n")).toBe(true);
    expect(wrapped.endsWith("\n`````")).toBe(true);
    // Round-trip: stripping the outer fence recovers the body intact.
    expect(wrapped.slice(6, -6)).toBe(body);
  });

  it("uses a minimal triple fence for plain content", () => {
    expect(fence("plain", "json")).toBe("```json\nplain\n```");
  });
});

describe("buildRecordBundle", () => {
  it("assembles metadata, http envelope and fenced bodies", () => {
    const bundle = buildRecordBundle(record, content, {
      serviceLabel: "Primary gateway",
    });

    expect(bundle).toContain("# AstrLink 请求记录 req_bundle_test");
    expect(bundle).toContain("- 状态: 成功 · HTTP 200");
    expect(bundle).toContain(
      "- 隐私还原: 已开启 · 映射 4 · 已还原 5 · 安全降级 0",
    );
    expect(bundle).toContain("Primary gateway");
    expect(bundle).toContain(
      "- Token: 输入 10 / 输出 20 / 总计 30（缓存读取 4 / 缓存写入 1）",
    );
    expect(bundle).toContain("## 客户端 HTTP 请求");
    expect(bundle).toContain("POST /v1/responses?stream=true HTTP/1.1");
    expect(bundle).toContain("authorization: Bearer <redacted:51 chars>");
    expect(bundle).toContain("## 客户端 HTTP 响应");
    expect(bundle).toContain("x-request-id: req_up_1");
    expect(bundle).toContain("## 上游 HTTP 请求");
    expect(bundle).toContain("POST /prefix/v1/responses?stream=true HTTP/1.1");
    expect(bundle).toContain("## 客户端请求体");
    expect(bundle).toContain('{"model":"gpt-4.1"}');
    expect(bundle).toContain("## 客户端响应内容");
    expect(bundle).toContain("## 上游请求体");
    expect(bundle).toContain('{"model":"upstream-model"}');
    // Truncation must be flagged inline so an LLM reading the bundle knows
    // the bytes are incomplete.
    expect(bundle).toContain("已截断");
  });

  it("omits bodies when includeBodies is false but keeps the envelope", () => {
    const bundle = buildRecordBundle(record, content, {
      includeBodies: false,
    });
    expect(bundle).toContain("## 客户端 HTTP 请求");
    expect(bundle).not.toContain("## 客户端请求体");
    expect(bundle).not.toContain('{"model":"gpt-4.1"}');
  });

  it("states explicitly when http metadata was never captured", () => {
    const bundle = buildRecordBundle(
      record,
      { ...content, http_meta: null, upstream_http_meta: null },
      {},
    );
    expect(bundle).toContain("（此记录未捕获 HTTP 元数据）");
    expect(bundle).not.toContain("## 客户端 HTTP 请求");
  });

  it("handles a record with no decrypted content at all", () => {
    const bundle = buildRecordBundle(record, null, {});
    expect(bundle).toContain("（此记录未捕获 HTTP 元数据）");
    expect(bundle).toContain("## 客户端请求体\n（未捕获）");
    expect(bundle).toContain("## 客户端响应内容\n（未捕获）");
    expect(bundle).toContain("## 上游请求体\n（未捕获）");
  });

  it("includes the error section for failed records", () => {
    const failed: RequestRecord = {
      ...record,
      status: "failed",
      error: {
        category: "upstream",
        code: "upstream_unavailable",
        message: "gateway unavailable",
        retryable: true,
      },
    };
    const bundle = buildRecordBundle(failed, content, {});
    expect(bundle).toContain("## 错误");
    expect(bundle).toContain("upstream_unavailable");
    expect(bundle).toContain("gateway unavailable");
  });

  it("labels a legacy succeeded HTTP 502 as failed", () => {
    const bundle = buildRecordBundle(
      { ...record, status: "succeeded", http_status: 502, error: null },
      content,
      {},
    );
    expect(bundle).toContain("状态: 失败 · HTTP 502");
  });

  it("renders the same facts as plain text without markdown decoration", () => {
    const bundle = buildRecordBundle(record, content, {
      format: "txt",
      serviceLabel: "Primary gateway",
    });

    expect(bundle).toContain("AstrLink 请求记录 req_bundle_test");
    expect(bundle).not.toContain("# AstrLink");
    expect(bundle).not.toContain("## ");
    expect(bundle).not.toContain("```");
    expect(bundle).toContain("状态: 成功 · HTTP 200");
    expect(bundle).toContain("POST /v1/responses?stream=true HTTP/1.1");
    expect(bundle).toContain('{"model":"gpt-4.1"}');
    expect(bundle).toContain("已截断");
  });
});

describe("bundleFilename", () => {
  it("uses the record id and format extension", () => {
    expect(bundleFilename("req_bundle_test", "markdown")).toBe(
      "astrlink-req_bundle_test.md",
    );
    expect(bundleFilename("req_bundle_test", "txt")).toBe(
      "astrlink-req_bundle_test.txt",
    );
  });

  it("strips path separators from the id", () => {
    expect(bundleFilename("req/../evil name", "txt")).toBe(
      "astrlink-req_.._evil_name.txt",
    );
  });
});

describe("buildHeadersText", () => {
  it("renders name: value lines", () => {
    expect(buildHeadersText(content.http_meta!.request_headers)).toBe(
      "authorization: Bearer <redacted:51 chars>\ncontent-type: application/json",
    );
  });
});
