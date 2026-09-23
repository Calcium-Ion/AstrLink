import { formatExactNumber } from "./format-compact-number";
import { i18n } from "./i18n";
import {
  statusLabel,
  type RequestEvent,
  type RequestRecord,
  type RequestStatus,
} from "./request-record-model";

export type TrajectoryLane = "client" | "gateway" | "upstream";
export type TrajectoryChip =
  | "TURN"
  | "CLIENT"
  | "POLICY"
  | "ROUTE"
  | "UPSTREAM"
  | "RETRY"
  | "RESTORE"
  | "RESULT";

export type TrajectoryTone =
  | "ok"
  | "failed"
  | "blocked"
  | "pending"
  | "cancelled";

export type InspectorPart =
  | "request_body"
  | "upstream_request_body"
  | "upstream_response_content"
  | "response_content"
  | "route";

export function inspectorPart(chip: TrajectoryChip): InspectorPart {
  switch (chip) {
    case "TURN":
    case "CLIENT":
      return "request_body";
    case "POLICY":
      return "upstream_request_body";
    case "ROUTE":
      return "route";
    case "UPSTREAM":
    case "RETRY":
      return "upstream_response_content";
    case "RESTORE":
    case "RESULT":
      return "response_content";
  }
}

export function inspectorTitle(chip: TrajectoryChip): string {
  switch (chip) {
    case "TURN":
      return i18n.t("trajectory.turnHeaderTitle");
    case "CLIENT":
      return i18n.t("trajectory.clientBody");
    case "POLICY":
      return i18n.t("trajectory.hit");
    case "ROUTE":
      return i18n.t("trajectory.route");
    case "UPSTREAM":
    case "RETRY":
      return i18n.t("trajectory.upstreamResponse");
    case "RESTORE":
      return i18n.t("trajectory.restore");
    case "RESULT":
      return i18n.t("trajectory.result");
  }
}

const privacyPlaceholderPattern =
  /<(?:PRIVATE_[A-Za-z0-9_]+|SECRET(?:_[A-Za-z0-9]+)?)>/g;

/**
 * Natural stand-ins are indistinguishable from genuine values by eye, which is
 * the point upstream but leaves the operator with no way to tell what was
 * replaced. These patterns mirror the reserved namespaces Core mints from
 * (`core/internal/privacy/placeholders.go`) so the audit panel can name them.
 */
const naturalPlaceholderPatterns: ReadonlyArray<
  [(typeof privacyKindOrder)[number], RegExp]
> = [
  ["email", /\bredacted-[0-9a-f]{12}@private\.invalid\b/gi],
  ["url", /\bhttps:\/\/private\.invalid\/r\/[0-9a-f]{12}\b/gi],
  ["phone", /\+1-555-555-01\d{2}\b/g],
  ["payment_card", /\b4000 ?0000 ?0000 ?0\d{3}\b/g],
  ["account", /\bXX00REDACTED\d{10}\b/gi],
  ["ip_address", /\b(?:203\.0\.113|192\.0\.2|198\.51\.100)\.\d{1,3}\b/g],
  ["ip_address", /\b2001:db8::[0-9a-f]{1,4}(?::[0-9a-f]{1,4})*\b/gi],
];

const privacyKindOrder = [
  "email",
  "phone",
  "account",
  "payment_card",
  "ip_address",
  "url",
  "common_secret",
  "private_address",
  "private_date",
  "private_person",
  "unknown",
] as const;

function privacyKindLabel(kind: (typeof privacyKindOrder)[number]): string {
  return i18n.t(`privacy.${kind}`);
}

const privacyTokenKinds: Array<[string, (typeof privacyKindOrder)[number]]> = [
  ["ACCOUNT_NUMBER", "account"],
  ["PAYMENT_CARD", "payment_card"],
  ["IP_ADDRESS", "ip_address"],
  ["ADDRESS", "private_address"],
  ["PERSON", "private_person"],
  ["EMAIL", "email"],
  ["PHONE", "phone"],
  ["DATE", "private_date"],
  ["URL", "url"],
];

export interface PrivacyHitGroup {
  kind: string;
  label: string;
  count: number;
  placeholders: string[];
}

export interface PrivacyHighlightSpan {
  text: string;
  kind?: string;
}

export function privacyHitLabel(kind: string): string {
  return privacyKindOrder.includes(kind as (typeof privacyKindOrder)[number])
    ? privacyKindLabel(kind as (typeof privacyKindOrder)[number])
    : kind;
}

export function recordedPrivacyHits(
  restore: RequestRecord["privacy_restore"],
): PrivacyHitGroup[] {
  if (!restore?.hits?.length) return [];
  return restore.hits.map((hit) => ({
    kind: hit.kind,
    label: privacyHitLabel(hit.kind),
    count: hit.count,
    placeholders: [],
  }));
}

