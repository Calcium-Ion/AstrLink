import {
  CLIENT_TYPES,
  isModelDiscoveryProtocol,
  type ClientType,
  type OutputActivity,
  type RequestActivity,
  type RequestActivityCall,
  type RequestSession,
  type SessionStatus,
} from "./request-record-model";
import { sessionRunning } from "./session-live-model";

/**
 * How long a conversation still counts as in progress after its last call
 * ended with nothing for the client to run, such as an answer or a failure.
 * A quick reply keeps it on the board instead of dropping out and back.
 */
export const ACTIVE_GRACE_MS = 60_000;

/**
 * How long a conversation still counts as in progress after a call that asked
 * the client to run tools. Commands, tests and subagents can take minutes; the
 * next call brings their results and ends the wait sooner. The bound lets a
 * client that quit mid-tool drop off the board.
 */
export const TOOL_WAIT_MS = 30 * 60_000;

/** The span the live board charts and counts recent failures over. */
export const ACTIVITY_WINDOW_MS = 5 * 60_000;

/** The chart reads the count once per step across the window. */
export const ACTIVITY_STEP_MS = 1000;

/** The span the board averages the models' output over. */
export const THROUGHPUT_WINDOW_MS = 60_000;

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

/** Whether a call that ended left the client running the tools it asked for. */
function awaitsTools(
  status: SessionStatus,
  output: OutputActivity | undefined,
): boolean {
  return (
    status === "succeeded" && output?.kind === "tool" && output.tools.length > 0
  );
}

/** The first other call in `own`, oldest first, to start once `call` ended. */
function nextCall(
  own: readonly RequestActivityCall[],
  call: RequestActivityCall,
): RequestActivityCall | undefined {
  const ended = Date.parse(call.completed_at ?? "");
  return own.find(
    (other) => other !== call && Date.parse(other.started_at) >= ended,
  );
}

/**
 * What a conversation is doing at `nowMs`: running a call, or waiting between
 * calls, for the grace or while the client runs its tools. Null once it no
 * longer counts as in progress, and for model discovery, which is a single
 * lookup rather than a conversation.
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
  const wait = awaitsTools(session.status, session.output)
    ? TOOL_WAIT_MS
    : ACTIVE_GRACE_MS;
  if (!Number.isFinite(ended) || nowMs - ended >= wait) return null;
  return { state: "idle", since: Math.min(ended, nowMs) };
}

/**
 * Until when a call keeps its conversation in progress: while it runs, then
 * until the conversation's next call while the client runs the tools it asked
 * for, at most `TOOL_WAIT_MS`, or for the grace. `own` is the conversation's
 * calls, oldest first.
 */
function busyUntil(
  call: RequestActivityCall,
  own: readonly RequestActivityCall[],
): number {
  if (call.completed_at === null) return Number.POSITIVE_INFINITY;
  const ended = Date.parse(call.completed_at);
  if (!awaitsTools(call.status, call.output)) return ended + ACTIVE_GRACE_MS;
  const next = nextCall(own, call);
  const bound = ended + TOOL_WAIT_MS;
  return next ? Math.min(Date.parse(next.started_at), bound) : bound;
}

/** How many of the conversations in progress one client runs. */
export interface ActivityClient {
  clientType: ClientType;
  count: number;
}

