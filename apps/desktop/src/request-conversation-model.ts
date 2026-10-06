import { i18n } from "./i18n";
import {
  displayRequestStatus,
  type AuditContent,
  type AuditContentPart,
  type RequestRecord,
} from "./request-record-model";
import {
  clientDisconnect,
  groupTurns,
  type TrajectoryTone,
} from "./request-trajectory-model";
import type { ResponseOutput } from "./response-preview-model";

/**
 * The conversation view reads a session the way the operator experienced it:
 * one user turn, the agent loop that answered it, the reply. The gateway only
 * sees model calls, so everything here is rebuilt from request bodies (what the
 * client sent back, including tool results) and response outputs (what the
 * model said and which tools it asked for).
 */

/** Text a coding client wraps around the operator's own words. */
export type ClientWrapperKind = "pasted" | "system" | "command";

export interface ClientWrapper {
  kind: ClientWrapperKind;
  /** Characters inside the wrapper, for the folded label. */
  chars: number;
  /** The command name for `command` wrappers. */
  name?: string;
}

export interface UserText {
  text: string;
  wrappers: ClientWrapper[];
  /** `preview` is the 80-rune summary Core keeps for every call. */
  source: "body" | "preview" | "none";
}

/** A tool result the client handed back in its next request. */
export interface ToolResult {
  id: string | null;
  name: string | null;
  text: string;
  isError: boolean;
}

/** A tool the model asked for, paired with what the client brought back. */
export interface ConversationToolCall {
  id: string | null;
  name: string;
  args: string;
  result: string | null;
  isError: boolean;
}

/** What one call's captured content yields once parsed. */
export interface ParsedCall {
  requestId: string;
  user: UserText | null;
  toolResults: ToolResult[];
  outputs: ResponseOutput[];
  /** A captured part Core withholds until raw reading is unlocked. */
  locked: boolean;
  /** Neither body was captured, so there is nothing to read. */
  captured: boolean;
  /** The response was readable, so the tool calls below are the whole list. */
  responseCaptured: boolean;
}

export interface ConversationCall {
  record: RequestRecord;
  /** Earlier attempts of this call that failed before the recorded one. */
  children: RequestRecord[];
  /** 1-based position within the turn. */
  index: number;
  tone: TrajectoryTone;
  /** Shown expanded by default: failed, retried, blocked, a tool error. */
  anomaly: boolean;
  /** This call wrote the turn's final reply, which is shown beneath its row. */
  reply: boolean;
  /** One line on why the call failed or had to retry. */
  reason: string | null;
  /** Text the model wrote around its tool calls, not the final reply. */
  narration: string[];
  /** What the model thought before answering, as the provider exposed it. */
  reasoning: string[];
  /**
   * How long the model thought before its first answer: from the first
   * streamed token to the first non-reasoning output. Null for non-streaming
   * and older records, and while a running call is still thinking.
   */
  reasoningMs: number | null;
  toolCalls: ConversationToolCall[];
  /** Null until the response content has been read. */
  toolCount: number | null;
  /** Time from this call's end to the next call's start in the same turn. */
  gapMs: number | null;
  durationMs: number | null;
  tokens: TokenUsage | null;
  parsed: ParsedCall | null;
}

export interface TokenUsage {
  input: number;
  output: number;
}

export type ConversationSegment =
  | { kind: "call"; call: ConversationCall }
  | {
      kind: "run";
      calls: ConversationCall[];
      /** Tool name → count over the calls already read. */
      toolCounts: Array<[string, number]>;
      /** Null while a call in the run still has no content. */
      toolTotal: number | null;
      durationMs: number;
      tokens: TokenUsage | null;
    };

export interface ConversationTurnStats {
  calls: number;
  /** Null while no call has been read; `partial` when some have not. */
  toolCalls: number | null;
  toolCallsPartial: boolean;
  failed: number;
  /** Calls that ended without an answer because the call was cancelled. */
  cancelled: number;
  /** Those of the cancelled calls where the client hung up. */
  disconnected: number;
  durationMs: number;
  tokens: TokenUsage | null;
  pending: boolean;
}