export function splitPrivacyHighlights(text: string): PrivacyHighlightSpan[] {
  if (!text) return [];
  const marks: Array<{ start: number; end: number; kind: string }> = [];
  for (const match of text.matchAll(privacyPlaceholderPattern)) {
    const start = match.index ?? 0;
    marks.push({
      start,
      end: start + match[0].length,
      kind: kindFromPlaceholder(match[0]),
    });
  }
  for (const [kind, pattern] of naturalPlaceholderPatterns) {
    for (const match of text.matchAll(pattern)) {
      const start = match.index ?? 0;
      marks.push({ start, end: start + match[0].length, kind });
    }
  }
  marks.sort((left, right) => left.start - right.start);
  const spans: PrivacyHighlightSpan[] = [];
  let last = 0;
  for (const mark of marks) {
    // A natural stand-in can sit inside a token placeholder's payload, so a
    // later mark that starts behind the cursor is already covered.
    if (mark.start < last) continue;
    if (mark.start > last) {
      spans.push({ text: text.slice(last, mark.start) });
    }
    spans.push({ text: text.slice(mark.start, mark.end), kind: mark.kind });
    last = mark.end;
  }
  if (last < text.length) {
    spans.push({ text: text.slice(last) });
  }
  return spans.length > 0 ? spans : [{ text }];
}

export function extractPrivacyHits(text: string): PrivacyHitGroup[] {
  if (!text) return [];
  const grouped = new Map<string, Set<string>>();
  const record = (kind: string, placeholder: string) => {
    const seen = grouped.get(kind) ?? new Set<string>();
    seen.add(placeholder);
    grouped.set(kind, seen);
  };
  for (const match of text.matchAll(privacyPlaceholderPattern)) {
    record(kindFromPlaceholder(match[0]), match[0]);
  }
  for (const [kind, pattern] of naturalPlaceholderPatterns) {
    for (const match of text.matchAll(pattern)) {
      record(kind, match[0]);
    }
  }
  return privacyKindOrder
    .filter((kind) => grouped.has(kind))
    .map((kind) => {
      const placeholders = [...(grouped.get(kind) ?? [])];
      return {
        kind,
        label: privacyKindLabel(kind),
        count: placeholders.length,
        placeholders,
      };
    });
}

function kindFromPlaceholder(token: string): (typeof privacyKindOrder)[number] {
  const inner = token.slice(1, -1);
  if (inner === "SECRET" || inner.startsWith("SECRET_")) {
    return "common_secret";
  }
  if (!inner.startsWith("PRIVATE_")) {
    return "unknown";
  }
  const rest = inner.slice("PRIVATE_".length).replace(/_[0-9a-f]{16}$/i, "");
  for (const [tokenKind, kind] of privacyTokenKinds) {
    if (rest === tokenKind) return kind;
  }
  return "unknown";
}

export interface TrajectoryRow {
  id: string;
  requestId: string;
  chip: TrajectoryChip;
  summary: string;
  result: string;
  status: RequestStatus;
  tone: TrajectoryTone;
  startedAt: string;
  endedAt: string | null;
  lane: TrajectoryLane;
  child: boolean;
  turnIndex: number | null;
}

export const TIMELINE_GAP_COLLAPSE_MS = 2000;

export interface TrajectoryTimelinePhase {
  rowId: string;
  chip: TrajectoryChip;
  lane: TrajectoryLane;
  tone: TrajectoryTone;
  summary: string;
  /** Offset from the start of the call this phase belongs to. */
  startMs: number;
  durationMs: number;
  /** The phase has no end yet, so its width is whatever the clock says. */
  open: boolean;
}

export interface TrajectoryTimelineCall {
  requestId: string;
  rowId: string;
  turnRowId: string | null;
  turnIndex: number | null;
  turnFirst: boolean;
  startMs: number;
  durationMs: number;
  tone: TrajectoryTone;
  summary: string;
  result: string;
  phases: TrajectoryTimelinePhase[];
  open: boolean;
}

export type TrajectoryTimelineItem =
  | { kind: "call"; call: TrajectoryTimelineCall }
  | {
      kind: "gap";
      durationMs: number;
      collapsed: boolean;
      turnRowId: string | null;
    };

export interface TrajectoryTimeline {
  startedAtMs: number;
  durationMs: number;
  /** Duration the width scale stays linear up to. See `timelineKneeMs`. */
  kneeMs: number;
  items: TrajectoryTimelineItem[];
  /** At least one call is still running, so the strip needs a clock. */
  open: boolean;
}

