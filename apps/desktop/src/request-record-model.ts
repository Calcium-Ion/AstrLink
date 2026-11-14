import { i18n } from "./i18n";
import { invalidData, type DataProblem } from "./ipc-data-error";

export const CLIENT_TYPES = [
  "unknown",
  "codex",
  "claude_code",
  "cursor",
  "grok_cli",
  "gemini_cli",
  "opencode",
  "openclaw",
  "cline",
  "pi",
  "deepseek_harness",
  "codewhale",
  "reasonix",
  "dimagent",
  "qwen_code",
  "kimi_code",
  "codebuddy",
  "copilot",
  "droid",
  "crush",
  "kilo_code",
  "roo_code",
  "mistral_vibe",
  "zed",
  "cherry_studio",
  "goose",
] as const;

export type ClientType = (typeof CLIENT_TYPES)[number];

/**
 * Changes the gateway makes before a request leaves for the provider, in the
 * order it applies them: the first five before routing, the last two for the
 * chosen provider.
 */
export const GATEWAY_REWRITES = [
  "model_redirect",
  "privacy",
  "image_compression",
  "global_prompt",
  "reasoning_effort",
  "conversion",
  "client_identity",
] as const;

export type GatewayRewrite = (typeof GATEWAY_REWRITES)[number];

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
  /** The upstream stream ended before final usage was observed. */
  billing_incomplete?: boolean;
}

export interface RequestErrorSummary {
  category: string;
  code: string;
  message: string;
  retryable: boolean;
  /** The provider's own HTTP error response, kept verbatim. */
  upstream?: UpstreamErrorResponse;
}

