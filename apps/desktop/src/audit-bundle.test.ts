import { describe, expect, it } from "vitest";

import {
  buildHeadersText,
  buildRecordBundle,
  bundleFilename,
  fence,
} from "./audit-bundle";
import type { ExportEnvironment } from "./export-environment";
import {
  emptyTrajectoryFields,
  type AuditContent,
  type RequestRecord,
  type RequestSession,
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
    expect(bundle).toContain("- 状态: 完成 · HTTP 200");
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

  it("states a model redirect right after the protocol line", () => {
    const bundle = buildRecordBundle(
      {
        ...record,
        model_redirect: { from: "gpt-4.1", to: "claude-sonnet-4-5" },
      },
      content,
      { format: "txt" },
    );
    const protocol = bundle.indexOf(
      "协议: openai.responses · 模型: gpt-4.1 · 流式: 是",
    );
    const redirect = bundle.indexOf("模型重定向: gpt-4.1 → claude-sonnet-4-5");
    expect(protocol).toBeGreaterThanOrEqual(0);
    expect(redirect).toBeGreaterThan(protocol);
    expect(bundle.slice(protocol, redirect).split("\n")).toHaveLength(2);
    // The legacy record has no events, so the trajectory synthesizes the step.
    expect(bundle).toMatch(/重定向 · 完成 · .* · gpt-4\.1 → claude-sonnet-4-5/);
  });

  it("leaves the redirect line out when the model was not redirected", () => {
    const bundle = buildRecordBundle(record, content, { format: "txt" });
    expect(bundle).not.toContain("模型重定向");
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
    expect(bundle).toContain("状态: 完成 · HTTP 200");
    expect(bundle).toContain("POST /v1/responses?stream=true HTTP/1.1");
    expect(bundle).toContain('{"model":"gpt-4.1"}');
    expect(bundle).toContain("已截断");
  });
});

