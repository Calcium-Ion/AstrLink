import { describe, expect, it } from "vitest";

import {
  ACTIVE_GRACE_MS,
  activitySince,
  conversationActivity,
  laneAction,
  laneTrail,
  LANE_TRAIL_STEPS,
  nestSubagents,
  splitActiveSessions,
  summarizeActivity,
  TOOL_WAIT_MS,
} from "./request-activity-model";
import type {
  RequestActivityCall,
  RequestSession,
} from "./request-record-model";

const NOW = Date.parse("2026-10-23T01:00:00Z");
const at = (offsetMs: number) => new Date(NOW + offsetMs).toISOString();

function session(overrides: Partial<RequestSession> = {}): RequestSession {
  return {
    id: "session_a",
    title: "fix the login redirect",
    started_at: at(-600_000),
    last_started_at: at(-30_000),
    completed_at: at(-20_000),
    duration_ms: 10_000,
    active_request_starts: [],
    turn_count: 1,
    call_count: 3,
    status: "succeeded",
    requested_model: "claude-opus-5-5",
    input_protocol: "anthropic.messages",
    service_id: "service_01",
    local_access_token_id: null,
    ...overrides,
  };
}

const running = (overrides: Partial<RequestSession> = {}) =>
  session({
    status: "pending",
    completed_at: null,
    active_request_starts: [at(-12_000)],
    ...overrides,
  });

describe("conversationActivity", () => {
  it("dates a running conversation from its oldest running call", () => {
    expect(
      conversationActivity(
        running({ active_request_starts: [at(-12_000), at(-3_000)] }),
        NOW,
      ),
    ).toEqual({ state: "calling", since: NOW - 12_000, calls: 2 });
  });

  it("falls back to the latest start for a pending conversation without starts", () => {
    expect(
      conversationActivity(running({ active_request_starts: [] }), NOW),
    ).toEqual({ state: "calling", since: NOW - 30_000, calls: 1 });
  });

  it("keeps a conversation between calls until the grace runs out", () => {
    expect(conversationActivity(session(), NOW)).toEqual({
      state: "idle",
      since: NOW - 20_000,
    });
    const ended = session({ completed_at: at(-ACTIVE_GRACE_MS) });
    expect(conversationActivity(ended, NOW)).toBeNull();
    expect(conversationActivity(ended, NOW - 1)).not.toBeNull();
  });

  it("keeps a conversation while its client runs tools, until the wait runs out", () => {
    const tools = { kind: "tool" as const, tools: ["Bash"] };
    const longCommand = session({ output: tools, completed_at: at(-180_000) });
    expect(conversationActivity(longCommand, NOW)).toEqual({
      state: "idle",
      since: NOW - 180_000,
    });
    const quit = session({ output: tools, completed_at: at(-TOOL_WAIT_MS) });
    expect(conversationActivity(quit, NOW)).toBeNull();
    expect(conversationActivity(quit, NOW - 1)).not.toBeNull();
    // A failed call leaves nothing for the client to run.
    const failed = session({
      output: tools,
      status: "failed",
      completed_at: at(-ACTIVE_GRACE_MS),
    });
    expect(conversationActivity(failed, NOW)).toBeNull();
  });

  it("leaves out model discovery and conversations without an end", () => {
    expect(
      conversationActivity(running({ input_protocol: "openai.models" }), NOW),
    ).toBeNull();
    expect(
      conversationActivity(session({ completed_at: null }), NOW),
    ).toBeNull();
  });
});

function call(
  session_id: string,
  startedOffset: number,
  completedOffset: number | null,
  overrides: Partial<RequestActivityCall> = {},
): RequestActivityCall {
  return {
    session_id,
    started_at: at(startedOffset),
    completed_at: completedOffset === null ? null : at(completedOffset),
    status: completedOffset === null ? "pending" : "succeeded",
    ...overrides,
  };
}