export interface ConversationTurn {
  id: string;
  turnIndex: number | null;
  calls: ConversationCall[];
  segments: ConversationSegment[];
  user: UserText;
  startedAt: string;
  /** The final reply text and the call that produced it. */
  reply: { text: string | null; record: RequestRecord; at: string | null };
  stats: ConversationTurnStats;
  /** Every call that was read is locked, so the bodies need an unlock. */
  locked: boolean;
}

type JsonObject = Record<string, unknown>;

const object = (value: unknown): JsonObject =>
  value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as JsonObject)
    : {};
const array = (value: unknown): unknown[] =>
  Array.isArray(value) ? value : [];
const string = (value: unknown): string =>
  typeof value === "string" ? value : "";

/** Longest text kept per tool argument or result; the inspector has the rest. */
export const TOOL_TEXT_LIMIT = 16 * 1024;

const WRAPPERS: ReadonlyArray<{ kind: ClientWrapperKind; pattern: RegExp }> = [
  {
    kind: "system",
    pattern: /<system-reminder>([\s\S]*?)<\/system-reminder>/g,
  },
  {
    kind: "pasted",
    pattern: /<pasted_content\b[^>]*>([\s\S]*?)<\/pasted_content>/g,
  },
  { kind: "command", pattern: /<command-name>([\s\S]*?)<\/command-name>/g },
];
const COMMAND_PARTS = /<command-(message|args)>[\s\S]*?<\/command-\1>/g;

/**
 * Pulls the client's own wrappers out of a user message so the words the
 * operator typed read on their own. Claude Code pastes long input inside
 * `pasted_content`, adds `system-reminder` blocks beside tool results, and
 * sends a skill invocation as `command-name` / `command-message` /
 * `command-args`.
 */
export function stripClientWrappers(text: string): UserText {
  const wrappers: ClientWrapper[] = [];
  let rest = text;
  for (const { kind, pattern } of WRAPPERS) {
    rest = rest.replace(pattern, (_match, inner: string) => {
      wrappers.push(
        kind === "command"
          ? { kind, chars: inner.length, name: inner.trim() }
          : { kind, chars: inner.length },
      );
      return "";
    });
  }
  rest = rest.replace(COMMAND_PARTS, "");
  return {
    text: rest.replace(/\n{3,}/g, "\n\n").trim(),
    wrappers,
    source: "body",
  };
}

function parseBody(body: string): JsonObject | null {
  try {
    const value: unknown = JSON.parse(body);
    const parsed = object(value);
    return Object.keys(parsed).length > 0 ? parsed : null;
  } catch {
    return null;
  }
}

type BodyShape = "responses" | "messages" | "contents";

function bodyShape(protocol: string): BodyShape | null {
  if (protocol.startsWith("openai.responses")) return "responses";
  if (protocol === "openai.chat" || protocol === "anthropic.messages") {
    return "messages";
  }
  if (protocol === "google.generate_content") return "contents";
  return null;
}

function bodyItems(root: JsonObject, shape: BodyShape): unknown {
  switch (shape) {
    case "responses":
      return root.input;
    case "messages":
      return root.messages;
    case "contents":
      return root.contents;
  }
}

function isUserItem(item: JsonObject, shape: BodyShape): boolean {
  const role = string(item.role);
  if (shape === "responses") {
    const type = string(item.type);
    return role === "user" && (type === "" || type === "message");
  }
  // Gemini may leave the role off a single-turn request.
  return role === "user" || (shape === "contents" && role === "");
}

function textOfBlocks(blocks: unknown[]): string {
  return blocks
    .map((value) => {
      const block = object(value);
      const type = string(block.type);
      if (type === "text" || type === "input_text" || type === "output_text") {
        return string(block.text);
      }
      // Gemini parts carry text with no type.
      if (type === "" && typeof block.text === "string") return block.text;
      return "";
    })
    .filter(Boolean)
    .join("\n\n");
}

function userItemText(item: JsonObject, shape: BodyShape): string {
  const content = shape === "contents" ? item.parts : item.content;
  if (typeof content === "string") return content;
  return textOfBlocks(array(content));
}