export interface UpstreamErrorResponse {
  status: number;
  content_type?: string;
  body: string;
  truncated: boolean;
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

/**
 * What the privacy policy did to this request. `none` means no policy applied,
 * so the body went upstream untouched; `trusted` means a policy applied but the
 * body went uninspected to a provider the user trusts; `allow` means a policy
 * inspected it and found nothing to act on. Absent on records written before
 * decisions existed.
 */
export type PrivacyDecision =
  | "none"
  | "trusted"
  | "allow"
  | "warn"
  | "redact"
  | "block"
  | "fail_open";

/** One privacy hit, by kind and structural path only. Never a value. */
export interface PrivacyFinding {
  kind: string;
  json_path: string;
  count: number;
  /** Absent when the regex detector decided and on older records. */
  confidence?: PrivacyConfidence;
}

/** Local model confidence over the matches an entry counts, from 0 to 1. */
export interface PrivacyConfidence {
  min: number;
  max: number;
  /** The policy threshold the matches were judged against. */
  threshold: number;
}

export type PrivacyIgnoreReason =
  | "kind_disabled"
  | "low_confidence"
  | "allowlisted"
  | "astrlink_identifier"
  | "example_value";

/** Matches of one kind the policy left in the request for one reason. */
export interface PrivacyIgnored {
  kind: string;
  reason: PrivacyIgnoreReason;
  count: number;
  /** Absent when the regex detector decided and on older records. */
  confidence?: PrivacyConfidence;
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
  | "model_redirect"
  | "privacy"
  | "image_compression"
  | "routed"
  | "upstream"
  | "restore"
  | "completed";

export type OutputKind = "reasoning" | "text" | "tool";

/**
 * What a call's response produces: the part arriving now while it streams,
 * the last part once it ended, and the tool calls it made, in order.
 */
export interface OutputActivity {
  kind: OutputKind;
  tools: string[];
}

/** The routing-settings redirect applied to a call: client model → routed model. */
export interface RequestModelRedirect {
  from: string;
  to: string;
}

/** Why an attempt used its API provider. */
export type RoutingSelection =
  | "priority"
  | "session_binding"
  | "response_affinity"
  | "websocket_connection"
  | "failover"
  | "client_pin"
  | "claude_cli";

/** Why routing excluded a provider before any attempt. */
export type RoutingSkipReason =
  | "disabled"
  | "not_connected"
  | "risk_paused"
  | "model_not_listed"
  | "protocol_unsupported"
  | "streaming_unsupported"
  | "conversion_unavailable"
  | "circuit_open"
  | "rate_limited"
  | "websocket_disabled"
  | "websocket_unsupported"
  | "access_token_scope";

export interface RoutingSkip {
  service_id: string;
  reason: RoutingSkipReason;
}

/**
 * Why routing chose service_id. `selected` is absent when no provider could be
 * selected. `skipped` lists, in priority order, the providers ranked ahead of
 * service_id that routing excluded (every excluded provider when none was
 * selected); providers tried and rejected show up as events or retry children.
 */
export interface RequestRoutingDecision {
  selected?: RoutingSelection;
  skipped: RoutingSkip[];
}

/**
 * One tool or field that converting between the client's and the provider's
 * protocol dropped or rewrote. `error` can change what the model or its tools
 * do; `warning` loses presentation-only detail. The call still succeeded.
 */
export interface ConversionDiagnostic {
  phase: "request" | "response";
  severity: "warning" | "error";
  code: string;
  /** Location in the converted body, such as `tools[0]`. */
  path?: string;
  message: string;
}

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
  path_id?: string;
  path_name?: string;
  path_version?: string;
  step_id?: string;
  upstream_model?: string;
  action?: "retry" | "failover";
  reason?: string;
  delay_ms: number;
  stop_reason?: string;
}

export interface RequestRecord {
  /** Original inbound client; absent on historical records. */
  client_type?: ClientType;
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
  model_redirect?: RequestModelRedirect;
  /** Absent before any output and on historical records. */
  output?: OutputActivity;
  reasoning_effort?: string | null;
  streaming: boolean;
  route_id: string | null;
  service_id: string | null;
  /** Absent on historical records, discovery, and pending calls. */
  routing_decision?: RequestRoutingDecision;
  /** Absent when this attempt's conversion lost nothing or none ran. */
  conversion_diagnostics?: ConversionDiagnostic[];
  local_access_token_id: string | null;
  http_status: number | null;
  latency_ms: number | null;
  first_token_ms?: number | null;
  /** First non-reasoning output; minus first_token_ms is the thinking time. */
  first_answer_ms?: number | null;
  usage: RequestUsage | null;
  error: RequestErrorSummary | null;
  audit: RequestAuditSummary;
  privacy_restore: PrivacyRestoreSummary | null;
  /** Absent on historical records and while the decision is still pending. */
  privacy_decision?: PrivacyDecision;
  /** Present for redact and block decisions; absent otherwise. */
  privacy_findings?: PrivacyFinding[];
  /** Absent when nothing was left in place and on older records. */
  privacy_ignored?: PrivacyIgnored[];
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
  /** The conversation that started this record's one as a subagent. */
  parent_session_id?: string;
  /**
   * What the call carried to the model; absent on retries, on calls whose
   * body was not read, and on records from before it was kept.
   */
  context?: RequestContext;
  /** The project the agent worked in, the home directory written as ~. */
  project?: string;
  cursors: SessionCursor[];
  events: RequestEvent[];
}

export type ContextItemKind = "rules" | "memory" | "skills" | "mcp";
export type ContextItemScope = "global" | "project" | "local";

/** Where a call's input came from, in tokens; sums to its input once known. */
export interface ContextParts {
  history: number;
  tool_results: number;
  tools: number;
  system: number;
  /** Rules files, memory files and the skills catalog. */
  memory: number;
  other: number;
}

/** A rules file, memory file, skills catalog or the MCP tools a call carried. */
export interface ContextItem {
  kind: ContextItemKind;
  /** File name; absent for skills and MCP. */
  name?: string;
  path?: string;
  scope?: ContextItemScope;
  /** Lines of a memory index, skills of a catalog, MCP tools. */
  count?: number;
  servers?: string[];
  tokens: number;
  /** Keyed digest; equal hashes mean unchanged content. */
  hash?: string;
}

/** A memory or rules file the previous response's tool calls wrote. */
export interface MemoryWrite {
  kind: "rules" | "memory";
  name: string;
  path?: string;
  /** Absent when the record may not show the client's text. */
  title?: string;
  /** The memory file a new index line points to. */
  entry?: string;
  /** What that index line says after the link. */
  hook?: string;
}

/** A memory file the previous response's tool calls read. */
export interface MemoryRead {
  name: string;
  path?: string;
}

/** One tool call repeated with the same arguments, failing each time. */
export interface FailedRepeats {
  tool: string;
  count: number;
  /** Set when the tool runs shell commands. */
  command?: boolean;
}

/** What one call sent the model, read from its request body. */
export interface RequestContext {
  /** Absent when the body only held what is new since a stored response. */
  parts?: ContextParts;
  items: ContextItem[];
  system_hash?: string;
  tools_hash?: string;
  /** The call asked for a compaction summary. */
  summary?: boolean;
  compacted?: string;
  helper?: boolean;
  memory_writes: MemoryWrite[];
  /** Memory files the tool calls read, the index left out. */
  memory_reads?: MemoryRead[];
  /** Skills the tool calls loaded. */
  skills_used?: string[];
  failed_repeats?: FailedRepeats;
}

/** One project the calls named, and the clients whose calls named it. */
export interface MemoryProject {
  /** Absent for the calls that named no project. */
  project?: string;
  /** The most recent first. */
  client_types: ClientType[];
  last_seen_at: string;
}

/** One memory, a file the index links. */
export interface MemoryEntry {
  file: string;
  /** Absent when the calls did not say where the index is. */
  path?: string;
  /** Absent when no record may show the client's text. */
  title?: string;
  hook?: string;
  /** Absent when the index listed it before the gateway first saw it. */
  noted_at?: string;
  /** Turns that read it per day, 14 days, the oldest first. */
  reads: number[];
  total_reads: number;
}

/** A memory an agent noted, with what the user said in that turn. */
export interface MemoryStory {
  file: string;
  title?: string;
  hook?: string;
  at: string;
  request_id?: string;
  session_id?: string;
  prompt?: string;
}

/** A rules file the newest main call carried, with its latest change. */
export interface MemoryRules {
  name: string;
  path?: string;
  scope?: ContextItemScope;
  first_seen_at?: string;
  /** When a call first carried the current version, once one came before. */
  changed_at?: string;
  request_id?: string;
  session_id?: string;
  /** The last call that carried the version before. */
  before_request_id?: string;
  /** Both calls' bodies were captured, so the versions can be compared. */
  captured?: boolean;
}

export interface ToolboxSkill {
  name: string;
  description?: string;
  /** Turns that loaded it in the last 30 days. */
  uses: number;
  /** A call of the last 30 days listed it. */
  carried: boolean;
}

export interface ToolboxTool {
  name: string;
  /** Calls that made it in the last 30 days. */
  uses: number;
  /** A call of the last 30 days carried it. */
  carried: boolean;
}

export interface ToolboxServer {
  server: string;
  tools: ToolboxTool[];
}

/**
 * The skills and MCP tools the calls of the last 30 days carried or used.
 * Within the same days the lists only grow and no count drops; a call leaves
 * them when its day does, each midnight.
 */
export interface MemoryToolbox {
  /** When those days began. */
  since: string;
  skills: ToolboxSkill[];
  mcp: ToolboxServer[];
  /** Tokens the skills and tools carried but not used add to each call. */
  idle_tokens: number;
}

/** What one client's agents remembered in one project. */
export interface ProjectMemory {
  projects: MemoryProject[];
  /** Absent for the calls that named no project, or when none named any. */
  project?: string;
  /** Absent when no call named the project. */
  client_type?: ClientType;
  /** When the newest day of the read counts began. */
  day_start: string;
  /** Entries noted in the last seven days. */
  recent: number;
  entries: MemoryEntry[];
  /** The newest first. */
  stories: MemoryStory[];
  rules: MemoryRules[];
  toolbox: MemoryToolbox;
  has_memory: boolean;
  has_rules: boolean;
}

export type MemoryEventKind = "noted" | "read";

/** One note or read of a memory, at the call whose history shows it. */
export interface MemoryEvent {
  kind: MemoryEventKind;
  at: string;
  request_id: string;
  session_id?: string;
  prompt?: string;
  /** The call's body was captured, so the file's text can be read. */
  captured?: boolean;
}

/** One memory and what happened to it, the oldest first. */
export interface MemoryEntryDetail {
  entry: MemoryEntry;
  events: MemoryEvent[];
}

/** A rules file, memory file or skills catalog as a call carried it. */
export interface ContextItemText {
  kind: "rules" | "memory" | "skills";
  name?: string;
  path?: string;
  text: string;
}

/** What one tool call wrote to a memory or rules file. */
export interface MemoryWriteText {
  kind: "rules" | "memory";
  name: string;
  path?: string;
  content: string;
}

/** A memory file one tool call read, as the tool returned it. */
export interface MemoryReadText {
  name: string;
  path?: string;
  content: string;
}

/** What one captured call carried, as text. */
export interface RequestContextTexts {
  request_id: string;
  /**
   * The body cannot be read in this view. `reason` says why, and is absent
   * when the body was not captured.
   */
  withheld?: boolean;
  reason?: AuditWithheldReason;
  /** Capture kept only the start of the body. */
  truncated?: boolean;
  items: ContextItemText[];
  /** The compaction summary the call continued from. */
  summary?: string;
  writes: MemoryWriteText[];
  reads: MemoryReadText[];
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
  /** Original inbound client; absent on historical records. */
  client_type?: ClientType;
  id: string;
  title: string;
  started_at: string;
  last_started_at: string;
  completed_at: string | null;
  duration_ms: number;
  /** Start of the one merged block of running calls; null when none run. */
  running_since: string | null;
  tool_duration_ms?: number | null;
  average_ttft_ms?: number | null;
  output_tokens_per_second?: number | null;
  active_request_starts: string[];
  turn_count: number;
  call_count: number;
  status: SessionStatus;
  requested_model: string | null;
  model_redirect?: RequestModelRedirect;
  /** Absent before any output and on historical records. */
  output?: OutputActivity;
  reasoning_effort?: string | null;
  input_protocol: string;
  service_id: string | null;
  local_access_token_id: string | null;
  /**
   * Set when the warn action let at least one call through as is; absent
   * otherwise and from a Core that predates it.
   */
  privacy_warned?: boolean;
  /** The conversation that started this one as a subagent. */
  parent_session_id?: string;
  /**
   * How many conversations this one started as subagents, theirs included;
   * absent for none.
   */
  subagent_count?: number;
  /**
   * Input tokens of the newest main call, cache included; absent when no
   * main call reported usage. Helper calls such as a title are left out.
   */
  context_tokens?: number;
  /** Where the client compacts, inferred; absent before any compaction. */
  compaction_point_tokens?: number;
  /** The official model's context window; absent when unknown. */
  context_window_tokens?: number;
  /** Absent when the context was never compacted. */
  compaction_count?: number;
  /** Tokens of every call, retries included; absent before any usage. */
  usage?: SessionUsage;
  /** Official-price value; only a session's detail carries it. */
  cost?: SessionCost;
  /** Set while the newest main call keeps repeating a failing tool call. */
  failed_repeats?: FailedRepeats;
}

export interface SessionUsage {
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  /** Absent when no call reported its cache use. */
  cache_hit_ratio?: number;
}

export interface SessionCost {
  amount_usd: string;
  priced: number;
  unpriced: number;
  pending: number;
}

export interface ContextCall {
  request_id: string;
  tokens: number;
}

export interface ContextCompaction {
  /** The first main call after it, whose context dropped. */
  request_id: string;
  /** The call that wrote the summary, when the client marked one. */
  summary_request_id?: string;
  before_tokens: number;
  after_tokens: number;
  /** False when only the drop in context showed the compaction. */
  marked: boolean;
}

export type CacheMissReason =
  | "provider_changed"
  | "model_changed"
  | "expired"
  | "prefix_changed"
  | "unknown";

export interface CacheMiss {
  request_id: string;
  reason: CacheMissReason;
  /**
   * For prefix_changed: "system", "tools", "skills", a file such as
   * AGENTS.md, or the kind ("rules", "memory") of a file without a name.
   */
  changed?: string;
  idle_ms?: number;
  ttl_ms?: number;
  rewritten_tokens: number;
  /** Absent when the call has no price. */
  extra_usd?: string;
}

/** What a conversation sent the model, call by call. */
export interface SessionContext {
  calls: ContextCall[];
  compactions: ContextCompaction[];
  cache_misses: CacheMiss[];
}

export interface RequestSessionPage {
  items: RequestSession[];
  next_cursor: string | null;
}

export interface RequestSessionDetail extends RequestSession {
  turns: RequestRecord[];
  /**
   * The conversations this one started as subagents, theirs included: each
   * right after the one that started it, siblings oldest first.
   */
  subagents?: RequestSession[];
  /** Absent when no main call reported usage. */
  context?: SessionContext;
  /** This conversation together with every subagent under it. */
  with_subagents?: SessionTotals;
}

/** Calls, retries included, and the tokens they reported. */
export interface SessionTotals {
  call_count: number;
  input_tokens: number;
  output_tokens: number;
}

/** One root model call's span. Retries stay inside it. */
export interface RequestActivityCall {
  session_id: string;
  /** Set on a subagent's calls: the conversation that started it. */
  parent_session_id?: string;
  /** Original inbound client; absent on historical records. */
  client_type?: ClientType;
  /**
   * The model the call ran with, as usage counts it: served, else a
   * redirect's target, else the client's; absent when none is known.
   */
  model?: string;
  started_at: string;
  completed_at: string | null;
  status: RequestStatus;
  /** What the call produced; absent before any output and on old records. */
  output?: OutputActivity;
  /**
   * Tokens the upstream reported the call generated, reasoning included;
   * absent while it runs and when no final usage came back.
   */
  output_tokens?: number;
  /** The provider of the last attempt; set once the call ended. */
  service_id?: string;
  /** The provider the call failed over from. */
  failed_service_id?: string;
  /** What the gateway changed, in the order it applies them. */
  rewrites?: GatewayRewrite[];
}

/** Model calls that overlap a recent window, with the gateway's clock. */
export interface RequestActivity {
  now: string;
  calls: RequestActivityCall[];
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
  local_access_token_ids?: string[];
  /** Roots asking for, redirected to, or served by any of these models. */
  models?: string[];
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
  local_access_token_ids?: string[];
  /** Roots asking for, redirected to, or served by any of these models. */
  models?: string[];
  status?: RequestStatus;
  /** Records sent by this kind of client. */
  client_type?: ClientType;
}

export type AuditPartExposure = "shareable" | "raw";

export interface AuditContentPart {
  media_type: string;
  content: string;
  truncated: boolean;
  captured_bytes: number;
  /** Who may read the part without proof; absent from a Core that predates it. */
  exposure?: AuditPartExposure;
}

export type AuditWithheldReason =
  | "privacy_redacted"
  | "privacy_blocked"
  | "privacy_restored"
  | "privacy_fail_open"
  /** Sent uninspected to a trusted provider, so it holds the original values. */
  | "privacy_trusted"
  | "privacy_pending"
  | "privacy_unknown"
  | "raw_locked"
  /** Captured while no raw password was set, so it was never kept. */
  | "raw_not_kept";

/** A captured part this read may not see. It never carries content. */
export interface AuditWithheldPart {
  reason: AuditWithheldReason;
  /** An agent may ask the user for this part. */
  raw_available: boolean;
  media_type: string;
  truncated: boolean;
  captured_bytes: number;
}

export type AuditBodyPartName =
  | "request_body"
  | "response_content"
  | "upstream_request_body"
  | "upstream_response_content";

/** What the privacy decision found, by kind and structural path only. */
export interface AuditPrivacyFinding {
  kind: string;
  json_path: string;
  count: number;
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
  view: "shareable" | "full";
  http_meta: AuditHTTPMeta | null;
  request_body: AuditContentPart | null;
  response_content: AuditContentPart | null;
  upstream_http_meta: AuditHTTPMeta | null;
  upstream_request_body: AuditContentPart | null;
  upstream_response_content: AuditContentPart | null;
  /**
   * Captured parts left out of this read, such as raw parts while raw
   * reading is locked. Their body fields above stay null.
   */
  withheld: Partial<Record<AuditBodyPartName, AuditWithheldPart>>;
  privacy_findings: AuditPrivacyFinding[];
}

/** Whether the content holds a raw part, which a raw key locks again. */
export function holdsRawPart(content: AuditContent): boolean {
  return [
    content.request_body,
    content.response_content,
    content.upstream_request_body,
    content.upstream_response_content,
  ].some((part) => part?.exposure === "raw");
}

/** Whether the content leaves out a part until raw reading is unlocked. */
export function holdsLockedPart(content: AuditContent): boolean {
  return Object.values(content.withheld).some(
    (part) => part?.reason === "raw_locked",
  );
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

function invalid(
  path: string,
  problem: DataProblem,
  values?: Record<string, number>,
): never {
  return invalidData("requestRecord", path, problem, values);
}

function objectAt(value: unknown, path: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalid(path, "object");
  }
  return value as JsonObject;
}

function stringAt(value: unknown, path: string): string {
  if (typeof value !== "string") {
    return invalid(path, "string");
  }
  return value;
}

function nullableStringAt(value: unknown, path: string): string | null {
  if (value === null) return null;
  return stringAt(value, path);
}

function intAt(value: unknown, path: string): number {
  if (typeof value !== "number" || !Number.isInteger(value)) {
    return invalid(path, "integer");
  }
  return value;
}

function nullableIntAt(value: unknown, path: string): number | null {
  if (value === null) return null;
  return intAt(value, path);
}

function boolAt(value: unknown, path: string): boolean {
  if (typeof value !== "boolean") {
    return invalid(path, "boolean");
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
  if (usage.billing_incomplete !== undefined) {
    result.billing_incomplete = boolAt(
      usage.billing_incomplete,
      `${path}.billing_incomplete`,
    );
  }
  return result;
}

function parseError(value: unknown, path: string): RequestErrorSummary | null {
  if (value === null) return null;
  const error = objectAt(value, path);
  const result: RequestErrorSummary = {
    category: stringAt(error.category, `${path}.category`),
    code: stringAt(error.code, `${path}.code`),
    message: stringAt(error.message, `${path}.message`),
    retryable: boolAt(error.retryable, `${path}.retryable`),
  };
  if (error.upstream !== undefined && error.upstream !== null) {
    const upstream = objectAt(error.upstream, `${path}.upstream`);
    result.upstream = {
      status: intAt(upstream.status, `${path}.upstream.status`),
      body: stringAt(upstream.body, `${path}.upstream.body`),
      truncated: boolAt(upstream.truncated, `${path}.upstream.truncated`),
    };
    if (upstream.content_type !== undefined) {
      result.upstream.content_type = stringAt(
        upstream.content_type,
        `${path}.upstream.content_type`,
      );
    }
  }
  return result;
}

function optionalBoolAt(
  value: unknown,
  path: string,
  fallback = false,
): boolean {
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
    return invalid(path, "nonNegativeInteger");
  }
  if (visibleRestoredCount + toolArgumentRestoredCount > restoredCount) {
    return invalid(path, "exceedsTotal");
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
    return invalid(path, "array");
  }
  const seen = new Set<string>();
  return value.map((item, index) => {
    const hit = objectAt(item, `${path}[${index}]`);
    const kind = stringAt(hit.kind, `${path}[${index}].kind`);
    const count = intAt(hit.count, `${path}[${index}].count`);
    if (!privacyHitKinds.has(kind)) {
      return invalid(`${path}[${index}].kind`, "unknownType");
    }
    if (count < 1) {
      return invalid(`${path}[${index}].count`, "positiveInteger");
    }
    if (seen.has(kind)) {
      return invalid(`${path}[${index}].kind`, "duplicate");
    }
    seen.add(kind);
    return { kind, count };
  });
}

function optionalPrivacyDecision(
  value: unknown,
  path: string,
): { privacy_decision?: PrivacyDecision } {
  const decision = parsePrivacyDecision(value, `${path}.privacy_decision`);
  return decision === null ? {} : { privacy_decision: decision };
}

function optionalPrivacyFindings(
  value: unknown,
  path: string,
): { privacy_findings?: PrivacyFinding[] } {
  const findings = parsePrivacyFindings(value, `${path}.privacy_findings`);
  return findings.length === 0 ? {} : { privacy_findings: findings };
}

const privacyDecisions = new Set<PrivacyDecision>([
  "none",
  "trusted",
  "allow",
  "warn",
  "redact",
  "block",
  "fail_open",
]);

function parsePrivacyDecision(
  value: unknown,
  path: string,
): PrivacyDecision | null {
  if (value === undefined || value === null) return null;
  if (
    typeof value !== "string" ||
    !privacyDecisions.has(value as PrivacyDecision)
  ) {
    return invalid(path, "unknownDecision");
  }
  return value as PrivacyDecision;
}

function parsePrivacyFindings(value: unknown, path: string): PrivacyFinding[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) return invalid(path, "array");
  return value.map((item, index) => {
    const finding = objectAt(item, `${path}[${index}]`);
    const count = intAt(finding.count, `${path}[${index}].count`);
    if (count < 1) return invalid(`${path}[${index}].count`, "positiveInteger");
    return {
      kind: stringAt(finding.kind, `${path}[${index}].kind`),
      json_path: stringAt(finding.json_path, `${path}[${index}].json_path`),
      count,
      ...optionalPrivacyConfidence(
        finding.confidence,
        `${path}[${index}].confidence`,
      ),
    };
  });
}

