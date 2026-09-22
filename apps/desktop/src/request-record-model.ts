import { i18n } from "./i18n";

export type RequestStatus =
  | "pending"
  | "succeeded"
  | "failed"
  | "cancelled"
  | "blocked";

/**
 * Outcome of a whole conversation. `interrupted` has no record-level
 * counterpart: the session delivered answers and then the client abandoned
 * the last stream, so a stopped agent loop is not painted as if nothing ran.
 */
export type SessionStatus = RequestStatus | "interrupted";

export interface RequestUsage {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
}

export interface RequestErrorSummary {
  category: string;
  code: string;
  message: string;
  retryable: boolean;
}

export interface RequestAuditSummary {
  request_body_captured: boolean;
  response_content_captured: boolean;
  request_body_truncated: boolean;
  response_content_truncated: boolean;
  upstream_request_body_captured: boolean;
  upstream_response_content_captured: boolean;
  upstream_request_body_truncated: boolean;
  upstream_response_content_truncated: boolean;
}

export interface PrivacyHitCount {
  kind: string;
  count: number;
}

export interface PrivacyRestoreSummary {
  enabled: boolean;
  mapping_count: number;
  restored_count: number;
  /**
   * Split of restored_count by channel. Both are zero on records written before
   * the split, which report only the total.
   */
  visible_restored_count: number;
  tool_argument_restored_count: number;
  fallback_count: number;
  hits?: PrivacyHitCount[];
}

export type RequestEventKind =
  | "accepted"
  | "privacy"
  | "routed"
  | "upstream"
  | "restore"
  | "completed";

export interface RequestEvent {
  kind: RequestEventKind;
  started_at: string;
  ended_at: string | null;
  status: RequestStatus;
  summary: string;
  attempt_index: number;
}

/**
 * How a session cursor value came to exist. `explicit` cursors are ids the
 * client named on purpose (matched across tokens); `echo_id` and
 * `fingerprint` are inferred from replayed history and matched only within
 * the same access token and a time window.
 */
export type SessionCursorKind = "explicit" | "echo_id" | "fingerprint";

export type SessionCursorDirection = "in" | "out";

export interface SessionCursor {
  kind: SessionCursorKind;
  direction: SessionCursorDirection;
  value: string;
}

/** How a record joined its session; null when it started the session. */
export interface SessionLink {
  kind: SessionCursorKind;
  value: string;
}

export interface RequestRecovery {
 path_id?:string;path_name?:string;path_version?:string;step_id?:string;
  upstream_model?: string;
  action?: "retry" | "failover";
  reason?: string;
  delay_ms: number;
  stop_reason?: string;
}

export interface RequestRecord {
  recovery?: RequestRecovery;
  id: string;
  parent_request_id: string | null;
  attempt_index: number;
  child_count: number;
  started_at: string;
  completed_at: string | null;
  status: RequestStatus;
  input_protocol: string;
  requested_model: string | null;
  reasoning_effort?: string | null;
  streaming: boolean;
  route_id: string | null;
  service_id: string | null;
  local_access_token_id: string | null;
  http_status: number | null;
  latency_ms: number | null;
  first_token_ms?: number | null;
  usage: RequestUsage | null;
  error: RequestErrorSummary | null;
  audit: RequestAuditSummary;
  privacy_restore: PrivacyRestoreSummary | null;
  session_id: string | null;
  previous_response_id: string | null;
  output_response_id: string | null;
  input_preview: string | null;
  /**
   * 1-based user turn within the session. Every model call of one agent loop
   * shares the same value; null for protocols without user turns and for
   * records written before linking existed.
   */
  turn_index: number | null;
  session_link: SessionLink | null;
  cursors: SessionCursor[];
  events: RequestEvent[];
}

export const emptyTrajectoryFields = {
  session_id: null,
  previous_response_id: null,
  output_response_id: null,
  input_preview: null,
  turn_index: null,
  session_link: null,
  cursors: [] as SessionCursor[],
  events: [] as RequestEvent[],
};