/**
 * The operator's message a request is answering: the last user item that
 * says something beyond tool results and client reminders. Every call of an
 * agent loop replays the turn's history, so any call in the turn yields the
 * same text.
 */
export function extractLastUserText(
  protocol: string,
  body: string,
): UserText | null {
  const shape = bodyShape(protocol);
  const root = shape ? parseBody(body) : null;
  if (!shape || !root) return null;
  const items = bodyItems(root, shape);
  if (typeof items === "string") return stripClientWrappers(items);
  if (!Array.isArray(items)) return null;
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const item = object(items[index]);
    if (!isUserItem(item, shape)) continue;
    const raw = userItemText(item, shape);
    if (!raw.trim()) continue;
    const stripped = stripClientWrappers(raw);
    const onlyReminders =
      stripped.text === "" &&
      stripped.wrappers.every((wrapper) => wrapper.kind === "system");
    if (onlyReminders) continue;
    return stripped;
  }
  return null;
}

function resultText(content: unknown): string {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    const text = textOfBlocks(content);
    if (text) return text;
  }
  if (content == null) return "";
  return JSON.stringify(content, null, 2);
}

/**
 * The tool results the client sent back with this request, so they can sit
 * under the previous call's tool rows. Only the trailing results count: an
 * earlier user message's results answered an earlier call.
 */
export function extractToolResults(
  protocol: string,
  body: string,
): ToolResult[] {
  const shape = bodyShape(protocol);
  const root = shape ? parseBody(body) : null;
  if (!shape || !root) return [];
  const items = array(bodyItems(root, shape)).map(object);
  if (items.length === 0) return [];
  if (shape === "responses") {
    const results: ToolResult[] = [];
    for (let index = items.length - 1; index >= 0; index -= 1) {
      const item = items[index];
      const type = string(item.type);
      if (type !== "function_call_output" && type !== "custom_tool_call_output")
        break;
      results.unshift({
        id: string(item.call_id) || null,
        name: null,
        text: resultText(item.output),
        isError: false,
      });
    }
    return results;
  }
  if (shape === "contents") {
    const last = items[items.length - 1];
    return array(last.parts)
      .map(object)
      .filter((part) => part.functionResponse !== undefined)
      .map((part) => {
        const response = object(part.functionResponse);
        return {
          id: null,
          name: string(response.name) || null,
          text: resultText(response.response),
          isError: false,
        };
      });
  }
  // OpenAI Chat answers each call with a `tool` message; Anthropic puts
  // `tool_result` blocks inside the next user message.
  const last = items[items.length - 1];
  if (string(last.role) === "tool") {
    const results: ToolResult[] = [];
    for (let index = items.length - 1; index >= 0; index -= 1) {
      const item = items[index];
      if (string(item.role) !== "tool") break;
      results.unshift({
        id: string(item.tool_call_id) || null,
        name: string(item.name) || null,
        text: resultText(item.content),
        isError: false,
      });
    }
    return results;
  }
  if (string(last.role) !== "user") return [];
  return array(last.content)
    .map(object)
    .filter((block) => string(block.type) === "tool_result")
    .map((block) => ({
      id: string(block.tool_use_id) || null,
      name: null,
      text: resultText(block.content),
      isError: block.is_error === true,
    }));
}

function clip(text: string): string {
  return text.length > TOOL_TEXT_LIMIT ? text.slice(0, TOOL_TEXT_LIMIT) : text;
}

function withheldLocked(content: AuditContent): boolean {
  return [
    content.withheld.request_body,
    content.withheld.response_content,
  ].some((part) => part?.reason === "raw_locked");
}

/** The client response, or the provider's when the client one was not kept. */
export function conversationResponsePart(
  content: AuditContent,
): AuditContentPart | null {
  return content.response_content ?? content.upstream_response_content ?? null;
}

/**
 * Reads one call's content into what the conversation needs and nothing
 * more: bodies are megabytes, so the original text is let go here.
 */
