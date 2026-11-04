import { describe, expect, it } from "vitest";

import {
  clientSeriesColor,
  modelSeriesColors,
  MUTED_SERIES_COLOR,
} from "./series-colors";
import {
  clientShares,
  starTrail,
  starTrailByModel,
  starTrailLegend,
  wholePercents,
} from "./star-trail-model";
import {
  emptyUsageSummary,
  emptyUsageTotals,
  resolveUsageWindow,
  type UsageGroup,
  type UsageHourModelBucket,
} from "./usage-range";

function group(
  id: string | null,
  total_tokens: number,
  requests = 1,
): UsageGroup {
  return { ...emptyUsageTotals(), id, total_tokens, requests };
}

function bucket(
  date: string,
  hour: number,
  model: string | null,
  requests: number,
  total_tokens: number,
): UsageHourModelBucket {
  return { date, hour, model, requests, total_tokens };
}

describe("series colors", () => {
  it("gives a model its family's color and the client its own", () => {
    const [opus, sonnet, gpt, local, localUpper] = modelSeriesColors([
      "claude-opus-5-5",
      "claude-sonnet-5",
      "gpt-5.5",
      "my-local-model",
      "My-Local-Model",
    ]);
    expect(opus).toBe("var(--model-series-1)");
    expect(sonnet).toBe(opus);
    expect(gpt).toBe("var(--model-series-2)");
    expect(local).toMatch(/^var\(--model-series-\d\)$/);
    expect(localUpper).toBe(local);
    expect(clientSeriesColor("unknown")).toBe(MUTED_SERIES_COLOR);
    expect(clientSeriesColor(null)).toBe(MUTED_SERIES_COLOR);
  });

  it("never gives two families on one chart the same color", () => {
    const colors = modelSeriesColors([
      "claude-opus-5-5",
      "deepseek-v4",
      "grok-4.7",
      "gpt-6.1-sol",
      "gemini-3.1-pro",
    ]);
    expect(new Set(colors).size).toBe(5);
    // Grok's mark has no hue; it takes gold, not Claude's orange.
    expect(colors[2]).toBe("var(--model-series-5)");
    // GLM asks for DeepSeek's blue and takes the next free color.
    expect(modelSeriesColors(["deepseek-v4", "glm-5"])).toEqual([
      "var(--model-series-3)",
      "var(--model-series-4)",
    ]);
  });

  it("moves a family into a free color, not a quieter family's own", () => {
    // Mistral asks for Claude's orange; GPT, though quieter, keeps green.
    expect(
      modelSeriesColors(["claude-opus-5-5", "mistral-large-3", "gpt-5.5"]),
    ).toEqual([
      "var(--model-series-1)",
      "var(--model-series-3)",
      "var(--model-series-2)",
    ]);
  });

  it("keeps Codex, DeepSeek Harness and Cursor apart", () => {
    expect(clientSeriesColor("deepseek_harness")).toBe("var(--series-1)");
    expect(clientSeriesColor("codex")).toBe("var(--series-7)");
    expect(clientSeriesColor("cursor")).toBe("var(--series-8)");
  });
});