export interface RequestSession {
  id: string;
  title: string;
  started_at: string;
  last_started_at: string;
  completed_at: string | null;
  duration_ms: number;
  tool_duration_ms?: number | null;
  average_ttft_ms?: number | null;
  output_tokens_per_second?: number | null;
  active_request_starts: string[];
  turn_count: number;
  call_count: number;
  status: SessionStatus;
  requested_model: string | null;
  reasoning_effort?: string | null;
  input_protocol: string;
  service_id: string | null;
  local_access_token_id: string | null;
}

export interface RequestSessionPage {
  items: RequestSession[];
  next_cursor: string | null;
}

export interface RequestSessionDetail extends RequestSession {
  turns: RequestRecord[];
}

export type RequestSessionKind = "inference" | "discovery";

export function isModelDiscoveryProtocol(protocol: string): boolean {
  return protocol === "openai.models" || protocol === "google.models";
}

export interface RequestSessionListQuery {
  kind?: RequestSessionKind;
  limit?: number;
  cursor?: string;
  from?: string;
  to?: string;
  protocol?: string;
  service_id?: string;
  local_access_token_id?: string;
  // Filters on record status, not on the aggregated session outcome, so it
  // cannot name a session-only status such as interrupted.
  status?: RequestStatus;
}

export interface RequestRecordPage {
  items: RequestRecord[];
  next_cursor: string | null;
}

export interface RequestRecordListQuery {
  limit?: number;
  cursor?: string;
  from?: string;
  to?: string;
  protocol?: string;
  service_id?: string;
  local_access_token_id?: string;
  status?: RequestStatus;
}

export interface AuditContentPart {
  media_type: string;
  content: string;
  truncated: boolean;
  captured_bytes: number;
}

export interface AuditHeader {
  name: string;
  value: string;
  redacted: boolean;
}

export interface AuditHTTPMeta {
  method: string;
  url: string;
  http_version: string;
  request_headers: AuditHeader[];
  response_status: number | null;
  response_headers: AuditHeader[];
}

export interface AuditContent {
  request_id: string;
  http_meta: AuditHTTPMeta | null;
  request_body: AuditContentPart | null;
  response_content: AuditContentPart | null;
  upstream_http_meta: AuditHTTPMeta | null;
  upstream_request_body: AuditContentPart | null;
  upstream_response_content: AuditContentPart | null;
}

export interface PurgeResult {
  deleted_records: number;
  deleted_audit_blobs: number;
}

type JsonObject = Record<string, unknown>;

const statuses = new Set<RequestStatus>([
  "pending",
  "succeeded",
  "failed",
  "cancelled",
  "blocked",
]);

const sessionStatuses = new Set<SessionStatus>([...statuses, "interrupted"]);

function invalid(path: string, message: string): never {
  throw new Error(`请求记录数据无效（${path}）：${message}`);
}

function objectAt(value: unknown, path: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalid(path, "应为对象");
  }
  return value as JsonObject;
}

function stringAt(value: unknown, path: string): string {
  if (typeof value !== "string") {
    return invalid(path, "应为字符串");
  }
  return value;
}

function nullableStringAt(value: unknown, path: string): string | null {
  if (value === null) return null;
  return stringAt(value, path);
}

function intAt(value: unknown, path: string): number {
  if (typeof value !== "number" || !Number.isInteger(value)) {
    return invalid(path, "应为整数");
  }
  return value;
}

function nullableIntAt(value: unknown, path: string): number | null {
  if (value === null) return null;
  return intAt(value, path);
}

function boolAt(value: unknown, path: string): boolean {
  if (typeof value !== "boolean") {
    return invalid(path, "应为布尔值");
  }
  return value;
}

function parseUsage(value: unknown, path: string): RequestUsage | null {
  if (value === null) return null;
  const usage = objectAt(value, path);
  const result: RequestUsage = {
    input_tokens: intAt(usage.input_tokens, `${path}.input_tokens`),
    output_tokens: intAt(usage.output_tokens, `${path}.output_tokens`),
    total_tokens: intAt(usage.total_tokens, `${path}.total_tokens`),
  };
  if (Object.hasOwn(usage, "cache_read_tokens")) {
    result.cache_read_tokens = intAt(
      usage.cache_read_tokens,
      `${path}.cache_read_tokens`,
    );
  } else if (Object.hasOwn(usage, "cached_input_tokens")) {
    // Legacy records stored a single cached_input_tokens field.
    result.cache_read_tokens = intAt(
      usage.cached_input_tokens,
      `${path}.cached_input_tokens`,
    );
  }
  if (Object.hasOwn(usage, "cache_write_tokens")) {
    result.cache_write_tokens = intAt(
      usage.cache_write_tokens,
      `${path}.cache_write_tokens`,
    );
  }
  return result;
}

