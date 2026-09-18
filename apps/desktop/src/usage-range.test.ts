import { describe, expect, it } from "vitest";

import {
  emptyTrajectoryFields,
  type RequestRecord,
} from "./request-record-model";
import {
  aggregateUsage,
  aggregateUsageRecords,
  cacheHitRate,
  dayTokenStack,
  DEFAULT_USAGE_RANGE_PRESET,
  emptyUsageSummary,
  emptyUsageTotals,
  formatCacheHitPercent,
  isUsageRangePreset,
  localDayKey,
  mergeCatalogServiceUsage,
  modelUsageLabel,
  resolveUsageWindow,
  startOfTodayIso,
  usageWindowHourSlots,
  UNATTRIBUTED_SERVICE_LABEL,
  UNKNOWN_MODEL_LABEL,
  UNKNOWN_SERVICE_LABEL,
  usageBarPercent,
  usageRangeDays,
  usageWindowDayKeys,
} from "./usage-range";

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

/** A record started at a local wall-clock time, so day bucketing is stable. */
function localRecord(
  local: [year: number, month: number, day: number, hour?: number],
  usage: RequestRecord["usage"],
  overrides: Partial<RequestRecord> = {},
): RequestRecord {
  const [year, month, day, hour = 12] = local;
  return record(usage, {
    started_at: new Date(year, month, day, hour).toISOString(),
    ...overrides,
  });
}

describe("usage range windows", () => {
  it("defaults to seven local days ending today", () => {
    expect(DEFAULT_USAGE_RANGE_PRESET).toBe("7d");
    expect(usageRangeDays("1d")).toBe(1);
    expect(usageRangeDays("7d")).toBe(7);
    expect(usageRangeDays("30d")).toBe(30);

    const window = resolveUsageWindow("7d", new Date(2026, 8, 4, 23, 30));
    const from = new Date(window.from);
    const to = new Date(window.to);

    expect(localDayKey(from)).toBe("2026-08-29");
    expect(from.getHours()).toBe(0);
    expect(from.getMinutes()).toBe(0);
    expect(localDayKey(to)).toBe("2026-09-05");
    expect(to.getHours()).toBe(0);
  });

  it("opens a rolling twenty-four hour window ending at the current hour", () => {
    const window = resolveUsageWindow("1d", new Date(2026, 8, 4, 0, 5));
    const from = new Date(window.from);
    const to = new Date(window.to);
    expect(localDayKey(from)).toBe("2026-09-03");
    expect(from.getHours()).toBe(1);
    expect(from.getMinutes()).toBe(0);
    expect(localDayKey(to)).toBe("2026-09-04");
    expect(to.getHours()).toBe(1);
    expect(usageWindowDayKeys(window)).toEqual(["2026-09-03", "2026-09-04"]);
  });

  it("labels rolling hours in the computer time zone, not UTC", () => {
    const now = new Date("2026-09-06T00:30:00.000Z");
    const window = resolveUsageWindow("1d", now, "Asia/Shanghai");
    const slots = usageWindowHourSlots(window);

    expect(slots).toHaveLength(24);
    expect(slots[0]).toEqual({ date: "2026-09-05", hour: 9 });
    expect(slots.at(-1)).toEqual({ date: "2026-09-06", hour: 8 });

    const summary = aggregateUsage(
      [
        record(
          { input_tokens: 1, output_tokens: 1, total_tokens: 2 },
          { started_at: "2026-09-06T00:10:00.000Z" },
        ),
      ],
      window,
      false,
    );
    expect(
      summary.by_hour.find(
        (bucket) => bucket.date === "2026-09-06" && bucket.hour === 8,
      ),
    ).toMatchObject({ requests: 1, total_tokens: 2 });
    expect(
      summary.by_hour.some((bucket) => bucket.hour === 0 && bucket.requests > 0),
    ).toBe(false);
  });

  it("walks month boundaries when listing window days", () => {
    const window = resolveUsageWindow("7d", new Date(2026, 2, 3, 9));
    expect(usageWindowDayKeys(window)).toEqual([
      "2026-02-25",
      "2026-02-26",
      "2026-02-27",
      "2026-02-28",
      "2026-03-01",
      "2026-03-02",
      "2026-03-03",
    ]);
  });

  it("recognizes only supported presets", () => {
    expect(isUsageRangePreset("7d")).toBe(true);
    expect(isUsageRangePreset("90d")).toBe(false);
    expect(isUsageRangePreset(null)).toBe(false);
  });

  it("returns local midnight as an ISO string", () => {
    const start = new Date(startOfTodayIso(new Date(2026, 6, 25, 15, 30, 0)));
    expect(localDayKey(start)).toBe("2026-07-25");
    expect(start.getHours()).toBe(0);
    expect(start.getMinutes()).toBe(0);
  });

  it("pads an empty summary with one zero bucket per window day", () => {
    const window = resolveUsageWindow("7d", new Date(2026, 8, 4, 12));
    const summary = emptyUsageSummary(window, true);

    expect(summary.capped).toBe(true);
    expect(summary.totals).toEqual(emptyUsageTotals());
    expect(summary.by_day).toHaveLength(7);
    expect(summary.by_hour).toEqual([]);
    expect(summary.by_day.at(-1)).toEqual({
      date: "2026-09-04",
      ...emptyUsageTotals(),
    });
  });

  it("pads twenty-four empty hours across midnight for a rolling day", () => {
    const window = resolveUsageWindow("1d", new Date(2026, 8, 4, 12));
    const summary = emptyUsageSummary(window);

    expect(summary.by_day.map((bucket) => bucket.date)).toEqual([
      "2026-09-03",
      "2026-09-04",
    ]);
    expect(summary.by_hour).toHaveLength(24);
    expect(summary.by_hour[0]).toEqual({
      date: "2026-09-03",
      hour: 13,
      ...emptyUsageTotals(),
    });
    expect(summary.by_hour.at(-1)).toEqual({
      date: "2026-09-04",
      hour: 12,
      ...emptyUsageTotals(),
    });
    expect(summary.by_hour.map((bucket) => bucket.hour)).toEqual([
      13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9,
      10, 11, 12,
    ]);
  });
});

