import {
  CLIENT_TYPES,
  isModelDiscoveryProtocol,
  type ClientType,
  type RequestActivity,
  type RequestSession,
} from "./request-record-model";
import { sessionRunning } from "./session-live-model";

/**
 * How long a conversation still counts as in progress after its last call
 * ended. Agents run their tools between two calls, so a conversation in the
 * middle of a loop stays on the board instead of dropping out and back.
 */
export const ACTIVE_GRACE_MS = 60_000;

/** The span the live board charts and counts recent failures over. */
export const ACTIVITY_WINDOW_MS = 5 * 60_000;

/** The chart reads the count once per step across the window. */
export const ACTIVITY_STEP_MS = 1000;

export type ConversationActivity =
  | {
      state: "calling";
      /** Start of the oldest call still running. */
      since: number;
      calls: number;
    }
  | {
      state: "idle";
      /** When the last call ended. */
      since: number;
    };

function parseTime(value: string | null | undefined): number {
  return value ? Date.parse(value) : Number.NaN;
}

/**
 * What a conversation is doing at `nowMs`: running a call, or waiting between
 * calls within the grace. Null once it no longer counts as in progress, and
 * for model discovery, which is a single lookup rather than a conversation.
 */
export function conversationActivity(
  session: RequestSession,
  nowMs: number,
): ConversationActivity | null {
  if (isModelDiscoveryProtocol(session.input_protocol)) return null;
  if (sessionRunning(session)) {
    const starts = session.active_request_starts
      .map(parseTime)
      .filter(Number.isFinite);
    const latest = parseTime(session.last_started_at);
    const since =
      starts.length > 0
        ? Math.min(...starts)
        : Number.isFinite(latest)
          ? latest
          : nowMs;
    return {
      state: "calling",
      since: Math.min(since, nowMs),
      calls: Math.max(1, starts.length),
    };
  }
  const ended = parseTime(session.completed_at);
  if (!Number.isFinite(ended) || nowMs - ended >= ACTIVE_GRACE_MS) return null;
  return { state: "idle", since: Math.min(ended, nowMs) };
}

/** How many of the conversations in progress one client runs. */
export interface ActivityClient {
  clientType: ClientType;
  count: number;
}

export interface ActivitySnapshot {
  /** Conversations running a call or waiting between calls. */
  conversations: number;
  /** Calls in flight across those conversations. */
  calls: number;
  /** Conversations whose latest call failed within the window. */
  failures: number;
  /** The clients those conversations come from, busiest first. */
  clients: ActivityClient[];
}

// Ties keep the fixed client order, with unrecognised clients last.
const clientRank = (clientType: ClientType) =>
  clientType === "unknown"
    ? CLIENT_TYPES.length
    : CLIENT_TYPES.indexOf(clientType);

export interface ActivitySample {
  at: number;
  /** Conversations in progress. */
  value: number;
  /** The clients behind `value`, busiest first. */
  clients: readonly ActivityClient[];
}

/** Where a poll starts reading: the window plus the grace before it. */
export function activitySince(nowMs: number): string {
  return new Date(nowMs - ACTIVITY_WINDOW_MS - ACTIVE_GRACE_MS).toISOString();
}

/**
 * The board's numbers and chart from the calls the gateway listed, on its
 * clock. A conversation is in progress from a call's start until the grace
 * after it ends, and while a call runs; it counts once under its latest
 * call's client, however many calls overlap.
 */