const chipByKind: Record<RequestEvent["kind"], TrajectoryChip> = {
  accepted: "CLIENT",
  privacy: "POLICY",
  routed: "ROUTE",
  upstream: "UPSTREAM",
  restore: "RESTORE",
  completed: "RESULT",
};

const laneByChip: Record<TrajectoryChip, TrajectoryLane> = {
  TURN: "client",
  CLIENT: "client",
  POLICY: "gateway",
  ROUTE: "gateway",
  UPSTREAM: "upstream",
  RETRY: "upstream",
  RESTORE: "gateway",
  RESULT: "client",
};

export function synthesizeEvents(record: RequestRecord): RequestEvent[] {
  if (record.events.length > 0) return record.events;
  const started = record.started_at;
  const ended = record.completed_at;
  const events: RequestEvent[] = [
    {
      kind: "accepted",
      started_at: started,
      ended_at: ended,
      status: record.status === "pending" ? "pending" : "succeeded",
      summary: [
        record.requested_model ?? i18n.t("records.unspecifiedModel"),
        record.input_protocol,
      ]
        .filter(Boolean)
        .join(" · "),
      attempt_index: record.attempt_index,
    },
  ];
  if (record.privacy_restore) {
    events.push({
      kind: "privacy",
      started_at: started,
      ended_at: ended,
      status: record.status === "blocked" ? "blocked" : "succeeded",
      summary: record.privacy_restore.enabled
        ? `redact · ${record.privacy_restore.mapping_count}`
        : "allow",
      attempt_index: record.attempt_index,
    });
  }
  if (record.service_id || record.route_id) {
    events.push({
      kind: "routed",
      started_at: started,
      ended_at: ended,
      status: "succeeded",
      summary: record.service_id ?? record.route_id ?? "routed",
      attempt_index: record.attempt_index,
    });
  }
  if (record.attempt_index > 0 || record.http_status !== null) {
    events.push({
      kind: "upstream",
      started_at: started,
      ended_at: ended,
      status: record.status,
      summary:
        record.error?.code ??
        (record.http_status !== null
          ? `HTTP ${record.http_status}`
          : "upstream"),
      attempt_index: record.attempt_index,
    });
  }
  if (record.privacy_restore?.enabled) {
    events.push({
      kind: "restore",
      started_at: started,
      ended_at: ended,
      status: record.status,
      summary: `restore · ${record.privacy_restore.restored_count}/${record.privacy_restore.mapping_count}`,
      attempt_index: record.attempt_index,
    });
  }
  events.push({
    kind: "completed",
    started_at: ended ?? started,
    ended_at: ended,
    status: record.status,
    summary: record.error
      ? `${record.error.category} · ${record.error.code}`
      : statusLabel(record.status),
    attempt_index: record.attempt_index,
  });
  return events;
}

/**
 * Groups consecutive root records into user turns the same way Core counts
 * `turn_count`: a new group starts whenever `turn_index` changes, and a null
 * index (no user turns, or a legacy row) is always its own group. Headers are
 * only worth drawing when there is more than one call to group.
 */
export interface TrajectoryTurnGroup {
  turnIndex: number | null;
  records: RequestRecord[];
}

export function groupTurns(turns: RequestRecord[]): TrajectoryTurnGroup[] {
  const groups: TrajectoryTurnGroup[] = [];
  for (const record of turns) {
    const last = groups[groups.length - 1];
    if (
      last &&
      last.turnIndex !== null &&
      record.turn_index !== null &&
      last.turnIndex === record.turn_index
    ) {
      last.records.push(record);
      continue;
    }
    groups.push({ turnIndex: record.turn_index, records: [record] });
  }
  return groups;
}

export function trajectoryRows(
  turns: RequestRecord[],
  childrenByRoot: Record<string, RequestRecord[]>,
): TrajectoryRow[] {
  const rows: TrajectoryRow[] = [];
  const groups = groupTurns(turns);
  const headers = turns.length > 1;
  for (const group of groups) {
    if (headers) {
      rows.push(turnHeaderRow(group));
    }
    for (const turn of group.records) {
      rows.push(...recordRows(turn, childrenByRoot));
    }
  }
  return rows;
}

function turnHeaderRow(group: TrajectoryTurnGroup): TrajectoryRow {
  const first = group.records[0];
  const last = group.records[group.records.length - 1];
  const preview = first.input_preview;
  const label =
    group.turnIndex === null
      ? i18n.t("trajectory.turnHeaderUnnumbered")
      : i18n.t("trajectory.turnHeader", { index: group.turnIndex });
  const status = turnGroupStatus(group.records);
  return {
    id: `${first.id}:turn`,
    requestId: first.id,
    chip: "TURN",
    summary: preview ? `${label} · ${preview}` : label,
    result: i18n.t("trajectory.turnCalls", { count: group.records.length }),
    status,
    tone: statusTone(status),
    startedAt: first.started_at,
    endedAt:
      status === "pending" ? null : (last.completed_at ?? last.started_at),
    lane: "client",
    child: false,
    turnIndex: group.turnIndex,
  };
}

