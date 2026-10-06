import type { AuditContentPart } from "./request-record-model";
import { parseSSEIncremental } from "./sse-review-model";

export interface ResponseOutput {
  kind: "text" | "reasoning" | "tool" | "error" | "other";
  text: string;
  name?: string;
  /** The provider's call id on a tool output, so a later request can answer it. */
  id?: string;
}

export interface ResponsePreview {
  outputs: ResponseOutput[];
  incomplete: boolean;
  unparsed: boolean;
}

type ObjectValue = Record<string, unknown>;
const object = (value: unknown): ObjectValue =>
  value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as ObjectValue)
    : {};
const array = (value: unknown): unknown[] =>
  Array.isArray(value) ? value : [];
const string = (value: unknown): string =>
  typeof value === "string" ? value : "";
const detail = (value: unknown): string =>
  value == null
    ? ""
    : typeof value === "string"
      ? value
      : JSON.stringify(value, null, 2);

/** Reconstruct the client-visible output, keeping snapshots from duplicating deltas. */
export async function parseResponsePreview(
  part: AuditContentPart,
  signal?: AbortSignal,
): Promise<ResponsePreview> {
  const outputs = new Map<string, ResponseOutput>();
  const itemIndexes = new Map<string, string>();
  let incomplete = part.truncated;
  let unparsed = false;
  const put = (
    key: string,
    kind: ResponseOutput["kind"],
    text: string,
    append = false,
    name?: string,
    id?: string,
  ) => {
    const previous = outputs.get(key);
    const callId = id || previous?.id;
    outputs.set(key, {
      kind,
      text: append ? (previous?.text ?? "") + text : text,
      name: name || previous?.name,
      ...(callId ? { id: callId } : {}),
    });
  };
  const removePrefix = (prefix: string) => {
    for (const key of outputs.keys())
      if (key.startsWith(prefix)) outputs.delete(key);
  };
  // Reasoning that only streamed as deltas, by output slot, while a terminal
  // snapshot replaces the items: a snapshot's reasoning item may carry no
  // text (summaries are optional), and the streamed text must survive it.
  let streamedReasoning = new Map<string, ResponseOutput[]>();
  // A reasoning item's text lives in `summary` (OpenAI), `content` with
  // reasoning_text blocks (open-weight models and gateways), or flat fields.
  const reasoningTexts = (item: ObjectValue): string[] =>
    [
      ...array(item.summary).map((block) => string(object(block).text)),
      ...array(item.content).map((block) => string(object(block).text)),
      string(item.text),
      string(item.reasoning_content),
      string(item.reasoning),
    ].filter((text) => text.trim() !== "");
  // Errors keep the provider's code as the name, so the reason can lead.
  const error = (value: unknown) => {
    const data = object(value);
    const message = string(data.message) || string(value);
    const code =
      string(data.code) || (data.type === "error" ? "" : string(data.type));
    if (message) put("error", "error", message, false, code);
    else if (code && !outputs.has("error")) put("error", "error", code);
  };
  const contentBlock = (value: unknown, key: string) => {
    const block = object(value);
    const type = string(block.type);
    if (["text", "output_text", "summary_text"].includes(type)) {
      put(key, "text", string(block.text));
    } else if (type === "refusal") {
      put(key, "text", string(block.refusal));
    } else if (type === "thinking") {
      put(key, "reasoning", string(block.thinking));
    } else if (type === "tool_use" || type === "server_tool_use") {
      put(
        key,
        "tool",
        detail(block.input),
        false,
        string(block.name),
        string(block.id),
      );
    } else if (type && type !== "redacted_thinking") {
      put(key, "other", detail(block), false, type);
    }
  };
  const responseItem = (value: unknown, index: number, snapshot: boolean) => {
    const item = object(value);
    const id = string(item.id);
    const slot = String(index);
    if (id) itemIndexes.set(id, slot);
    const key = `response:${slot}`;
    const type = string(item.type);
    if (type === "message") {
      array(item.content).forEach((block, i) =>
        contentBlock(block, `${key}:text:${i}`),
      );
    } else if (type === "reasoning") {
      const texts = reasoningTexts(item);
      if (texts.length > 0) {
        texts.forEach((text, i) =>
          put(`${key}:reasoning:${i}`, "reasoning", text),
        );
      } else {
        (streamedReasoning.get(slot) ?? []).forEach((output, i) =>
          outputs.set(`${key}:reasoning:${i}`, output),
        );
      }
    } else if (type === "function_call" || type === "custom_tool_call") {
      const text = detail(item.arguments ?? item.input);
      if (snapshot || text || !outputs.has(`${key}:tool`)) {
        put(
          `${key}:tool`,
          "tool",
          text,
          false,
          string(item.name),
          string(item.call_id) || string(item.id),
        );
      }
    } else if (type.endsWith("_call")) {
      put(`${key}:tool`, "tool", detail(item.action ?? item), false, type);
    } else if (type) {
      put(key, "other", detail(item), false, type);
    }
  };
  const consume = (value: unknown, eventType = "") => {
    const data = object(value);
    const type = string(data.type) || eventType;
    error(data.error);
    if (type === "error") error(data);
    if (type.startsWith("response.")) {
      const response = object(data.response);
      error(response.error);
      if (type === "response.failed" || type === "response.incomplete") {
        incomplete = true;
        const reason = string(object(response.incomplete_details).reason);
        if (reason) put("error", "error", reason);
      }
      if (Array.isArray(response.output) && response.output.length > 0) {
        // A terminal response snapshot is authoritative, including its item order.
        streamedReasoning = new Map();
        for (const [key, output] of outputs) {
          const match = /^response:(\d+):reasoning(?::|$)/.exec(key);
          if (!match || output.kind !== "reasoning" || !output.text) continue;
          const list = streamedReasoning.get(match[1]!) ?? [];
          list.push(output);
          streamedReasoning.set(match[1]!, list);
        }
        removePrefix("response:");
        response.output.forEach((item, i) => responseItem(item, i, true));
        streamedReasoning = new Map();
      }
      const item = object(data.item);
      const slot = String(
        data.output_index ?? itemIndexes.get(string(data.item_id)) ?? 0,
      );
      if (
        type === "response.output_item.added" ||
        type === "response.output_item.done"
      ) {
        responseItem(item, Number(slot), type.endsWith(".done"));
      } else if (/^response\.(output_text|refusal)\.(delta|done)$/.test(type)) {
        const index = data.content_index ?? 0;
        put(
          `response:${slot}:text:${index}`,
          "text",
          string(data.delta ?? data.text ?? data.refusal),
          type.endsWith(".delta"),
        );
      } else if (/^response\.reasoning\w*\.(added|delta|done)$/.test(type)) {
        // reasoning_summary_text, reasoning_text and gateway variants; a part
        // event carries its text inside `part`. An empty done never erases.
        const index = data.summary_index ?? data.content_index ?? 0;
        const delta = type.endsWith(".delta");
        const text = string(data.delta ?? data.text ?? object(data.part).text);
        if (delta || text) {
          put(`response:${slot}:reasoning:${index}`, "reasoning", text, delta);
        }
      } else if (
        /^response\.(function_call_arguments|custom_tool_call_input)\.(delta|done)$/.test(
          type,
        )
      ) {
        put(
          `response:${slot}:tool`,
          "tool",
          string(data.delta ?? data.arguments ?? data.input),
          type.endsWith(".delta"),
        );
      }
      return;
    }
    if (Array.isArray(data.output)) {
      data.output.forEach((item, i) => responseItem(item, i, true));
      if (data.status === "incomplete" || data.status === "failed")
        incomplete = true;
      return;
    }
    if (typeof data.output_text === "string")
      put("text", "text", data.output_text);
    if (Array.isArray(data.choices)) {
      data.choices.forEach((value, i) => {
        const choice = object(value);
        const key = `choice:${choice.index ?? i}`;
        const delta = choice.delta != null;
        const message = object(choice.delta ?? choice.message);
        if (typeof choice.text === "string")
          put(`${key}:text`, "text", choice.text, true);
        if (typeof message.content === "string")
          put(`${key}:text`, "text", message.content, delta);
        else
          array(message.content).forEach((block, j) =>
            contentBlock(block, `${key}:text:${j}`),
          );
        if (typeof message.refusal === "string")
          put(`${key}:refusal`, "text", message.refusal, delta);
        const reasoning = message.reasoning_content ?? message.reasoning;
        if (typeof reasoning === "string")
          put(`${key}:reasoning`, "reasoning", reasoning, delta);
        const tools = array(message.tool_calls);
        if (message.function_call)
          tools.push({ function: message.function_call });
        tools.forEach((value, j) => {
          const tool = object(value);
          const fn = object(tool.function);
          const toolKey = `${key}:tool:${tool.index ?? j}`;
          put(
            toolKey,
            "tool",
            string(fn.arguments),
            delta,
            delta
              ? (outputs.get(toolKey)?.name ?? "") + string(fn.name)
              : string(fn.name),
            string(tool.id),
          );
        });
        if (
          choice.finish_reason === "length" ||
          choice.finish_reason === "content_filter"
        )
          incomplete = true;
      });
      return;
    }
    if (type === "content_block_start") {
      contentBlock(data.content_block, `anthropic:${data.index ?? 0}`);
    } else if (type === "content_block_delta") {
      const delta = object(data.delta);
      const key = `anthropic:${data.index ?? 0}`;
      if (delta.type === "text_delta")
        put(key, "text", string(delta.text), true);
      if (delta.type === "thinking_delta")
        put(key, "reasoning", string(delta.thinking), true);
      if (delta.type === "input_json_delta") {
        // The start event's empty input is a placeholder, not an argument fragment.
        if (outputs.get(key)?.text === "{}") put(key, "tool", "");
        put(key, "tool", string(delta.partial_json), true);
      }
    } else if (type === "message_start") {
      array(object(data.message).content).forEach((block, i) =>
        contentBlock(block, `anthropic:${i}`),
      );
    } else if (Array.isArray(data.content)) {
      data.content.forEach((block, i) => contentBlock(block, `anthropic:${i}`));
    }
    const stopReason = data.stop_reason ?? object(data.delta).stop_reason;
    if (stopReason === "max_tokens") incomplete = true;
    array(data.candidates).forEach((value, i) => {
      const candidate = object(value);
      const key = `gemini:${candidate.index ?? i}`;
      array(object(candidate.content).parts).forEach((value, j) => {
        const part = object(value);
        if (typeof part.text === "string") {
          const kind = part.thought === true ? "reasoning" : "text";
          put(`${key}:${kind}`, kind, part.text, true);
        } else if (part.functionCall) {
          const call = object(part.functionCall);
          put(
            `${key}:tool:${outputs.size}`,
            "tool",
            detail(call.args),
            false,
            string(call.name),
          );
        } else {
          put(`${key}:other:${j}`, "other", detail(part), false, "media");
        }
      });
      if (
        candidate.finishReason === "MAX_TOKENS" ||
        candidate.finishReason === "SAFETY"
      )
        incomplete = true;
    });
    const blocked = string(object(data.promptFeedback).blockReason);
    if (blocked) put("error", "error", blocked);
  };

  if (
    part.media_type.toLowerCase().includes("text/event-stream") ||
    /^\s*(?:event:|data:)/.test(part.content)
  ) {
    const parsed = await parseSSEIncremental(part.content, {
      signal,
      truncated: part.truncated,
    });
    incomplete ||= parsed.incompleteLastEvent;
    unparsed = parsed.invalidJsonCount > 0;
    for (let i = 0; i < parsed.events.length; i++) {
      const event = parsed.events[i];
      if (event.json != null) consume(event.json, event.type);
      if (i > 0 && i % 500 === 0) {
        await new Promise((resolve) => globalThis.setTimeout(resolve, 0));
        signal?.throwIfAborted();
      }
    }
  } else {
    try {
      const value: unknown = JSON.parse(part.content);
      if (Array.isArray(value)) value.forEach((item) => consume(item));
      else consume(value);
    } catch {
      if (
        part.media_type.toLowerCase().startsWith("text/plain") &&
        !/^\s*[[{]/.test(part.content)
      ) {
        put("text", "text", part.content);
      } else unparsed = true;
    }
  }
  return {
    outputs: [...outputs.values()].filter((item) => item.text || item.name),
    incomplete,
    unparsed,
  };
}