describe("buildRecordBundle diagnosis context", () => {
  // A call stuck in local-model privacy inspection, after an earlier turn of
  // the same session timed out in the detector.
  const failedTurn: RequestRecord = {
    ...record,
    id: "req_detector_timeout",
    attempt_index: 0,
    started_at: "2026-09-20T09:55:00Z",
    completed_at: "2026-09-20T09:57:00Z",
    status: "failed",
    service_id: null,
    route_id: null,
    http_status: 503,
    latency_ms: 120_000,
    usage: null,
    privacy_restore: null,
    error: {
      category: "privacy",
      code: "safety_engine_unavailable",
      message: "local privacy detector timed out",
      retryable: true,
    },
    turn_index: 2,
  };
  const liveTurn: RequestRecord = {
    ...record,
    id: "req_live_inspection",
    attempt_index: 0,
    started_at: "2026-09-20T10:00:00Z",
    completed_at: null,
    status: "pending",
    service_id: null,
    route_id: null,
    http_status: null,
    latency_ms: null,
    usage: null,
    privacy_restore: null,
    turn_index: 3,
    events: [
      {
        kind: "accepted",
        started_at: "2026-09-20T10:00:00Z",
        ended_at: "2026-09-20T10:00:00.200Z",
        status: "succeeded",
        summary: "gpt-5.5 · openai.responses",
        attempt_index: 0,
      },
      {
        kind: "privacy",
        started_at: "2026-09-20T10:00:00.200Z",
        ended_at: null,
        status: "pending",
        summary: "local_model · inspecting · 96.6 KiB",
        attempt_index: 0,
      },
    ],
  };
  const session: RequestSession = {
    id: "session_live",
    title: "Fix the build",
    started_at: failedTurn.started_at,
    last_started_at: liveTurn.started_at,
    completed_at: null,
    duration_ms: 520_000,
    active_request_starts: [liveTurn.started_at],
    turn_count: 2,
    call_count: 2,
    status: "pending",
    requested_model: "gpt-5.5",
    input_protocol: "openai.responses",
    service_id: null,
    local_access_token_id: "token_01",
  };
  const environment: ExportEnvironment = {
    version: { app: "0.9.0", core: "0.9.0", build_commit: "abc1234" },
    privacy: {
      enabled: true,
      detector: "local_model",
      local_model_id: "model_01",
      local_model_name: "Privacy Filter · Q4",
      request_action: "redact",
      response_restore: true,
      restore_tool_arguments: true,
      skip_tool_declarations: false,
      inspect_additional_tools: false,
    },
    limits: {
      response_start_timeout_seconds: 120,
      max_concurrent_inspections: 1,
      max_request_body_mib: 32,
    },
    routing: {
      strategy: "priority",
      max_attempts: 3,
      model_redirects: [
        { from: "gpt-5.5", to: "claude-opus-4-1", enabled: true },
        { from: "o3", to: "gpt-5.5-pro", enabled: false },
      ],
    },
    capture: {
      request_body_enabled: true,
      response_content_enabled: false,
      http_meta_enabled: true,
    },
  };
  const exportedAt = new Date("2026-09-20T10:06:40.200Z");
  const liveContent: AuditContent = {
    ...content,
    request_id: liveTurn.id,
    upstream_http_meta: null,
  };

  const bundleFor = (format: "markdown" | "txt") =>
    buildRecordBundle(liveTurn, liveContent, {
      format,
      exportedAt,
      session,
      turns: [failedTurn, liveTurn],
      childrenByRoot: {},
      serviceNames: {},
      environment,
    });

  it("says where a pending call is stuck and for how long", () => {
    const bundle = bundleFor("txt");

    expect(bundle).toContain(
      "导出时间: 2026-09-20T10:06:40.200Z · 已进行 6m 40s",
    );
    expect(bundle).toContain("版本: 应用 0.9.0 · 核心 0.9.0 · 提交 abc1234");
    expect(bundle).toContain("API 提供商: 正在选择 API 提供商");
    expect(bundle).not.toContain("未路由");
    expect(bundle).toContain(
      "当前阶段: 隐私检测 · 已等待 6m 40s · local_model · inspecting · 96.6 KiB",
    );
    expect(bundle).toContain(
      "+0 ms · 客户端 · 完成 · 200 ms · gpt-5.5 · openai.responses",
    );
    expect(bundle).toContain(
      "+200 ms · 策略 · 进行中 · 已 6m 40s（未结束） · local_model · inspecting · 96.6 KiB",
    );
    // The gateway never sent it upstream; the metadata was not lost.
    expect(bundle).toContain("上游 HTTP\n（未发出上游请求）");
  });

  it("lists the session calls and the gateway settings", () => {
    const bundle = bundleFor("txt");

    expect(bundle).toContain("同会话请求（第 1–2 条，共 2 条）");
    expect(bundle).toContain(
      "  第 2 轮 · 2026-09-20T09:55:00Z · 失败 · 尝试 0 · 重试 0 · 无可用提供商 · 2m 00s · privacy · safety_engine_unavailable · req_detector_timeout",
    );
    expect(bundle).toContain(
      "▶ 第 3 轮 · 2026-09-20T10:00:00Z · 进行中 · 尝试 0 · 重试 0 · 正在选择 API 提供商 · 6m 40s · req_live_inspection",
    );
    expect(bundle).toContain(
      "隐私保护: 已开启 · 检测方式: local_model · 本地模型: Privacy Filter · Q4 · 请求动作: redact · 响应还原: 已开启",
    );
    expect(bundle).toContain(
      "跳过函数调用检查: 已关闭 · 跳过 additional_tools 检查: 已开启",
    );
    expect(bundle).toContain(
      "响应开始超时: 120 秒 · 并发检测数: 1 · 请求体上限: 32 MiB",
    );
    expect(bundle).toContain("路由策略: priority · 最大尝试次数: 3");
    // Only enabled rules shape routing, so only they are named.
    expect(bundle).toContain(
      "模型重定向规则: 已启用 1 · gpt-5.5 → claude-opus-4-1",
    );
    expect(bundle).not.toContain("o3 → gpt-5.5-pro");
    expect(bundle.indexOf("模型重定向规则")).toBeGreaterThan(
      bundle.indexOf("路由策略"),
    );
    expect(bundle).toContain(
      "内容捕获: 请求体 已开启 · 响应内容 已关闭 · HTTP 元数据 已开启",
    );
  });

  it("reports each tool declaration switch on its own", () => {
    const bundle = buildRecordBundle(liveTurn, liveContent, {
      format: "txt",
      exportedAt,
      environment: {
        ...environment,
        privacy: {
          ...environment.privacy!,
          skip_tool_declarations: true,
          inspect_additional_tools: true,
        },
      },
    });
    expect(bundle).toContain(
      "跳过函数调用检查: 已开启 · 跳过 additional_tools 检查: 已关闭",
    );
  });

  it("ends with a machine-readable diagnostic, raw in txt", () => {
    const bundle = bundleFor("txt");
    expect(bundle).not.toContain("# AstrLink");
    expect(bundle).not.toContain("## ");
    expect(bundle).not.toContain("```");

    const heading = "机器可读诊断（JSON）\n";
    const payload = JSON.parse(
      bundle.slice(bundle.indexOf(heading) + heading.length),
    );
    expect(payload.selected_request_id).toBe("req_live_inspection");
    expect(payload.exported_at).toBe("2026-09-20T10:06:40.200Z");
    expect(payload.environment.privacy.detector).toBe("local_model");
    expect(payload.environment.privacy.inspect_additional_tools).toBe(false);
    expect(payload.environment.routing.model_redirects).toStrictEqual([
      { from: "gpt-5.5", to: "claude-opus-4-1", enabled: true },
      { from: "o3", to: "gpt-5.5-pro", enabled: false },
    ]);
    expect(
      payload.records.map((item: { id: string }) => item.id),
    ).toStrictEqual(["req_detector_timeout", "req_live_inspection"]);
    expect(payload.records[1].events[1]).toMatchObject({
      kind: "privacy",
      status: "pending",
      ended_at: null,
    });
  });

  it("fences the diagnostic in markdown", () => {
    const bundle = bundleFor("markdown");
    expect(bundle).toContain("## 机器可读诊断（JSON）\n```json\n{");
    expect(bundle).toContain("## 执行轨迹");
    expect(bundle).toContain("- ▶ 第 3 轮");
  });

  it("says when no redirect rule is enabled", () => {
    const bundle = buildRecordBundle(liveTurn, liveContent, {
      format: "txt",
      exportedAt,
      environment: {
        ...environment,
        routing: {
          strategy: "priority",
          max_attempts: 3,
          model_redirects: [{ from: "o3", to: "gpt-5.5-pro", enabled: false }],
        },
      },
    });
    expect(bundle).toContain("模型重定向规则: 已启用 0 · （无）");
  });

  it("names the first 20 enabled redirect rules and counts the rest", () => {
    const rules = Array.from({ length: 23 }, (_, index) => ({
      from: `client-${index + 1}`,
      to: `target-${index + 1}`,
      enabled: true,
    }));
    const bundle = buildRecordBundle(liveTurn, liveContent, {
      format: "txt",
      exportedAt,
      environment: {
        ...environment,
        routing: {
          strategy: "priority",
          max_attempts: 3,
          model_redirects: rules,
        },
      },
    });
    const listed = rules
      .slice(0, 20)
      .map((rule) => `${rule.from} → ${rule.to}`)
      .join("、");
    expect(bundle).toContain(`模型重定向规则: 已启用 23 · ${listed}、另 3 条`);
    expect(bundle).not.toContain("client-21 → target-21");
  });

  it("marks settings it could not read instead of dropping them", () => {
    const bundle = buildRecordBundle(liveTurn, liveContent, {
      format: "txt",
      exportedAt,
      environment: {
        version: null,
        privacy: null,
        limits: null,
        routing: null,
        capture: null,
      },
    });
    expect(bundle).toContain("隐私保护: （未能读取）");
    expect(bundle).toContain(
      "跳过函数调用检查: （未能读取） · 跳过 additional_tools 检查: （未能读取）",
    );
    expect(bundle).toContain(
      "模型重定向规则: 已启用 （未能读取） · （未能读取）",
    );
    expect(bundle).not.toContain("版本:");
    // Without a session there is nothing to anchor the diagnostic to.
    expect(bundle).not.toContain("机器可读诊断");
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
