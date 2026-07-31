import { describe, expect, it } from "vitest";

import type { RequestRecord } from "./request-record-model";
import { aggregateTodayUsage, startOfTodayIso } from "./today-usage";

function record(
  usage: RequestRecord["usage"],
  overrides: Partial<RequestRecord> = {},
): RequestRecord {
  return {
    id: "req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    parent_request_id: null,
    attempt_index: 1,
    child_count: 0,
    started_at: "2026-07-25T10:00:00Z",
    completed_at: null,
    status: "succeeded",
    input_protocol: "openai.chat",
    requested_model: null,
    streaming: false,
    route_id: null,
    service_id: null,
    local_access_token_id: null,
    http_status: 200,
    latency_ms: 10,
    usage,
    error: null,
    audit: {
      request_body_captured: false,
      response_content_captured: false,
      request_body_truncated: false,
      response_content_truncated: false,
      upstream_request_body_captured: false,
      upstream_response_content_captured: false,
      upstream_request_body_truncated: false,
      upstream_response_content_truncated: false,
    },
    privacy_restore: null,
    ...overrides,
  };
}

describe("today usage aggregation", () => {
  it("sums usage and counts null-usage requests as zero tokens", () => {
    expect(
      aggregateTodayUsage(
        [
          record({
            input_tokens: 10,
            output_tokens: 20,
            total_tokens: 30,
          }),
          record(null),
          record({
            input_tokens: 1,
            output_tokens: 2,
            total_tokens: 3,
          }),
        ],
        false,
      ),
    ).toEqual({
      requests: 3,
      input_tokens: 11,
      output_tokens: 22,
      total_tokens: 33,
      capped: false,
    });
  });

  it("preserves the capped flag", () => {
    expect(aggregateTodayUsage([], true).capped).toBe(true);
  });

  it("counts only successful root records", () => {
    expect(
      aggregateTodayUsage(
        [
          record({ input_tokens: 1, output_tokens: 1, total_tokens: 2 }),
          record(
            { input_tokens: 9, output_tokens: 9, total_tokens: 18 },
            { status: "failed" },
          ),
          record(
            { input_tokens: 5, output_tokens: 5, total_tokens: 10 },
            {
              id: "req_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
              parent_request_id: "req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
              attempt_index: 1,
            },
          ),
        ],
        false,
      ),
    ).toEqual({
      requests: 1,
      input_tokens: 1,
      output_tokens: 1,
      total_tokens: 2,
      capped: false,
    });
  });

  it("returns local midnight as an ISO string", () => {
    const now = new Date(2026, 6, 25, 15, 30, 0);
    const start = new Date(startOfTodayIso(now));
    expect(start.getFullYear()).toBe(2026);
    expect(start.getMonth()).toBe(6);
    expect(start.getDate()).toBe(25);
    expect(start.getHours()).toBe(0);
    expect(start.getMinutes()).toBe(0);
  });
});
