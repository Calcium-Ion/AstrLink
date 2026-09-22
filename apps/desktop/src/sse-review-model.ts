const EVENT_BATCH_SIZE = 500;
const CHARACTER_BATCH_SIZE = 1024 * 1024;

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
    incompleteLastEvent =
      options.truncated === true || !content.endsWith("\n\n");
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
    const event = parseEventLines(
      block.split("\n"),
      events.length + 1,
      incomplete,
    );
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
    type:
      eventName !== "message"
        ? eventName
        : (dataType ?? (done ? "[DONE]" : "message")),
    data,
    json,
    invalidJson,
    done,
    incomplete,
  };
}

function isObject(value: unknown): value is Record<string, any> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function throwIfCancelled(signal?: AbortSignal): void {
  if (signal?.aborted) throw new SSEParseCancelledError();
}

function yieldToMainThread(): Promise<void> {
  return new Promise((resolve) => globalThis.setTimeout(resolve, 0));
}