describe("star trail model", () => {
  it("names the top five models and folds the rest, the unknown model included", () => {
    const legend = starTrailLegend([
      group("gpt-5.5", 400),
      group(null, 300),
      group("claude-opus-5-5", 200),
      group("glm-5.3", 50),
      group("gemini-3-pro", 30),
      group("kimi-k3", 15),
      group("qwen-4", 4),
      group("idle-model", 0, 0),
    ]);
    expect(legend.rows.map((row) => row.id)).toEqual([
      "gpt-5.5",
      "claude-opus-5-5",
      "glm-5.3",
      "gemini-3-pro",
      "kimi-k3",
    ]);
    expect(legend.rows.map((row) => row.bar)).toEqual([100, 50, 13, 8, 4]);
    expect(legend.rows.map((row) => row.color)).toEqual([
      "var(--model-series-2)",
      "var(--model-series-1)",
      "var(--model-series-3)",
      "var(--model-series-4)",
      "var(--model-series-5)",
    ]);
    expect(legend.rest).toMatchObject({
      id: null,
      total_tokens: 304,
      folded: 2,
      color: MUTED_SERIES_COLOR,
    });
    const percents = [...legend.rows, legend.rest!].map((row) => row.percent);
    expect(percents.reduce((sum, value) => sum + value, 0)).toBe(100);
    expect(starTrailLegend([group("gpt-5.5", 10)]).rest).toBeNull();
  });

  it("lights each hour in its top model's legend color, by requests against the busiest hour", () => {
    const summary = emptyUsageSummary(
      resolveUsageWindow("7d", new Date(2026, 9, 31, 12)),
    );
    const [first, , , , , , today] = summary.by_day.map((day) => day.date);
    summary.by_hour_model = [
      bucket(first, 3, "gpt-5.5", 1, 500),
      bucket(first, 3, "claude-opus-5-5", 3, 400),
      bucket(today, 9, "outside-model", 4, 900),
      bucket(today, 9, "gpt-5.5", 4, 100),
      bucket(today, 10, null, 2, 50),
      bucket("2026-01-01", 1, "gpt-5.5", 9, 9),
    ];
    summary.by_model = [
      group("gpt-5.5", 600),
      group("claude-opus-5-5", 400),
      group("outside-model", 90),
    ];
    const legend = starTrailLegend(summary.by_model, 2);
    const trail = starTrail(summary, legend);
    expect(trail.days).toHaveLength(7);
    expect(trail.arcs.map((arc) => [arc.ring, arc.hour])).toEqual([
      [0, 3],
      [6, 9],
      [6, 10],
    ]);
    const [early, busy, unknown] = trail.arcs;
    // The hour's top model is the one with the most tokens, not calls.
    expect(early).toMatchObject({
      model: "gpt-5.5",
      requests: 4,
      total_tokens: 900,
    });
    expect(early.color).toBe(
      legend.rows.find((row) => row.id === "gpt-5.5")!.color,
    );
    expect(busy.brightness).toBe(1);
    expect(early.brightness).toBe(0.5);
    // A top model the legend does not name takes the rest's muted color.
    expect(busy).toMatchObject({
      model: "outside-model",
      color: MUTED_SERIES_COLOR,
    });
    expect(unknown).toMatchObject({ model: null, color: MUTED_SERIES_COLOR });
  });

  it("draws a day as a ring per model, the busiest outermost and the rest inside", () => {
    const summary = emptyUsageSummary(
      resolveUsageWindow("1d", new Date(2026, 10, 2, 9, 30)),
    );
    const [yesterday, today] = summary.by_day.map((day) => day.date);
    summary.by_hour_model = [
      bucket(today, 9, "claude-opus-5-5", 2, 300),
      bucket(yesterday, 22, "gpt-5.5", 1, 200),
      bucket(today, 8, "gpt-5.5", 3, 800),
      bucket(today, 8, "glm-5.3", 1, 40),
      bucket(today, 8, null, 1, 10),
      bucket(today, 9, "gpt-5.5", 0, 0),
    ];
    summary.by_model = [
      group("gpt-5.5", 1000),
      group("claude-opus-5-5", 300),
      group("glm-5.3", 40),
      group(null, 10),
    ];
    const legend = starTrailLegend(summary.by_model, 2);
    const trail = starTrailByModel(summary, legend);
    expect(trail).toMatchObject({
      by: "model",
      days: [yesterday, today],
      rings: 3,
    });
    expect(
      trail.arcs.map((arc) => [arc.ring, arc.model, arc.date, arc.hour]),
    ).toEqual([
      // The rest ring folds glm-5.3 and the unknown model into one arc.
      [0, null, today, 8],
      [1, "claude-opus-5-5", today, 9],
      [2, "gpt-5.5", yesterday, 22],
      [2, "gpt-5.5", today, 8],
    ]);
    const [rest, claude, , busiest] = trail.arcs;
    expect(rest).toMatchObject({
      requests: 2,
      total_tokens: 50,
      color: MUTED_SERIES_COLOR,
    });
    expect(claude.color).toBe("var(--model-series-1)");
    // Brightness follows tokens, not calls.
    expect(busiest.brightness).toBe(1);
    expect(claude.brightness).toBe(300 / 800);
    expect(new Set(trail.arcs.map((arc) => arc.key)).size).toBe(4);
  });

  it("draws a single model as a single ring", () => {
    const summary = emptyUsageSummary(
      resolveUsageWindow("1d", new Date(2026, 10, 2, 9, 30)),
    );
    const today = summary.by_day.at(-1)!.date;
    summary.by_hour_model = [bucket(today, 7, "gpt-5.5", 4, 120)];
    summary.by_model = [group("gpt-5.5", 120, 4)];
    const trail = starTrailByModel(summary, starTrailLegend(summary.by_model));
    expect(trail.rings).toBe(1);
    expect(trail.arcs).toMatchObject([
      { ring: 0, model: "gpt-5.5", hour: 7, brightness: 1 },
    ]);
    expect(
      starTrailByModel(emptyUsageSummary(summary.window), {
        rows: [],
        rest: null,
      }),
    ).toMatchObject({ rings: 0, arcs: [] });
  });

  it("splits clients into shares of 100 with unknown and historical calls last", () => {
    const shares = clientShares([
      group(null, 120),
      group("claude_code", 333),
      group("codex", 333),
      group("future_client", 100),
      group("cursor", 0, 4),
    ]);
    expect(shares.map((share) => share.id)).toEqual([
      "claude_code",
      "codex",
      null,
    ]);
    expect(shares.at(-1)).toMatchObject({
      total_tokens: 220,
      color: MUTED_SERIES_COLOR,
    });
    expect(shares.reduce((sum, share) => sum + share.percent, 0)).toBe(100);
    expect(clientShares([group("codex", 0)])).toEqual([]);
  });

  it("rounds whole percents to exactly 100", () => {
    expect(wholePercents([1, 1, 1])).toEqual([34, 33, 33]);
    expect(wholePercents([0, 0])).toEqual([0, 0]);
    expect(wholePercents([2, 0, 1])).toEqual([67, 0, 33]);
  });
});