const privacyIgnoreReasons = new Set<PrivacyIgnoreReason>([
  "kind_disabled",
  "low_confidence",
  "allowlisted",
  "astrlink_identifier",
  "example_value",
]);

function optionalPrivacyIgnored(
  value: unknown,
  path: string,
): { privacy_ignored?: PrivacyIgnored[] } {
  if (value === undefined || value === null) return {};
  if (!Array.isArray(value)) return invalid(`${path}.privacy_ignored`, "array");
  const ignored = value.map((item, index): PrivacyIgnored => {
    const itemPath = `${path}.privacy_ignored[${index}]`;
    const entry = objectAt(item, itemPath);
    const reason = stringAt(entry.reason, `${itemPath}.reason`);
    if (!privacyIgnoreReasons.has(reason as PrivacyIgnoreReason)) {
      return invalid(`${itemPath}.reason`, "unknownReason");
    }
    const count = intAt(entry.count, `${itemPath}.count`);
    if (count < 1) return invalid(`${itemPath}.count`, "positiveInteger");
    return {
      kind: stringAt(entry.kind, `${itemPath}.kind`),
      reason: reason as PrivacyIgnoreReason,
      count,
      ...optionalPrivacyConfidence(entry.confidence, `${itemPath}.confidence`),
    };
  });
  return ignored.length === 0 ? {} : { privacy_ignored: ignored };
}