export function parseCallContent(
  record: RequestRecord,
  content: AuditContent,
  outputs: ResponseOutput[],
): ParsedCall {
  const request = content.request_body ?? null;
  const protocol = record.input_protocol;
  return {
    requestId: record.id,
    user: request ? extractLastUserText(protocol, request.content) : null,
    toolResults: request
      ? extractToolResults(protocol, request.content).map((result) => ({
          ...result,
          text: clip(result.text),
        }))
      : [],
    outputs: outputs.map((output) =>
      output.kind === "tool" || output.kind === "other"
        ? { ...output, text: clip(output.text) }
        : output,
    ),
    locked: withheldLocked(content),
    captured: request !== null || conversationResponsePart(content) !== null,
    responseCaptured: conversationResponsePart(content) !== null,
  };
}

/**
 * Pairs the tools a call asked for with the results the next call brought
 * back: by id where the protocol has one, else by name and order.
 */
export function pairToolCalls(
  outputs: ResponseOutput[],
  results: ToolResult[],
): ConversationToolCall[] {
  const unmatched = [...results];
  const take = (predicate: (result: ToolResult) => boolean) => {
    const at = unmatched.findIndex(predicate);
    return at < 0 ? null : unmatched.splice(at, 1)[0];
  };
  const tools = outputs.filter((output) => output.kind === "tool");
  const paired = tools.map((tool) => ({
    tool,
    result: tool.id ? take((result) => result.id === tool.id) : null,
  }));
  return paired.map(({ tool, result: byId }) => {
    const result =
      byId ??
      (tool.id
        ? null
        : (take((candidate) => candidate.name === tool.name) ??
          take(
            (candidate) => candidate.id === null && candidate.name === null,
          )));
    return {
      id: tool.id ?? null,
      name: tool.name ?? "",
      args: tool.text,
      result: result?.text ?? null,
      isError: result?.isError ?? false,
    };
  });
}

function callTone(record: RequestRecord): TrajectoryTone {
  switch (displayRequestStatus(record.status, record.http_status)) {
    case "succeeded":
      return "ok";
    case "failed":
      return "failed";
    case "blocked":
      return "blocked";
    case "cancelled":
      return "cancelled";
    case "pending":
      return "pending";
  }
}

const REASON_LIMIT = 160;

function errorLine(error: NonNullable<RequestRecord["error"]>): string {
  const head = error.upstream
    ? i18n.t("conversation.upstreamStatus", { status: error.upstream.status })
    : "";
  const code = error.code || error.category;
  const message = error.message.replace(/\s+/g, " ").trim();
  const text = [head, code, message && message !== code ? message : ""]
    .filter(Boolean)
    .join(" · ");
  return text.length > REASON_LIMIT ? `${text.slice(0, REASON_LIMIT)}…` : text;
}

/** Why a call failed, or why it had to retry before it succeeded. */
export function callReason(
  record: RequestRecord,
  children: RequestRecord[],
): string | null {
  const tone = callTone(record);
  const retries = children.length;
  if (tone === "ok" && retries > 0) {
    const earlier = children.find((child) => child.error)?.error;
    return earlier
      ? i18n.t("conversation.retriedAfter", {
          reason: errorLine(earlier),
          count: retries,
        })
      : i18n.t("conversation.retried", { count: retries });
  }
  if (record.error) return errorLine(record.error);
  if (tone === "cancelled" && clientDisconnect(record)) {
    return i18n.t("trajectory.clientDisconnected");
  }
  if (tone === "blocked") return i18n.t("trajectory.policyBlockHint");
  return null;
}

function usageOf(records: RequestRecord[]): TokenUsage | null {
  let input = 0;
  let output = 0;
  let any = false;
  for (const record of records) {
    if (!record.usage) continue;
    any = true;
    input += record.usage.input_tokens;
    output += record.usage.output_tokens;
  }
  return any ? { input, output } : null;
}

function addUsage(left: TokenUsage | null, right: TokenUsage | null) {
  if (!left) return right;
  if (!right) return left;
  return {
    input: left.input + right.input,
    output: left.output + right.output,
  };
}

function endOf(record: RequestRecord, nowMs: number): number {
  if (record.completed_at) return Date.parse(record.completed_at);
  const started = Date.parse(record.started_at);
  if (record.latency_ms !== null) return started + record.latency_ms;
  return record.status === "pending" ? nowMs : started;
}

