import { describe, expect, it } from "vitest";

import {
  emptyTrajectoryFields,
  type RequestRecord,
  type RequestSession,
} from "./request-record-model";
import {
  buildSkillDiagnostic,
  buildSkillDiagnosticPayload,
  SKILL_DIAGNOSTIC_KIND,
  SKILL_DIAGNOSTIC_SKILL,
} from "./skill-diagnostic";

const emptyAudit = {
  request_body_captured: false,
  response_content_captured: false,
  request_body_truncated: false,
  response_content_truncated: false,
  upstream_request_body_captured: false,
  upstream_response_content_captured: false,
  upstream_request_body_truncated: false,
  upstream_response_content_truncated: false,
};

const root: RequestRecord = {
  id: "req_skill_root",
  parent_request_id: null,
  attempt_index: 2,
  child_count: 1,
  started_at: "2026-09-16T12:00:00Z",
  completed_at: "2026-09-16T12:00:04Z",
  status: "cancelled",
  input_protocol: "anthropic.messages",
  requested_model: "glm-5.3-flash",
  streaming: true,
  route_id: "route_native",
  service_id: "service_01",
  local_access_token_id: "token_secret_id",
  http_status: 200,
  latency_ms: 104000,
  usage: { input_tokens: 11, output_tokens: 22, total_tokens: 33 },
  error: null,
  audit: { ...emptyAudit, request_body_captured: true },
  privacy_restore: {
    enabled: true,
    mapping_count: 0,
    restored_count: 0,
    visible_restored_count: 0,
    tool_argument_restored_count: 0,
    fallback_count: 0,
  },
  ...emptyTrajectoryFields,
  session_id: "sess_skill",
  turn_index: 1,
  input_preview: "给这个仓库适配一下我们现在的 deepseek harness",
  events: [
    {
      kind: "accepted",
      started_at: "2026-09-16T12:00:00Z",
      ended_at: "2026-09-16T12:00:00Z",
      status: "pending",
      summary: "accepted",
      attempt_index: 1,
    },
    {
      kind: "privacy",
      started_at: "2026-09-16T12:00:00Z",
      ended_at: "2026-09-16T12:00:00Z",
      status: "succeeded",
      summary: "allow",
      attempt_index: 1,
    },
    {
      kind: "routed",
      started_at: "2026-09-16T12:00:00Z",
      ended_at: "2026-09-16T12:00:00Z",
      status: "succeeded",
      summary: "native · service_01",
      attempt_index: 1,
    },
    {
      kind: "upstream",
      started_at: "2026-09-16T12:00:00Z",
      ended_at: "2026-09-16T12:00:02Z",
      status: "succeeded",
      summary: "HTTP 200",
      attempt_index: 1,
    },
    {
      kind: "completed",
      started_at: "2026-09-16T12:00:04Z",
      ended_at: "2026-09-16T12:00:04Z",
      status: "cancelled",
      summary: "cancelled",
      attempt_index: 2,
    },
  ],
};

const child: RequestRecord = {
  ...root,
  id: "req_skill_child",
  parent_request_id: root.id,
  attempt_index: 1,
  child_count: 0,
  status: "failed",
  http_status: 502,
  error: {
    category: "upstream",
    code: "upstream_unavailable",
    message: "connect: connection refused 10.0.0.8:443",
    retryable: true,
  },
  recovery: {
    delay_ms: 200,
    action: "retry",
    reason: "upstream_unavailable",
    path_name: "default",
  },
  events: [
    {
      kind: "upstream",
      started_at: "2026-09-16T12:00:00Z",
      ended_at: "2026-09-16T12:00:01Z",
      status: "failed",
      summary: "HTTP 502",
      attempt_index: 1,
    },
    {
      kind: "completed",
      started_at: "2026-09-16T12:00:01Z",
      ended_at: "2026-09-16T12:00:01Z",
      status: "failed",
      summary: "upstream_unavailable · connect: connection refused 10.0.0.8:443",
      attempt_index: 1,
    },
  ],
};

const session: RequestSession = {
  id: "sess_skill",
  title: "给这个仓库适配一下我们现在的 deepseek harness",
  started_at: root.started_at,
  last_started_at: root.started_at,
  completed_at: root.completed_at,
  turn_count: 1,
  call_count: 2,
  status: "cancelled",
  requested_model: root.requested_model,
  input_protocol: root.input_protocol,
  service_id: root.service_id,
  local_access_token_id: "token_secret_id",
};

describe("buildSkillDiagnosticPayload", () => {
  it("keeps skill fields and nests retry children under the root", () => {
    const payload = buildSkillDiagnosticPayload({
      session,
      selectedRequestId: root.id,
      turns: [root],
      childrenByRoot: { [root.id]: [child] },
      serviceNames: { service_01: "Primary gateway" },
    });

    expect(payload.kind).toBe(SKILL_DIAGNOSTIC_KIND);
    expect(payload.skill).toBe(SKILL_DIAGNOSTIC_SKILL);
    expect(payload.selected_request_id).toBe(root.id);
    expect(payload.children_incomplete).toBeUndefined();
    expect(payload.session).toMatchObject({
      id: "sess_skill",
      status: "cancelled",
      entry: "/v1/messages",
      service_name: "Primary gateway",
      turn_count: 1,
      call_count: 2,
    });
    expect(payload.session).not.toHaveProperty("local_access_token_id");

    const [record] = payload.records;
    expect(record).toMatchObject({
      id: root.id,
      selected: true,
      entry: "/v1/messages",
      service_name: "Primary gateway",
      status: "cancelled",
      requested_model: "glm-5.3-flash",
      input_preview: root.input_preview,
    });
    expect(record).not.toHaveProperty("local_access_token_id");
    expect(record.events.map((event) => event.kind)).toEqual([
      "accepted",
      "privacy",
      "routed",
      "upstream",
      "completed",
    ]);
    expect(record.children).toEqual([
      expect.objectContaining({
        id: child.id,
        parent_request_id: root.id,
        status: "failed",
        recovery: child.recovery,
        error: child.error,
      }),
    ]);
    expect(record.children?.[0]).not.toHaveProperty("selected");
  });

  it("marks a selected child and flags missing retries", () => {
    const loaded = buildSkillDiagnosticPayload({
      session,
      selectedRequestId: child.id,
      turns: [root],
      childrenByRoot: { [root.id]: [child] },
    });
    expect(loaded.records[0]).not.toHaveProperty("selected");
    expect(loaded.records[0]?.children?.[0]?.selected).toBe(true);

    const pending = buildSkillDiagnosticPayload({
      session,
      selectedRequestId: child.id,
      turns: [root],
    });
    expect(pending.children_incomplete).toBe(true);
    expect(pending.records[0]?.children_incomplete).toBe(true);
    expect(pending.records[0]?.children).toBeUndefined();
  });
});

describe("buildSkillDiagnostic", () => {
  it("wraps the payload for pasting and never includes bodies or token ids", () => {
    const text = buildSkillDiagnostic({
      session,
      selectedRequestId: root.id,
      turns: [root],
      childrenByRoot: { [root.id]: [child] },
    });

    expect(text).toContain("astrlink-debug");
    expect(text).toContain("Read this snapshot first");
    expect(text).toContain("```json");
    expect(text).toContain(root.id);
    expect(text).toContain(child.id);
    expect(text).toContain("upstream_unavailable");
    expect(text).not.toContain("token_secret_id");
    expect(text).not.toContain("Authorization");
    expect(text).not.toContain("sk-");
  });
});