function optionalPrivacyConfidence(
  value: unknown,
  path: string,
): { confidence?: PrivacyConfidence } {
  if (value === undefined || value === null) return {};
  const confidence = objectAt(value, path);
  const min = probabilityAt(confidence.min, `${path}.min`);
  const max = probabilityAt(confidence.max, `${path}.max`);
  if (min > max) return invalid(`${path}.min`, "unexpectedValue");
  return {
    confidence: {
      min,
      max,
      threshold: probabilityAt(confidence.threshold, `${path}.threshold`),
    },
  };
}

function probabilityAt(value: unknown, path: string): number {
  if (typeof value !== "number" || !(value >= 0 && value <= 1)) {
    return invalid(path, "unexpectedValue");
  }
  return value;
}

function parseRecovery(value: unknown, path: string): RequestRecovery {
  const record = objectAt(value, path);
  const delay = intAt(record.delay_ms, `${path}.delay_ms`);
  if (delay < 0 || delay > 60000)
    invalid(`${path}.delay_ms`, "integerBetween", {
      minimum: 0,
      maximum: 60000,
    });
  if (
    record.action !== undefined &&
    record.action !== "retry" &&
    record.action !== "failover"
  )
    invalid(`${path}.action`, "unexpectedValue");
  const result: RequestRecovery = { delay_ms: delay };
  if (record.action) result.action = record.action as RequestRecovery["action"];
  for (const key of [
    "upstream_model",
    "reason",
    "stop_reason",
    "path_id",
    "path_name",
    "path_version",
    "step_id",
  ] as const) {
    if (record[key] !== undefined) {
      const text = stringAt(record[key], `${path}.${key}`);
      if ([...text].length > (key === "upstream_model" ? 256 : 128))
        invalid(`${path}.${key}`, "tooLong");
      result[key] = text;
    }
  }
  return result;
}

function parseModelRedirect(
  value: unknown,
  path: string,
): RequestModelRedirect | undefined {
  if (value == null) return undefined;
  const redirect = objectAt(value, path);
  const from = stringAt(redirect.from, `${path}.from`);
  const to = stringAt(redirect.to, `${path}.to`);
  if (!from || !to || [...from].length > 256 || [...to].length > 256)
    invalid(path, "badFormat");
  return { from, to };
}

function optionalModelRedirect(
  value: unknown,
  path: string,
): { model_redirect?: RequestModelRedirect } {
  const redirect = parseModelRedirect(value, path);
  return redirect ? { model_redirect: redirect } : {};
}

const outputKinds = new Set<OutputKind>(["reasoning", "text", "tool"]);

function optionalOutput(
  value: unknown,
  path: string,
): { output?: OutputActivity } {
  if (value == null) return {};
  const output = objectAt(value, path);
  // A kind a newer Core added reads as no output instead of failing the list.
  if (
    typeof output.kind !== "string" ||
    !outputKinds.has(output.kind as OutputKind)
  ) {
    return {};
  }
  if (output.tools != null && !Array.isArray(output.tools)) {
    invalid(`${path}.tools`, "array");
  }
  const tools = ((output.tools as unknown[] | undefined) ?? []).filter(
    (name): name is string => typeof name === "string" && name !== "",
  );
  return { output: { kind: output.kind as OutputKind, tools } };
}

const routingSelections = new Set<RoutingSelection>([
  "priority",
  "session_binding",
  "response_affinity",
  "websocket_connection",
  "failover",
  "client_pin",
  "claude_cli",
]);

const routingSkipReasons = new Set<RoutingSkipReason>([
  "disabled",
  "not_connected",
  "risk_paused",
  "model_not_listed",
  "protocol_unsupported",
  "streaming_unsupported",
  "conversion_unavailable",
  "circuit_open",
  "rate_limited",
  "websocket_disabled",
  "websocket_unsupported",
  "access_token_scope",
]);

function optionalRoutingDecision(
  value: unknown,
  path: string,
): { routing_decision?: RequestRoutingDecision } {
  if (value == null) return {};
  const decision = objectAt(value, path);
  if (!Array.isArray(decision.skipped)) invalid(`${path}.skipped`, "array");
  if (decision.skipped.length > 64) invalid(`${path}.skipped`, "tooMany");
  const parsed: RequestRoutingDecision = {
    skipped: decision.skipped.map((item, index) => {
      const skip = objectAt(item, `${path}.skipped[${index}]`);
      if (
        typeof skip.reason !== "string" ||
        !routingSkipReasons.has(skip.reason as RoutingSkipReason)
      ) {
        invalid(`${path}.skipped[${index}].reason`, "unknownReason");
      }
      return {
        service_id: stringAt(
          skip.service_id,
          `${path}.skipped[${index}].service_id`,
        ),
        reason: skip.reason as RoutingSkipReason,
      };
    }),
  };
  if (decision.selected !== undefined) {
    if (
      typeof decision.selected !== "string" ||
      !routingSelections.has(decision.selected as RoutingSelection)
    ) {
      invalid(`${path}.selected`, "unknownReason");
    }
    parsed.selected = decision.selected as RoutingSelection;
  }
  return { routing_decision: parsed };
}

const conversionPhases = new Set(["request", "response"]);
const conversionSeverities = new Set(["warning", "error"]);

function optionalConversionDiagnostics(
  value: unknown,
  path: string,
): { conversion_diagnostics?: ConversionDiagnostic[] } {
  if (value == null) return {};
  if (!Array.isArray(value)) invalid(path, "array");
  if (value.length > 64) invalid(path, "tooMany");
  if (value.length === 0) return {};
  return {
    conversion_diagnostics: value.map((item, index) => {
      const at = `${path}[${index}]`;
      const diagnostic = objectAt(item, at);
      if (
        typeof diagnostic.phase !== "string" ||
        !conversionPhases.has(diagnostic.phase)
      ) {
        invalid(`${at}.phase`, "unexpectedValue");
      }
      if (
        typeof diagnostic.severity !== "string" ||
        !conversionSeverities.has(diagnostic.severity)
      ) {
        invalid(`${at}.severity`, "unexpectedValue");
      }
      const code = stringAt(diagnostic.code, `${at}.code`);
      if (!code) invalid(`${at}.code`, "empty");
      const parsed: ConversionDiagnostic = {
        phase: diagnostic.phase as ConversionDiagnostic["phase"],
        severity: diagnostic.severity as ConversionDiagnostic["severity"],
        code,
        message: stringAt(diagnostic.message, `${at}.message`),
      };
      if (diagnostic.path !== undefined) {
        parsed.path = stringAt(diagnostic.path, `${at}.path`);
      }
      return parsed;
    }),
  };
}

function optionalClientType(value: unknown): { client_type?: ClientType } {
  if (value == null) return {};
  // Future Core labels remain displayable by older desktop builds.
  return {
    client_type:
      typeof value === "string" &&
      (CLIENT_TYPES as readonly string[]).includes(value)
        ? (value as ClientType)
        : "unknown",
  };
}

export function parseRequestRecord(value: unknown): RequestRecord {
  return parseRequestRecordAt(value, "$");
}

function parseRequestRecordAt(value: unknown, path: string): RequestRecord {
  const record = objectAt(value, path);
  if (!Object.hasOwn(record, "id")) invalid(`${path}.id`, "missing");
  if (
    typeof record.status !== "string" ||
    !statuses.has(record.status as RequestStatus)
  ) {
    invalid(`${path}.status`, "unknownStatus");
  }

  const attemptIndex = Object.hasOwn(record, "attempt_index")
    ? intAt(record.attempt_index, `${path}.attempt_index`)
    : 1;
  const childCount = Object.hasOwn(record, "child_count")
    ? intAt(record.child_count, `${path}.child_count`)
    : 0;
  if (attemptIndex < 0) invalid(`${path}.attempt_index`, "nonNegativeInteger");
  if (childCount < 0) invalid(`${path}.child_count`, "nonNegativeInteger");

  return {
    ...(record.recovery === undefined
      ? {}
      : { recovery: parseRecovery(record.recovery, `${path}.recovery`) }),
    ...optionalClientType(record.client_type),
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
    ...optionalModelRedirect(record.model_redirect, `${path}.model_redirect`),
    ...optionalOutput(record.output, `${path}.output`),
    reasoning_effort:
      record.reasoning_effort == null
        ? null
        : nullableStringAt(record.reasoning_effort, `${path}.reasoning_effort`),
    streaming: boolAt(record.streaming, `${path}.streaming`),
    route_id: nullableStringAt(record.route_id, `${path}.route_id`),
    service_id: nullableStringAt(record.service_id, `${path}.service_id`),
    ...optionalRoutingDecision(
      record.routing_decision,
      `${path}.routing_decision`,
    ),
    ...optionalConversionDiagnostics(
      record.conversion_diagnostics,
      `${path}.conversion_diagnostics`,
    ),
    local_access_token_id: nullableStringAt(
      record.local_access_token_id,
      `${path}.local_access_token_id`,
    ),
    http_status: nullableIntAt(record.http_status, `${path}.http_status`),
    latency_ms: nullableIntAt(record.latency_ms, `${path}.latency_ms`),
    first_token_ms: optionalPerformanceNumber(
      record.first_token_ms,
      `${path}.first_token_ms`,
      true,
    ),
    first_answer_ms: optionalPerformanceNumber(
      record.first_answer_ms,
      `${path}.first_answer_ms`,
      true,
    ),
    usage: parseUsage(record.usage, `${path}.usage`),
    error: parseError(record.error, `${path}.error`),
    audit: parseAuditSummary(record.audit, `${path}.audit`),
    privacy_restore: parsePrivacyRestore(
      record.privacy_restore,
      `${path}.privacy_restore`,
    ),
    ...optionalPrivacyDecision(record.privacy_decision, path),
    ...optionalPrivacyFindings(record.privacy_findings, path),
    ...optionalPrivacyIgnored(record.privacy_ignored, path),
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
      ? nullableStringAt(
          record.output_response_id,
          `${path}.output_response_id`,
        )
      : null,
    input_preview: Object.hasOwn(record, "input_preview")
      ? nullableStringAt(record.input_preview, `${path}.input_preview`)
      : null,
    turn_index: parseTurnIndex(record.turn_index, `${path}.turn_index`),
    session_link: parseSessionLink(record.session_link, `${path}.session_link`),
    ...optionalParentSession(record.parent_session_id, path),
    ...(record.context == null
      ? {}
      : { context: parseRequestContext(record.context, `${path}.context`) }),
    ...(record.project == null
      ? {}
      : { project: stringAt(record.project, `${path}.project`) }),
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
    return invalid(path, "unknownType");
  }
  return value as SessionCursorKind;
}