// The header reflects the worst outcome of the calls it groups so a failed
// tool loop is visible before expanding it.
function turnGroupStatus(records: RequestRecord[]): RequestStatus {
  const statuses = new Set(records.map((record) => record.status));
  if (statuses.has("pending")) return "pending";
  if (statuses.has("blocked")) return "blocked";
  if (statuses.has("failed")) return "failed";
  if (statuses.has("cancelled")) return "cancelled";
  return "succeeded";
}

function statusTone(status: RequestStatus): TrajectoryTone {
  switch (status) {
    case "failed":
      return "failed";
    case "blocked":
      return "blocked";
    case "cancelled":
      return "cancelled";
    case "pending":
      return "pending";
    default:
      return "ok";
  }
}

/**
 * The inspector window only receives one record, so the chain has to be
 * rebuilt from that record's events. Child retries are a different request
 * and stay out of this list.
 */
export function inspectorChainRows(record: RequestRecord): TrajectoryRow[] {
  const child = record.parent_request_id !== null;
  const rows: TrajectoryRow[] = [];
  for (const event of synthesizeEvents(record)) {
    const row = rowFromEvent(record, event, child);
    if (event.kind === "accepted" && record.session_link) {
      row.summary = `${row.summary} · ${i18n.t(
        `trajectory.linkedVia.${record.session_link.kind}`,
      )}`;
    }
    rows.push(row);
  }
  return uniqueRowIds(rows);
}

// Candidates rejected in one pass can share a timestamp, and a row id is
// otherwise kind + time + attempt.
function uniqueRowIds(rows: TrajectoryRow[]): TrajectoryRow[] {
  const seen = new Map<string, number>();
  for (const row of rows) {
    const count = seen.get(row.id) ?? 0;
    seen.set(row.id, count + 1);
    if (count > 0) row.id = `${row.id}:${count}`;
  }
  return rows;
}

function recordRows(
  turn: RequestRecord,
  childrenByRoot: Record<string, RequestRecord[]>,
): TrajectoryRow[] {
  const rows = inspectorChainRows(turn);
  const children = childrenByRoot[turn.id] ?? [];
  children.forEach((child, index) => {
    const childRows: TrajectoryRow[] = [];
    for (const event of synthesizeEvents(child)) {
      const row = rowFromEvent(child, event, true);
      if (event.kind === "upstream") {
        row.chip = "RETRY";
        row.summary = i18n.t("trajectory.childRequest", {
          index: index + 1,
          summary: row.summary,
        });
      }
      childRows.push(row);
    }
    rows.push(...uniqueRowIds(childRows));
  });
  return rows;
}

// Records stored before the gateway settled the accepted phase keep it at
// pending for good, which painted the client row of a call that finished half
// an hour ago as still running. Acceptance succeeded the moment the call
// reached a terminal state; the failure lives on the later phases.
function settledEvent(
  record: RequestRecord,
  event: RequestEvent,
): RequestEvent {
  if (
    event.kind !== "accepted" ||
    event.status !== "pending" ||
    record.status === "pending"
  ) {
    return event;
  }
  return { ...event, status: "succeeded" };
}

// Core writes call usage into upstream and result summaries as "in → out".
const USAGE_SEGMENT = /^(\d+) → (\d+)$/;

/** Spell out the bare "in → out" token segment with input/output labels. */
export function readableUsageSummary(summary: string): string {
  return summary
    .split(" · ")
    .map((part) => {
      const match = USAGE_SEGMENT.exec(part);
      if (!match) return part;
      return i18n.t("trajectory.tokenUsage", {
        input: formatExactNumber(Number(match[1])),
        output: formatExactNumber(Number(match[2])),
      });
    })
    .join(" · ");
}

function rowFromEvent(
  record: RequestRecord,
  rawEvent: RequestEvent,
  child: boolean,
): TrajectoryRow {
  const event = settledEvent(record, rawEvent);
  const chip =
    child && event.kind === "upstream" ? "RETRY" : chipByKind[event.kind];
  const summary =
    event.kind === "upstream" || event.kind === "completed"
      ? readableUsageSummary(event.summary)
      : event.summary;
  return {
    id: `${record.id}:${event.kind}:${event.started_at}:${event.attempt_index}`,
    requestId: record.id,
    chip,
    summary: summary || chip,
    result: eventResult(record, event),
    status: event.status,
    tone: eventTone(record, event),
    startedAt: event.started_at,
    endedAt: event.ended_at,
    lane: laneByChip[chip],
    child,
    turnIndex: null,
  };
}

