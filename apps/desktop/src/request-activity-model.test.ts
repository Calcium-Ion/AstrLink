import { describe, expect, it } from "vitest";

import {
  ACTIVE_GRACE_MS,
  activitySince,
  conversationActivity,
  laneAction,
  splitActiveSessions,
  summarizeActivity,
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

describe("splitActiveSessions", () => {
  it("leads with conversations in progress, newest conversation first", () => {
    const older = running({ id: "older", started_at: at(-900_000) });
    const newer = session({ id: "newer", started_at: at(-60_000) });
    const settled = session({ id: "settled", completed_at: at(-120_000) });
    const { active, rest } = splitActiveSessions([older, settled, newer], NOW);
    expect(active.map((item) => item.id)).toEqual(["newer", "older"]);
    expect(rest.map((item) => item.id)).toEqual(["settled"]);
  });
});