// A core sidecar that predates conversation linking omits these fields; treat
// absence like null so old records still render.
function parseTurnIndex(value: unknown, path: string): number | null {
  if (value === undefined || value === null) return null;
  const turn = intAt(value, path);
  if (turn < 1) return invalid(path, "integerAtLeast", { minimum: 1 });
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
  if (!Array.isArray(value)) return invalid(path, "array");
  return value.map((item, index) => {
    const cursor = objectAt(item, `${path}[${index}]`);
    const direction = cursor.direction;
    if (direction !== "in" && direction !== "out") {
      return invalid(`${path}[${index}].direction`, "unexpectedValue");
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
  "model_redirect",
  "privacy",
  "image_compression",
  "routed",
  "upstream",
  "restore",
  "completed",
]);

function parseRequestEvents(value: unknown, path: string): RequestEvent[] {
  // A record with no trajectory arrives as a nil Go slice, i.e. `null`, and
  // records written before events existed omit the key. Both mean "no events".
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) invalid(path, "array");
  return value.map((item, index) => {
    const event = objectAt(item, `${path}[${index}]`);
    if (
      typeof event.kind !== "string" ||
      !eventKinds.has(event.kind as RequestEventKind)
    ) {
      invalid(`${path}[${index}].kind`, "unknownType");
    }
    if (
      typeof event.status !== "string" ||
      !statuses.has(event.status as RequestStatus)
    ) {
      invalid(`${path}[${index}].status`, "unknownStatus");
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

function optionalPerformanceNumber(
  value: unknown,
  path: string,
  integer = false,
): number | null {
  if (value == null) return null;
  if (
    typeof value !== "number" ||
    !Number.isFinite(value) ||
    value < 0 ||
    (integer && !Number.isInteger(value))
  ) {
    return invalid(path, integer ? "nonNegativeInteger" : "nonNegativeNumber");
  }
  return value;
}

function parseRequestSessionAt(value: unknown, path: string): RequestSession {
  const session = objectAt(value, path);
  if (
    typeof session.status !== "string" ||
    !sessionStatuses.has(session.status as SessionStatus)
  ) {
    invalid(`${path}.status`, "unknownStatus");
  }
  const turnCount = intAt(session.turn_count, `${path}.turn_count`);
  const callCount = intAt(session.call_count, `${path}.call_count`);
  if (turnCount < 1 || callCount < 1) {
    invalid(path, "integerAtLeast", { minimum: 1 });
  }
  const durationMs = intAt(session.duration_ms, `${path}.duration_ms`);
  if (durationMs < 0) invalid(`${path}.duration_ms`, "nonNegativeInteger");
  const runningSince = nullableStringAt(
    session.running_since,
    `${path}.running_since`,
  );
  if (runningSince !== null && !Number.isFinite(Date.parse(runningSince))) {
    invalid(`${path}.running_since`, "timestamp");
  }
  if (!Array.isArray(session.active_request_starts)) {
    invalid(`${path}.active_request_starts`, "array");
  }
  const activeRequestStarts = session.active_request_starts.map(
    (value, index) => {
      const itemPath = `${path}.active_request_starts[${index}]`;
      const started = stringAt(value, itemPath);
      if (!Number.isFinite(Date.parse(started))) invalid(itemPath, "timestamp");
      return started;
    },
  );
  return {
    ...optionalClientType(session.client_type),
    id: stringAt(session.id, `${path}.id`),
    title: stringAt(session.title, `${path}.title`),
    started_at: stringAt(session.started_at, `${path}.started_at`),
    last_started_at: stringAt(
      session.last_started_at,
      `${path}.last_started_at`,
    ),
    completed_at: nullableStringAt(
      session.completed_at,
      `${path}.completed_at`,
    ),
    duration_ms: durationMs,
    running_since: runningSince,
    tool_duration_ms: optionalPerformanceNumber(
      session.tool_duration_ms,
      `${path}.tool_duration_ms`,
      true,
    ),
    average_ttft_ms: optionalPerformanceNumber(
      session.average_ttft_ms,
      `${path}.average_ttft_ms`,
    ),
    output_tokens_per_second: optionalPerformanceNumber(
      session.output_tokens_per_second,
      `${path}.output_tokens_per_second`,
    ),
    active_request_starts: activeRequestStarts,
    turn_count: turnCount,
    call_count: callCount,
    status: session.status as SessionStatus,
    requested_model: nullableStringAt(
      session.requested_model,
      `${path}.requested_model`,
    ),
    ...optionalModelRedirect(session.model_redirect, `${path}.model_redirect`),
    ...optionalOutput(session.output, `${path}.output`),
    reasoning_effort:
      session.reasoning_effort == null
        ? null
        : nullableStringAt(
            session.reasoning_effort,
            `${path}.reasoning_effort`,
          ),
    input_protocol: stringAt(session.input_protocol, `${path}.input_protocol`),
    service_id: nullableStringAt(session.service_id, `${path}.service_id`),
    local_access_token_id: nullableStringAt(
      session.local_access_token_id,
      `${path}.local_access_token_id`,
    ),
    ...(session.privacy_warned == null
      ? {}
      : {
          privacy_warned: boolAt(
            session.privacy_warned,
            `${path}.privacy_warned`,
          ),
        }),
    ...optionalParentSession(session.parent_session_id, path),
    ...(session.subagent_count == null
      ? {}
      : {
          subagent_count: nonNegativeIntAt(
            session.subagent_count,
            `${path}.subagent_count`,
          ),
        }),
    ...optionalCount(session, "context_tokens", path),
    ...optionalCount(session, "compaction_point_tokens", path),
    ...optionalCount(session, "context_window_tokens", path),
    ...optionalCount(session, "compaction_count", path),
    ...(session.usage == null
      ? {}
      : { usage: parseSessionUsage(session.usage, `${path}.usage`) }),
    ...(session.cost == null
      ? {}
      : { cost: parseSessionCost(session.cost, `${path}.cost`) }),
    ...(session.failed_repeats == null
      ? {}
      : {
          failed_repeats: parseFailedRepeats(
            session.failed_repeats,
            `${path}.failed_repeats`,
          ),
        }),
  };
}

type SessionCountField =
  | "context_tokens"
  | "compaction_point_tokens"
  | "context_window_tokens"
  | "compaction_count";

function optionalCount<Field extends SessionCountField>(
  value: JsonObject,
  field: Field,
  path: string,
): Partial<Record<Field, number>> {
  if (value[field] == null) return {};
  return {
    [field]: nonNegativeIntAt(value[field], `${path}.${field}`),
  } as Partial<Record<Field, number>>;
}

const usdAmountPattern = /^(0|[1-9][0-9]*)(\.[0-9]{1,9})?$/;

function usdAmountAt(value: unknown, path: string): string {
  const amount = stringAt(value, path);
  if (!usdAmountPattern.test(amount)) invalid(path, "badFormat");
  return amount;
}

function parseSessionUsage(value: unknown, path: string): SessionUsage {
  const usage = objectAt(value, path);
  const ratio = usage.cache_hit_ratio;
  if (
    ratio != null &&
    (typeof ratio !== "number" || !(ratio >= 0 && ratio <= 1))
  ) {
    invalid(`${path}.cache_hit_ratio`, "nonNegativeNumber");
  }
  return {
    input_tokens: nonNegativeIntAt(usage.input_tokens, `${path}.input_tokens`),
    output_tokens: nonNegativeIntAt(
      usage.output_tokens,
      `${path}.output_tokens`,
    ),
    cache_read_tokens: nonNegativeIntAt(
      usage.cache_read_tokens,
      `${path}.cache_read_tokens`,
    ),
    cache_write_tokens: nonNegativeIntAt(
      usage.cache_write_tokens,
      `${path}.cache_write_tokens`,
    ),
    ...(ratio == null ? {} : { cache_hit_ratio: ratio as number }),
  };
}

function parseSessionCost(value: unknown, path: string): SessionCost {
  const cost = objectAt(value, path);
  return {
    amount_usd: usdAmountAt(cost.amount_usd, `${path}.amount_usd`),
    priced: nonNegativeIntAt(cost.priced, `${path}.priced`),
    unpriced: nonNegativeIntAt(cost.unpriced, `${path}.unpriced`),
    pending: nonNegativeIntAt(cost.pending, `${path}.pending`),
  };
}

const cacheMissReasons = new Set<CacheMissReason>([
  "provider_changed",
  "model_changed",
  "expired",
  "prefix_changed",
  "unknown",
]);

function parseSessionContext(value: unknown, path: string): SessionContext {
  const context = objectAt(value, path);
  for (const field of ["calls", "compactions", "cache_misses"]) {
    if (!Array.isArray(context[field])) invalid(`${path}.${field}`, "array");
  }
  const calls = context.calls as unknown[];
  const compactions = context.compactions as unknown[];
  const misses = context.cache_misses as unknown[];
  return {
    calls: calls.map((item, index) => {
      const itemPath = `${path}.calls[${index}]`;
      const call = objectAt(item, itemPath);
      return {
        request_id: stringAt(call.request_id, `${itemPath}.request_id`),
        tokens: nonNegativeIntAt(call.tokens, `${itemPath}.tokens`),
      };
    }),
    compactions: compactions.map((item, index) => {
      const itemPath = `${path}.compactions[${index}]`;
      const compaction = objectAt(item, itemPath);
      return {
        request_id: stringAt(compaction.request_id, `${itemPath}.request_id`),
        ...(compaction.summary_request_id == null
          ? {}
          : {
              summary_request_id: stringAt(
                compaction.summary_request_id,
                `${itemPath}.summary_request_id`,
              ),
            }),
        before_tokens: nonNegativeIntAt(
          compaction.before_tokens,
          `${itemPath}.before_tokens`,
        ),
        after_tokens: nonNegativeIntAt(
          compaction.after_tokens,
          `${itemPath}.after_tokens`,
        ),
        marked: boolAt(compaction.marked, `${itemPath}.marked`),
      };
    }),
    cache_misses: misses.map((item, index) => {
      const itemPath = `${path}.cache_misses[${index}]`;
      const miss = objectAt(item, itemPath);
      if (
        typeof miss.reason !== "string" ||
        !cacheMissReasons.has(miss.reason as CacheMissReason)
      ) {
        invalid(`${itemPath}.reason`, "unknownReason");
      }
      return {
        request_id: stringAt(miss.request_id, `${itemPath}.request_id`),
        reason: miss.reason as CacheMissReason,
        ...(miss.changed == null
          ? {}
          : { changed: stringAt(miss.changed, `${itemPath}.changed`) }),
        ...(miss.idle_ms == null
          ? {}
          : { idle_ms: nonNegativeIntAt(miss.idle_ms, `${itemPath}.idle_ms`) }),
        ...(miss.ttl_ms == null
          ? {}
          : { ttl_ms: nonNegativeIntAt(miss.ttl_ms, `${itemPath}.ttl_ms`) }),
        rewritten_tokens: nonNegativeIntAt(
          miss.rewritten_tokens,
          `${itemPath}.rewritten_tokens`,
        ),
        ...(miss.extra_usd == null
          ? {}
          : {
              extra_usd: usdAmountAt(miss.extra_usd, `${itemPath}.extra_usd`),
            }),
      };
    }),
  };
}

const contextPartFields = [
  "history",
  "tool_results",
  "tools",
  "system",
  "memory",
  "other",
] as const;
const contextItemKinds = new Set<ContextItemKind>([
  "rules",
  "memory",
  "skills",
  "mcp",
]);
const contextItemScopes = new Set<ContextItemScope>([
  "global",
  "project",
  "local",
]);

function optionalStringField<Field extends string>(
  value: JsonObject,
  field: Field,
  path: string,
): Partial<Record<Field, string>> {
  if (value[field] == null) return {};
  return { [field]: stringAt(value[field], `${path}.${field}`) } as Partial<
    Record<Field, string>
  >;
}

function parseFailedRepeats(value: unknown, path: string): FailedRepeats {
  const repeats = objectAt(value, path);
  return {
    tool: stringAt(repeats.tool, `${path}.tool`),
    count: nonNegativeIntAt(repeats.count, `${path}.count`),
    ...(repeats.command == null
      ? {}
      : { command: boolAt(repeats.command, `${path}.command`) }),
  };
}

/** The fields a carried item has wherever it is listed, but its hash. */
function contextItemFields(
  item: JsonObject,
  path: string,
): Omit<ContextItem, "hash"> {
  if (
    item.scope != null &&
    !contextItemScopes.has(item.scope as ContextItemScope)
  ) {
    invalid(`${path}.scope`, "unknownScope");
  }
  return {
    kind: item.kind as ContextItemKind,
    ...optionalStringField(item, "name", path),
    ...optionalStringField(item, "path", path),
    ...(item.scope == null ? {} : { scope: item.scope as ContextItemScope }),
    ...optionalIntField(item, "count", path),
    ...optionalStringList(item, "servers", path),
    tokens: nonNegativeIntAt(item.tokens, `${path}.tokens`),
  };
}

function optionalIntField<Field extends string>(
  value: JsonObject,
  field: Field,
  path: string,
): Partial<Record<Field, number>> {
  if (value[field] == null) return {};
  return {
    [field]: nonNegativeIntAt(value[field], `${path}.${field}`),
  } as Partial<Record<Field, number>>;
}

function optionalStringList<Field extends string>(
  value: JsonObject,
  field: Field,
  path: string,
): Partial<Record<Field, string[]>> {
  const list = value[field];
  if (list == null) return {};
  if (!Array.isArray(list)) invalid(`${path}.${field}`, "array");
  return {
    [field]: (list as unknown[]).map((entry, index) =>
      stringAt(entry, `${path}.${field}[${index}]`),
    ),
  } as Partial<Record<Field, string[]>>;
}

/**
 * What a call carried. Items of a kind this build does not know are left
 * out rather than failing the record.
 */
function parseRequestContext(value: unknown, path: string): RequestContext {
  const context = objectAt(value, path);
  let parts: ContextParts | undefined;
  if (context.parts != null) {
    const raw = objectAt(context.parts, `${path}.parts`);
    parts = Object.fromEntries(
      contextPartFields.map((field) => [
        field,
        nonNegativeIntAt(raw[field], `${path}.parts.${field}`),
      ]),
    ) as unknown as ContextParts;
  }
  const items: ContextItem[] = [];
  if (context.items != null) {
    if (!Array.isArray(context.items)) invalid(`${path}.items`, "array");
    (context.items as unknown[]).forEach((entry, index) => {
      const itemPath = `${path}.items[${index}]`;
      const item = objectAt(entry, itemPath);
      if (!contextItemKinds.has(item.kind as ContextItemKind)) return;
      items.push({
        ...contextItemFields(item, itemPath),
        ...optionalStringField(item, "hash", itemPath),
      });
    });
  }
  const writes: MemoryWrite[] = [];
  if (context.memory_writes != null) {
    if (!Array.isArray(context.memory_writes)) {
      invalid(`${path}.memory_writes`, "array");
    }
    (context.memory_writes as unknown[]).forEach((entry, index) => {
      const writePath = `${path}.memory_writes[${index}]`;
      const write = objectAt(entry, writePath);
      if (write.kind !== "rules" && write.kind !== "memory") return;
      writes.push({
        kind: write.kind,
        name: stringAt(write.name, `${writePath}.name`),
        ...optionalStringField(write, "path", writePath),
        ...optionalStringField(write, "title", writePath),
        ...optionalStringField(write, "entry", writePath),
        ...optionalStringField(write, "hook", writePath),
      });
    });
  }
  let reads: MemoryRead[] | undefined;
  if (context.memory_reads != null) {
    if (!Array.isArray(context.memory_reads)) {
      invalid(`${path}.memory_reads`, "array");
    }
    reads = (context.memory_reads as unknown[]).map((entry, index) => {
      const readPath = `${path}.memory_reads[${index}]`;
      const read = objectAt(entry, readPath);
      return {
        name: stringAt(read.name, `${readPath}.name`),
        ...optionalStringField(read, "path", readPath),
      };
    });
  }
  const flag = (field: "summary" | "helper") =>
    context[field] == null
      ? {}
      : { [field]: boolAt(context[field], `${path}.${field}`) };
  return {
    ...(parts ? { parts } : {}),
    items,
    ...optionalStringField(context, "system_hash", path),
    ...optionalStringField(context, "tools_hash", path),
    ...flag("summary"),
    ...optionalStringField(context, "compacted", path),
    ...flag("helper"),
    memory_writes: writes,
    ...(reads ? { memory_reads: reads } : {}),
    ...optionalStringList(context, "skills_used", path),
    ...(context.failed_repeats == null
      ? {}
      : {
          failed_repeats: parseFailedRepeats(
            context.failed_repeats,
            `${path}.failed_repeats`,
          ),
        }),
  };
}

/** The optional link of a subagent's conversation to its parent. */
function optionalParentSession(
  value: unknown,
  path: string,
): { parent_session_id?: string } {
  return value == null
    ? {}
    : { parent_session_id: stringAt(value, `${path}.parent_session_id`) };
}

function nonNegativeIntAt(value: unknown, path: string): number {
  const count = intAt(value, path);
  if (count < 0) invalid(path, "nonNegativeInteger");
  return count;
}

export function parseRequestSessionPage(value: unknown): RequestSessionPage {
  const page = objectAt(value, "$");
  if (!Array.isArray(page.items)) invalid("$.items", "array");
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

export function parseRequestActivity(value: unknown): RequestActivity {
  const activity = objectAt(value, "$");
  if (!Array.isArray(activity.calls)) invalid("$.calls", "array");
  return {
    now: stringAt(activity.now, "$.now"),
    calls: activity.calls.map((item, index) => {
      const path = `$.calls[${index}]`;
      const call = objectAt(item, path);
      if (
        typeof call.status !== "string" ||
        !statuses.has(call.status as RequestStatus)
      ) {
        invalid(`${path}.status`, "unknownStatus");
      }
      return {
        session_id: stringAt(call.session_id, `${path}.session_id`),
        ...optionalParentSession(call.parent_session_id, path),
        ...optionalClientType(call.client_type),
        ...(call.model == null
          ? {}
          : { model: stringAt(call.model, `${path}.model`) }),
        started_at: stringAt(call.started_at, `${path}.started_at`),
        completed_at: nullableStringAt(
          call.completed_at,
          `${path}.completed_at`,
        ),
        status: call.status as RequestStatus,
        ...optionalOutput(call.output, `${path}.output`),
        ...(call.output_tokens == null
          ? {}
          : {
              output_tokens: nonNegativeIntAt(
                call.output_tokens,
                `${path}.output_tokens`,
              ),
            }),
        ...(call.service_id == null
          ? {}
          : { service_id: stringAt(call.service_id, `${path}.service_id`) }),
        ...(call.failed_service_id == null
          ? {}
          : {
              failed_service_id: stringAt(
                call.failed_service_id,
                `${path}.failed_service_id`,
              ),
            }),
        ...(call.rewrites == null
          ? {}
          : { rewrites: gatewayRewritesAt(call.rewrites, `${path}.rewrites`) }),
      };
    }),
  };
}

/** Known rewrites in the gateway's order; a newer Core's others are left out. */
function gatewayRewritesAt(value: unknown, path: string): GatewayRewrite[] {
  const named = arrayAt(value, path).map((item, index) =>
    stringAt(item, `${path}[${index}]`),
  );
  return GATEWAY_REWRITES.filter((rewrite) => named.includes(rewrite));
}

/** A required client label; one this build does not know reads as unknown. */
function clientTypeAt(value: unknown): ClientType {
  return optionalClientType(value).client_type ?? "unknown";
}

function arrayAt(value: unknown, path: string): unknown[] {
  if (!Array.isArray(value)) invalid(path, "array");
  return value as unknown[];
}

function optionalBoolField<Field extends string>(
  value: JsonObject,
  field: Field,
  path: string,
): Partial<Record<Field, boolean>> {
  if (value[field] == null) return {};
  return {
    [field]: boolAt(value[field], `${path}.${field}`),
  } as Partial<Record<Field, boolean>>;
}

function parseMemoryEntry(value: unknown, path: string): MemoryEntry {
  const entry = objectAt(value, path);
  return {
    file: stringAt(entry.file, `${path}.file`),
    ...optionalStringField(entry, "path", path),
    ...optionalStringField(entry, "title", path),
    ...optionalStringField(entry, "hook", path),
    ...optionalStringField(entry, "noted_at", path),
    reads: arrayAt(entry.reads, `${path}.reads`).map((count, index) =>
      nonNegativeIntAt(count, `${path}.reads[${index}]`),
    ),
    total_reads: nonNegativeIntAt(entry.total_reads, `${path}.total_reads`),
  };
}

function parseMemoryToolbox(value: unknown, path: string): MemoryToolbox {
  const toolbox = objectAt(value, path);
  return {
    since: stringAt(toolbox.since, `${path}.since`),
    skills: arrayAt(toolbox.skills, `${path}.skills`).map((entry, index) => {
      const skillPath = `${path}.skills[${index}]`;
      const skill = objectAt(entry, skillPath);
      return {
        name: stringAt(skill.name, `${skillPath}.name`),
        ...optionalStringField(skill, "description", skillPath),
        uses: nonNegativeIntAt(skill.uses, `${skillPath}.uses`),
        carried: boolAt(skill.carried, `${skillPath}.carried`),
      };
    }),
    mcp: arrayAt(toolbox.mcp, `${path}.mcp`).map((entry, index) => {
      const serverPath = `${path}.mcp[${index}]`;
      const server = objectAt(entry, serverPath);
      return {
        server: stringAt(server.server, `${serverPath}.server`),
        tools: arrayAt(server.tools, `${serverPath}.tools`).map(
          (raw, toolIndex) => {
            const toolPath = `${serverPath}.tools[${toolIndex}]`;
            const tool = objectAt(raw, toolPath);
            return {
              name: stringAt(tool.name, `${toolPath}.name`),
              uses: nonNegativeIntAt(tool.uses, `${toolPath}.uses`),
              carried: boolAt(tool.carried, `${toolPath}.carried`),
            };
          },
        ),
      };
    }),
    idle_tokens: nonNegativeIntAt(toolbox.idle_tokens, `${path}.idle_tokens`),
  };
}

/** What one client's agents remembered in one project. */
export function parseProjectMemory(value: unknown): ProjectMemory {
  const memory = objectAt(value, "$");
  const projects = arrayAt(memory.projects, "$.projects").map(
    (entry, index) => {
      const path = `$.projects[${index}]`;
      const project = objectAt(entry, path);
      return {
        ...optionalStringField(project, "project", path),
        client_types: arrayAt(project.client_types, `${path}.client_types`).map(
          clientTypeAt,
        ),
        last_seen_at: stringAt(project.last_seen_at, `${path}.last_seen_at`),
      };
    },
  );
  const rules: MemoryRules[] = arrayAt(memory.rules, "$.rules").map(
    (entry, index) => {
      const path = `$.rules[${index}]`;
      const file = objectAt(entry, path);
      if (
        file.scope != null &&
        !contextItemScopes.has(file.scope as ContextItemScope)
      ) {
        invalid(`${path}.scope`, "unknownScope");
      }
      return {
        name: stringAt(file.name, `${path}.name`),
        ...optionalStringField(file, "path", path),
        ...(file.scope == null
          ? {}
          : { scope: file.scope as ContextItemScope }),
        ...optionalStringField(file, "first_seen_at", path),
        ...optionalStringField(file, "changed_at", path),
        ...optionalStringField(file, "request_id", path),
        ...optionalStringField(file, "session_id", path),
        ...optionalStringField(file, "before_request_id", path),
        ...optionalBoolField(file, "captured", path),
      };
    },
  );
  return {
    projects,
    ...optionalStringField(memory, "project", "$"),
    ...(memory.client_type == null
      ? {}
      : { client_type: clientTypeAt(memory.client_type) }),
    day_start: stringAt(memory.day_start, "$.day_start"),
    recent: nonNegativeIntAt(memory.recent, "$.recent"),
    entries: arrayAt(memory.entries, "$.entries").map((entry, index) =>
      parseMemoryEntry(entry, `$.entries[${index}]`),
    ),
    stories: arrayAt(memory.stories, "$.stories").map((entry, index) => {
      const path = `$.stories[${index}]`;
      const story = objectAt(entry, path);
      return {
        file: stringAt(story.file, `${path}.file`),
        ...optionalStringField(story, "title", path),
        ...optionalStringField(story, "hook", path),
        at: stringAt(story.at, `${path}.at`),
        ...optionalStringField(story, "request_id", path),
        ...optionalStringField(story, "session_id", path),
        ...optionalStringField(story, "prompt", path),
      };
    }),
    rules,
    toolbox: parseMemoryToolbox(memory.toolbox, "$.toolbox"),
    has_memory: boolAt(memory.has_memory, "$.has_memory"),
    has_rules: boolAt(memory.has_rules, "$.has_rules"),
  };
}

/** One memory and its events; events of a kind unknown here are left out. */
export function parseMemoryEntryDetail(value: unknown): MemoryEntryDetail {
  const detail = objectAt(value, "$");
  const events: MemoryEvent[] = [];
  arrayAt(detail.events, "$.events").forEach((entry, index) => {
    const path = `$.events[${index}]`;
    const event = objectAt(entry, path);
    if (event.kind !== "noted" && event.kind !== "read") return;
    events.push({
      kind: event.kind,
      at: stringAt(event.at, `${path}.at`),
      request_id: stringAt(event.request_id, `${path}.request_id`),
      ...optionalStringField(event, "session_id", path),
      ...optionalStringField(event, "prompt", path),
      ...optionalBoolField(event, "captured", path),
    });
  });
  return { entry: parseMemoryEntry(detail.entry, "$.entry"), events };
}

/** What a captured call carried, as text; unknown kinds are left out. */
export function parseRequestContextTexts(value: unknown): RequestContextTexts {
  const texts = objectAt(value, "$");
  for (const field of ["items", "writes", "reads"]) {
    if (!Array.isArray(texts[field])) invalid(`$.${field}`, "array");
  }
  let reason: AuditWithheldReason | undefined;
  if (texts.reason != null) {
    const raw = stringAt(texts.reason, "$.reason");
    if (!auditWithheldReasons.has(raw as AuditWithheldReason)) {
      invalid("$.reason", "unknownReason");
    }
    reason = raw as AuditWithheldReason;
  }
  const items: ContextItemText[] = [];
  (texts.items as unknown[]).forEach((entry, index) => {
    const path = `$.items[${index}]`;
    const item = objectAt(entry, path);
    if (
      item.kind !== "rules" &&
      item.kind !== "memory" &&
      item.kind !== "skills"
    ) {
      return;
    }
    items.push({
      kind: item.kind,
      ...optionalStringField(item, "name", path),
      ...optionalStringField(item, "path", path),
      text: stringAt(item.text, `${path}.text`),
    });
  });
  const writes: MemoryWriteText[] = [];
  (texts.writes as unknown[]).forEach((entry, index) => {
    const path = `$.writes[${index}]`;
    const write = objectAt(entry, path);
    if (write.kind !== "rules" && write.kind !== "memory") return;
    writes.push({
      kind: write.kind,
      name: stringAt(write.name, `${path}.name`),
      ...optionalStringField(write, "path", path),
      content: stringAt(write.content, `${path}.content`),
    });
  });
  return {
    request_id: stringAt(texts.request_id, "$.request_id"),
    ...(texts.withheld == null
      ? {}
      : { withheld: boolAt(texts.withheld, "$.withheld") }),
    ...(reason ? { reason } : {}),
    ...(texts.truncated == null
      ? {}
      : { truncated: boolAt(texts.truncated, "$.truncated") }),
    items,
    ...optionalStringField(texts, "summary", "$"),
    writes,
    reads: (texts.reads as unknown[]).map((entry, index) => {
      const path = `$.reads[${index}]`;
      const read = objectAt(entry, path);
      return {
        name: stringAt(read.name, `${path}.name`),
        ...optionalStringField(read, "path", path),
        content: stringAt(read.content, `${path}.content`),
      };
    }),
  };
}

export function parseRequestSessionDetail(
  value: unknown,
): RequestSessionDetail {
  const session = parseRequestSessionAt(value, "$");
  const detail = objectAt(value, "$");
  if (!Array.isArray(detail.turns)) invalid("$.turns", "array");
  if (detail.subagents != null && !Array.isArray(detail.subagents)) {
    invalid("$.subagents", "array");
  }
  return {
    ...session,
    turns: detail.turns.map((item, index) =>
      parseRequestRecordAt(item, `$.turns[${index}]`),
    ),
    ...(Array.isArray(detail.subagents)
      ? {
          subagents: detail.subagents.map((item, index) =>
            parseRequestSessionAt(item, `$.subagents[${index}]`),
          ),
        }
      : {}),
    ...(detail.context == null
      ? {}
      : { context: parseSessionContext(detail.context, "$.context") }),
    ...(detail.with_subagents == null
      ? {}
      : { with_subagents: parseSessionTotals(detail.with_subagents) }),
  };
}

function parseSessionTotals(value: unknown): SessionTotals {
  const totals = objectAt(value, "$.with_subagents");
  return {
    call_count: nonNegativeIntAt(
      totals.call_count,
      "$.with_subagents.call_count",
    ),
    input_tokens: nonNegativeIntAt(
      totals.input_tokens,
      "$.with_subagents.input_tokens",
    ),
    output_tokens: nonNegativeIntAt(
      totals.output_tokens,
      "$.with_subagents.output_tokens",
    ),
  };
}

export function parseRequestRecordPage(value: unknown): RequestRecordPage {
  const page = objectAt(value, "$");
  if (!Array.isArray(page.items)) invalid("$.items", "array");
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

const auditWithheldReasons = new Set<AuditWithheldReason>([
  "privacy_redacted",
  "privacy_blocked",
  "privacy_restored",
  "privacy_fail_open",
  "privacy_trusted",
  "privacy_pending",
  "privacy_unknown",
  "raw_locked",
  "raw_not_kept",
]);

const auditBodyPartNames: AuditBodyPartName[] = [
  "request_body",
  "response_content",
  "upstream_request_body",
  "upstream_response_content",
];

type ParsedAuditPart =
  | { readable: AuditContentPart; withheld?: undefined }
  | { readable?: undefined; withheld: AuditWithheldPart };

function parseAuditContentPart(
  value: unknown,
  path: string,
): ParsedAuditPart | null {
  if (value === null || value === undefined) return null;
  const part = objectAt(value, path);
  const media_type = stringAt(part.media_type, `${path}.media_type`);
  const truncated = boolAt(part.truncated, `${path}.truncated`);
  const captured_bytes = intAt(part.captured_bytes, `${path}.captured_bytes`);
  if (Object.hasOwn(part, "withheld")) {
    if (boolAt(part.withheld, `${path}.withheld`) !== true) {
      invalid(`${path}.withheld`, "unexpectedValue");
    }
    if (Object.hasOwn(part, "content")) {
      invalid(`${path}.content`, "unsupportedField");
    }
    const reason = stringAt(part.reason, `${path}.reason`);
    if (!auditWithheldReasons.has(reason as AuditWithheldReason)) {
      invalid(`${path}.reason`, "unknownReason");
    }
    return {
      withheld: {
        reason: reason as AuditWithheldReason,
        raw_available: boolAt(part.raw_available, `${path}.raw_available`),
        media_type,
        truncated,
        captured_bytes,
      },
    };
  }
  const readable: AuditContentPart = {
    media_type,
    content: stringAt(part.content, `${path}.content`),
    truncated,
    captured_bytes,
  };
  if (Object.hasOwn(part, "exposure")) {
    const exposure = stringAt(part.exposure, `${path}.exposure`);
    if (exposure !== "shareable" && exposure !== "raw") {
      invalid(`${path}.exposure`, "unexpectedValue");
    }
    readable.exposure = exposure;
  }
  return { readable };
}

function parseAuditPrivacyFinding(
  value: unknown,
  path: string,
): AuditPrivacyFinding {
  const finding = objectAt(value, path);
  const kind = stringAt(finding.kind, `${path}.kind`);
  if (kind === "") invalid(`${path}.kind`, "empty");
  return {
    kind,
    json_path: stringAt(finding.json_path, `${path}.json_path`),
    count: intAt(finding.count, `${path}.count`),
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
    invalid(`${path}.request_headers`, "array");
  }
  if (!Array.isArray(meta.response_headers)) {
    invalid(`${path}.response_headers`, "array");
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
  const view = Object.hasOwn(content, "view")
    ? stringAt(content.view, "$.view")
    : "full";
  if (view !== "shareable" && view !== "full") {
    invalid("$.view", "unexpectedValue");
  }
  const withheld: AuditContent["withheld"] = {};
  const parts = {} as Record<AuditBodyPartName, AuditContentPart | null>;
  // Upstream parts may be absent from a core sidecar that predates them.
  for (const name of auditBodyPartNames) {
    const parsed = parseAuditContentPart(content[name], `$.${name}`);
    parts[name] = parsed?.readable ?? null;
    if (parsed?.withheld) withheld[name] = parsed.withheld;
  }
  const findings = Object.hasOwn(content, "privacy_findings")
    ? content.privacy_findings
    : [];
  if (!Array.isArray(findings)) {
    invalid("$.privacy_findings", "array");
  }
  return {
    request_id: stringAt(content.request_id, "$.request_id"),
    view,
    // Tolerate an absent key for compatibility with a core sidecar that
    // predates http_meta capture.
    http_meta: Object.hasOwn(content, "http_meta")
      ? parseAuditHTTPMeta(content.http_meta, "$.http_meta")
      : null,
    request_body: parts.request_body,
    response_content: parts.response_content,
    upstream_http_meta: Object.hasOwn(content, "upstream_http_meta")
      ? parseAuditHTTPMeta(content.upstream_http_meta, "$.upstream_http_meta")
      : null,
    upstream_request_body: parts.upstream_request_body,
    upstream_response_content: parts.upstream_response_content,
    withheld,
    privacy_findings: findings.map((finding, index) =>
      parseAuditPrivacyFinding(finding, `$.privacy_findings[${index}]`),
    ),
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