function spanMs(records: RequestRecord[], nowMs: number): number {
  if (records.length === 0) return 0;
  const start = Math.min(...records.map((r) => Date.parse(r.started_at)));
  const end = Math.max(...records.map((r) => endOf(r, nowMs)));
  return Math.max(0, end - start);
}

function buildCall(
  record: RequestRecord,
  children: RequestRecord[],
  index: number,
  parsed: ParsedCall | null,
  nextParsed: ParsedCall | null,
  nextStartedAt: string | null,
  nowMs: number,
): ConversationCall {
  const tone = callTone(record);
  const toolCalls = parsed
    ? pairToolCalls(parsed.outputs, nextParsed?.toolResults ?? [])
    : [];
  const reasoning = parsed
    ? parsed.outputs
        .filter((output) => output.kind === "reasoning" && output.text.trim())
        .map((output) => output.text.trim())
    : [];
  const reasoningMs =
    record.first_token_ms != null && record.first_answer_ms != null
      ? Math.max(0, record.first_answer_ms - record.first_token_ms)
      : null;
  const nextStart = nextStartedAt ? Date.parse(nextStartedAt) : NaN;
  const end = endOf(record, nowMs);
  const gapMs =
    Number.isFinite(nextStart) && record.completed_at
      ? Math.max(0, nextStart - end)
      : null;
  const fallback = record.privacy_restore?.fallback_count ?? 0;
  const conversionError =
    record.conversion_diagnostics?.some(
      (diagnostic) => diagnostic.severity === "error",
    ) ?? false;
  return {
    record,
    children,
    index,
    tone,
    anomaly:
      tone !== "ok" ||
      children.length > 0 ||
      toolCalls.some((tool) => tool.isError) ||
      fallback > 0 ||
      conversionError,
    reply: false,
    reason: callReason(record, children),
    narration: parsed
      ? parsed.outputs
          .filter((output) => output.kind === "text" && output.text.trim())
          .map((output) => output.text.trim())
      : [],
    reasoning,
    reasoningMs,
    toolCalls,
    toolCount: parsed?.responseCaptured ? toolCalls.length : null,
    gapMs,
    durationMs:
      record.latency_ms ??
      (record.completed_at
        ? Date.parse(record.completed_at) - Date.parse(record.started_at)
        : record.status === "pending"
          ? nowMs - Date.parse(record.started_at)
          : null),
    tokens: usageOf([record, ...children]),
    parsed,
  };
}

/**
 * Routine calls fold into one row so the anomalies stand out. The last call
 * of a turn stays on its own row: it carries the reply's numbers.
 */
export function foldRoutineCalls(
  calls: ConversationCall[],
  nowMs: number,
): ConversationSegment[] {
  const segments: ConversationSegment[] = [];
  let run: ConversationCall[] = [];
  const flush = () => {
    if (run.length >= 2) {
      const counts = new Map<string, number>();
      for (const call of run) {
        for (const tool of call.toolCalls) {
          counts.set(tool.name, (counts.get(tool.name) ?? 0) + 1);
        }
      }
      const unread = run.some((call) => call.toolCount === null);
      segments.push({
        kind: "run",
        calls: run,
        toolCounts: [...counts.entries()].sort((a, b) => b[1] - a[1]),
        toolTotal: unread
          ? null
          : run.reduce((sum, call) => sum + (call.toolCount ?? 0), 0),
        durationMs: spanMs(
          run.flatMap((call) => [call.record, ...call.children]),
          nowMs,
        ),
        tokens: run.reduce<TokenUsage | null>(
          (sum, call) => addUsage(sum, call.tokens),
          null,
        ),
      });
    } else {
      for (const call of run) segments.push({ kind: "call", call });
    }
    run = [];
  };
  calls.forEach((call, position) => {
    const last = position === calls.length - 1;
    // The reply call always has its own row: its text hangs beneath it.
    if (call.anomaly || call.tone === "pending" || call.reply || last) {
      flush();
      segments.push({ kind: "call", call });
      return;
    }
    run.push(call);
  });
  flush();
  return segments;
}

