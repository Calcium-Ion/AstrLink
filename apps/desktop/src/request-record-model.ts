export type RequestStatus =
  | "pending"
  | "succeeded"
  | "failed"
  | "cancelled"
  | "blocked";

export interface RequestUsage {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cached_input_tokens?: number;
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

export interface PrivacyRestoreSummary {
  enabled: boolean;
  mapping_count: number;
  restored_count: number;
  fallback_count: number;
}

export interface RequestRecord {
  id: string;
  parent_request_id: string | null;
  attempt_index: number;
  child_count: number;
  started_at: string;
  completed_at: string | null;
  status: RequestStatus;
  input_protocol: string;
  requested_model: string | null;
  streaming: boolean;
  route_id: string | null;
  service_id: string | null;
  local_access_token_id: string | null;
  http_status: number | null;
  latency_ms: number | null;
  usage: RequestUsage | null;
  error: RequestErrorSummary | null;
  audit: RequestAuditSummary;
  privacy_restore: PrivacyRestoreSummary | null;
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
  if (Object.hasOwn(usage, "cached_input_tokens")) {
    result.cached_input_tokens = intAt(
      usage.cached_input_tokens,
      `${path}.cached_input_tokens`,
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

function parsePrivacyRestore(
  value: unknown,
  path: string,
): PrivacyRestoreSummary | null {
  if (value === null || value === undefined) return null;
  const summary = objectAt(value, path);
  const mappingCount = intAt(summary.mapping_count, `${path}.mapping_count`);
  const restoredCount = intAt(summary.restored_count, `${path}.restored_count`);
  const fallbackCount = intAt(summary.fallback_count, `${path}.fallback_count`);
  if (mappingCount < 0 || restoredCount < 0 || fallbackCount < 0) {
    return invalid(path, "计数不得为负数");
  }
  return {
    enabled: boolAt(summary.enabled, `${path}.enabled`),
    mapping_count: mappingCount,
    restored_count: restoredCount,
    fallback_count: fallbackCount,
  };
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
    streaming: boolAt(record.streaming, `${path}.streaming`),
    route_id: nullableStringAt(record.route_id, `${path}.route_id`),
    service_id: nullableStringAt(record.service_id, `${path}.service_id`),
    local_access_token_id: nullableStringAt(
      record.local_access_token_id,
      `${path}.local_access_token_id`,
    ),
    http_status: nullableIntAt(record.http_status, `${path}.http_status`),
    latency_ms: nullableIntAt(record.latency_ms, `${path}.latency_ms`),
    usage: parseUsage(record.usage, `${path}.usage`),
    error: parseError(record.error, `${path}.error`),
    audit: parseAuditSummary(record.audit, `${path}.audit`),
    privacy_restore: parsePrivacyRestore(
      record.privacy_restore,
      `${path}.privacy_restore`,
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

export function statusLabel(status: RequestStatus): string {
  switch (status) {
    case "pending":
      return "进行中";
    case "succeeded":
      return "成功";
    case "failed":
      return "失败";
    case "cancelled":
      return "已取消";
    case "blocked":
      return "已拦截";
  }
}

export function statusTone(
  status: RequestStatus,
): "positive" | "negative" | "neutral" | "pending" {
  switch (status) {
    case "succeeded":
      return "positive";
    case "failed":
      return "negative";
    case "pending":
      return "pending";
    case "cancelled":
    case "blocked":
      return "pending";
  }
}