describe("summarizeActivity", () => {
  it("counts conversations, running calls and recent failures at the gateway's now", () => {
    const { now, snapshot } = summarizeActivity({
      now: at(0),
      calls: [
        call("a", -5_000, null),
        call("a", -40_000, null),
        call("b", -8_000, null),
        call("c", -30_000, -20_000),
        call("d", -100_000, -90_000),
        call("e", -125_000, -120_000, { status: "failed" }),
        call("f", -305_000, -301_000, { status: "failed" }),
        call("g", -200_000, -190_000, { status: "failed" }),
        call("g", -10_000, -9_000),
      ],
    });
    expect(now).toBe(NOW);
    // g recovered after its failure, so it counts as in progress, not failed.
    expect(snapshot).toEqual({
      conversations: 4,
      calls: 3,
      failures: 1,
      clients: [{ clientType: "unknown", count: 4 }],
    });
  });

  it("charts every step of the window, a session once per moment", () => {
    const { points } = summarizeActivity(
      {
        now: at(0),
        calls: [
          call("a", -250_000, -240_000),
          call("a", -245_000, -241_000),
          call("b", -100_000, null),
        ],
      },
      300_000,
      10_000,
    );
    expect(points).toHaveLength(31);
    expect(points[0].at).toBe(NOW - 300_000);
    expect(points.at(-1)?.at).toBe(NOW);
    const value = (offset: number) =>
      points.find((point) => point.at === NOW + offset)?.value;
    expect(value(-260_000)).toBe(0);
    expect(value(-240_000)).toBe(1);
    // Waiting between calls still counts until the grace after the call.
    expect(value(-190_000)).toBe(1);
    expect(value(-180_000)).toBe(0);
    expect(value(-100_000)).toBe(1);
    expect(value(0)).toBe(1);
  });

  it("keeps a conversation in progress until its tools' results come back", () => {
    const tools = { kind: "tool" as const, tools: ["Bash"] };
    const { points, snapshot } = summarizeActivity(
      {
        now: at(0),
        calls: [
          // A long command, then its results and an answer.
          call("a", -290_000, -280_000, { output: tools }),
          call("a", -100_000, -95_000, { output: { kind: "text", tools: [] } }),
          // Still running a tool the model asked for twenty minutes ago.
          call("b", -1_200_000, -1_190_000, { output: tools }),
          // Its wait runs out inside the window.
          call("c", -TOOL_WAIT_MS - 130_000, -TOOL_WAIT_MS - 120_000, {
            output: tools,
          }),
          // A failed call keeps only the grace.
          call("d", -250_000, -240_000, { output: tools, status: "failed" }),
        ],
      },
      300_000,
      10_000,
    );
    const value = (offset: number) =>
      points.find((point) => point.at === NOW + offset)?.value;
    expect(value(-200_000)).toBe(4);
    expect(value(-150_000)).toBe(3);
    // The results arrive with the next call, which takes over the span.
    expect(value(-100_000)).toBe(2);
    expect(value(-30_000)).toBe(1);
    expect(snapshot.conversations).toBe(1);
  });

  it("names the clients behind each moment, busiest first", () => {
    const { snapshot, points } = summarizeActivity({
      now: at(0),
      calls: [
        call("a", -20_000, -10_000, { client_type: "codex" }),
        call("b", -12_000, null, { client_type: "claude_code" }),
        call("c", -15_000, -14_000),
        call("d", -9_000, null, { client_type: "claude_code" }),
        call("e", -30_000, -25_000, { client_type: "cursor" }),
        call("f", -200_000, -150_000, { client_type: "pi" }),
        // A conversation counts under the client of its latest call.
        call("e", -2_000, null, { client_type: "pi" }),
      ],
    });
    expect(snapshot.clients).toEqual([
      { clientType: "claude_code", count: 2 },
      { clientType: "codex", count: 1 },
      { clientType: "pi", count: 1 },
      { clientType: "unknown", count: 1 },
    ]);
    expect(points.find((point) => point.at === NOW - 160_000)?.clients).toEqual(
      [{ clientType: "pi", count: 1 }],
    );
  });

  it("counts subagents with the conversation that started them", () => {
    const { snapshot, points } = summarizeActivity(
      {
        now: at(0),
        calls: [
          call("parent", -40_000, -30_000, {
            output: { kind: "tool", tools: ["Agent"] },
          }),
          call("child_a", -25_000, null, { parent_session_id: "parent" }),
          call("child_b", -20_000, null, { parent_session_id: "parent" }),
          call("grandchild", -10_000, null, { parent_session_id: "child_b" }),
          call("other", -5_000, null),
        ],
      },
      60_000,
      10_000,
    );
    // Four calls run, in two conversations.
    expect(snapshot.conversations).toBe(2);
    expect(snapshot.calls).toBe(4);
    expect(points.at(-1)?.value).toBe(2);
  });

  it("reads a conversation's failure from its latest call, subagents included", () => {
    const { snapshot } = summarizeActivity({
      now: at(0),
      calls: [
        call("parent", -60_000, -50_000),
        call("child", -40_000, -30_000, {
          parent_session_id: "parent",
          status: "failed",
        }),
      ],
    });
    expect(snapshot.failures).toBe(1);
  });

  it("reads from the window plus the grace before it", () => {
    expect(activitySince(NOW)).toBe(at(-360_000));
  });
});

describe("laneAction", () => {
  it("follows the running call's output", () => {
    expect(laneAction(running())).toEqual({ kind: "waiting" });
    expect(
      laneAction(running({ output: { kind: "reasoning", tools: [] } })),
    ).toEqual({ kind: "thinking" });
    expect(
      laneAction(running({ output: { kind: "text", tools: [] } })),
    ).toEqual({ kind: "writing" });
    expect(
      laneAction(
        running({ output: { kind: "tool", tools: ["Read", "Bash"] } }),
      ),
    ).toEqual({ kind: "calling", tool: "Bash" });
  });

  it("tells running tools from a finished answer between calls", () => {
    expect(
      laneAction(
        session({ output: { kind: "tool", tools: ["Read", "Read", "Bash"] } }),
      ),
    ).toEqual({ kind: "running", tools: ["Read", "Bash"] });
    expect(
      laneAction(session({ output: { kind: "text", tools: ["Read"] } })),
    ).toEqual({ kind: "replied" });
    expect(laneAction(session())).toEqual({ kind: "idle" });
  });
});