function parseError(
  value: unknown,
  path: string,
): RequestErrorSummary | null {
  if (value === null) return null;
  const error = objectAt(value, path);
  return {
    category: stringAt(error.category, `${path}.category`),
    code: stringAt(error.code, `${path}.code`),
    message: stringAt(error.message, `${path}.message`),
    retryable: boolAt(error.retryable, `${path}.retryable`),
  };
}

function optionalBoolAt(value: unknown, path: string, fallback = false): boolean {
  if (value === undefined) return fallback;
  return boolAt(value, path);
}

function parseAuditSummary(value: unknown, path: string): RequestAuditSummary {
  const audit = objectAt(value, path);
  return {
    request_body_captured: boolAt(
      audit.request_body_captured,
      `${path}.request_body_captured`,
    ),
    response_content_captured: boolAt(
      audit.response_content_captured,
      `${path}.response_content_captured`,
    ),
    request_body_truncated: boolAt(
      audit.request_body_truncated,
      `${path}.request_body_truncated`,
    ),
    response_content_truncated: boolAt(
      audit.response_content_truncated,
      `${path}.response_content_truncated`,
    ),
    upstream_request_body_captured: optionalBoolAt(
      audit.upstream_request_body_captured,
      `${path}.upstream_request_body_captured`,
    ),
    upstream_response_content_captured: optionalBoolAt(
      audit.upstream_response_content_captured,
      `${path}.upstream_response_content_captured`,
    ),
    upstream_request_body_truncated: optionalBoolAt(
      audit.upstream_request_body_truncated,
      `${path}.upstream_request_body_truncated`,
    ),
    upstream_response_content_truncated: optionalBoolAt(
      audit.upstream_response_content_truncated,
      `${path}.upstream_response_content_truncated`,
    ),
  };
}

const privacyHitKinds = new Set([
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
]);

function parsePrivacyRestore(
  value: unknown,
  path: string,
): PrivacyRestoreSummary | null {
  if (value === null || value === undefined) return null;
  const summary = objectAt(value, path);
  const mappingCount = intAt(summary.mapping_count, `${path}.mapping_count`);
  const restoredCount = intAt(summary.restored_count, `${path}.restored_count`);
  const fallbackCount = intAt(summary.fallback_count, `${path}.fallback_count`);
  const visibleRestoredCount =
    summary.visible_restored_count === undefined
      ? 0
      : intAt(summary.visible_restored_count, `${path}.visible_restored_count`);
  const toolArgumentRestoredCount =
    summary.tool_argument_restored_count === undefined
      ? 0
      : intAt(
          summary.tool_argument_restored_count,
          `${path}.tool_argument_restored_count`,
        );
  if (
    mappingCount < 0 ||
    restoredCount < 0 ||
    fallbackCount < 0 ||
    visibleRestoredCount < 0 ||
    toolArgumentRestoredCount < 0
  ) {
    return invalid(path, "计数不得为负数");
  }
  if (visibleRestoredCount + toolArgumentRestoredCount > restoredCount) {
    return invalid(path, "分通道还原计数不得超过总数");
  }
  const parsed: PrivacyRestoreSummary = {
    enabled: boolAt(summary.enabled, `${path}.enabled`),
    mapping_count: mappingCount,
    restored_count: restoredCount,
    visible_restored_count: visibleRestoredCount,
    tool_argument_restored_count: toolArgumentRestoredCount,
    fallback_count: fallbackCount,
  };
  if (summary.hits !== undefined) {
    parsed.hits = parsePrivacyHits(summary.hits, `${path}.hits`);
  }
  return parsed;
}

