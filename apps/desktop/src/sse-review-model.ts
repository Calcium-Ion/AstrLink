const EVENT_BATCH_SIZE = 500;
const CHARACTER_BATCH_SIZE = 1024 * 1024;
const TIMELINE_TEXT_CHUNK = 8 * 1024;

export interface SSEEvent {
  index: number;
  event: string;
  type: string;
  data: string;
  json: unknown | null;
  invalidJson: boolean;
  done: boolean;
  incomplete: boolean;
}

export interface SSEParseProgress {
  events: SSEEvent[];
  processedCharacters: number;
  totalCharacters: number;
}

export interface SSEParseResult {
  events: SSEEvent[];
  invalidJsonCount: number;
  incompleteLastEvent: boolean;
}

export interface TimelineItem {
  id: string;
  kind: "lifecycle" | "text" | "reasoning" | "tool" | "error" | "event";
  title: string;
  text: string;
  firstEvent: number;
  lastEvent: number;
}

export class SSEParseCancelledError extends Error {
  constructor() {
    super("SSE parsing cancelled");
    this.name = "SSEParseCancelledError";
  }
}

export async function parseSSEIncremental(
  content: string,
  options: {
    signal?: AbortSignal;
    truncated?: boolean;
    onProgress?: (progress: SSEParseProgress) => void;
  } = {},
): Promise<SSEParseResult> {
  const events: SSEEvent[] = [];
  let lines: string[] = [];
  let cursor = 0;
  let charactersSinceYield = 0;
  let eventsSinceYield = 0;
  let incompleteLastEvent = false;

  const flush = (incomplete: boolean) => {
    if (lines.length === 0) return;
    const event = parseEventLines(lines, events.length + 1, incomplete);
    lines = [];
    if (event) {
      events.push(event);
      eventsSinceYield += 1;
    }
  };

  while (cursor < content.length) {
    throwIfCancelled(options.signal);
    const newline = content.indexOf("\n", cursor);
    const end = newline === -1 ? content.length : newline;
    let line = content.slice(cursor, end);
    if (line.endsWith("\r")) line = line.slice(0, -1);
    charactersSinceYield += end - cursor + (newline === -1 ? 0 : 1);
    cursor = newline === -1 ? content.length : newline + 1;
    if (line === "") flush(false);
    else lines.push(line);

    if (
      eventsSinceYield >= EVENT_BATCH_SIZE ||
      charactersSinceYield >= CHARACTER_BATCH_SIZE
    ) {
      options.onProgress?.({
        events: [...events],
        processedCharacters: cursor,
        totalCharacters: content.length,
      });
      eventsSinceYield = 0;
      charactersSinceYield = 0;
      await yieldToMainThread();
    }
  }
  if (lines.length > 0) {
    incompleteLastEvent = options.truncated === true || !content.endsWith("\n\n");
    flush(incompleteLastEvent);
  }
  throwIfCancelled(options.signal);
  options.onProgress?.({
    events: [...events],
    processedCharacters: content.length,
    totalCharacters: content.length,
  });
  return {
    events,
    invalidJsonCount: events.filter((event) => event.invalidJson).length,
    incompleteLastEvent,
  };
}

export function parseSSESynchronously(
  content: string,
  truncated = false,
): SSEParseResult {
  const events: SSEEvent[] = [];
  const normalized = content.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
  const blocks = normalized.split("\n\n");
  const hasOpenLastBlock = !normalized.endsWith("\n\n");
  blocks.forEach((block, blockIndex) => {
    if (!block) return;
    const incomplete =
      blockIndex === blocks.length - 1 && (truncated || hasOpenLastBlock);
    const event = parseEventLines(block.split("\n"), events.length + 1, incomplete);
    if (event) events.push(event);
  });
  return {
    events,
    invalidJsonCount: events.filter((event) => event.invalidJson).length,
    incompleteLastEvent:
      events.length > 0 && events[events.length - 1].incomplete,
  };
}

function parseEventLines(
  lines: string[],
  index: number,
  incomplete: boolean,
): SSEEvent | null {
  let eventName = "message";
  const dataLines: string[] = [];
  let hasField = false;
  for (const line of lines) {
    if (!line || line.startsWith(":")) continue;
    const colon = line.indexOf(":");
    const field = colon === -1 ? line : line.slice(0, colon);
    let value = colon === -1 ? "" : line.slice(colon + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "event") {
      eventName = value || "message";
      hasField = true;
    } else if (field === "data") {
      dataLines.push(value);
      hasField = true;
    } else if (field === "id" || field === "retry") {
      hasField = true;
    }
  }
  if (!hasField) return null;
  const data = dataLines.join("\n");
  const done = data.trim() === "[DONE]";
  let json: unknown | null = null;
  let invalidJson = false;
  if (data && !done) {
    try {
      json = JSON.parse(data);
    } catch {
      invalidJson = true;
    }
  }
  const dataType =
    isObject(json) && typeof json.type === "string" ? json.type : null;
  return {
    index,
    event: eventName,
    type: eventName !== "message" ? eventName : dataType ?? (done ? "[DONE]" : "message"),
    data,
    json,
    invalidJson,
    done,
    incomplete,
  };
}