/**
 * The gateway marks the whole call cancelled when the client closes the
 * HTTP connection. That is not an upstream or policy failure: the later
 * phases often already have HTTP 200 and tokens. The cancel belongs on
 * the client RESULT lane so the timeline can say which side aborted.
 */
export function clientDisconnect(record: RequestRecord): boolean {
  if (record.status !== "cancelled" || record.error) return false;
  const stop = record.recovery?.stop_reason;
  return stop === undefined || stop === "cancelled";
}

export function clientDisconnectNote(record: RequestRecord): string | null {
  if (!clientDisconnect(record)) return null;
  if (record.http_status !== null) {
    return i18n.t("trajectory.clientDisconnectedNote", {
      status: record.http_status,
    });
  }
  return i18n.t("trajectory.clientDisconnectedNoteNoUpstream");
}

export function eventTone(
  record: RequestRecord,
  event: RequestEvent,
): TrajectoryTone {
  if (clientDisconnect(record)) {
    switch (event.kind) {
      case "completed":
        return "cancelled";
      case "upstream":
      case "restore":
        if (record.http_status !== null && record.http_status >= 400) {
          return "failed";
        }
        if (event.status === "pending") return "pending";
        return "ok";
      default:
        break;
    }
  }
  switch (event.status) {
    case "failed":
      return "failed";
    case "blocked":
      return "blocked";
    case "cancelled":
      return "cancelled";
    case "pending":
      return "pending";
    default:
      break;
  }
  if (
    (event.kind === "completed" || event.kind === "upstream") &&
    record.http_status !== null &&
    record.http_status >= 400
  ) {
    return "failed";
  }
  return "ok";
}

function eventResult(record: RequestRecord, event: RequestEvent): string {
  if (event.kind === "completed" && clientDisconnect(record)) {
    return i18n.t("trajectory.clientDisconnected");
  }
  if (event.kind === "completed" || event.kind === "upstream") {
    if (record.error && event.status !== "pending") {
      return `${record.error.category} · ${record.error.code}`;
    }
    if (record.http_status !== null) {
      return `HTTP ${record.http_status}`;
    }
  }
  return statusLabel(event.status);
}

export function trajectoryTimeline(
  allRows: TrajectoryRow[],
  nowMs: number,
): TrajectoryTimeline {
  const grouped = new Map<
    string,
    { rows: TrajectoryRow[]; turn: TrajectoryRow | null }
  >();
  const order: string[] = [];
  let lastTurn: TrajectoryRow | null = null;
  for (const row of allRows) {
    if (row.chip === "TURN") {
      lastTurn = row;
      continue;
    }
    if (row.child) continue;
    let group = grouped.get(row.requestId);
    if (!group) {
      group = { rows: [], turn: lastTurn };
      grouped.set(row.requestId, group);
      order.push(row.requestId);
    }
    group.rows.push(row);
  }

  const drafted = order
    .map((requestId) => grouped.get(requestId)!)
    .map((group) => draftTimelineCall(group.rows, group.turn, nowMs))
    .filter((call): call is DraftedCall => call !== null);

  const startedAtMs =
    drafted.length > 0
      ? Math.min(...drafted.map((call) => call.startAbs))
      : nowMs;
  const endedAtMs =
    drafted.length > 0
      ? Math.max(...drafted.map((call) => call.endAbs))
      : nowMs;
  const durationMs = Math.max(1, endedAtMs - startedAtMs);
  if (drafted.length === 0) {
    return {
      startedAtMs,
      durationMs,
      kneeMs: TIMELINE_KNEE_MS,
      items: [],
      open: false,
    };
  }

  drafted.sort((left, right) => left.startAbs - right.startAbs);
  const seenTurns = new Set<string>();
  const calls: TrajectoryTimelineCall[] = drafted.map((draft) => {
    const turnFirst = Boolean(
      draft.turnRowId && !seenTurns.has(draft.turnRowId),
    );
    if (draft.turnRowId) seenTurns.add(draft.turnRowId);
    return {
      requestId: draft.requestId,
      rowId: draft.rowId,
      turnRowId: draft.turnRowId,
      turnIndex: draft.turnIndex,
      turnFirst,
      startMs: Math.max(0, draft.startAbs - startedAtMs),
      durationMs: Math.max(1, draft.endAbs - draft.startAbs),
      tone: draft.tone,
      summary: draft.summary,
      result: draft.result,
      phases: draft.phases,
      open: draft.open,
    };
  });

  const items: TrajectoryTimelineItem[] = [];
  calls.forEach((call, index) => {
    if (index > 0) {
      const previous = calls[index - 1]!;
      const raw = call.startMs - (previous.startMs + previous.durationMs);
      const gapMs = Math.max(0, raw);
      items.push({
        kind: "gap",
        durationMs: gapMs,
        collapsed: gapMs > TIMELINE_GAP_COLLAPSE_MS,
        turnRowId:
          previous.turnRowId && previous.turnRowId === call.turnRowId
            ? previous.turnRowId
            : null,
      });
    }
    items.push({ kind: "call", call });
  });

  return {
    startedAtMs,
    durationMs,
    kneeMs: timelineKneeMs(calls.map((call) => call.durationMs)),
    items,
    open: calls.some((call) => call.open),
  };
}