describe("usage aggregation", () => {
  it("sums usage including cache read/write", () => {
    const aggregate = aggregateUsageRecords([
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
    ]);

    expect(aggregate.totals).toEqual({
      requests: 3,
      failed_requests: 0,
      input_tokens: 11,
      output_tokens: 22,
      total_tokens: 33,
      cache_read_tokens: 4,
      cache_write_tokens: 1,
    });
    expect(aggregate.scanned_records).toBe(3);
    expect(aggregate.by_service).toEqual([
      {
        id: null,
        requests: 3,
        failed_requests: 0,
        input_tokens: 11,
        output_tokens: 22,
        total_tokens: 33,
        cache_read_tokens: 4,
        cache_write_tokens: 1,
      },
    ]);
    expect(aggregate.by_model).toEqual(aggregate.by_service);
  });

  it("counts only successful root records toward usage", () => {
    const aggregate = aggregateUsageRecords([
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
    ]);

    expect(aggregate.totals.requests).toBe(1);
    expect(aggregate.totals.failed_requests).toBe(2);
    expect(aggregate.totals.total_tokens).toBe(2);
    expect(aggregate.scanned_records).toBe(3);
  });

  it("counts failed roots and ignores cancelled or blocked roots", () => {
    const aggregate = aggregateUsageRecords([
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
    ]);

    expect(aggregate.totals.requests).toBe(1);
    expect(aggregate.totals.failed_requests).toBe(1);
  });

  it("groups successful usage by service and model and sorts by tokens", () => {
    const aggregate = aggregateUsageRecords([
      record(
        { input_tokens: 10, output_tokens: 2, total_tokens: 12 },
        { id: "req_1", service_id: "service_b", requested_model: "gpt-4o" },
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
        { id: "req_3", service_id: "service_a", requested_model: "gpt-4o" },
      ),
      record(
        { input_tokens: 3, output_tokens: 1, total_tokens: 4 },
        { id: "req_4", service_id: null, requested_model: "  " },
      ),
    ]);

    expect(
      aggregate.by_service.map((group) => [group.id, group.total_tokens]),
    ).toEqual([
      ["service_a", 54],
      ["service_b", 12],
      [null, 4],
    ]);
    expect(
      aggregate.by_model.map((group) => [group.id, group.total_tokens]),
    ).toEqual([
      ["claude-sonnet", 48],
      ["gpt-4o", 18],
      [null, 4],
    ]);
    expect(aggregate.by_service[0]?.requests).toBe(2);
  });

  it("buckets records into local days and pads the gaps", () => {
    const window = resolveUsageWindow("7d", new Date(2026, 8, 4, 20));
    const summary = aggregateUsage(
      [
        localRecord([2026, 8, 4, 9], {
          input_tokens: 10,
          output_tokens: 5,
          total_tokens: 15,
        }),
        localRecord([2026, 8, 4, 21], {
          input_tokens: 2,
          output_tokens: 1,
          total_tokens: 3,
        }),
        localRecord([2026, 8, 1, 8], {
          input_tokens: 7,
          output_tokens: 3,
          total_tokens: 10,
        }),
        localRecord([2026, 8, 1, 8], null, { status: "failed" }),
      ],
      window,
      false,
    );

    expect(summary.window).toEqual(window);
    expect(summary.by_day.map((bucket) => bucket.date)).toEqual([
      "2026-08-29",
      "2026-08-30",
      "2026-08-31",
      "2026-09-01",
      "2026-09-02",
      "2026-09-03",
      "2026-09-04",
    ]);
    expect(
      summary.by_day.map((bucket) => [
        bucket.date,
        bucket.requests,
        bucket.total_tokens,
        bucket.failed_requests,
      ]),
    ).toEqual([
      ["2026-08-29", 0, 0, 0],
      ["2026-08-30", 0, 0, 0],
      ["2026-08-31", 0, 0, 0],
      ["2026-09-01", 1, 10, 1],
      ["2026-09-02", 0, 0, 0],
      ["2026-09-03", 0, 0, 0],
      ["2026-09-04", 2, 18, 0],
    ]);
    expect(summary.totals.total_tokens).toBe(28);
    expect(summary.totals.failed_requests).toBe(1);
    expect(summary.scanned_records).toBe(4);
    expect(summary.by_hour).toEqual([]);
  });

  it("drops hour buckets outside the rolling window but keeps them in totals", () => {
    const window = resolveUsageWindow("1d", new Date(2026, 8, 4, 20));
    const summary = aggregateUsage(
      [
        localRecord([2026, 8, 4, 9], {
          input_tokens: 4,
          output_tokens: 1,
          total_tokens: 5,
        }),
        localRecord([2026, 8, 3, 9], {
          input_tokens: 6,
          output_tokens: 2,
          total_tokens: 8,
        }),
      ],
      window,
      false,
    );

    expect(summary.by_day.map((bucket) => bucket.date)).toEqual([
      "2026-09-03",
      "2026-09-04",
    ]);
    expect(summary.by_hour).toHaveLength(24);
    expect(
      summary.by_hour.find(
        (bucket) => bucket.date === "2026-09-04" && bucket.hour === 9,
      ),
    ).toMatchObject({
      requests: 1,
      total_tokens: 5,
    });
    expect(summary.by_hour.filter((bucket) => bucket.requests > 0)).toHaveLength(
      1,
    );
    expect(summary.totals.total_tokens).toBe(13);
  });

  it("splits the last twenty-four hours across midnight", () => {
    const window = resolveUsageWindow("1d", new Date(2026, 8, 4, 20));
    const summary = aggregateUsage(
      [
        localRecord([2026, 8, 4, 9], {
          input_tokens: 4,
          output_tokens: 1,
          total_tokens: 5,
        }),
        localRecord([2026, 8, 4, 20], {
          input_tokens: 2,
          output_tokens: 1,
          total_tokens: 3,
        }),
        localRecord([2026, 8, 4, 20], null, { status: "failed" }),
        localRecord([2026, 8, 3, 22], {
          input_tokens: 1,
          output_tokens: 1,
          total_tokens: 2,
        }),
        localRecord([2026, 8, 3, 9], {
          input_tokens: 6,
          output_tokens: 2,
          total_tokens: 8,
        }),
      ],
      window,
      false,
    );

    expect(summary.by_hour).toHaveLength(24);
    expect(summary.by_hour[0]).toMatchObject({
      date: "2026-09-03",
      hour: 21,
    });
    expect(summary.by_hour.at(-1)).toMatchObject({
      date: "2026-09-04",
      hour: 20,
    });
    expect(
      summary.by_hour.find(
        (bucket) => bucket.date === "2026-09-04" && bucket.hour === 9,
      ),
    ).toMatchObject({
      requests: 1,
      failed_requests: 0,
      total_tokens: 5,
    });
    expect(
      summary.by_hour.find(
        (bucket) => bucket.date === "2026-09-04" && bucket.hour === 20,
      ),
    ).toMatchObject({
      requests: 1,
      failed_requests: 1,
      total_tokens: 3,
    });
    expect(
      summary.by_hour.find(
        (bucket) => bucket.date === "2026-09-03" && bucket.hour === 22,
      ),
    ).toMatchObject({
      requests: 1,
      total_tokens: 2,
    });
    expect(
      summary.by_hour.filter(
        (bucket) => bucket.requests > 0 || bucket.failed_requests > 0,
      ),
    ).toHaveLength(3);
    expect(summary.totals.total_tokens).toBe(18);
  });

  it("preserves the capped flag", () => {
    const window = resolveUsageWindow("7d", new Date(2026, 8, 4, 12));
    expect(aggregateUsage([], window, true).capped).toBe(true);
    expect(aggregateUsage([], window, false).capped).toBe(false);
  });

  it("computes cache hit rate from normalized input", () => {
    const withCache = {
      ...emptyUsageTotals(),
      requests: 1,
      input_tokens: 100,
      output_tokens: 10,
      total_tokens: 110,
      cache_read_tokens: 40,
    };
    expect(cacheHitRate(withCache)).toBe(0.4);
    expect(formatCacheHitPercent(withCache)).toBe("40%");
    expect(cacheHitRate(emptyUsageTotals())).toBeNull();
    expect(formatCacheHitPercent(emptyUsageTotals())).toBe("—");
  });

  it("splits a day so cache read/write stay inside the total", () => {
    expect(
      dayTokenStack({
        total_tokens: 120,
        output_tokens: 20,
        cache_read_tokens: 40,
        cache_write_tokens: 10,
      }),
    ).toEqual({ input: 50, output: 20, cache_read: 40, cache_write: 10 });
  });

  it("keeps an empty day at zero", () => {
    expect(dayTokenStack(emptyUsageTotals())).toEqual({
      input: 0,
      output: 0,
      cache_read: 0,
      cache_write: 0,
    });
  });

  it("clamps cache segments when they exceed the total", () => {
    expect(
      dayTokenStack({
        total_tokens: 50,
        output_tokens: 10,
        cache_read_tokens: 40,
        cache_write_tokens: 30,
      }),
    ).toEqual({ input: 0, output: 0, cache_read: 40, cache_write: 10 });
    expect(
      dayTokenStack({
        total_tokens: 20,
        output_tokens: 5,
        cache_read_tokens: 80,
        cache_write_tokens: 10,
      }),
    ).toEqual({ input: 0, output: 0, cache_read: 20, cache_write: 0 });
  });
});

describe("usage presentation helpers", () => {
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
          ...emptyUsageTotals(),
          id: "service_a",
          requests: 2,
          input_tokens: 10,
          output_tokens: 4,
          total_tokens: 14,
        },
        {
          ...emptyUsageTotals(),
          id: "service_gone",
          requests: 1,
          input_tokens: 3,
          output_tokens: 1,
          total_tokens: 4,
        },
        {
          ...emptyUsageTotals(),
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
    expect(rows[3]?.requests).toBe(0);
  });
});