function parsePrivacyHits(value: unknown, path: string): PrivacyHitCount[] {
  if (!Array.isArray(value)) {
    return invalid(path, "应为数组");
  }
  const seen = new Set<string>();
  return value.map((item, index) => {
    const hit = objectAt(item, `${path}[${index}]`);
    const kind = stringAt(hit.kind, `${path}[${index}].kind`);
    const count = intAt(hit.count, `${path}[${index}].count`);
    if (!privacyHitKinds.has(kind)) {
      return invalid(`${path}[${index}].kind`, "类别无效");
    }
    if (count < 1) {
      return invalid(`${path}[${index}].count`, "计数必须为正整数");
    }
    if (seen.has(kind)) {
      return invalid(`${path}[${index}].kind`, "类别重复");
    }
    seen.add(kind);
    return { kind, count };
  });
}

function parseRecovery(value: unknown, path: string): RequestRecovery {
  const record = objectAt(value, path);
  const delay = intAt(record.delay_ms, `${path}.delay_ms`);
  if (delay < 0 || delay > 60000) invalid(path, "invalid recovery delay");
  if (record.action !== undefined && record.action !== "retry" && record.action !== "failover") invalid(path, "invalid recovery action");
  const result: RequestRecovery = { delay_ms: delay };
  if (record.action) result.action = record.action as RequestRecovery["action"];
  for (const key of ["upstream_model", "reason", "stop_reason", "path_id", "path_name", "path_version", "step_id"] as const) {
    if (record[key] !== undefined) {
      const text = stringAt(record[key], `${path}.${key}`);
      if ([...text].length > (key === "upstream_model" ? 256 : 128)) invalid(path, "recovery text too long");
      result[key] = text;
    }
  }
  return result;
}

export function parseRequestRecord(value: unknown): RequestRecord {
  return parseRequestRecordAt(value, "$");
}

function parseRequestRecordAt(value: unknown, path: string): RequestRecord {
  const record = objectAt(value, path);
  if (!Object.hasOwn(record, "id")) invalid(`${path}.id`, "缺少字段");
  if (
    typeof record.status !== "string" ||
    !statuses.has(record.status as RequestStatus)
  ) {
    invalid(`${path}.status`, "状态枚举无效");
  }

  const attemptIndex = Object.hasOwn(record, "attempt_index")
    ? intAt(record.attempt_index, `${path}.attempt_index`)
    : 1;
  const childCount = Object.hasOwn(record, "child_count")
    ? intAt(record.child_count, `${path}.child_count`)
    : 0;
  if (attemptIndex < 0) invalid(`${path}.attempt_index`, "不得为负数");
  if (childCount < 0) invalid(`${path}.child_count`, "不得为负数");

  return {
    ...(record.recovery === undefined ? {} : { recovery: parseRecovery(record.recovery, `${path}.recovery`) }),
    id: stringAt(record.id, `${path}.id`),
    parent_request_id: Object.hasOwn(record, "parent_request_id")
      ? nullableStringAt(record.parent_request_id, `${path}.parent_request_id`)
      : null,
    attempt_index: attemptIndex,
    child_count: childCount,
    started_at: stringAt(record.started_at, `${path}.started_at`),
    completed_at: nullableStringAt(record.completed_at, `${path}.completed_at`),
    status: record.status as RequestStatus,
    input_protocol: stringAt(record.input_protocol, `${path}.input_protocol`),
    requested_model: nullableStringAt(
      record.requested_model,
      `${path}.requested_model`,
    ),
    reasoning_effort:
      record.reasoning_effort == null
        ? null
        : nullableStringAt(record.reasoning_effort, `${path}.reasoning_effort`),
    streaming: boolAt(record.streaming, `${path}.streaming`),
    route_id: nullableStringAt(record.route_id, `${path}.route_id`),
    service_id: nullableStringAt(record.service_id, `${path}.service_id`),
    local_access_token_id: nullableStringAt(
      record.local_access_token_id,
      `${path}.local_access_token_id`,
    ),
    http_status: nullableIntAt(record.http_status, `${path}.http_status`),
    latency_ms: nullableIntAt(record.latency_ms, `${path}.latency_ms`),
    first_token_ms: optionalPerformanceNumber(record.first_token_ms, `${path}.first_token_ms`, true),
    usage: parseUsage(record.usage, `${path}.usage`),
    error: parseError(record.error, `${path}.error`),
    audit: parseAuditSummary(record.audit, `${path}.audit`),
    privacy_restore: parsePrivacyRestore(
      record.privacy_restore,
      `${path}.privacy_restore`,
    ),
    session_id: Object.hasOwn(record, "session_id")
      ? nullableStringAt(record.session_id, `${path}.session_id`)
      : null,
    previous_response_id: Object.hasOwn(record, "previous_response_id")
      ? nullableStringAt(
          record.previous_response_id,
          `${path}.previous_response_id`,
        )
      : null,
    output_response_id: Object.hasOwn(record, "output_response_id")
      ? nullableStringAt(record.output_response_id, `${path}.output_response_id`)
      : null,
    input_preview: Object.hasOwn(record, "input_preview")
      ? nullableStringAt(record.input_preview, `${path}.input_preview`)
      : null,
    turn_index: parseTurnIndex(record.turn_index, `${path}.turn_index`),
    session_link: parseSessionLink(record.session_link, `${path}.session_link`),
    cursors: parseSessionCursors(record.cursors, `${path}.cursors`),
    events: parseRequestEvents(record.events, `${path}.events`),
  };
}