describe("laneTrail", () => {
  const tools = (...names: string[]) => ({
    kind: "tool" as const,
    tools: names,
  });
  const answer = { kind: "text" as const, tools: [] };

  it("folds repeated steps and times the tools between calls", () => {
    const calls = [
      call("session_a", -60_000, -55_000, { output: tools("Read") }),
      call("session_a", -54_000, -50_000, { output: tools("Read") }),
      call("session_a", -49_500, -40_000, { output: tools("Grep", "Bash") }),
      call("session_a", -28_000, null),
      call("other", -30_000, -20_000, { output: tools("Edit") }),
    ];
    expect(laneTrail(calls, running({ id: "session_a" }))).toEqual([
      { kind: "tool", tool: "Read", count: 2, ranMs: 1_500 },
      { kind: "tool", tool: "Grep", count: 1, ranMs: null },
      { kind: "tool", tool: "Bash", count: 1, ranMs: 12_000 },
    ]);
  });

  it("leaves the last call to the lane's action between calls", () => {
    const calls = [
      call("session_a", -60_000, -50_000, {
        output: { kind: "reasoning", tools: [] },
      }),
      call("session_a", -45_000, -40_000, { output: answer }),
      call("session_a", -30_000, -25_000, { output: tools("Read") }),
      call("session_a", -20_000, -10_000),
    ];
    expect(laneTrail(calls, session({ id: "session_a" }))).toEqual([
      { kind: "reasoning", tool: "", count: 1, ranMs: null },
      { kind: "answer", tool: "", count: 1, ranMs: null },
      { kind: "tool", tool: "Read", count: 1, ranMs: 5_000 },
    ]);
  });

  it("keeps only the newest steps", () => {
    const calls = Array.from({ length: LANE_TRAIL_STEPS + 3 }, (_, index) =>
      call("session_a", -100_000 + index * 5_000, -98_000 + index * 5_000, {
        output: tools(`tool_${index}`),
      }),
    );
    const trail = laneTrail(calls, running({ id: "session_a" }));
    expect(trail).toHaveLength(LANE_TRAIL_STEPS);
    expect(trail.at(-1)?.tool).toBe(`tool_${LANE_TRAIL_STEPS + 2}`);
  });
});

describe("nestSubagents", () => {
  it("folds subagents under the conversation on the page that started them", () => {
    const parent = session({ id: "parent", started_at: at(-900_000) });
    const late = session({
      id: "late",
      parent_session_id: "parent",
      started_at: at(-100_000),
    });
    const early = session({
      id: "early",
      parent_session_id: "parent",
      started_at: at(-800_000),
    });
    const nested = session({
      id: "nested",
      parent_session_id: "early",
      started_at: at(-700_000),
    });
    const orphan = session({ id: "orphan", parent_session_id: "elsewhere" });
    const { roots, subagentsOf } = nestSubagents([
      late,
      parent,
      nested,
      orphan,
      early,
    ]);
    expect(roots.map((item) => item.id)).toEqual(["parent", "orphan"]);
    expect(subagentsOf.get("parent")?.map((item) => item.id)).toEqual([
      "early",
      "nested",
      "late",
    ]);
  });

  it("stands a loop of parents on its own", () => {
    const left = session({ id: "left", parent_session_id: "right" });
    const right = session({ id: "right", parent_session_id: "left" });
    const { roots } = nestSubagents([left, right]);
    expect(roots.map((item) => item.id)).toEqual(["left", "right"]);
  });
});

describe("splitActiveSessions", () => {
  it("keeps a conversation in progress while a subagent it started works", () => {
    const parent = session({ id: "parent", completed_at: at(-600_000) });
    const child = running({ id: "child", parent_session_id: "parent" });
    const { active, rest } = splitActiveSessions(
      [parent],
      NOW,
      new Map([["parent", [child]]]),
    );
    expect(active.map((item) => item.id)).toEqual(["parent"]);
    expect(rest).toEqual([]);
  });

  it("leads with conversations in progress, newest conversation first", () => {
    const older = running({ id: "older", started_at: at(-900_000) });
    const newer = session({ id: "newer", started_at: at(-60_000) });
    const settled = session({ id: "settled", completed_at: at(-120_000) });
    const { active, rest } = splitActiveSessions([older, settled, newer], NOW);
    expect(active.map((item) => item.id)).toEqual(["newer", "older"]);
    expect(rest.map((item) => item.id)).toEqual(["settled"]);
  });
});