export function summarizeActivity(
  activity: RequestActivity,
  windowMs: number = ACTIVITY_WINDOW_MS,
  stepMs: number = ACTIVITY_STEP_MS,
): { now: number; snapshot: ActivitySnapshot; points: ActivitySample[] } {
  const now = Date.parse(activity.now);
  const spans = activity.calls
    .map((call) => ({
      session: call.session_id,
      client: call.client_type ?? "unknown",
      start: Date.parse(call.started_at),
      end:
        call.completed_at === null
          ? Number.POSITIVE_INFINITY
          : Date.parse(call.completed_at) + ACTIVE_GRACE_MS,
    }))
    .filter((span) => Number.isFinite(span.start))
    .sort((left, right) => right.start - left.start);
  const sampleAt = (at: number): ActivitySample => {
    const sessions = new Map<string, ClientType>();
    for (const span of spans) {
      if (span.start <= at && at < span.end && !sessions.has(span.session)) {
        sessions.set(span.session, span.client);
      }
    }
    const perClient = new Map<ClientType, number>();
    for (const client of sessions.values()) {
      perClient.set(client, (perClient.get(client) ?? 0) + 1);
    }
    return { at, value: sessions.size, clients: rankClients(perClient) };
  };
  const points: ActivitySample[] = [];
  for (let at = now - windowMs; at < now; at += stepMs) {
    points.push(sampleAt(at));
  }
  const current = sampleAt(now);
  points.push(current);

  // A conversation failed when its latest call did, within the window.
  const latest = new Map<string, RequestActivity["calls"][number]>();
  for (const call of activity.calls) {
    const known = latest.get(call.session_id);
    if (!known || Date.parse(call.started_at) > Date.parse(known.started_at)) {
      latest.set(call.session_id, call);
    }
  }
  let failures = 0;
  for (const call of latest.values()) {
    const ended = parseTime(call.completed_at);
    if (call.status === "failed" && now - ended < windowMs) failures += 1;
  }
  return {
    now,
    snapshot: {
      conversations: current.value,
      calls: activity.calls.filter((call) => call.completed_at === null).length,
      failures,
      clients: [...current.clients],
    },
    points,
  };
}

function rankClients(perClient: Map<ClientType, number>): ActivityClient[] {
  return Array.from(perClient, ([clientType, count]) => ({
    clientType,
    count,
  })).sort(
    (left, right) =>
      right.count - left.count ||
      clientRank(left.clientType) - clientRank(right.clientType),
  );
}

/** What a conversation in progress is doing, as its lane says it. */
export type LaneAction =
  /** A call runs and the model has produced nothing yet. */
  | { kind: "waiting" }
  | { kind: "thinking" }
  | { kind: "writing" }
  /** The model is writing a call of `tool`; empty before its name arrives. */
  | { kind: "calling"; tool: string }
  /** Between calls: the client runs the tools the last call asked for. */
  | { kind: "running"; tools: string[] }
  /** Between calls: the model answered and the conversation waits for more. */
  | { kind: "replied" }
  /** Between calls, from a call that recorded no output. */
  | { kind: "idle" };

export function laneAction(session: RequestSession): LaneAction {
  const output = session.output;
  if (sessionRunning(session)) {
    if (!output) return { kind: "waiting" };
    if (output.kind === "reasoning") return { kind: "thinking" };
    if (output.kind === "text") return { kind: "writing" };
    return { kind: "calling", tool: output.tools.at(-1) ?? "" };
  }
  if (output?.kind === "tool" && output.tools.length > 0) {
    return { kind: "running", tools: [...new Set(output.tools)] };
  }
  return output ? { kind: "replied" } : { kind: "idle" };
}

/**
 * The conversations in progress and the rest in their list order. Active ones
 * keep their place while they alternate between calls and tools, so they are
 * ordered by when each conversation began, newest first.
 */
export function splitActiveSessions(
  sessions: readonly RequestSession[],
  nowMs: number,
): { active: RequestSession[]; rest: RequestSession[] } {
  const active: RequestSession[] = [];
  const rest: RequestSession[] = [];
  for (const session of sessions) {
    if (conversationActivity(session, nowMs)) active.push(session);
    else rest.push(session);
  }
  active.sort((left, right) => {
    const difference =
      Date.parse(right.started_at) - Date.parse(left.started_at);
    return difference || right.id.localeCompare(left.id);
  });
  return { active, rest };
}