export function buildSemanticTimeline(
  protocol: string,
  events: SSEEvent[],
): TimelineItem[] {
  const timeline: TimelineItem[] = [];
  for (const event of events) {
    const fragments = semanticFragments(protocol, event);
    if (fragments.length === 0) {
      appendTimeline(timeline, {
        kind: event.invalidJson ? "error" : "event",
        title: event.invalidJson ? `${event.type} · JSON 无效` : event.type,
        text: event.data,
        key: event.type,
        eventIndex: event.index,
        aggregate: false,
      });
      continue;
    }
    for (const fragment of fragments) appendTimeline(timeline, fragment);
  }
  return timeline;
}

interface TimelineFragment {
  kind: TimelineItem["kind"];
  title: string;
  text: string;
  key: string;
  eventIndex: number;
  aggregate: boolean;
}

function semanticFragments(
  protocol: string,
  event: SSEEvent,
): TimelineFragment[] {
  if (event.done) {
    return [lifecycle(event, "流结束", "[DONE]")];
  }
  if (!isObject(event.json)) return [];
  if (protocol.includes("responses")) return responsesFragments(event);
  if (protocol.includes("chat") || protocol.includes("completion")) {
    return chatFragments(event);
  }
  if (protocol.includes("anthropic")) return anthropicFragments(event);
  if (
    protocol.includes("gemini") ||
    protocol.includes("google.generate_content")
  ) {
    return geminiFragments(event);
  }
  return genericFragments(event);
}

function responsesFragments(event: SSEEvent): TimelineFragment[] {
  const value = event.json as Record<string, unknown>;
  const type = stringValue(value.type) ?? event.type;
  const delta = stringValue(value.delta) ?? "";
  if (type.includes("output_text.delta")) {
    return [aggregate(event, "text", "输出文本", delta, "output")];
  }
  if (type.includes("reasoning_summary") && type.endsWith(".delta")) {
    return [aggregate(event, "reasoning", "推理摘要", delta, "reasoning")];
  }
  if (type.includes("function_call_arguments") && type.endsWith(".delta")) {
    const key =
      stringValue(value.item_id) ??
      stringValue(value.call_id) ??
      String(value.output_index ?? "tool");
    return [aggregate(event, "tool", "工具调用参数", delta, `tool:${key}`)];
  }
  if (type.includes("error") || type.includes("failed")) {
    return [lifecycle(event, "错误", compactJson(value), "error")];
  }
  return [lifecycle(event, lifecycleTitle(type), summarizeLifecycle(value))];
}

function chatFragments(event: SSEEvent): TimelineFragment[] {
  const value = event.json as Record<string, unknown>;
  const choices = Array.isArray(value.choices) ? value.choices : [];
  const fragments: TimelineFragment[] = [];
  for (const choiceValue of choices) {
    if (!isObject(choiceValue)) continue;
    const delta = isObject(choiceValue.delta) ? choiceValue.delta : {};
    const text = stringValue(delta.content);
    if (text) fragments.push(aggregate(event, "text", "输出文本", text, "output"));
    const reasoning =
      stringValue(delta.reasoning_content) ?? stringValue(delta.reasoning);
    if (reasoning) {
      fragments.push(
        aggregate(event, "reasoning", "推理内容", reasoning, "reasoning"),
      );
    }
    const toolCalls = Array.isArray(delta.tool_calls) ? delta.tool_calls : [];
    toolCalls.forEach((toolValue, toolIndex) => {
      if (!isObject(toolValue)) return;
      const fn = isObject(toolValue.function) ? toolValue.function : {};
      const text = `${stringValue(fn.name) ?? ""}${stringValue(fn.arguments) ?? ""}`;
      if (!text) return;
      fragments.push(
        aggregate(
          event,
          "tool",
          "工具调用",
          text,
          `tool:${String(toolValue.index ?? toolIndex)}`,
        ),
      );
    });
    const finishReason = stringValue(choiceValue.finish_reason);
    if (finishReason) {
      fragments.push(lifecycle(event, "完成原因", finishReason));
    }
  }
  if (fragments.length > 0) return fragments;
  if (isObject(value.error)) return [lifecycle(event, "错误", compactJson(value.error), "error")];
  return [lifecycle(event, lifecycleTitle(event.type), summarizeLifecycle(value))];
}

