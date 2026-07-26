// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { AuditReviewer } from "./AuditReviewer";
import type { AuditContent, RequestRecord } from "./request-record-model";

const record: RequestRecord = {
  id: "request_stream",
  started_at: "2026-07-25T10:00:00Z",
  completed_at: "2026-07-25T10:00:02Z",
  status: "succeeded",
  input_protocol: "openai.responses",
  requested_model: "gpt-stream",
  streaming: true,
  route_id: null,
  endpoint_id: null,
  local_access_token_id: null,
  http_status: 200,
  latency_ms: 2000,
  usage: null,
  error: null,
  audit: {
    request_body_captured: false,
    response_content_captured: true,
    request_body_truncated: false,
    response_content_truncated: false,
  },
};

describe("AuditReviewer", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it("shows an 85KB stream as a semantic timeline and batches event rendering", async () => {
    const delta = "readable-output-".repeat(12);
    const eventCount = 460;
    const stream = Array.from(
      { length: eventCount },
      (_, index) =>
        `event: response.output_text.delta\ndata: ${JSON.stringify({
          type: "response.output_text.delta",
          delta: `${index}:${delta}`,
        })}\n\n`,
    ).join("");
    expect(stream.length).toBeGreaterThan(85 * 1024);
    const content: AuditContent = {
      request_id: record.id,
      request_body: null,
      response_content: {
        media_type: "text/event-stream",
        content: stream,
        truncated: false,
        captured_bytes: stream.length,
      },
    };

    await act(async () => {
      root.render(
        <AuditReviewer
          content={content}
          onBack={() => undefined}
          onClear={() => undefined}
          record={record}
        />,
      );
      await Promise.resolve();
    });
    await act(async () => await Promise.resolve());

    expect(container.querySelector(".audit-timeline")).not.toBeNull();
    expect(container.querySelector(".audit-raw")).toBeNull();
    const timelineBlocks = [
      ...container.querySelectorAll(".audit-timeline__content"),
    ];
    expect(timelineBlocks.length).toBeGreaterThan(8);
    expect(
      Math.max(...timelineBlocks.map((node) => node.textContent?.length ?? 0)),
    ).toBeLessThanOrEqual(8 * 1024);

    const eventsTab = [...container.querySelectorAll("button")].find(
      (button) => button.textContent?.includes(`事件 · ${eventCount}`),
    );
    await act(async () => (eventsTab as HTMLButtonElement).click());
    expect(container.querySelectorAll(".audit-event-card")).toHaveLength(300);
    const loadMore = [...container.querySelectorAll("button")].find((button) =>
      button.textContent?.includes("再显示 160 个事件"),
    );
    await act(async () => (loadMore as HTMLButtonElement).click());
    expect(container.querySelectorAll(".audit-event-card")).toHaveLength(
      eventCount,
    );
  });
});
