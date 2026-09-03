import { describe, expect, it } from "vitest";

import { emptyTrajectoryFields, type RequestRecord } from "./request-record-model";
import {
  eventTone,
  extractPrivacyHits,
  inspectorPart,
  inspectorTitle,
  recordedPrivacyHits,
  splitPrivacyHighlights,
  synthesizeEvents,
  trajectoryLanes,
  trajectoryRows,
} from "./request-trajectory-model";

const record: RequestRecord = {
  id: "req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  parent_request_id: null,
  attempt_index: 1,
  child_count: 0,
  started_at: "2026-08-16T10:00:00Z",
  completed_at: "2026-08-16T10:00:02Z",
  status: "succeeded",
  input_protocol: "openai.responses",
  requested_model: "gpt-4.1",
  streaming: true,
  route_id: "route_01",
  service_id: "service_01",
  local_access_token_id: null,
  http_status: 200,
  latency_ms: 2000,
  usage: { input_tokens: 1, output_tokens: 2, total_tokens: 3 },
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
  privacy_restore: {
    enabled: true,
    mapping_count: 2,
    restored_count: 2,
    visible_restored_count: 2,
    tool_argument_restored_count: 0,
    fallback_count: 0,
  },
  ...emptyTrajectoryFields,
};

describe("request trajectory model", () => {
  it("synthesizes a coarse trajectory for legacy records", () => {
    const events = synthesizeEvents(record);
    expect(events.map((event) => event.kind)).toEqual([
      "accepted",
      "privacy",
      "routed",
      "upstream",
      "restore",
      "completed",
    ]);
  });

  it("keeps persisted events when present", () => {
    const persisted = {
      ...record,
      events: [
        {
          kind: "accepted" as const,
          started_at: record.started_at,
          ended_at: record.completed_at,
          status: "succeeded" as const,
          summary: "kept",
          attempt_index: 1,
        },
      ],
    };
    expect(synthesizeEvents(persisted)).toHaveLength(1);
  });

  it("marks child upstream rows as RETRY", () => {
    const child: RequestRecord = {
      ...record,
      id: "req_childaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      parent_request_id: record.id,
      child_count: 0,
    };
    const rows = trajectoryRows([record], { [record.id]: [child] });
    expect(rows.some((row) => row.chip === "RETRY")).toBe(true);
    const lanes = trajectoryLanes(rows, Date.parse(record.completed_at ?? ""));
    expect(lanes.segments.some((segment) => segment.lane === "upstream")).toBe(
      true,
    );
  });

  it("treats HTTP 403 as a failed tone even when the record succeeded", () => {
    const forbidden: RequestRecord = {
      ...record,
      status: "succeeded",
      http_status: 403,
      events: [
        {
          kind: "upstream",
          started_at: record.started_at,
          ended_at: record.completed_at,
          status: "succeeded",
          summary: "HTTP 403",
          attempt_index: 1,
        },
        {
          kind: "completed",
          started_at: record.completed_at ?? record.started_at,
          ended_at: record.completed_at,
          status: "succeeded",
          summary: "HTTP 403",
          attempt_index: 1,
        },
      ],
    };
    expect(
      eventTone(forbidden, forbidden.events[0]!),
    ).toBe("failed");
    const rows = trajectoryRows([forbidden], {});
    expect(rows.map((row) => [row.chip, row.tone])).toEqual([
      ["UPSTREAM", "failed"],
      ["RESULT", "failed"],
    ]);
    expect(
      trajectoryLanes(rows, Date.parse(forbidden.completed_at ?? "")).segments.every(
        (segment) => segment.tone === "failed",
      ),
    ).toBe(true);
  });

  it("groups an agent loop under one TURN header and starts a new one per user turn", () => {
    const accepted = (id: string, summary: string) => ({
      kind: "accepted" as const,
      started_at: record.started_at,
      ended_at: record.completed_at,
      status: "succeeded" as const,
      summary,
      attempt_index: 1,
    });
    const step1: RequestRecord = {
      ...record,
      id: "req_loop_1",
      turn_index: 1,
      input_preview: "帮我看看仓库里有哪些文件",
      events: [accepted("req_loop_1", "gpt-4.1 · openai.chat")],
    };
    const step2: RequestRecord = {
      ...step1,
      id: "req_loop_2",
      started_at: "2026-08-16T10:00:03Z",
      completed_at: "2026-08-16T10:00:04Z",
      session_link: { kind: "echo_id", value: "call_8f3kd92ls0a1Qz7" },
      events: [accepted("req_loop_2", "gpt-4.1 · openai.chat")],
    };
    const followUp: RequestRecord = {
      ...step1,
      id: "req_loop_3",
      started_at: "2026-08-16T10:00:10Z",
      completed_at: null,
      status: "pending",
      turn_index: 2,
      input_preview: "第二个文件是做什么的",
      session_link: { kind: "fingerprint", value: "fp1_0123456789abcdef0123456789abcdef" },
      events: [accepted("req_loop_3", "gpt-4.1 · openai.chat")],
    };
    const legacy: RequestRecord = {
      ...step1,
      id: "req_loop_legacy",
      started_at: "2026-08-16T10:00:20Z",
      turn_index: null,
      input_preview: null,
      events: [accepted("req_loop_legacy", "gpt-4.1 · openai.chat")],
    };

    const rows = trajectoryRows([step1, step2, followUp, legacy], {});
    expect(rows.map((row) => [row.chip, row.summary])).toEqual([
      ["TURN", "第 1 轮 · 帮我看看仓库里有哪些文件"],
      ["CLIENT", "gpt-4.1 · openai.chat"],
      ["CLIENT", "gpt-4.1 · openai.chat · 回显 ID 接续"],
      ["TURN", "第 2 轮 · 第二个文件是做什么的"],
      ["CLIENT", "gpt-4.1 · openai.chat · 回复指纹接续"],
      ["TURN", "未标注轮次"],
      ["CLIENT", "gpt-4.1 · openai.chat"],
    ]);
    const headers = rows.filter((row) => row.chip === "TURN");
    expect(headers.map((row) => row.result)).toEqual([
      "2 次调用",
      "1 次调用",
      "1 次调用",
    ]);
    expect(headers[0]).toMatchObject({
      requestId: "req_loop_1",
      status: "succeeded",
      tone: "ok",
      startedAt: step1.started_at,
      endedAt: step2.completed_at,
      lane: "client",
    });
    expect(headers[1]).toMatchObject({ status: "pending", tone: "pending", endedAt: null });

    // Headers span whole turns, so they stay off the lane bars.
    const lanes = trajectoryLanes(rows, Date.parse("2026-08-16T10:00:30Z"));
    expect(lanes.segments).toHaveLength(rows.length - headers.length);

    // A single call has nothing to group: no header, unchanged trajectory.
    expect(trajectoryRows([step2], {}).map((row) => row.chip)).toEqual(["CLIENT"]);
  });

  it("maps trajectory chips to inspector audit parts", () => {
    expect(inspectorPart("TURN")).toBe("request_body");
    expect(inspectorPart("CLIENT")).toBe("request_body");
    expect(inspectorPart("POLICY")).toBe("upstream_request_body");
    expect(inspectorPart("ROUTE")).toBe("route");
    expect(inspectorPart("UPSTREAM")).toBe("upstream_response_content");
    expect(inspectorPart("RETRY")).toBe("upstream_response_content");
    expect(inspectorPart("RESTORE")).toBe("response_content");
    expect(inspectorPart("RESULT")).toBe("response_content");
    expect(inspectorTitle("POLICY")).toBe("命中");
    expect(inspectorTitle("RETRY")).toBe("上游响应");
  });

  it("extracts privacy hit kinds from placeholders without originals", () => {
    const hits = extractPrivacyHits(
      `alice@example.com <PRIVATE_EMAIL_aaaaaaaaaaaaaaaa> phone +14155550001 <PRIVATE_PHONE_bbbbbbbbbbbbbbbb> again <PRIVATE_EMAIL_cccccccccccccccc>`,
    );
    expect(hits.map((hit) => [hit.kind, hit.label, hit.count])).toEqual([
      ["email", "邮箱", 2],
      ["phone", "电话", 1],
    ]);
    expect(hits.flatMap((hit) => hit.placeholders).join(" ")).not.toContain(
      "alice@",
    );
    expect(hits.flatMap((hit) => hit.placeholders).join(" ")).not.toContain(
      "+1415",
    );
  });

  // A natural stand-in is indistinguishable from a real value by eye, which is
  // the point upstream but leaves the operator with nothing to audit. The
  // reserved namespaces are recognizable, so the panel can name them.
  it("recognizes natural stand-ins alongside token placeholders", () => {
    const hits = extractPrivacyHits(
      "mail redacted-a1b2c3d4e5f6@private.invalid " +
        "link https://private.invalid/r/0f1e2d3c4b5a " +
        "call +1-555-555-0142 card 4000 0000 0000 0173 " +
        "iban XX00REDACTED0000000042 host 203.0.113.7 v6 2001:db8::a1b2:c3d4:e5f6 " +
        "and a genuine alice@example.com",
    );
    expect(hits.map((hit) => [hit.kind, hit.count])).toEqual([
      ["email", 1],
      ["phone", 1],
      ["account", 1],
      ["payment_card", 1],
      ["ip_address", 2],
      ["url", 1],
    ]);
    expect(hits.flatMap((hit) => hit.placeholders)).not.toContain(
      "alice@example.com",
    );
  });

  it("highlights natural stand-ins without splitting the surrounding text", () => {
    expect(
      splitPrivacyHighlights("mail redacted-a1b2c3d4e5f6@private.invalid now"),
    ).toEqual([
      { text: "mail " },
      { text: "redacted-a1b2c3d4e5f6@private.invalid", kind: "email" },
      { text: " now" },
    ]);
  });

  it("reads recorded privacy hits instead of scanning current text", () => {
    expect(
      recordedPrivacyHits({
        enabled: true,
        mapping_count: 3,
        restored_count: 3,
        visible_restored_count: 3,
        tool_argument_restored_count: 0,
        fallback_count: 0,
        hits: [
          { kind: "email", count: 2 },
          { kind: "url", count: 1 },
        ],
      }).map((hit) => [hit.kind, hit.label, hit.count]),
    ).toEqual([
      ["email", "邮箱", 2],
      ["url", "URL", 1],
    ]);
    expect(recordedPrivacyHits(record.privacy_restore)).toEqual([]);
  });

  it("splits highlight spans so originals stay plain text", () => {
    const spans = splitPrivacyHighlights(
      `alice@example.com <PRIVATE_EMAIL_aaaaaaaaaaaaaaaa>`,
    );
    expect(spans).toEqual([
      { text: "alice@example.com " },
      { text: "<PRIVATE_EMAIL_aaaaaaaaaaaaaaaa>", kind: "email" },
    ]);
  });
});