const sessionCursorKinds = new Set<SessionCursorKind>([
  "explicit",
  "echo_id",
  "fingerprint",
]);

function sessionCursorKindAt(value: unknown, path: string): SessionCursorKind {
  if (
    typeof value !== "string" ||
    !sessionCursorKinds.has(value as SessionCursorKind)
  ) {
    return invalid(path, "游标类型无效");
  }
  return value as SessionCursorKind;
}

// A core sidecar that predates conversation linking omits these fields; treat
// absence like null so old records still render.
function parseTurnIndex(value: unknown, path: string): number | null {
  if (value === undefined || value === null) return null;
  const turn = intAt(value, path);
  if (turn < 1) return invalid(path, "轮次必须至少为 1");
  return turn;
}

function parseSessionLink(value: unknown, path: string): SessionLink | null {
  if (value === undefined || value === null) return null;
  const link = objectAt(value, path);
  return {
    kind: sessionCursorKindAt(link.kind, `${path}.kind`),
    value: stringAt(link.value, `${path}.value`),
  };
}

function parseSessionCursors(value: unknown, path: string): SessionCursor[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) return invalid(path, "应为数组");
  return value.map((item, index) => {
    const cursor = objectAt(item, `${path}[${index}]`);
    const direction = cursor.direction;
    if (direction !== "in" && direction !== "out") {
      return invalid(`${path}[${index}].direction`, "游标方向无效");
    }
    return {
      kind: sessionCursorKindAt(cursor.kind, `${path}[${index}].kind`),
      direction,
      value: stringAt(cursor.value, `${path}[${index}].value`),
    };
  });
}

const eventKinds = new Set<RequestEventKind>([
  "accepted",
  "privacy",
  "routed",
  "upstream",
  "restore",
  "completed",
]);

function parseRequestEvents(value: unknown, path: string): RequestEvent[] {
  // A record with no trajectory arrives as a nil Go slice, i.e. `null`, and
  // records written before events existed omit the key. Both mean "no events".
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) invalid(path, "应为数组");
  return value.map((item, index) => {
    const event = objectAt(item, `${path}[${index}]`);
    if (
      typeof event.kind !== "string" ||
      !eventKinds.has(event.kind as RequestEventKind)
    ) {
      invalid(`${path}[${index}].kind`, "事件类型无效");
    }
    if (
      typeof event.status !== "string" ||
      !statuses.has(event.status as RequestStatus)
    ) {
      invalid(`${path}[${index}].status`, "状态枚举无效");
    }
    return {
      kind: event.kind as RequestEventKind,
      started_at: stringAt(event.started_at, `${path}[${index}].started_at`),
      ended_at: nullableStringAt(event.ended_at, `${path}[${index}].ended_at`),
      status: event.status as RequestStatus,
      summary: stringAt(event.summary, `${path}[${index}].summary`),
      attempt_index: intAt(
        event.attempt_index,
        `${path}[${index}].attempt_index`,
      ),
    };
  });
}

