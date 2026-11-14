import { describe, expect, it } from "vitest";

import {
  arcPlaces,
  clientPlaces,
  flowWidth,
  pluginHealth,
  sidePlugins,
  starPlugins,
} from "./overview-star-model";
import type { PluginSummary } from "./plugin-model";
import type { Service } from "./service-model";
import {
  emptyUsageSummary,
  emptyUsageTotals,
  resolveUsageWindow,
  type UsageGroup,
} from "./usage-range";

const place = (id: string, tokens: number, requests = 1) => ({
  id,
  name: id,
  tokens,
  requests,
  failed: 0,
});

function group(id: string | null, overrides: Partial<UsageGroup>): UsageGroup {
  return { ...emptyUsageTotals(), id, ...overrides };
}

function plugin(overrides: Partial<PluginSummary>): PluginSummary {
  return {
    key: "memory",
    name: "Memory",
    version: "1.0.0",
    source: "directory",
    enabled: true,
    state: "loaded",
    requested_capabilities: [],
    granted_capabilities: [],
    needs_network: false,
    kind: "extension",
    ...overrides,
  };
}

describe("the star map's places", () => {
  it("names the busiest and merges the rest of the totals into one", () => {
    const groups = ["a", "b", "c", "d"].map((id, index) =>
      place(id, 400 - index * 100),
    );
    const totals = { tokens: 1500, requests: 6, failed: 1 };
    expect(arcPlaces(groups, totals, 2)).toEqual([
      place("a", 400),
      place("b", 300),
      {
        id: null,
        name: "",
        tokens: 800,
        requests: 4,
        failed: 1,
        rest: { count: 2, whole: false },
      },
    ]);
    // One more than the limit keeps its own name; nothing left, no rest.
    expect(
      arcPlaces(groups.slice(0, 3), { tokens: 900, requests: 3, failed: 0 }, 2),
    ).toEqual(groups.slice(0, 3));
  });

  it("leaves out idle places and stands in for a range that names none", () => {
    expect(
      arcPlaces([place("idle", 0, 0)], { tokens: 50, requests: 2, failed: 0 }),
    ).toEqual([
      {
        id: null,
        name: "",
        tokens: 50,
        requests: 2,
        failed: 0,
        rest: { count: 0, whole: true },
      },
    ]);
  });

  it("counts a token no longer in the catalog with the rest", () => {
    const summary = {
      ...emptyUsageSummary(resolveUsageWindow("30d", new Date())),
      totals: { ...emptyUsageTotals(), requests: 5, total_tokens: 500 },
      by_token: [
        group("token_kept", { requests: 3, total_tokens: 300 }),
        group("token_deleted", { requests: 2, total_tokens: 200 }),
      ],
    };
    expect(
      clientPlaces(summary, [{ id: "token_kept", name: "Laptop" }]),
    ).toMatchObject([
      { id: "token_kept", name: "Laptop", tokens: 300 },
      { id: null, tokens: 200, requests: 2, rest: { count: 0 } },
    ]);
  });

  it("sizes lines by the square root of what they carry", () => {
    expect(flowWidth(0, 100)).toBe(3);
    expect(flowWidth(100, 100)).toBe(16);
    expect(flowWidth(25, 100)).toBe(9.5);
  });
});

describe("the star map's plugins", () => {
  const services = [
    { id: "svc_video", kind: "plugin", plugin: { key: "video" } },
    { id: "svc_other", kind: "newapi" },
  ] as unknown as Service[];

  it("tells each plugin's side and state by what it did over the range", () => {
    const plugins = starPlugins(
      [
        plugin({}),
        plugin({ key: "rules", name: "Rules" }),
        plugin({ key: "broken", name: "Broken", state: "failed" }),
        plugin({ key: "idle", name: "Idle", enabled: false }),
        plugin({
          key: "video",
          name: "Video",
          kind: "task",
          requested_capabilities: ["provider"],
        }),
      ],
      [
        {
          plugin: "memory",
          requests: 8,
          failed_requests: 0,
          tasks: 0,
          failed_tasks: 0,
        },
        {
          plugin: "rules",
          requests: 3,
          failed_requests: 1,
          tasks: 0,
          failed_tasks: 0,
        },
        {
          plugin: "video",
          requests: 0,
          failed_requests: 0,
          tasks: 4,
          failed_tasks: 0,
        },
      ],
      services,
      [
        group("svc_video", { requests: 5, failed_requests: 1 }),
        group("svc_other", { requests: 9 }),
      ],
    );
    expect(
      plugins.map(
        ({ plugin, side, state, calls, tasks, providers, served }) => [
          plugin.key,
          side,
          state,
          calls,
          tasks,
          providers,
          served,
        ],
      ),
    ).toEqual([
      ["memory", "enhancer", "running", 8, 0, 0, 0],
      ["rules", "enhancer", "failing", 4, 0, 0, 0],
      ["broken", "enhancer", "failed", 0, 0, 0, 0],
      ["idle", "enhancer", "off", 0, 0, 0, 0],
      ["video", "provider", "running", 0, 4, 1, 6],
    ]);
    expect(pluginHealth(plugins)).toEqual({
      installed: 5,
      running: 3,
      problems: 2,
    });
    // Running first, the busiest of them first; then the broken, then off.
    expect(
      sidePlugins(plugins, "enhancer").map((entry) => entry.plugin.key),
    ).toEqual(["memory", "rules", "broken", "idle"]);
  });
});