export interface ActivitySnapshot {
  /** Conversations running a call or waiting between calls. */
  conversations: number;
  /**
   * Tokens the models generated per second over the last
   * `THROUGHPUT_WINDOW_MS`, from the calls that reported them.
   */
  outputTokensPerSecond: number;
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

/**
 * Where a poll starts reading: the window plus the grace before it. The
 * gateway adds the earlier calls whose tools may still be running.
 */
export function activitySince(nowMs: number): string {
  return new Date(nowMs - ACTIVITY_WINDOW_MS - ACTIVE_GRACE_MS).toISOString();
}

const byStart = (left: RequestActivityCall, right: RequestActivityCall) =>
  Date.parse(left.started_at) - Date.parse(right.started_at);

/**
 * The top-level conversation a conversation belongs to, following the
 * subagent links `parentOf` knows; one it does not know, or one in a loop of
 * links, is its own root.
 */
function rootOf(
  parentOf: ReadonlyMap<string, string>,
  sessionId: string,
): string {
  let root = sessionId;
  const seen = new Set([root]);
  for (let parent = parentOf.get(root); parent; parent = parentOf.get(root)) {
    if (seen.has(parent)) return sessionId;
    seen.add(parent);
    root = parent;
  }
  return root;
}

/**
 * The board's numbers and chart from the calls the gateway listed, on its
 * clock. A conversation is in progress while a call runs and after it ends,
 * as `busyUntil` says; it counts once under its latest call's client, however
 * many calls overlap. Subagents count with the conversation that started
 * them.
 */
export function summarizeActivity(
  activity: RequestActivity,
  windowMs: number = ACTIVITY_WINDOW_MS,
  stepMs: number = ACTIVITY_STEP_MS,
): { now: number; snapshot: ActivitySnapshot; points: ActivitySample[] } {
  const now = Date.parse(activity.now);
  const bySession = new Map<string, RequestActivityCall[]>();
  const parentOf = new Map<string, string>();
  for (const call of activity.calls) {
    const own = bySession.get(call.session_id);
    if (own) own.push(call);
    else bySession.set(call.session_id, [call]);
    if (call.parent_session_id) {
      parentOf.set(call.session_id, call.parent_session_id);
    }
  }
  for (const own of bySession.values()) own.sort(byStart);
  const spans = activity.calls
    .map((call) => ({
      session: rootOf(parentOf, call.session_id),
      client: call.client_type ?? "unknown",
      start: Date.parse(call.started_at),
      end: busyUntil(call, bySession.get(call.session_id) ?? []),
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
    const root = rootOf(parentOf, call.session_id);
    const known = latest.get(root);
    if (!known || Date.parse(call.started_at) > Date.parse(known.started_at)) {
      latest.set(root, call);
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
      outputTokensPerSecond: outputThroughput(activity.calls, now),
      failures,
      clients: [...current.clients],
    },
    points,
  };
}

/**
 * Tokens the models generated per second over the `windowMs` before `now`.
 * Upstreams report a call's tokens once it ends, so a running call adds
 * nothing yet. An ended call spreads its tokens evenly over its span, and the
 * window takes the share it overlaps.
 */
export function outputThroughput(
  calls: readonly RequestActivityCall[],
  now: number,
  windowMs: number = THROUGHPUT_WINDOW_MS,
): number {
  const from = now - windowMs;
  let tokens = 0;
  for (const call of calls) {
    if (!call.output_tokens || call.completed_at === null) continue;
    const start = Date.parse(call.started_at);
    const end = Date.parse(call.completed_at);
    if (!Number.isFinite(start) || !Number.isFinite(end)) continue;
    if (end <= start) {
      if (from < end && end <= now) tokens += call.output_tokens;
      continue;
    }
    const overlap = Math.min(end, now) - Math.max(start, from);
    if (overlap > 0) tokens += (call.output_tokens * overlap) / (end - start);
  }
  return (tokens * 1000) / windowMs;
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

/** One earlier step of a conversation, as its lane draws it. */
export interface LaneStep {
  /** A tool the client ran, or what the model did without one. */
  kind: "tool" | "reasoning" | "answer";
  /** The tool's name; empty for reasoning and answers. */
  tool: string;
  /** Steps in a row with the same kind and tool fold into one. */
  count: number;
  /** How long the client took over the tools before the next call. */
  ranMs: number | null;
}

/** The most steps a lane shows; older ones drop off its start. */
export const LANE_TRAIL_STEPS = 5;

/**
 * What a conversation did before what its lane says it does now, oldest
 * first, from the calls of the last few minutes. The call behind the current
 * action is left out: a running call is that action, and between calls the
 * last call's tools or answer are.
 */
export function laneTrail(
  calls: readonly RequestActivityCall[],
  session: RequestSession,
): LaneStep[] {
  const own = calls
    .filter((call) => call.session_id === session.id)
    .sort(byStart);
  const finished = own.filter((call) => call.completed_at !== null);
  const earlier = sessionRunning(session) ? finished : finished.slice(0, -1);
  const steps: LaneStep[] = [];
  const add = (step: LaneStep) => {
    const last = steps.at(-1);
    if (last && last.kind === step.kind && last.tool === step.tool) {
      last.count += step.count;
      if (step.ranMs !== null) last.ranMs = (last.ranMs ?? 0) + step.ranMs;
    } else {
      steps.push(step);
    }
  };
  for (const call of earlier) {
    const output = call.output;
    if (!output) continue;
    if (output.kind !== "tool" || output.tools.length === 0) {
      add({
        kind: output.kind === "reasoning" ? "reasoning" : "answer",
        tool: "",
        count: 1,
        ranMs: null,
      });
      continue;
    }
    // The gap before the conversation's next call is the client running them.
    const ended = Date.parse(call.completed_at ?? "");
    const next = nextCall(own, call);
    const ranMs = next ? Date.parse(next.started_at) - ended : null;
    output.tools.forEach((tool, index) =>
      add({
        kind: "tool",
        tool,
        count: 1,
        ranMs: index === output.tools.length - 1 ? ranMs : null,
      }),
    );
  }
  return steps.slice(-LANE_TRAIL_STEPS);
}

/**
 * The conversations of a list page that stand on their own, and the
 * subagents each of them started, oldest first. A subagent of a subagent
 * goes under the top-level conversation too; one whose parent is not on the
 * page stands on its own.
 */
export function nestSubagents(sessions: readonly RequestSession[]): {
  roots: RequestSession[];
  subagentsOf: Map<string, RequestSession[]>;
} {
  const listed = new Map(sessions.map((session) => [session.id, session]));
  const parentOf = new Map<string, string>();
  for (const session of sessions) {
    const parent = session.parent_session_id;
    if (parent && parent !== session.id && listed.has(parent)) {
      parentOf.set(session.id, parent);
    }
  }
  const roots: RequestSession[] = [];
  const subagentsOf = new Map<string, RequestSession[]>();
  for (const session of sessions) {
    const root = rootOf(parentOf, session.id);
    if (root === session.id || !listed.has(root)) {
      roots.push(session);
      continue;
    }
    const own = subagentsOf.get(root);
    if (own) own.push(session);
    else subagentsOf.set(root, [session]);
  }
  for (const own of subagentsOf.values()) {
    own.sort(
      (left, right) =>
        Date.parse(left.started_at) - Date.parse(right.started_at) ||
        left.id.localeCompare(right.id),
    );
  }
  return { roots, subagentsOf };
}

/**
 * The conversations in progress and the rest in their list order. Active ones
 * keep their place while they alternate between calls and tools, so they are
 * ordered by when each conversation began, newest first. A conversation stays
 * in progress while a subagent it started works.
 */
export function splitActiveSessions(
  sessions: readonly RequestSession[],
  nowMs: number,
  subagentsOf?: ReadonlyMap<string, readonly RequestSession[]>,
): { active: RequestSession[]; rest: RequestSession[] } {
  const active: RequestSession[] = [];
  const rest: RequestSession[] = [];
  for (const session of sessions) {
    const busy =
      conversationActivity(session, nowMs) !== null ||
      (subagentsOf?.get(session.id) ?? []).some(
        (subagent) => conversationActivity(subagent, nowMs) !== null,
      );
    if (busy) active.push(session);
    else rest.push(session);
  }
  active.sort((left, right) => {
    const difference =
      Date.parse(right.started_at) - Date.parse(left.started_at);
    return difference || right.id.localeCompare(left.id);
  });
  return { active, rest };
}