export function parseRequestSession(value: unknown): RequestSession {
  return parseRequestSessionAt(value, "$");
}

function optionalPerformanceNumber(value: unknown, path: string, integer = false): number | null {
  if (value == null) return null;
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0 || (integer && !Number.isInteger(value))) {
    return invalid(path, "应为非负有效数值");
  }
  return value;
}

function parseRequestSessionAt(value: unknown, path: string): RequestSession {
  const session = objectAt(value, path);
  if (
    typeof session.status !== "string" ||
    !sessionStatuses.has(session.status as SessionStatus)
  ) {
    invalid(`${path}.status`, "状态枚举无效");
  }
  const turnCount = intAt(session.turn_count, `${path}.turn_count`);
  const callCount = intAt(session.call_count, `${path}.call_count`);
  if (turnCount < 1 || callCount < 1) {
    invalid(path, "计数必须至少为 1");
  }
  const durationMs = intAt(session.duration_ms, `${path}.duration_ms`);
  if (durationMs < 0) invalid(`${path}.duration_ms`, "应为非负整数");
  if (!Array.isArray(session.active_request_starts)) {
    invalid(`${path}.active_request_starts`, "应为数组");
  }
  const activeRequestStarts = session.active_request_starts.map((value, index) => {
    const itemPath = `${path}.active_request_starts[${index}]`;
    const started = stringAt(value, itemPath);
    if (!Number.isFinite(Date.parse(started))) invalid(itemPath, "时间戳无效");
    return started;
  });
  return {
    id: stringAt(session.id, `${path}.id`),
    title: stringAt(session.title, `${path}.title`),
    started_at: stringAt(session.started_at, `${path}.started_at`),
    last_started_at: stringAt(session.last_started_at, `${path}.last_started_at`),
    completed_at: nullableStringAt(session.completed_at, `${path}.completed_at`),
    duration_ms: durationMs,
    tool_duration_ms: optionalPerformanceNumber(session.tool_duration_ms, `${path}.tool_duration_ms`, true),
    average_ttft_ms: optionalPerformanceNumber(session.average_ttft_ms, `${path}.average_ttft_ms`),
    output_tokens_per_second: optionalPerformanceNumber(session.output_tokens_per_second, `${path}.output_tokens_per_second`),
    active_request_starts: activeRequestStarts,
    turn_count: turnCount,
    call_count: callCount,
    status: session.status as SessionStatus,
    requested_model: nullableStringAt(
      session.requested_model,
      `${path}.requested_model`,
    ),
    reasoning_effort:
      session.reasoning_effort == null
        ? null
        : nullableStringAt(session.reasoning_effort, `${path}.reasoning_effort`),
    input_protocol: stringAt(session.input_protocol, `${path}.input_protocol`),
    service_id: nullableStringAt(session.service_id, `${path}.service_id`),
    local_access_token_id: nullableStringAt(
      session.local_access_token_id,
      `${path}.local_access_token_id`,
    ),
  };
}

export function parseRequestSessionPage(value: unknown): RequestSessionPage {
  const page = objectAt(value, "$");
  if (!Array.isArray(page.items)) invalid("$.items", "应为数组");
  const nextCursor =
    page.next_cursor === null
      ? null
      : stringAt(page.next_cursor, "$.next_cursor");
  return {
    items: page.items.map((item, index) =>
      parseRequestSessionAt(item, `$.items[${index}]`),
    ),
    next_cursor: nextCursor,
  };
}

export function parseRequestSessionDetail(
  value: unknown,
): RequestSessionDetail {
  const session = parseRequestSessionAt(value, "$");
  const detail = objectAt(value, "$");
  if (!Array.isArray(detail.turns)) invalid("$.turns", "应为数组");
  return {
    ...session,
    turns: detail.turns.map((item, index) =>
      parseRequestRecordAt(item, `$.turns[${index}]`),
    ),
  };
}

export function parseRequestRecordPage(value: unknown): RequestRecordPage {
  const page = objectAt(value, "$");
  if (!Array.isArray(page.items)) invalid("$.items", "应为数组");
  const nextCursor =
    page.next_cursor === null
      ? null
      : stringAt(page.next_cursor, "$.next_cursor");
  return {
    items: page.items.map((item, index) =>
      parseRequestRecordAt(item, `$.items[${index}]`),
    ),
    next_cursor: nextCursor,
  };
}

