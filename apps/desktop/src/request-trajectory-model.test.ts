import { describe, expect, it } from "vitest";

import {
  emptyTrajectoryFields,
  type RequestRecord,
} from "./request-record-model";
import {
  clientDisconnect,
  clientDisconnectNote,
  eventTone,
  extendPendingTimeline,
  extractPrivacyHits,
  inspectorChainRows,
  inspectorPart,
  inspectorTitle,
  modelRedirectSummary,
  callProgressAtListOffset,
  callProgressAtScrollLeft,
  listOffsetForCall,
  listScrollForTimeline,
  recordedPrivacyHits,
  scrollLeftForCall,
  splitPrivacyHighlights,
  synthesizeEvents,
  timelineKneeMs,
  timelineScrollForList,
  timelineWeight,
  trajectoryRows,
  trajectoryTimeline,
  TIMELINE_KNEE_MS,
  type TrajectoryTimeline,
} from "./request-trajectory-model";

function timelineCalls(timeline: TrajectoryTimeline) {
  return timeline.items.flatMap((item) =>
    item.kind === "call" ? [item.call] : [],
  );
}

function timelinePhases(timeline: TrajectoryTimeline) {
  return timelineCalls(timeline).flatMap((call) => call.phases);
}

function itemShape(timeline: TrajectoryTimeline) {
  return timeline.items.map((item) =>
    item.kind === "gap"
      ? {
          kind: "gap" as const,
          durationMs: item.durationMs,
          collapsed: item.collapsed,
        }
      : { kind: "call" as const, requestId: item.call.requestId },
  );
}

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

  it("synthesizes the redirect step right after the client for legacy records", () => {
    const redirected: RequestRecord = {
      ...record,
      model_redirect: { from: "gpt-4.1", to: "claude-sonnet-4-5" },
    };
    const events = synthesizeEvents(redirected);
    expect(events.map((event) => event.kind)).toEqual([
      "accepted",
      "model_redirect",
      "privacy",
      "routed",
      "upstream",
      "restore",
      "completed",
    ]);
    expect(events[0]?.summary).toBe("gpt-4.1 · openai.responses");
    expect(events[1]).toStrictEqual({
      kind: "model_redirect",
      started_at: record.started_at,
      ended_at: record.started_at,
      status: "succeeded",
      summary: "gpt-4.1 → claude-sonnet-4-5",
      attempt_index: record.attempt_index,
    });

    const rows = inspectorChainRows(redirected);
    expect(rows.map((row) => row.chip)).toEqual([
      "CLIENT",
      "REDIRECT",
      "POLICY",
      "ROUTE",
      "UPSTREAM",
      "RESTORE",
      "RESULT",
    ]);
    expect(rows[1]).toMatchObject({
      chip: "REDIRECT",
      lane: "gateway",
      tone: "ok",
      summary: "gpt-4.1 → claude-sonnet-4-5",
      redirect: { from: "gpt-4.1", to: "claude-sonnet-4-5" },
    });
    expect(rows.filter((row) => row.redirect)).toHaveLength(1);
    expect(new Set(rows.map((row) => row.id)).size).toBe(rows.length);
  });

  it("maps a persisted model_redirect event onto the REDIRECT chip", () => {
    const at = record.started_at;
    const event = {
      kind: "model_redirect" as const,
      started_at: at,
      ended_at: at,
      status: "succeeded" as const,
      summary: modelRedirectSummary({ from: "gpt-4.1", to: "glm-5" }),
      attempt_index: 0,
    };
    const persisted: RequestRecord = {
      ...record,
      model_redirect: { from: "gpt-4.1", to: "glm-5" },
      events: [
        {
          kind: "accepted",
          started_at: at,
          ended_at: at,
          status: "succeeded",
          summary: "gpt-4.1 · openai.responses",
          attempt_index: 0,
        },
        event,
      ],
    };
    const [client, redirect] = inspectorChainRows(persisted);
    expect(client?.chip).toBe("CLIENT");
    expect(client?.redirect).toBeUndefined();
    expect(redirect).toMatchObject({
      chip: "REDIRECT",
      lane: "gateway",
      summary: "gpt-4.1 → glm-5",
      redirect: { from: "gpt-4.1", to: "glm-5" },
    });

    // A record without the field still recovers the models from the summary,
    // but an ambiguous summary stays plain text.
    const bare: RequestRecord = { ...persisted, model_redirect: undefined };
    expect(inspectorChainRows(bare)[1]?.redirect).toStrictEqual({
      from: "gpt-4.1",
      to: "glm-5",
    });
    const ambiguous: RequestRecord = {
      ...bare,
      events: [{ ...event, summary: "a → b → c" }],
    };
    expect(inspectorChainRows(ambiguous)[0]?.redirect).toBeUndefined();
    expect(inspectorChainRows(ambiguous)[0]?.summary).toBe("a → b → c");
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

  it("settles a stored accepted phase that stayed pending", () => {
    const accepted = {
      kind: "accepted" as const,
      started_at: record.started_at,
      ended_at: record.completed_at,
      // Records written before the gateway settled this phase kept pending.
      status: "pending" as const,
      summary: "gpt-4.1 · openai.chat",
      attempt_index: 1,
    };
    const stale: RequestRecord = { ...record, events: [accepted] };
    const settled = trajectoryRows([stale], {})[0]!;
    expect(settled.status).toBe("succeeded");
    expect(settled.tone).toBe("ok");
    expect(settled.result).toBe("完成");

    const live: RequestRecord = {
      ...stale,
      status: "pending",
      completed_at: null,
      events: [{ ...accepted, ended_at: null }],
    };
    const inFlight = trajectoryRows([live], {})[0]!;
    expect(inFlight.status).toBe("pending");
    expect(inFlight.result).toBe("进行中");
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
    const timeline = trajectoryTimeline(
      rows,
      Date.parse(record.completed_at ?? ""),
    );
    expect(timelineCalls(timeline)).toHaveLength(1);
    expect(timelinePhases(timeline).map((phase) => phase.chip)).toEqual([
      "CLIENT",
      "POLICY",
      "ROUTE",
      "UPSTREAM",
      "RESTORE",
      "RESULT",
    ]);
    expect(
      timelinePhases(timeline).some((phase) => phase.chip === "RETRY"),
    ).toBe(false);
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
    expect(eventTone(forbidden, forbidden.events[0]!)).toBe("failed");
    const rows = trajectoryRows([forbidden], {});
    expect(rows.map((row) => [row.chip, row.tone])).toEqual([
      ["UPSTREAM", "failed"],
      ["RESULT", "failed"],
    ]);
    expect(
      timelinePhases(
        trajectoryTimeline(rows, Date.parse(forbidden.completed_at ?? "")),
      ).every((phase) => phase.tone === "failed"),
    ).toBe(true);
  });

  it("puts a client abort on the RESULT lane and keeps a 200 upstream as ok", () => {
    const aborted: RequestRecord = {
      ...record,
      status: "cancelled",
      http_status: 200,
      recovery: {
        delay_ms: 0,
        stop_reason: "cancelled",
        upstream_model: "glm-5.3-flash",
      },
      events: [
        {
          kind: "accepted",
          started_at: record.started_at,
          ended_at: record.started_at,
          status: "succeeded",
          summary: "glm-5.3-flash · anthropic.messages",
          attempt_index: 0,
        },
        {
          kind: "upstream",
          started_at: record.started_at,
          ended_at: record.completed_at,
          status: "cancelled",
          summary: "HTTP 200 · 23973 → 151",
          attempt_index: 1,
        },
        {
          kind: "completed",
          started_at: record.completed_at ?? record.started_at,
          ended_at: record.completed_at,
          status: "cancelled",
          summary: "HTTP 200 · 23973 → 151",
          attempt_index: 1,
        },
      ],
    };
    expect(clientDisconnect(aborted)).toBe(true);
    expect(clientDisconnectNote(aborted)).toContain("HTTP 200");
    expect(eventTone(aborted, aborted.events[1]!)).toBe("ok");
    expect(eventTone(aborted, aborted.events[2]!)).toBe("cancelled");
    const rows = inspectorChainRows(aborted);
    expect(
      rows.map((row) => [row.chip, row.lane, row.tone, row.result]),
    ).toEqual([
      ["CLIENT", "client", "ok", "完成"],
      ["UPSTREAM", "upstream", "ok", "HTTP 200"],
      ["RESULT", "client", "cancelled", "客户端断开"],
    ]);
    expect(rows.find((row) => row.chip === "RESULT")?.summary).toBe(
      "HTTP 200 · 输入 23,973 Token · 输出 151 Token",
    );
    const phases = timelinePhases(
      trajectoryTimeline(rows, Date.parse(aborted.completed_at ?? "")),
    );
    expect(phases.map((phase) => [phase.chip, phase.lane, phase.tone])).toEqual(
      [
        ["CLIENT", "client", "ok"],
        ["UPSTREAM", "upstream", "ok"],
        ["RESULT", "client", "cancelled"],
      ],
    );
  });

  it("still treats a cancelled call with no upstream status as a client abort", () => {
    const early: RequestRecord = {
      ...record,
      status: "cancelled",
      http_status: null,
      usage: null,
      error: null,
      events: [
        {
          kind: "accepted",
          started_at: record.started_at,
          ended_at: record.started_at,
          status: "succeeded",
          summary: "gpt-4.1 · openai.chat",
          attempt_index: 0,
        },
        {
          kind: "completed",
          started_at: record.completed_at ?? record.started_at,
          ended_at: record.completed_at,
          status: "cancelled",
          summary: "已取消",
          attempt_index: 1,
        },
      ],
    };
    expect(clientDisconnectNote(early)).toContain("上游响应到达前");
    const result = inspectorChainRows(early).find(
      (row) => row.chip === "RESULT",
    );
    expect(result?.tone).toBe("cancelled");
    expect(result?.result).toBe("客户端断开");
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
      session_link: {
        kind: "fingerprint",
        value: "fp1_0123456789abcdef0123456789abcdef",
      },
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
      ["CLIENT", "gpt-4.1 · openai.chat"],
      ["TURN", "第 2 轮 · 第二个文件是做什么的"],
      ["CLIENT", "gpt-4.1 · openai.chat"],
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
    expect(headers[1]).toMatchObject({
      status: "pending",
      tone: "pending",
      endedAt: null,
    });

    const timeline = trajectoryTimeline(
      rows,
      Date.parse("2026-08-16T10:00:30Z"),
    );
    expect(timelineCalls(timeline).map((call) => call.requestId)).toEqual([
      "req_loop_1",
      "req_loop_2",
      "req_loop_3",
      "req_loop_legacy",
    ]);
    expect(
      timelinePhases(timeline).some((phase) => phase.chip === "TURN"),
    ).toBe(false);

    // A single call has nothing to group: no header, unchanged trajectory.
    expect(trajectoryRows([step2], {}).map((row) => row.chip)).toEqual([
      "CLIENT",
    ]);
  });

  it("emits one call per root record with sequential phases and skips child retries", () => {
    const finished = trajectoryRows([record], {});
    const timeline = trajectoryTimeline(
      finished,
      Date.parse(record.completed_at ?? ""),
    );
    expect(timelineCalls(timeline)).toHaveLength(1);
    expect(
      timelinePhases(timeline).map((phase) => [phase.chip, phase.lane]),
    ).toEqual([
      ["CLIENT", "client"],
      ["POLICY", "gateway"],
      ["ROUTE", "gateway"],
      ["UPSTREAM", "upstream"],
      ["RESTORE", "gateway"],
      ["RESULT", "client"],
    ]);
    expect(
      timelinePhases(timeline).find((phase) => phase.chip === "RESULT"),
    ).toMatchObject({ durationMs: 0 });
  });

  it("collapses idle gaps longer than two seconds and keeps short gaps proportional", () => {
    const accepted = (startedAt: string, endedAt: string) => ({
      kind: "accepted" as const,
      started_at: startedAt,
      ended_at: endedAt,
      status: "succeeded" as const,
      summary: "gpt-4.1 · openai.chat",
      attempt_index: 1,
    });
    const first: RequestRecord = {
      ...record,
      id: "req_gap_a",
      privacy_restore: null,
      route_id: null,
      service_id: null,
      http_status: null,
      started_at: "2026-08-16T10:00:00Z",
      completed_at: "2026-08-16T10:00:02Z",
      events: [accepted("2026-08-16T10:00:00Z", "2026-08-16T10:00:02Z")],
    };
    const far: RequestRecord = {
      ...first,
      id: "req_gap_b",
      started_at: "2026-08-16T10:03:02Z",
      completed_at: "2026-08-16T10:03:04Z",
      events: [accepted("2026-08-16T10:03:02Z", "2026-08-16T10:03:04Z")],
    };
    const near: RequestRecord = {
      ...first,
      id: "req_gap_c",
      started_at: "2026-08-16T10:00:03Z",
      completed_at: "2026-08-16T10:00:04Z",
      events: [accepted("2026-08-16T10:00:03Z", "2026-08-16T10:00:04Z")],
    };

    expect(
      itemShape(
        trajectoryTimeline(
          trajectoryRows([first, far], {}),
          Date.parse(far.completed_at ?? ""),
        ),
      ),
    ).toEqual([
      { kind: "call", requestId: "req_gap_a" },
      { kind: "gap", durationMs: 180_000, collapsed: true },
      { kind: "call", requestId: "req_gap_b" },
    ]);

    expect(
      itemShape(
        trajectoryTimeline(
          trajectoryRows([first, near], {}),
          Date.parse(near.completed_at ?? ""),
        ),
      ),
    ).toEqual([
      { kind: "call", requestId: "req_gap_a" },
      { kind: "gap", durationMs: 1000, collapsed: false },
      { kind: "call", requestId: "req_gap_c" },
    ]);
  });

  it("extends pending calls to now and attaches turn membership without drawing TURN as a call", () => {
    const pending: RequestRecord = {
      ...record,
      id: "req_pending",
      status: "pending",
      completed_at: null,
      privacy_restore: null,
      route_id: null,
      service_id: null,
      http_status: null,
      events: [
        {
          kind: "accepted",
          started_at: "2026-08-16T10:00:00Z",
          ended_at: null,
          status: "pending",
          summary: "gpt-4.1 · openai.chat",
          attempt_index: 1,
        },
      ],
    };
    const nowMs = Date.parse("2026-08-16T10:00:05Z");
    const pendingTimeline = trajectoryTimeline(
      trajectoryRows([pending], {}),
      nowMs,
    );
    expect(pendingTimeline.durationMs).toBe(5000);
    expect(timelineCalls(pendingTimeline)[0]).toMatchObject({
      durationMs: 5000,
      phases: [{ chip: "CLIENT", durationMs: 5000 }],
    });

    const accepted = (startedAt: string, endedAt: string) => ({
      kind: "accepted" as const,
      started_at: startedAt,
      ended_at: endedAt,
      status: "succeeded" as const,
      summary: "gpt-4.1 · openai.chat",
      attempt_index: 1,
    });
    const turnA1: RequestRecord = {
      ...record,
      id: "req_turn_a1",
      turn_index: 1,
      privacy_restore: null,
      route_id: null,
      service_id: null,
      http_status: null,
      started_at: "2026-08-16T10:00:00Z",
      completed_at: "2026-08-16T10:00:02Z",
      events: [accepted("2026-08-16T10:00:00Z", "2026-08-16T10:00:02Z")],
    };
    const turnA2: RequestRecord = {
      ...turnA1,
      id: "req_turn_a2",
      started_at: "2026-08-16T10:00:03Z",
      completed_at: "2026-08-16T10:00:04Z",
      events: [accepted("2026-08-16T10:00:03Z", "2026-08-16T10:00:04Z")],
    };
    const turnB: RequestRecord = {
      ...turnA1,
      id: "req_turn_b",
      turn_index: 2,
      started_at: "2026-08-16T10:03:04Z",
      completed_at: "2026-08-16T10:03:06Z",
      events: [accepted("2026-08-16T10:03:04Z", "2026-08-16T10:03:06Z")],
    };
    const grouped = trajectoryRows([turnA1, turnA2, turnB], {});
    expect(grouped.some((row) => row.chip === "TURN")).toBe(true);
    const timeline = trajectoryTimeline(
      grouped,
      Date.parse(turnB.completed_at ?? ""),
    );
    const calls = timelineCalls(timeline);
    expect(
      calls.map((call) => [
        call.requestId,
        call.turnIndex,
        call.turnFirst,
        call.turnRowId,
      ]),
    ).toEqual([
      ["req_turn_a1", 1, true, "req_turn_a1:turn"],
      ["req_turn_a2", 1, false, "req_turn_a1:turn"],
      ["req_turn_b", 2, true, "req_turn_b:turn"],
    ]);
    const gaps = timeline.items.filter((item) => item.kind === "gap");
    expect(gaps).toEqual([
      {
        kind: "gap",
        durationMs: 1000,
        collapsed: false,
        turnRowId: "req_turn_a1:turn",
      },
      {
        kind: "gap",
        durationMs: 180_000,
        collapsed: true,
        turnRowId: null,
      },
    ]);
  });

  // Rebuilding the whole strip on every clock tick handed React 200-odd fresh
  // call objects, so no memoized column could skip its re-render. Only the call
  // still waiting on its upstream may move.
  it("advances only the open call and reuses everything settled", () => {
    const phase = (startedAt: string, endedAt: string | null) => ({
      kind: "accepted" as const,
      started_at: startedAt,
      ended_at: endedAt,
      status: endedAt === null ? ("pending" as const) : ("succeeded" as const),
      summary: "gpt-4.1 · openai.chat",
      attempt_index: 1,
    });
    const bare = {
      ...record,
      privacy_restore: null,
      route_id: null,
      service_id: null,
      http_status: null,
    };
    const settled: RequestRecord = {
      ...bare,
      id: "req_extend_settled",
      started_at: "2026-08-16T10:00:00Z",
      completed_at: "2026-08-16T10:00:02Z",
      events: [phase("2026-08-16T10:00:00Z", "2026-08-16T10:00:02Z")],
    };
    const running: RequestRecord = {
      ...bare,
      id: "req_extend_running",
      status: "pending",
      started_at: "2026-08-16T10:00:05Z",
      completed_at: null,
      events: [phase("2026-08-16T10:00:05Z", null)],
    };

    const rows = trajectoryRows([settled, running], {});
    const base = trajectoryTimeline(rows, Date.parse("2026-08-16T10:00:07Z"));
    expect(base.open).toBe(true);
    expect(base.durationMs).toBe(7000);
    expect(
      timelineCalls(base).map((call) => [call.durationMs, call.open]),
    ).toEqual([
      [2000, false],
      [2000, true],
    ]);

    // Same instant in, same object out, so a repaint that changed nothing
    // cannot invalidate a single column.
    expect(
      extendPendingTimeline(base, Date.parse("2026-08-16T10:00:07Z")),
    ).toBe(base);

    const later = extendPendingTimeline(
      base,
      Date.parse("2026-08-16T10:00:12Z"),
    );
    expect(later).not.toBe(base);
    expect(later.items[0]).toBe(base.items[0]);
    expect(later.items[1]).toBe(base.items[1]);
    expect(later.items[2]).not.toBe(base.items[2]);
    expect(later.kneeMs).toBe(base.kneeMs);
    expect(later.durationMs).toBe(12_000);
    expect(timelineCalls(later).map((call) => call.durationMs)).toEqual([
      2000, 7000,
    ]);
    expect(
      timelinePhases(later).map((phase) => [phase.chip, phase.durationMs]),
    ).toEqual([
      ["CLIENT", 2000],
      ["CLIENT", 7000],
    ]);

    // A finished conversation has nothing to advance, whatever the clock says.
    const closed = trajectoryTimeline(
      trajectoryRows([settled], {}),
      Date.parse("2026-08-16T10:00:02Z"),
    );
    expect(closed.open).toBe(false);
    expect(extendPendingTimeline(closed, Date.now())).toBe(closed);
  });

  // A 232 s call is 75x the median. On a linear axis it takes the whole width
  // and leaves the short calls at one pixel, which is what made the strip
  // unreadable at 55 calls.
  it("keeps short calls proportional and compresses long ones", () => {
    expect(timelineWeight(3000)).toBe(3000);
    expect(timelineWeight(TIMELINE_KNEE_MS)).toBe(TIMELINE_KNEE_MS);
    expect(timelineWeight(0)).toBe(0);
    expect(timelineWeight(-1)).toBe(0);

    const long = timelineWeight(232_000);
    const short = timelineWeight(3100);
    expect(Math.round(long)).toBe(24_186);
    expect(long).toBeGreaterThan(timelineWeight(117_000));
    expect(232_000 / 3100).toBeGreaterThan(70);
    expect(long / short).toBeLessThan(10);
  });

  it("takes the knee from the session, never below the fixed one", () => {
    // Odd count takes the middle sample, even count averages the two middles.
    expect(timelineKneeMs([7000, 20_000, 103_000])).toBe(20_000);
    expect(timelineKneeMs([7000, 19_416, 22_285, 103_000])).toBe(20_850.5);
    // A session of fast calls needs no compression at all, so the knee floors
    // at the fixed one and those sessions draw exactly as they did before.
    expect(timelineKneeMs([120, 400, 900])).toBe(TIMELINE_KNEE_MS);
    expect(timelineKneeMs([])).toBe(TIMELINE_KNEE_MS);
    expect(timelineKneeMs([0, -1, Number.NaN])).toBe(TIMELINE_KNEE_MS);
  });

  // Real durations from a 12-call session that spent 7 s to 103 s per call.
  // Against the fixed 5 s knee every one of them lands in the logarithmic
  // tail, where the curve is flat enough to erase the 14x spread between the
  // shortest and longest wait.
  it("keeps a slow session's calls apart", () => {
    const durations = [
      34_201, 39_388, 22_285, 14_439, 7188, 19_416, 49_499, 9308, 12_602,
      11_914, 44_326, 102_805,
    ];
    const contrast = (kneeMs: number) => {
      const weights = durations.map((duration) =>
        timelineWeight(duration, kneeMs),
      );
      return Math.max(...weights) / Math.min(...weights);
    };

    expect(contrast(TIMELINE_KNEE_MS)).toBeLessThan(3.2);
    expect(contrast(timelineKneeMs(durations))).toBeGreaterThan(7);
  });

  it("publishes the knee its own calls earn", () => {
    const slowCall = (id: string, startedAt: string, endedAt: string) => ({
      ...record,
      id,
      privacy_restore: null,
      route_id: null,
      service_id: null,
      http_status: null,
      started_at: startedAt,
      completed_at: endedAt,
      events: [
        {
          kind: "accepted" as const,
          started_at: startedAt,
          ended_at: endedAt,
          status: "succeeded" as const,
          summary: "gpt-4.1 · openai.chat",
          attempt_index: 1,
        },
      ],
    });
    const slow = [
      slowCall("req_slow_a", "2026-08-16T10:00:00Z", "2026-08-16T10:00:20Z"),
      slowCall("req_slow_b", "2026-08-16T10:00:21Z", "2026-08-16T10:02:01Z"),
    ];
    const nowMs = Date.parse("2026-08-16T10:02:01Z");
    expect(trajectoryTimeline(trajectoryRows(slow, {}), nowMs).kneeMs).toBe(
      60_000,
    );

    expect(trajectoryTimeline(trajectoryRows([record], {}), nowMs).kneeMs).toBe(
      TIMELINE_KNEE_MS,
    );
    expect(trajectoryTimeline([], nowMs).kneeMs).toBe(TIMELINE_KNEE_MS);
  });

  it("maps a list row onto the call it belongs to", () => {
    const rows = [
      { requestId: "a" },
      { requestId: "a" },
      { requestId: "a" },
      { requestId: "b" },
      { requestId: "b" },
    ];

    expect(callProgressAtListOffset([], 0)).toBeNull();
    expect(callProgressAtListOffset(rows, 0)).toEqual({
      requestId: "a",
      fraction: 0,
    });
    expect(callProgressAtListOffset(rows, 1.5)).toEqual({
      requestId: "a",
      fraction: 0.5,
    });
    expect(callProgressAtListOffset(rows, 3)).toEqual({
      requestId: "b",
      fraction: 0,
    });
    expect(callProgressAtListOffset(rows, 4.5)).toEqual({
      requestId: "b",
      fraction: 0.75,
    });
    expect(callProgressAtListOffset(rows, -1)).toEqual({
      requestId: "a",
      fraction: 0,
    });
    expect(callProgressAtListOffset(rows, 20)).toEqual({
      requestId: "b",
      fraction: 1,
    });
  });

  it("maps a call onto a timeline scrollLeft and back", () => {
    const columns = [
      { requestId: "a", offset: 0, width: 100 },
      { requestId: "b", offset: 120, width: 200 },
    ];

    expect(scrollLeftForCall(columns, "a", 0, 400)).toBe(0);
    expect(scrollLeftForCall(columns, "a", 0.5, 400)).toBe(50);
    expect(scrollLeftForCall(columns, "b", 0, 400)).toBe(120);
    expect(scrollLeftForCall(columns, "b", 1, 400)).toBe(320);
    expect(scrollLeftForCall(columns, "b", 1, 200)).toBe(200);
    expect(scrollLeftForCall(columns, "missing", 0, 400)).toBe(0);
    expect(scrollLeftForCall(columns, "a", 0, 0)).toBe(0);

    expect(callProgressAtScrollLeft([], 0)).toBeNull();
    expect(callProgressAtScrollLeft(columns, 0)).toEqual({
      requestId: "a",
      fraction: 0,
    });
    expect(callProgressAtScrollLeft(columns, 50)).toEqual({
      requestId: "a",
      fraction: 0.5,
    });
    expect(callProgressAtScrollLeft(columns, 120)).toEqual({
      requestId: "b",
      fraction: 0,
    });
    expect(callProgressAtScrollLeft(columns, 220)).toEqual({
      requestId: "b",
      fraction: 0.5,
    });
    // A seam between columns belongs to the next call, not a travel percent.
    expect(callProgressAtScrollLeft(columns, 110)).toEqual({
      requestId: "b",
      fraction: 0,
    });
  });

  it("keeps both scroll endpoints reachable with unequal viewports and call widths", () => {
    const rows = ["a", "a", "a", "b", "b", "c", "c"].map((requestId) => ({
      requestId,
    }));
    const columns = [
      { requestId: "a", offset: 0, width: 800 },
      { requestId: "b", offset: 810, width: 100 },
      { requestId: "c", offset: 920, width: 80 },
    ];
    // Only 200px of horizontal travel against 1400px in the event list.
    // Leading-edge alignment used to clamp the strip during the first call.
    let previous = -1;
    for (const top of [0, 140, 400, 600, 900, 1100, 1390, 1400]) {
      const left = timelineScrollForList(rows, columns, top, 1400, 200);
      expect(left).toBeGreaterThan(previous);
      expect(listScrollForTimeline(rows, columns, left, 200, 1400)).toBeCloseTo(
        top,
      );
      previous = left;
    }
    expect(timelineScrollForList(rows, columns, 0, 1400, 200)).toBe(0);
    expect(timelineScrollForList(rows, columns, 1400, 1400, 200)).toBe(200);
    expect(listScrollForTimeline(rows, columns, 0, 200, 1400)).toBe(0);
    expect(listScrollForTimeline(rows, columns, 200, 200, 1400)).toBe(1400);
    expect(timelineScrollForList(rows, columns, -50, 1400, 200)).toBe(0);
    expect(listScrollForTimeline(rows, columns, 250, 200, 1400)).toBe(1400);
    expect(timelineScrollForList(rows, columns, 10, 0, 200)).toBe(0);
    expect(listScrollForTimeline(rows, columns, 10, 0, 1400)).toBe(0);
  });

  it("maps a call back onto a list offset", () => {
    const rows = [
      { requestId: "a" },
      { requestId: "a" },
      { requestId: "a" },
      { requestId: "b" },
      { requestId: "b" },
    ];

    expect(listOffsetForCall(rows, "a", 0, 10)).toBe(0);
    expect(listOffsetForCall(rows, "a", 0.5, 10)).toBe(15);
    expect(listOffsetForCall(rows, "b", 0, 10)).toBe(30);
    expect(listOffsetForCall(rows, "b", 1, 10)).toBe(50);
    expect(listOffsetForCall(rows, "missing", 0, 10)).toBe(0);
  });

  it("places each phase at its offset inside the call", () => {
    const phases = timelinePhases(
      trajectoryTimeline(
        trajectoryRows(
          [
            {
              ...record,
              events: [
                {
                  kind: "accepted",
                  started_at: "2026-08-16T10:00:00Z",
                  ended_at: "2026-08-16T10:00:00.020Z",
                  status: "succeeded",
                  summary: "gpt-4.1 · openai.chat",
                  attempt_index: 1,
                },
                {
                  kind: "upstream",
                  started_at: "2026-08-16T10:00:00.020Z",
                  ended_at: "2026-08-16T10:00:07Z",
                  status: "succeeded",
                  summary: "HTTP 200",
                  attempt_index: 1,
                },
              ],
            },
          ],
          {},
        ),
        Date.parse("2026-08-16T10:00:07Z"),
      ),
    );
    expect(
      phases.map((phase) => [phase.chip, phase.startMs, phase.durationMs]),
    ).toEqual([
      ["CLIENT", 0, 20],
      ["UPSTREAM", 20, 6980],
    ]);
  });

  it("builds an inspector chain from one record without its child retries", () => {
    const child: RequestRecord = {
      ...record,
      id: "req_childaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      parent_request_id: record.id,
    };
    const linked: RequestRecord = {
      ...record,
      session_link: { kind: "echo_id", value: "resp_1" },
    };
    expect(inspectorChainRows(linked).map((row) => row.chip)).toEqual([
      "CLIENT",
      "POLICY",
      "ROUTE",
      "UPSTREAM",
      "RESTORE",
      "RESULT",
    ]);
    expect(inspectorChainRows(linked)[0]?.conversationContinued).toBe(true);
    expect(
      inspectorChainRows(record)[0]?.conversationContinued,
    ).toBeUndefined();
    expect(inspectorChainRows(child).some((row) => row.chip === "RETRY")).toBe(
      true,
    );
    expect(
      inspectorChainRows(record).some((row) => row.requestId === child.id),
    ).toBe(false);
  });

  it("keeps each rejected provider its own row when they share an instant", () => {
    const at = record.started_at;
    const rejected: RequestRecord = {
      ...record,
      status: "failed",
      service_id: null,
      http_status: 503,
      events: (["service_a", "service_b"] as const).map((id) => ({
        kind: "routed" as const,
        started_at: at,
        ended_at: at,
        status: "failed" as const,
        summary: `${id} · circuit_open`,
        attempt_index: 0,
      })),
    };
    const routes = inspectorChainRows(rejected);
    expect(routes.map((row) => [row.summary, row.tone])).toEqual([
      ["service_a · circuit_open", "failed"],
      ["service_b · circuit_open", "failed"],
    ]);
    expect(new Set(routes.map((row) => row.id)).size).toBe(2);
  });

  it("maps trajectory chips to inspector audit parts", () => {
    expect(inspectorPart("TURN")).toBe("request_body");
    expect(inspectorPart("CLIENT")).toBe("request_body");
    expect(inspectorPart("POLICY")).toBe("upstream_request_body");
    expect(inspectorPart("ROUTE")).toBe("route");
    expect(inspectorPart("REDIRECT")).toBe("redirect");
    expect(inspectorPart("UPSTREAM")).toBe("upstream_response_content");
    expect(inspectorPart("RETRY")).toBe("upstream_response_content");
    expect(inspectorPart("RESTORE")).toBe("response_content");
    expect(inspectorPart("RESULT")).toBe("response_content");
    expect(inspectorTitle("POLICY")).toBe("命中");
    expect(inspectorTitle("RETRY")).toBe("上游响应");
    expect(inspectorTitle("REDIRECT")).toBe("模型重定向");
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
