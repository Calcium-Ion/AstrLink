import { describe, expect, it } from "vitest";

import { parseResponsePreview } from "./response-preview-model";

function preview(value: unknown, stream = false, truncated = false) {
  const content = stream
    ? (value as unknown[])
        .map((event) => `data: ${JSON.stringify(event)}\n\n`)
        .join("")
    : JSON.stringify(value);
  return parseResponsePreview({
    content,
    media_type: stream ? "text/event-stream" : "application/json",
    truncated,
    captured_bytes: content.length,
  });
}

describe("client response preview", () => {
  it("joins Responses deltas and replaces them with final snapshots without duplicates", async () => {
    const result = await preview(
      [
        { type: "response.created", response: { output: [] } },
        {
          type: "response.output_item.added",
          output_index: 0,
          item: { id: "msg_1", type: "message", content: [] },
        },
        {
          type: "response.output_text.delta",
          item_id: "msg_1",
          delta: "Hello ",
        },
        {
          type: "response.output_text.delta",
          item_id: "msg_1",
          delta: "world",
        },
        {
          type: "response.output_text.done",
          item_id: "msg_1",
          text: "Hello world",
        },
        {
          type: "response.output_item.done",
          output_index: 0,
          item: {
            id: "msg_1",
            type: "message",
            content: [{ type: "output_text", text: "Hello world" }],
          },
        },
        {
          type: "response.completed",
          response: {
            output: [
              {
                id: "msg_1",
                type: "message",
                content: [{ type: "output_text", text: "Hello world" }],
              },
            ],
          },
        },
      ],
      true,
    );
    expect(result.outputs).toEqual([
      { kind: "text", text: "Hello world", name: undefined },
    ]);
    expect(result.incomplete).toBe(false);
  });

  it("keeps streamed reasoning through a final snapshot that carries none", async () => {
    const result = await preview(
      [
        {
          type: "response.output_item.added",
          output_index: 0,
          item: { id: "rs_1", type: "reasoning", summary: [] },
        },
        {
          type: "response.reasoning_summary_part.added",
          item_id: "rs_1",
          output_index: 0,
          summary_index: 0,
          part: { type: "summary_text", text: "" },
        },
        {
          type: "response.reasoning_text.delta",
          item_id: "rs_1",
          output_index: 0,
          content_index: 0,
          delta: "The user asks ",
        },
        {
          type: "response.reasoning_text.delta",
          item_id: "rs_1",
          output_index: 0,
          content_index: 0,
          delta: "who that is.",
        },
        {
          type: "response.reasoning_text.done",
          item_id: "rs_1",
          output_index: 0,
          content_index: 0,
          text: "",
        },
        {
          type: "response.output_item.done",
          output_index: 0,
          item: { id: "rs_1", type: "reasoning", summary: [] },
        },
        {
          type: "response.output_item.added",
          output_index: 1,
          item: { id: "msg_1", type: "message", content: [] },
        },
        {
          type: "response.output_text.delta",
          item_id: "msg_1",
          output_index: 1,
          content_index: 0,
          delta: "知道的。",
        },
        {
          type: "response.completed",
          response: {
            output: [
              { id: "rs_1", type: "reasoning", summary: [] },
              {
                id: "msg_1",
                type: "message",
                content: [{ type: "output_text", text: "知道的。" }],
              },
            ],
          },
        },
      ],
      true,
    );
    expect(result.outputs).toEqual([
      {
        kind: "reasoning",
        name: undefined,
        text: "The user asks who that is.",
      },
      { kind: "text", name: undefined, text: "知道的。" },
    ]);
  });

  it("reads reasoning that a response item carries as content blocks", async () => {
    const result = await preview({
      output: [
        {
          type: "reasoning",
          summary: [],
          content: [{ type: "reasoning_text", text: "Think first." }],
        },
        { type: "message", content: [{ type: "output_text", text: "Done." }] },
      ],
    });
    expect(result.outputs).toEqual([
      { kind: "reasoning", name: undefined, text: "Think first." },
      { kind: "text", name: undefined, text: "Done." },
    ]);
  });

  it("keeps tool-only Responses output and reasoning separate from text", async () => {
    const result = await preview(
      [
        {
          type: "response.output_item.added",
          output_index: 0,
          item: {
            id: "tool_1",
            type: "function_call",
            name: "search",
            arguments: "",
          },
        },
        {
          type: "response.function_call_arguments.delta",
          item_id: "tool_1",
          delta: '{"query":',
        },
        {
          type: "response.function_call_arguments.delta",
          item_id: "tool_1",
          delta: '"OpenAI"}',
        },
        {
          type: "response.function_call_arguments.done",
          item_id: "tool_1",
          arguments: '{"query":"OpenAI"}',
        },
        {
          type: "response.reasoning_summary_text.delta",
          output_index: 1,
          summary_index: 0,
          delta: "Checking sources",
        },
        {
          type: "response.reasoning_summary_text.done",
          output_index: 1,
          summary_index: 0,
          text: "Checking sources",
        },
        {
          type: "response.output_item.done",
          output_index: 2,
          item: {
            type: "web_search_call",
            action: { type: "search", query: "news" },
          },
        },
      ],
      true,
    );
    expect(result.outputs).toEqual([
      {
        kind: "tool",
        name: "search",
        text: '{"query":"OpenAI"}',
        id: "tool_1",
      },
      { kind: "reasoning", name: undefined, text: "Checking sources" },
      {
        kind: "tool",
        name: "web_search_call",
        text: JSON.stringify({ type: "search", query: "news" }, null, 2),
      },
    ]);
  });

  it("preserves received text when a response fails mid-stream", async () => {
    const result = await preview(
      [
        { type: "response.output_text.delta", delta: "Partial reply" },
        {
          type: "response.failed",
          response: {
            output: [],
            error: { code: "upstream_error", message: "Stream interrupted" },
          },
        },
      ],
      true,
    );
    expect(result.incomplete).toBe(true);
    expect(result.outputs).toEqual([
      { kind: "text", text: "Partial reply" },
      { kind: "error", name: "upstream_error", text: "Stream interrupted" },
    ]);
  });

  it("does not present lifecycle events as a reply", async () => {
    const result = await preview(
      [
        { type: "response.created", response: { output: [] } },
        { type: "response.in_progress", response: { output: [] } },
      ],
      true,
    );
    expect(result.outputs).toEqual([]);
  });

  it("reconstructs Chat Completions text and interleaved tool calls", async () => {
    const result = await preview(
      [
        {
          choices: [
            {
              index: 0,
              delta: {
                content: "Hello",
                tool_calls: [
                  {
                    index: 0,
                    function: { name: "lookup", arguments: '{"id":' },
                  },
                ],
              },
            },
          ],
        },
        {
          choices: [
            {
              index: 0,
              delta: {
                content: "!",
                tool_calls: [
                  { index: 1, function: { name: "search", arguments: "{}" } },
                  { index: 0, function: { arguments: "1}" } },
                ],
              },
            },
          ],
        },
        { choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }] },
      ],
      true,
    );
    expect(result.outputs).toEqual([
      { kind: "text", text: "Hello!", name: undefined },
      { kind: "tool", text: '{"id":1}', name: "lookup" },
      { kind: "tool", text: "{}", name: "search" },
    ]);
  });

  it("parses complete JSON responses and refusals", async () => {
    expect(
      (
        await preview({
          output: [
            {
              type: "message",
              content: [{ type: "output_text", text: "Answer" }],
            },
          ],
        })
      ).outputs[0].text,
    ).toBe("Answer");
    expect(
      (
        await preview({
          choices: [{ message: { content: null, refusal: "Cannot comply" } }],
        })
      ).outputs[0].text,
    ).toBe("Cannot comply");
    expect(
      (await preview({ choices: [{ text: "Completion" }] })).outputs[0].text,
    ).toBe("Completion");
  });

  it("joins Anthropic text, thinking, and tool argument blocks", async () => {
    const result = await preview(
      [
        {
          type: "content_block_start",
          index: 0,
          content_block: { type: "thinking", thinking: "" },
        },
        {
          type: "content_block_delta",
          index: 0,
          delta: { type: "thinking_delta", thinking: "Consider this" },
        },
        {
          type: "content_block_start",
          index: 1,
          content_block: { type: "text", text: "" },
        },
        {
          type: "content_block_delta",
          index: 1,
          delta: { type: "text_delta", text: "Reply" },
        },
        {
          type: "content_block_start",
          index: 2,
          content_block: { type: "tool_use", name: "weather", input: {} },
        },
        {
          type: "content_block_delta",
          index: 2,
          delta: { type: "input_json_delta", partial_json: '{"city":' },
        },
        {
          type: "content_block_delta",
          index: 2,
          delta: { type: "input_json_delta", partial_json: '"Shanghai"}' },
        },
      ],
      true,
    );
    expect(result.outputs).toEqual([
      { kind: "reasoning", text: "Consider this", name: undefined },
      { kind: "text", text: "Reply", name: undefined },
      { kind: "tool", name: "weather", text: '{"city":"Shanghai"}' },
    ]);
    expect(
      (await preview({ content: [{ type: "text", text: "Answer" }] }))
        .outputs[0].text,
    ).toBe("Answer");
  });

  it("parses Gemini chunks including tools and thinking", async () => {
    const result = await preview(
      [
        {
          candidates: [
            {
              content: {
                parts: [{ text: "Think", thought: true }, { text: "Hello " }],
              },
            },
          ],
        },
        {
          candidates: [
            {
              content: {
                parts: [
                  { text: "world" },
                  { functionCall: { name: "search", args: { q: "news" } } },
                ],
              },
            },
          ],
        },
      ],
      true,
    );
    expect(
      result.outputs.map(({ kind, text, name }) => [kind, text, name]),
    ).toEqual([
      ["reasoning", "Think", undefined],
      ["text", "Hello world", undefined],
      ["tool", '{\n  "q": "news"\n}', "search"],
    ]);
  });

  it("reports partial captures and malformed events while keeping valid text", async () => {
    const content =
      'data: {"choices":[{"delta":{"content":"Saved"}}]}\n\ndata: {"broken"';
    const result = await parseResponsePreview({
      content,
      media_type: "text/event-stream",
      truncated: true,
      captured_bytes: content.length,
    });
    expect(result.incomplete).toBe(true);
    expect(result.unparsed).toBe(true);
    expect(result.outputs[0].text).toBe("Saved");
  });

  it("handles error JSON, plain text and unknown formats without inventing replies", async () => {
    expect(
      (
        await preview({
          error: { message: "Invalid key", code: "unauthorized" },
        })
      ).outputs[0],
    ).toEqual({ kind: "error", name: "unauthorized", text: "Invalid key" });
    expect(
      (
        await preview({
          type: "error",
          error: { type: "rate_limit_error", message: "Slow down" },
        })
      ).outputs,
    ).toEqual([{ kind: "error", name: "rate_limit_error", text: "Slow down" }]);
    expect((await preview({ arbitrary: "metadata" })).outputs).toEqual([]);
    expect(
      (
        await parseResponsePreview({
          content: "Hello",
          media_type: "text/plain",
          truncated: false,
          captured_bytes: 5,
        })
      ).outputs[0].text,
    ).toBe("Hello");
  });

  it("finishes multi-batch streams and supports cancellation", async () => {
    const events = Array.from({ length: 1200 }, () => ({
      type: "response.output_text.delta",
      delta: "a",
    }));
    expect((await preview(events, true)).outputs[0].text).toBe(
      "a".repeat(1200),
    );
    const controller = new AbortController();
    controller.abort();
    await expect(
      parseResponsePreview(
        {
          content: "data: {}\n\n",
          media_type: "text/event-stream",
          truncated: false,
          captured_bytes: 10,
        },
        controller.signal,
      ),
    ).rejects.toThrow();
  });
});
