import { describe, expect, it } from "vitest";

import {
  applyQueuedRecords,
  formatDuration,
  liveDurationMs,
  mergeLivePage,
  recordMatchesFilters,
} from "./request-live-model";
import type { RequestRecord } from "./request-record-model";

function record(
  id: string,
  startedAt: string,
  status: RequestRecord["status"] = "pending",
): RequestRecord {
  return {
    id,
    started_at: startedAt,
    completed_at: null,
    status,
    input_protocol: "openai.responses",
    requested_model: "gpt-test",
    streaming: true,
    route_id: null,
    service_id: "service_01",
    local_access_token_id: null,
    http_status: null,
    latency_ms: null,
    usage: null,
    error: null,
    audit: {
      request_body_captured: false,
      response_content_captured: false,
      request_body_truncated: false,
      response_content_truncated: false,
    },
    privacy_restore: null,
  };
}

describe("request live merge model", () => {
  it("updates an existing pending row in place while queuing new rows", () => {
    const pending = record("request_a", "2026-07-25T10:00:00Z");
    const older = record("request_old", "2026-07-25T09:00:00Z", "succeeded");
    const completed = {
      ...pending,
      completed_at: "2026-07-25T10:00:02Z",
      status: "succeeded" as const,
      latency_ms: 2000,
      http_status: 200,
    };
    const newRecord = record("request_b", "2026-07-25T10:01:00Z");

    const result = mergeLivePage(
      [pending, older],
      [],
      [newRecord, completed],
      true,
    );

    expect(result.items.map((item) => item.id)).toEqual([
      "request_a",
      "request_old",
    ]);
    expect(result.items[0]).toMatchObject({
      status: "succeeded",
      latency_ms: 2000,
    });
    expect(result.queued.map((item) => item.id)).toEqual(["request_b"]);
  });

  it("prepends at the top and applies queued records in newest-first order", () => {
    const current = record("request_a", "2026-07-25T10:00:00Z");
    const newest = record("request_c", "2026-07-25T10:02:00Z");
    const middle = record("request_b", "2026-07-25T10:01:00Z");
    const merged = mergeLivePage([current], [], [newest, middle], false);
    expect(merged.items.map((item) => item.id)).toEqual([
      "request_c",
      "request_b",
      "request_a",
    ]);
    expect(
      applyQueuedRecords([current], [middle, newest]).map((item) => item.id),
    ).toEqual(["request_c", "request_b", "request_a"]);
  });

  it("filters locally so status transitions can still be merged", () => {
    const value = record("request_a", "2026-07-25T10:00:00Z", "failed");
    expect(
      recordMatchesFilters(value, {
        status: "failed",
        serviceId: "service_01",
        protocol: "openai.responses",
      }),
    ).toBe(true);
    expect(
      recordMatchesFilters(value, {
        status: "pending",
        serviceId: "",
        protocol: "",
      }),
    ).toBe(false);
  });

  it("computes live elapsed time without replacing a terminal latency", () => {
    const pending = record("request_a", "2026-07-25T10:00:00Z");
    expect(
      liveDurationMs(pending, Date.parse("2026-07-25T10:00:01.500Z")),
    ).toBe(1500);
    expect(formatDuration(1500)).toBe("1.5 s");
    expect(
      liveDurationMs({ ...pending, latency_ms: 220 }, Date.now()),
    ).toBe(220);
  });
});
