import { describe, expect, it } from "vitest";

import {
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
