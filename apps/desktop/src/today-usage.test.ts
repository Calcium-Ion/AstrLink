import { describe, expect, it } from "vitest";

import {
  emptyTrajectoryFields,
  type RequestRecord,
} from "./request-record-model";
import {
  aggregateTodayUsage,
  cacheHitRate,
  emptyTodayUsageSummary,
  formatCacheHitPercent,
  mergeCatalogServiceUsage,
  modelUsageLabel,
  startOfTodayIso,
  UNATTRIBUTED_SERVICE_LABEL,
  UNKNOWN_MODEL_LABEL,
  UNKNOWN_SERVICE_LABEL,
  usageBarPercent,
} from "./today-usage";

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
    ...emptyTrajectoryFields,
    ...overrides,
  };
}

describe("today usage aggregation", () => {
  it("sums usage including cache read/write", () => {
    expect(
      aggregateTodayUsage(
        [
          record({
            input_tokens: 10,
            output_tokens: 20,
            total_tokens: 30,
            cache_read_tokens: 4,
            cache_write_tokens: 1,
          }),
          record(null),
          record({
            input_tokens: 1,
            output_tokens: 2,
            total_tokens: 3,
            cache_read_tokens: 0,
          }),
        ],
        false,
      ),
    ).toEqual({
      requests: 3,
      failed_requests: 0,
      input_tokens: 11,
      output_tokens: 22,
      total_tokens: 33,
      cache_read_tokens: 4,
      cache_write_tokens: 1,
      by_service: [
        {
          id: null,
          requests: 3,
          input_tokens: 11,
          output_tokens: 22,
          total_tokens: 33,
        },
      ],
      by_model: [
        {
          id: null,
          requests: 3,
          input_tokens: 11,
          output_tokens: 22,
          total_tokens: 33,
        },
      ],
      capped: false,
    });
  });

  it("preserves the capped flag", () => {
    expect(aggregateTodayUsage([], true).capped).toBe(true);
    expect(emptyTodayUsageSummary(true).capped).toBe(true);
  });

  it("counts only successful root records toward usage", () => {
    expect(
      aggregateTodayUsage(
        [
          record({ input_tokens: 1, output_tokens: 1, total_tokens: 2 }),
          record(
            { input_tokens: 9, output_tokens: 9, total_tokens: 18 },
            { status: "failed" },
          ),
          record(
            { input_tokens: 3, output_tokens: 3, total_tokens: 6 },
            { status: "succeeded", http_status: 502 },
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
      failed_requests: 2,
      input_tokens: 1,
      output_tokens: 1,
      total_tokens: 2,
      cache_read_tokens: 0,
      cache_write_tokens: 0,
      by_service: [
        {
          id: null,
          requests: 1,
          input_tokens: 1,
          output_tokens: 1,
          total_tokens: 2,
        },
      ],
      by_model: [
        {
          id: null,
          requests: 1,
          input_tokens: 1,
          output_tokens: 1,
          total_tokens: 2,
        },
      ],
      capped: false,
    });
  });

  it("counts failed roots and ignores cancelled or blocked roots", () => {
    const summary = aggregateTodayUsage(
      [
        record({ input_tokens: 2, output_tokens: 1, total_tokens: 3 }),
        record(null, { status: "failed" }),
        record(null, {
          id: "req_failed_child",
          parent_request_id: "req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
          status: "failed",
        }),
        record(null, { status: "cancelled" }),
        record(null, { status: "blocked" }),
        record(null, { status: "pending" }),
      ],
      false,
    );

    expect(summary.requests).toBe(1);
    expect(summary.failed_requests).toBe(1);
  });

  it("groups successful usage by service and model and sorts by tokens", () => {
    const summary = aggregateTodayUsage(
      [
        record(
          { input_tokens: 10, output_tokens: 2, total_tokens: 12 },
          {
            id: "req_1",
            service_id: "service_b",
            requested_model: "gpt-4o",
          },
        ),
        record(
          { input_tokens: 40, output_tokens: 8, total_tokens: 48 },
          {
            id: "req_2",
            service_id: "service_a",
            requested_model: "claude-sonnet",
          },
        ),
        record(
          { input_tokens: 5, output_tokens: 1, total_tokens: 6 },
          {
            id: "req_3",
            service_id: "service_a",
            requested_model: "gpt-4o",
          },
        ),
        record(
          { input_tokens: 3, output_tokens: 1, total_tokens: 4 },
          {
            id: "req_4",
            service_id: null,
            requested_model: "  ",
          },
        ),
      ],
      false,
    );

    expect(summary.by_service).toEqual([
      {
        id: "service_a",
        requests: 2,
        input_tokens: 45,
        output_tokens: 9,
        total_tokens: 54,
      },
      {
        id: "service_b",
        requests: 1,
        input_tokens: 10,
        output_tokens: 2,
        total_tokens: 12,
      },
      {
        id: null,
        requests: 1,
        input_tokens: 3,
        output_tokens: 1,
        total_tokens: 4,
      },
    ]);
    expect(summary.by_model).toEqual([
      {
        id: "claude-sonnet",
        requests: 1,
        input_tokens: 40,
        output_tokens: 8,
        total_tokens: 48,
      },
      {
        id: "gpt-4o",
        requests: 2,
        input_tokens: 15,
        output_tokens: 3,
        total_tokens: 18,
      },
      {
        id: null,
        requests: 1,
        input_tokens: 3,
        output_tokens: 1,
        total_tokens: 4,
      },
    ]);
  });

  it("computes cache hit rate from normalized input", () => {
    const withCache = {
      ...emptyTodayUsageSummary(),
      requests: 1,
      input_tokens: 100,
      output_tokens: 10,
      total_tokens: 110,
      cache_read_tokens: 40,
    };
    expect(cacheHitRate(withCache)).toBe(0.4);
    expect(formatCacheHitPercent(withCache)).toBe("40%");
    expect(cacheHitRate(emptyTodayUsageSummary())).toBeNull();
    expect(formatCacheHitPercent(emptyTodayUsageSummary())).toBe("—");
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

describe("today usage presentation helpers", () => {
  it("labels blank models as unknown", () => {
    expect(modelUsageLabel("gpt-4o")).toBe("gpt-4o");
    expect(modelUsageLabel(null)).toBe(UNKNOWN_MODEL_LABEL());
    expect(modelUsageLabel("   ")).toBe(UNKNOWN_MODEL_LABEL());
  });

  it("computes relative bar widths from the busiest group", () => {
    const groups = [
      { total_tokens: 80 },
      { total_tokens: 20 },
      { total_tokens: 0 },
    ];
    expect(usageBarPercent(80, groups)).toBe(100);
    expect(usageBarPercent(20, groups)).toBe(25);
    expect(usageBarPercent(0, groups)).toBe(0);
    expect(usageBarPercent(10, [{ total_tokens: 0 }])).toBe(0);
  });

  it("merges catalog services with usage and keeps unmatched ids", () => {
    const rows = mergeCatalogServiceUsage(
      [
        { id: "service_a", name: "Alpha", enabled: true },
        { id: "service_idle", name: "Idle", enabled: false },
      ],
      [
        {
          id: "service_a",
          requests: 2,
          input_tokens: 10,
          output_tokens: 4,
          total_tokens: 14,
        },
        {
          id: "service_gone",
          requests: 1,
          input_tokens: 3,
          output_tokens: 1,
          total_tokens: 4,
        },
        {
          id: null,
          requests: 1,
          input_tokens: 1,
          output_tokens: 1,
          total_tokens: 2,
        },
      ],
    );

    expect(rows.map((row) => [row.name, row.total_tokens, row.in_catalog])).toEqual([
      ["Alpha", 14, true],
      [UNKNOWN_SERVICE_LABEL(), 4, false],
      [UNATTRIBUTED_SERVICE_LABEL(), 2, false],
      ["Idle", 0, true],
    ]);
    expect(rows[0]?.enabled).toBe(true);
    expect(rows[1]?.id).toBe("service_gone");
    expect(rows[3]?.enabled).toBe(false);
  });
});