/**
 * Moves the clock forward on the calls that are still running, leaving every
 * settled call and gap at its previous object identity.
 *
 * `trajectoryTimeline` regroups, re-sorts and re-derives every phase of every
 * call. For a 216-call session that is a lot of work to discover that one
 * upstream wait grew by 100 ms, and it hands React 216 brand-new call objects,
 * so no memoized lane cell can bail out. Rebuilding only the open calls lets
 * the rest of the strip skip the re-render entirely.
 *
 * `kneeMs` deliberately stays put. The knee is the session's own scale, and
 * letting an in-flight call drag it would re-lay out all 216 columns on every
 * tick while the finished bars visibly twitch.
 */
export function extendPendingTimeline(
  timeline: TrajectoryTimeline,
  nowMs: number,
): TrajectoryTimeline {
  if (!timeline.open) return timeline;

  const startedAtMs = timeline.startedAtMs;
  let changed = false;
  const items = timeline.items.map((item) => {
    if (item.kind !== "call" || !item.call.open) return item;
    const call = extendTimelineCall(item.call, startedAtMs, nowMs);
    if (call === item.call) return item;
    changed = true;
    return { kind: "call", call } as TrajectoryTimelineItem;
  });
  if (!changed) return timeline;

  // A gap is the seam between two calls, so a call that grew closes the gap
  // behind it. An open call is normally the last one, but a session can have
  // two in flight at once and the seam still has to be honest.
  let endedAtMs = startedAtMs;
  let previousEndMs = 0;
  for (let index = 0; index < items.length; index += 1) {
    const item = items[index]!;
    if (item.kind === "call") {
      previousEndMs = item.call.startMs + item.call.durationMs;
      endedAtMs = Math.max(endedAtMs, startedAtMs + previousEndMs);
      continue;
    }
    const next = items[index + 1];
    if (!next || next.kind !== "call") continue;
    const gapMs = Math.max(0, next.call.startMs - previousEndMs);
    if (gapMs === item.durationMs) continue;
    items[index] = {
      kind: "gap",
      durationMs: gapMs,
      collapsed: gapMs > TIMELINE_GAP_COLLAPSE_MS,
      turnRowId: item.turnRowId,
    };
  }

  return {
    ...timeline,
    durationMs: Math.max(1, endedAtMs - startedAtMs),
    items,
  };
}

function extendTimelineCall(
  call: TrajectoryTimelineCall,
  startedAtMs: number,
  nowMs: number,
): TrajectoryTimelineCall {
  const callStartAbs = startedAtMs + call.startMs;
  let changed = false;
  let endMs = 0;
  const phases = call.phases.map((phase) => {
    const durationMs = phase.open
      ? Math.max(0, nowMs - (callStartAbs + phase.startMs))
      : phase.durationMs;
    endMs = Math.max(endMs, phase.startMs + durationMs);
    if (durationMs === phase.durationMs) return phase;
    changed = true;
    return { ...phase, durationMs };
  });
  const durationMs = Math.max(1, endMs);
  if (!changed && durationMs === call.durationMs) return call;
  return { ...call, durationMs, phases };
}

export const TIMELINE_KNEE_MS = 5000;

/**
 * Width still means duration, but a single slow call must not eat the axis: a
 * 232 s upstream wait is 75x the median and would leave the short calls at one
 * pixel. Below the knee the scale is the raw duration; above it the excess is
 * compressed logarithmically. Both sides have derivative 1 at the knee, so the
 * scale stays continuous and short calls keep their true proportions.
 */
export function timelineWeight(
  durationMs: number,
  kneeMs: number = TIMELINE_KNEE_MS,
): number {
  if (!Number.isFinite(durationMs) || durationMs <= 0) return 0;
  const knee =
    Number.isFinite(kneeMs) && kneeMs > 0 ? kneeMs : TIMELINE_KNEE_MS;
  if (durationMs <= knee) return durationMs;
  return knee * (1 + Math.log(durationMs / knee));
}

