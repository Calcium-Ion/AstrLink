import { i18n } from "./i18n";
import { formatDuration, liveDurationMs } from "./request-live-model";
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
}

export interface TrajectoryLaneSegment {
  lane: TrajectoryLane;
  startMs: number;
  endMs: number;
  status: RequestStatus;
  tone: TrajectoryTone;
}

export interface TrajectoryLanes {
  startedAtMs: number;
  durationMs: number;
  segments: TrajectoryLaneSegment[];
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
      summary: [record.requested_model ?? i18n.t("records.unspecifiedModel"), record.input_protocol]
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
        (record.http_status !== null ? `HTTP ${record.http_status}` : "upstream"),
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
    endedAt: status === "pending" ? null : last.completed_at ?? last.started_at,
    lane: "client",
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

function recordRows(
  turn: RequestRecord,
  childrenByRoot: Record<string, RequestRecord[]>,
): TrajectoryRow[] {
  const rows: TrajectoryRow[] = [];
  for (const event of synthesizeEvents(turn)) {
    const row = rowFromEvent(turn, event, turn.parent_request_id !== null);
    if (event.kind === "accepted" && turn.session_link) {
      row.summary = `${row.summary} · ${i18n.t(
        `trajectory.linkedVia.${turn.session_link.kind}`,
      )}`;
    }
    rows.push(row);
  }
  const children = childrenByRoot[turn.id] ?? [];
  children.forEach((child, index) => {
    for (const event of synthesizeEvents(child)) {
      const row = rowFromEvent(child, event, true);
      if (event.kind === "upstream") {
        row.chip = "RETRY";
        row.summary = i18n.t("trajectory.childRequest", {
          index: index + 1,
          summary: row.summary,
        });
      }
      rows.push(row);
    }
  });
  return rows;
}

function rowFromEvent(
  record: RequestRecord,
  event: RequestEvent,
  child: boolean,
): TrajectoryRow {
  const chip =
    child && event.kind === "upstream" ? "RETRY" : chipByKind[event.kind];
  return {
    id: `${record.id}:${event.kind}:${event.started_at}:${event.attempt_index}`,
    requestId: record.id,
    chip,
    summary: event.summary || chip,
    result: eventResult(record, event),
    status: event.status,
    tone: eventTone(record, event),
    startedAt: event.started_at,
    endedAt: event.ended_at,
    lane: laneByChip[chip],
  };
}

export function eventTone(
  record: RequestRecord,
  event: RequestEvent,
): TrajectoryTone {
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

export function trajectoryLanes(
  allRows: TrajectoryRow[],
  nowMs: number,
): TrajectoryLanes {
  // Turn headers span every call they group; drawing them would paint over
  // the per-call segments on the client lane.
  const rows = allRows.filter((row) => row.chip !== "TURN");
  const starts = rows
    .map((row) => Date.parse(row.startedAt))
    .filter((value) => !Number.isNaN(value));
  const startedAtMs = starts.length > 0 ? Math.min(...starts) : nowMs;
  const ends = rows.map((row) => {
    const ended = row.endedAt ? Date.parse(row.endedAt) : nowMs;
    return Number.isNaN(ended) ? nowMs : ended;
  });
  const endedAtMs = ends.length > 0 ? Math.max(...ends) : nowMs;
  const durationMs = Math.max(1, endedAtMs - startedAtMs);
  return {
    startedAtMs,
    durationMs,
    segments: rows.map((row) => {
      const start = Date.parse(row.startedAt);
      const end = row.endedAt ? Date.parse(row.endedAt) : nowMs;
      return {
        lane: row.lane,
        startMs: Number.isNaN(start) ? 0 : Math.max(0, start - startedAtMs),
        endMs: Number.isNaN(end)
          ? durationMs
          : Math.max(0, Math.min(durationMs, end - startedAtMs)),
        status: row.status,
        tone: row.tone,
      };
    }),
  };
}

export function sessionDurationMs(
  startedAt: string,
  lastStartedAt: string,
  turns: RequestRecord[],
  nowMs: number,
): number {
  const start = Date.parse(startedAt);
  if (Number.isNaN(start)) return 0;
  const lastTurn = turns[turns.length - 1];
  if (lastTurn) {
    return Math.max(0, start ? liveDurationMs(lastTurn, nowMs) + (Date.parse(lastTurn.started_at) - start) : liveDurationMs(lastTurn, nowMs));
  }
  const last = Date.parse(lastStartedAt);
  return Math.max(0, (Number.isNaN(last) ? nowMs : last) - start);
}

export function formatSessionDuration(
  startedAt: string,
  lastStartedAt: string,
  turns: RequestRecord[],
  nowMs: number,
): string {
  return formatDuration(sessionDurationMs(startedAt, lastStartedAt, turns, nowMs));
}