function parseAuditContentPart(
  value: unknown,
  path: string,
): AuditContentPart | null {
  if (value === null) return null;
  const part = objectAt(value, path);
  return {
    media_type: stringAt(part.media_type, `${path}.media_type`),
    content: stringAt(part.content, `${path}.content`),
    truncated: boolAt(part.truncated, `${path}.truncated`),
    captured_bytes: intAt(part.captured_bytes, `${path}.captured_bytes`),
  };
}

function parseAuditHeader(value: unknown, path: string): AuditHeader {
  const header = objectAt(value, path);
  return {
    name: stringAt(header.name, `${path}.name`),
    value: stringAt(header.value, `${path}.value`),
    redacted: boolAt(header.redacted, `${path}.redacted`),
  };
}

function parseAuditHTTPMeta(
  value: unknown,
  path: string,
): AuditHTTPMeta | null {
  if (value === null) return null;
  const meta = objectAt(value, path);
  if (!Array.isArray(meta.request_headers)) {
    invalid(`${path}.request_headers`, "应为数组");
  }
  if (!Array.isArray(meta.response_headers)) {
    invalid(`${path}.response_headers`, "应为数组");
  }
  return {
    method: stringAt(meta.method, `${path}.method`),
    url: stringAt(meta.url, `${path}.url`),
    http_version: stringAt(meta.http_version, `${path}.http_version`),
    request_headers: meta.request_headers.map((header, index) =>
      parseAuditHeader(header, `${path}.request_headers[${index}]`),
    ),
    response_status: nullableIntAt(
      meta.response_status,
      `${path}.response_status`,
    ),
    response_headers: meta.response_headers.map((header, index) =>
      parseAuditHeader(header, `${path}.response_headers[${index}]`),
    ),
  };
}

export function parseAuditContent(value: unknown): AuditContent {
  const content = objectAt(value, "$");
  return {
    request_id: stringAt(content.request_id, "$.request_id"),
    // Tolerate an absent key for compatibility with a core sidecar that
    // predates http_meta capture.
    http_meta: Object.hasOwn(content, "http_meta")
      ? parseAuditHTTPMeta(content.http_meta, "$.http_meta")
      : null,
    request_body: parseAuditContentPart(content.request_body, "$.request_body"),
    response_content: parseAuditContentPart(
      content.response_content,
      "$.response_content",
    ),
    upstream_http_meta: Object.hasOwn(content, "upstream_http_meta")
      ? parseAuditHTTPMeta(content.upstream_http_meta, "$.upstream_http_meta")
      : null,
    upstream_request_body: Object.hasOwn(content, "upstream_request_body")
      ? parseAuditContentPart(
          content.upstream_request_body,
          "$.upstream_request_body",
        )
      : null,
    upstream_response_content: Object.hasOwn(
      content,
      "upstream_response_content",
    )
      ? parseAuditContentPart(
          content.upstream_response_content,
          "$.upstream_response_content",
        )
      : null,
  };
}

export function parsePurgeResult(value: unknown): PurgeResult {
  const result = objectAt(value, "$");
  return {
    deleted_records: intAt(result.deleted_records, "$.deleted_records"),
    deleted_audit_blobs: intAt(
      result.deleted_audit_blobs,
      "$.deleted_audit_blobs",
    ),
  };
}

export function displayRequestStatus(
  status: RequestStatus,
  httpStatus: number | null,
): RequestStatus {
  if (status === "succeeded" && httpStatus !== null && httpStatus >= 400) {
    return "failed";
  }
  return status;
}

export function statusLabel(status: SessionStatus): string {
  return i18n.t(`status.${status}`);
}

export function statusTone(
  status: SessionStatus,
): "blocked" | "positive" | "negative" | "neutral" | "pending" {
  switch (status) {
    case "succeeded":
      return "positive";
    case "failed":
      return "negative";
    case "blocked":
      return "blocked";
    case "pending":
      return "pending";
    case "cancelled":
    case "interrupted":
      return "pending";
  }
}