/**
 * The knee has to follow the session, not the clock. A fixed 5 s knee reads
 * every call of a slow session as "long": a session of 7 s to 103 s calls puts
 * all twelve in the logarithmic tail, where the curve is nearly flat, and a
 * 14x spread in waiting time collapses into a 3x spread in width. Anchoring
 * the knee at the median keeps half the calls in the linear region, so the
 * compression only spends itself on the session's own outliers.
 *
 * The floor is the fixed knee, so the knee only ever moves up. A higher knee
 * is a scale closer to linear, which can only widen the contrast between
 * calls, and fast sessions (everything under 5 s) draw exactly as before.
 */
export function timelineKneeMs(durationsMs: number[]): number {
  const sorted = durationsMs
    .filter((duration) => Number.isFinite(duration) && duration > 0)
    .sort((left, right) => left - right);
  if (sorted.length === 0) return TIMELINE_KNEE_MS;
  const middle = sorted.length / 2;
  const median =
    sorted.length % 2 === 0
      ? (sorted[middle - 1]! + sorted[middle]!) / 2
      : sorted[Math.floor(middle)]!;
  return Math.max(TIMELINE_KNEE_MS, median);
}

export interface TrajectoryCallAnchor {
  requestId: string;
}

export interface TrajectoryCallColumn {
  requestId: string;
  offset: number;
  width: number;
}

export interface TrajectoryCallProgress {
  requestId: string;
  fraction: number;
}

/**
 * Which call the list is pointing at for a fractional row offset (scrollTop /
 * row height). TURN headers share the first call's request id, so a header
 * lands on that call at fraction 0. The fraction is how far the offset sits
 * through that call's own rows, so the strip can ease instead of jumping at
 * each request boundary.
 */
export function callProgressAtListOffset(
  rows: readonly TrajectoryCallAnchor[],
  offset: number,
): TrajectoryCallProgress | null {
  if (rows.length === 0) return null;
  const index = Math.min(rows.length, Math.max(0, offset));
  const requestId =
    rows[Math.min(rows.length - 1, Math.floor(index))]!.requestId;
  const { start, length } = callRowSpan(rows, requestId);
  if (length === 0) return { requestId, fraction: 0 };
  const fraction = Math.min(1, Math.max(0, (index - start) / length));
  return { requestId, fraction };
}

/**
 * ScrollLeft that puts the same call (and the same progress through it) at the
 * leading edge of the strip. Clamped so rubber-band overscroll cannot run past
 * the end.
 */
export function scrollLeftForCall(
  columns: readonly TrajectoryCallColumn[],
  requestId: string,
  fraction: number,
  maxScroll: number,
): number {
  if (maxScroll <= 0) return 0;
  const column = columns.find((item) => item.requestId === requestId);
  if (!column) return 0;
  const width = column.width > 0 ? column.width : 0;
  const raw = column.offset + Math.min(1, Math.max(0, fraction)) * width;
  return Math.min(maxScroll, Math.max(0, raw));
}

/** Inverse of `scrollLeftForCall`: which call the strip's leading edge is on. */
export function callProgressAtScrollLeft(
  columns: readonly TrajectoryCallColumn[],
  scrollLeft: number,
): TrajectoryCallProgress | null {
  if (columns.length === 0) return null;
  const x = Math.max(0, scrollLeft);
  for (let index = 0; index < columns.length; index += 1) {
    const column = columns[index]!;
    const end = column.offset + column.width;
    const last = index === columns.length - 1;
    if (x < end || last) {
      const width = column.width > 0 ? column.width : 1;
      const fraction = Math.min(1, Math.max(0, (x - column.offset) / width));
      return { requestId: column.requestId, fraction };
    }
  }
  return { requestId: columns[0]!.requestId, fraction: 0 };
}

/** List scrollTop that shows the same call progress `callProgressAtListOffset` would read. */
export function listOffsetForCall(
  rows: readonly TrajectoryCallAnchor[],
  requestId: string,
  fraction: number,
  rowHeight: number,
): number {
  if (rowHeight <= 0) return 0;
  const { start, length } = callRowSpan(rows, requestId);
  if (length === 0) return 0;
  return (start + Math.min(1, Math.max(0, fraction)) * length) * rowHeight;
}

/**
 * Project across the complete scroll ranges through a shared call position.
 * Aligning leading edges directly clamps the shorter axis too early: dragging
 * its scrollbar to the end could leave the other pane halfway through a
 * conversation. A moving anchor spans the full content from start to finish,
 * including the content inside each viewport, so both ends remain reachable.
 */