function anthropicFragments(event: SSEEvent): TimelineFragment[] {
  const value = event.json as Record<string, unknown>;
  const delta = isObject(value.delta) ? value.delta : {};
  const deltaType = stringValue(delta.type);
  if (deltaType === "text_delta") {
    return [aggregate(event, "text", "输出文本", stringValue(delta.text) ?? "", "output")];
  }
  if (deltaType === "thinking_delta" || deltaType === "signature_delta") {
    return [
      aggregate(
        event,
        "reasoning",
        deltaType === "signature_delta" ? "推理签名" : "推理内容",
        stringValue(delta.thinking) ?? stringValue(delta.signature) ?? "",
        deltaType,
      ),
    ];
  }
  if (deltaType === "input_json_delta") {
    return [
      aggregate(
        event,
        "tool",
        "工具输入",
        stringValue(delta.partial_json) ?? "",
        `tool:${String(value.index ?? "unknown")}`,
      ),
    ];
  }
  if (event.type === "error") {
    return [lifecycle(event, "错误", compactJson(value), "error")];
  }
  return [lifecycle(event, lifecycleTitle(event.type), summarizeLifecycle(value))];
}

function geminiFragments(event: SSEEvent): TimelineFragment[] {
  const value = event.json as Record<string, unknown>;
  const candidates = Array.isArray(value.candidates) ? value.candidates : [];
  const fragments: TimelineFragment[] = [];
  for (const candidateValue of candidates) {
    if (!isObject(candidateValue)) continue;
    const content = isObject(candidateValue.content) ? candidateValue.content : {};
    const parts = Array.isArray(content.parts) ? content.parts : [];
    for (const partValue of parts) {
      if (!isObject(partValue)) continue;
      const text = stringValue(partValue.text);
      if (text) fragments.push(aggregate(event, "text", "输出文本", text, "output"));
      if (isObject(partValue.functionCall)) {
        fragments.push(
          aggregate(
            event,
            "tool",
            "函数调用",
            compactJson(partValue.functionCall),
            `tool:${stringValue(partValue.functionCall.name) ?? "unknown"}`,
          ),
        );
      }
    }
    const finishReason = stringValue(candidateValue.finishReason);
    if (finishReason) fragments.push(lifecycle(event, "完成原因", finishReason));
  }
  return fragments.length > 0
    ? fragments
    : [lifecycle(event, lifecycleTitle(event.type), summarizeLifecycle(value))];
}

function genericFragments(event: SSEEvent): TimelineFragment[] {
  const value = event.json as Record<string, unknown>;
  const delta = stringValue(value.delta);
  if (delta) return [aggregate(event, "text", "增量内容", delta, "delta")];
  const text = stringValue(value.text);
  if (text) return [aggregate(event, "text", "文本内容", text, "text")];
  return [lifecycle(event, lifecycleTitle(event.type), summarizeLifecycle(value))];
}

function aggregate(
  event: SSEEvent,
  kind: TimelineItem["kind"],
  title: string,
  text: string,
  key: string,
): TimelineFragment {
  return { kind, title, text, key, eventIndex: event.index, aggregate: true };
}

function lifecycle(
  event: SSEEvent,
  title: string,
  text: string,
  key = event.type,
): TimelineFragment {
  return {
    kind: key === "error" ? "error" : "lifecycle",
    title,
    text,
    key,
    eventIndex: event.index,
    aggregate: false,
  };
}

function appendTimeline(
  timeline: TimelineItem[],
  fragment: TimelineFragment,
): void {
  const previous = timeline[timeline.length - 1];
  const aggregateKey = `${fragment.kind}:${fragment.key}`;
  if (
    fragment.aggregate &&
    previous?.id.startsWith(`${aggregateKey}:`) &&
    previous.text.length + fragment.text.length <= TIMELINE_TEXT_CHUNK
  ) {
    previous.text += fragment.text;
    previous.lastEvent = fragment.eventIndex;
    return;
  }
  timeline.push({
    id: `${aggregateKey}:${fragment.eventIndex}:${timeline.length}`,
    kind: fragment.kind,
    title: fragment.title,
    text: fragment.text,
    firstEvent: fragment.eventIndex,
    lastEvent: fragment.eventIndex,
  });
}

function summarizeLifecycle(value: Record<string, unknown>): string {
  for (const key of ["status", "finish_reason", "stop_reason", "message"]) {
    const text = stringValue(value[key]);
    if (text) return text;
  }
  return compactJson(value);
}

function lifecycleTitle(type: string): string {
  if (type.includes("created") || type.includes("start")) return "请求开始";
  if (type.includes("in_progress")) return "处理中";
  if (type.includes("completed") || type.includes("stop")) return "请求完成";
  return type;
}

function compactJson(value: unknown): string {
  try {
    const encoded = JSON.stringify(value);
    return encoded.length > 2000 ? `${encoded.slice(0, 2000)}…` : encoded;
  } catch {
    return String(value);
  }
}

function isObject(value: unknown): value is Record<string, any> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function stringValue(value: unknown): string | null {
  return typeof value === "string" ? value : null;
}

function throwIfCancelled(signal?: AbortSignal): void {
  if (signal?.aborted) throw new SSEParseCancelledError();
}

function yieldToMainThread(): Promise<void> {
  return new Promise((resolve) => globalThis.setTimeout(resolve, 0));
}
