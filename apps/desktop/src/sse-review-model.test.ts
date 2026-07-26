import { describe, expect, it } from "vitest";

import {
  buildSemanticTimeline,
  parseSSEIncremental,
  parseSSESynchronously,
  SSEParseCancelledError,
} from "./sse-review-model";

describe("SSE review model", () => {
  it("parses CRLF, multiline data, DONE, invalid JSON and a truncated tail", () => {
    const content =
      'event: response.created\r\ndata: {"type":"response.created",\r\ndata: "status":"in_progress"}\r\n\r\n' +
      "data: [DONE]\r\n\r\n" +
      "event: broken\r\ndata: {not-json}\r\n\r\n" +
      'event: tail\r\ndata: {"type":"tail"}';
    const result = parseSSESynchronously(content, true);

    expect(result.events).toHaveLength(4);
    expect(result.events[0].json).toMatchObject({
      type: "response.created",
      status: "in_progress",
    });
    expect(result.events[1].done).toBe(true);
    expect(result.events[2].invalidJson).toBe(true);
    expect(result.events[3].incomplete).toBe(true);
    expect(result.incompleteLastEvent).toBe(true);
  });

  it("aggregates Responses output, reasoning and tool argument deltas", () => {
    const parsed = parseSSESynchronously(
      [
        'data: {"type":"response.in_progress","status":"in_progress"}',
        "",
        'data: {"type":"response.output_text.delta","delta":"Hel"}',
        "",
        'data: {"type":"response.output_text.delta","delta":"lo"}',
        "",
        'data: {"type":"response.reasoning_summary_text.delta","delta":"Think"}',
        "",
        'data: {"type":"response.function_call_arguments.delta","item_id":"call_1","delta":"{\\"q\\":"}',
        "",
        'data: {"type":"response.function_call_arguments.delta","item_id":"call_1","delta":"1}"}',
        "",
      ].join("\n"),
    );
    const timeline = buildSemanticTimeline(
      "openai.responses",
      parsed.events,
    );

    expect(timeline.find((item) => item.kind === "text")?.text).toBe("Hello");
    expect(timeline.find((item) => item.kind === "reasoning")?.text).toBe(
      "Think",
    );
    expect(timeline.find((item) => item.kind === "tool")?.text).toBe('{"q":1}');
    expect(timeline.every((item) => item.firstEvent >= 1)).toBe(true);
  });

  it("extracts Chat, Anthropic and Gemini protocol semantics", () => {
    const chat = parseSSESynchronously(
      'data: {"choices":[{"delta":{"content":"chat"}}]}\n\n',
    );
    const anthropic = parseSSESynchronously(
      'event: content_block_delta\ndata: {"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"reason"}}\n\n',
    );
    const gemini = parseSSESynchronously(
      'data: {"candidates":[{"content":{"parts":[{"text":"gemini"},{"functionCall":{"name":"lookup","args":{"q":1}}}]}}]}\n\n',
    );

    expect(buildSemanticTimeline("openai.chat", chat.events)[0].text).toBe(
      "chat",
    );
    expect(
      buildSemanticTimeline("anthropic.messages", anthropic.events)[0],
    ).toMatchObject({ kind: "reasoning", text: "reason" });
    expect(buildSemanticTimeline("google.generate_content", gemini.events)).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ kind: "text", text: "gemini" }),
        expect.objectContaining({ kind: "tool" }),
      ]),
    );
  });

  it("parses incrementally and honours cancellation between batches", async () => {
    const controller = new AbortController();
    const content = 'data: {"type":"tick"}\n\n'.repeat(1200);
    await expect(
      parseSSEIncremental(content, {
        signal: controller.signal,
        onProgress: (progress) => {
          if (progress.events.length >= 500) controller.abort();
        },
      }),
    ).rejects.toBeInstanceOf(SSEParseCancelledError);
  });

  it("keeps a 12MB stream split into roughly 1MB main-thread batches", async () => {
    const payload = "x".repeat(64 * 1024);
    const event = `data: {"type":"response.output_text.delta","delta":"${payload}"}\n\n`;
    const repetitions = Math.ceil((12 * 1024 * 1024) / event.length);
    const content = event.repeat(repetitions);
    const progressMarks: number[] = [];

    const result = await parseSSEIncremental(content, {
      onProgress: (progress) =>
        progressMarks.push(progress.processedCharacters),
    });

    expect(content.length).toBeGreaterThanOrEqual(12 * 1024 * 1024);
    expect(result.events).toHaveLength(repetitions);
    expect(progressMarks.length).toBeGreaterThan(8);
    const batchSizes = progressMarks.map((mark, index) =>
      index === 0 ? mark : mark - progressMarks[index - 1],
    );
    expect(Math.max(...batchSizes)).toBeLessThan(1.2 * 1024 * 1024);
  });
});