export function timelineScrollForList(
  rows: readonly TrajectoryCallAnchor[],
  columns: readonly TrajectoryCallColumn[],
  listOffset: number,
  listMaxScroll: number,
  timelineMaxScroll: number,
): number {
  const last = columns[columns.length - 1];
  const timelineExtent = last ? last.offset + last.width : 0;
  if (listMaxScroll <= 0 || timelineMaxScroll <= 0 || timelineExtent <= 0)
    return 0;
  const ratio = Math.min(1, Math.max(0, listOffset / listMaxScroll));
  const progress = callProgressAtListOffset(rows, ratio * rows.length);
  if (!progress) return 0;
  const position = scrollLeftForCall(
    columns,
    progress.requestId,
    progress.fraction,
    timelineExtent,
  );
  return (position / timelineExtent) * timelineMaxScroll;
}

/** Inverse projection for a user scrolling the timeline. */
export function listScrollForTimeline(
  rows: readonly TrajectoryCallAnchor[],
  columns: readonly TrajectoryCallColumn[],
  timelineOffset: number,
  timelineMaxScroll: number,
  listMaxScroll: number,
): number {
  const last = columns[columns.length - 1];
  const timelineExtent = last ? last.offset + last.width : 0;
  if (
    rows.length === 0 ||
    timelineMaxScroll <= 0 ||
    listMaxScroll <= 0 ||
    timelineExtent <= 0
  )
    return 0;
  const ratio = Math.min(1, Math.max(0, timelineOffset / timelineMaxScroll));
  const progress = callProgressAtScrollLeft(columns, ratio * timelineExtent);
  if (!progress) return 0;
  const position = listOffsetForCall(
    rows,
    progress.requestId,
    progress.fraction,
    1,
  );
  return (position / rows.length) * listMaxScroll;
}

function callRowSpan(
  rows: readonly TrajectoryCallAnchor[],
  requestId: string,
): { start: number; length: number } {
  let start = -1;
  let end = -1;
  for (let index = 0; index < rows.length; index += 1) {
    if (rows[index]!.requestId !== requestId) continue;
    if (start === -1) start = index;
    end = index;
  }
  if (start === -1) return { start: 0, length: 0 };
  return { start, length: end - start + 1 };
}

interface DraftedCall {
  requestId: string;
  rowId: string;
  turnRowId: string | null;
  turnIndex: number | null;
  startAbs: number;
  endAbs: number;
  tone: TrajectoryTone;
  summary: string;
  result: string;
  phases: TrajectoryTimelinePhase[];
  open: boolean;
}

function draftTimelineCall(
  rows: TrajectoryRow[],
  turn: TrajectoryRow | null,
  nowMs: number,
): DraftedCall | null {
  if (rows.length === 0) return null;
  const timed = rows
    .map((row, index) => {
      const startAbs = rowStartAbs(row, nowMs);
      const endAbs = Math.max(startAbs, rowEndAbs(row, nowMs));
      return { row, index, startAbs, endAbs };
    })
    .sort(
      (left, right) =>
        left.startAbs - right.startAbs || left.index - right.index,
    );
  const first = timed[0]!;
  const last = timed[timed.length - 1]!;
  const resultRow = timed.find((item) => item.row.chip === "RESULT") ?? last;
  const startAbs = Math.min(...timed.map((item) => item.startAbs));
  return {
    requestId: first.row.requestId,
    rowId: first.row.id,
    turnRowId: turn?.id ?? null,
    turnIndex: turn?.turnIndex ?? null,
    startAbs,
    endAbs: Math.max(...timed.map((item) => item.endAbs)),
    tone: worstTone(rows.map((row) => row.tone)),
    summary: first.row.summary,
    result: resultRow.row.result,
    phases: timed.map((item) => ({
      rowId: item.row.id,
      chip: item.row.chip,
      lane: item.row.lane,
      tone: item.row.tone,
      summary: item.row.summary,
      startMs: Math.max(0, item.startAbs - startAbs),
      durationMs: Math.max(0, item.endAbs - item.startAbs),
      open: !item.row.endedAt,
    })),
    open: rows.some((row) => !row.endedAt),
  };
}

function rowStartAbs(row: TrajectoryRow, nowMs: number): number {
  const start = Date.parse(row.startedAt);
  return Number.isNaN(start) ? nowMs : start;
}

function rowEndAbs(row: TrajectoryRow, nowMs: number): number {
  if (!row.endedAt) return nowMs;
  const end = Date.parse(row.endedAt);
  return Number.isNaN(end) ? nowMs : end;
}

const toneRank: Record<TrajectoryTone, number> = {
  failed: 0,
  blocked: 1,
  pending: 2,
  cancelled: 3,
  ok: 4,
};

function worstTone(tones: TrajectoryTone[]): TrajectoryTone {
  let worst: TrajectoryTone = "ok";
  for (const tone of tones) {
    if (toneRank[tone] < toneRank[worst]) worst = tone;
  }
  return worst;
}