function previewUser(record: RequestRecord): UserText {
  const preview = record.input_preview?.trim() ?? "";
  return preview
    ? { text: preview, wrappers: [], source: "preview" }
    : { text: "", wrappers: [], source: "none" };
}

/**
 * Rebuilds the turns of a conversation from its calls. `parsed` holds the
 * calls whose content has been read; the rest still show their statistics.
 */
export function conversationTurns(
  turns: RequestRecord[],
  childrenByRoot: Record<string, RequestRecord[]>,
  parsed: ReadonlyMap<string, ParsedCall>,
  nowMs: number = Date.now(),
): ConversationTurn[] {
  return groupTurns(turns).map((group) => {
    const records = group.records;
    const calls = records.map((record, position) => {
      const next = records[position + 1] ?? null;
      return buildCall(
        record,
        childrenByRoot[record.id] ?? [],
        position + 1,
        parsed.get(record.id) ?? null,
        next ? (parsed.get(next.id) ?? null) : null,
        next?.started_at ?? null,
        nowMs,
      );
    });
    // Any read call replays the turn's user message; the first is preferred
    // because it is the one the operator actually sent.
    const bodyUser = calls
      .map((call) => call.parsed?.user)
      .find((user) => user && (user.text || user.wrappers.length > 0));
    const user = bodyUser ?? previewUser(records[0]);
    // The reply is the last successful call's text. Everything it said
    // earlier in the loop is narration on the calls that said it. While the
    // last call still runs there is no reply yet: what the model said before
    // it stays narration on that call.
    const lastCall = calls[calls.length - 1];
    const replyCall =
      lastCall.tone === "pending"
        ? lastCall
        : ([...calls].reverse().find((call) => call.tone === "ok") ?? lastCall);
    const replyText = replyCall.narration.length
      ? replyCall.narration.join("\n\n")
      : null;
    if (replyText !== null) replyCall.narration = [];
    replyCall.reply = true;
    const readCalls = calls.filter((call) => call.toolCount !== null);
    const toolCalls = readCalls.reduce(
      (sum, call) => sum + (call.toolCount ?? 0),
      0,
    );
    const everyRecord = calls.flatMap((call) => [
      call.record,
      ...call.children,
    ]);
    const pending = calls.some((call) => call.tone === "pending");
    // A client that hung up is not a failure; the call row says so itself.
    const failed = everyRecord.filter((record) => {
      const tone = callTone(record);
      return tone === "failed" || tone === "blocked";
    }).length;
    const cancelledRecords = everyRecord.filter(
      (record) => callTone(record) === "cancelled",
    );
    const stats: ConversationTurnStats = {
      calls: everyRecord.length,
      toolCalls: readCalls.length > 0 ? toolCalls : null,
      toolCallsPartial: readCalls.length > 0 && readCalls.length < calls.length,
      failed,
      cancelled: cancelledRecords.length,
      disconnected: cancelledRecords.filter(clientDisconnect).length,
      durationMs: spanMs(everyRecord, nowMs),
      tokens: usageOf(everyRecord),
      pending,
    };
    const read = calls.filter((call) => call.parsed !== null);
    return {
      id: `turn:${group.turnIndex ?? records[0].id}`,
      turnIndex: group.turnIndex,
      calls,
      segments: foldRoutineCalls(calls, nowMs),
      user,
      startedAt: records[0].started_at,
      reply: {
        text: replyText,
        record: replyCall.record,
        at: replyCall.record.completed_at,
      },
      stats,
      locked: read.length > 0 && read.every((call) => call.parsed?.locked),
    };
  });
}

/** How many calls of a turn were cut short, in the words the call rows use. */
export function cancelledLabel(stats: ConversationTurnStats): string | null {
  if (stats.cancelled === 0) return null;
  return i18n.t(
    stats.disconnected === stats.cancelled
      ? "conversation.disconnects"
      : "conversation.cancellations",
    { count: stats.cancelled },
  );
}

/** Tool name → count, sorted by count, for a run or a whole turn. */
export function toolCountLabel(counts: Array<[string, number]>): string {
  return counts.map(([name, count]) => `${name} ×${count}`).join(" · ");
}
