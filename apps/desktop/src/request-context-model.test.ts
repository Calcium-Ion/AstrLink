import { describe, expect, it } from "vitest";

import {
  cacheMissCost,
  cacheMissReason,
  callMarkers,
  callPlaces,
  carriedChangeText,
  carriedItems,
  carriedKind,
  carriedName,
  contextAxis,
  contextComposition,
  contextMeterValue,
  contextRuns,
  failedRepeatsText,
  formatExtraUSD,
  memoryWriteLine,
  nearestCall,
  sumUSD,
} from "./request-context-model";
import {
  emptyTrajectoryFields,
  type ContextItem,
  type MemoryWrite,
  type RequestContext,
  type RequestRecord,
  type SessionContext,
} from "./request-record-model";

function root(id: string, turn: number | null): RequestRecord {
  return {
    id,
    parent_request_id: null,
    attempt_index: 1,
    child_count: 0,
    started_at: "2026-10-20T09:00:00Z",
    completed_at: "2026-10-20T09:00:05Z",
    status: "succeeded",
    input_protocol: "anthropic.messages",
    requested_model: "claude-opus",
    streaming: true,
    route_id: null,
    service_id: null,
    local_access_token_id: null,
    http_status: 200,
    latency_ms: 5000,
    usage: null,
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
    turn_index: turn,
  };
}

const context: SessionContext = {
  calls: [
    { request_id: "req_1", tokens: 100_000 },
    { request_id: "req_2", tokens: 380_000 },
    { request_id: "req_summary", tokens: 381_000 },
    { request_id: "req_3", tokens: 40_000 },
    { request_id: "req_4", tokens: 150_000 },
  ],
  compactions: [
    {
      request_id: "req_3",
      summary_request_id: "req_summary",
      before_tokens: 381_000,
      after_tokens: 40_000,
      marked: true,
    },
  ],
  cache_misses: [
    {
      request_id: "req_4",
      reason: "prefix_changed",
      changed: "AGENTS.md",
      rewritten_tokens: 120_000,
      extra_usd: "0.405000000",
    },
  ],
};

describe("context meter", () => {
  it("measures against the compaction point, else the model window", () => {
    expect(
      contextMeterValue({
        context_tokens: 93_000,
        compaction_point_tokens: 100_000,
        context_window_tokens: 200_000,
      }),
    ).toMatchObject({ basis: "compaction", percent: 93, warning: true });
    expect(
      contextMeterValue({
        context_tokens: 41_000,
        context_window_tokens: 100_000,
      }),
    ).toMatchObject({ basis: "window", percent: 41, warning: false });
    // Exactly the threshold warns.
    expect(
      contextMeterValue({ context_tokens: 85, context_window_tokens: 100 })
        ?.warning,
    ).toBe(true);
    expect(contextMeterValue({ context_tokens: 41_000 })).toBeNull();
    expect(contextMeterValue({ context_window_tokens: 100_000 })).toBeNull();
  });
});

describe("context markers and geometry", () => {
  it("notes compactions and summaries by call, and not cache misses", () => {
    const markers = callMarkers(context);
    // A miss stays in the context view; the summary folds its call.
    expect(markers.has("req_4")).toBe(false);
    expect(markers.get("req_3")?.compactionBefore?.before_tokens).toBe(381_000);
    expect(markers.get("req_summary")).toStrictEqual({ summary: true });
    expect(markers.has("req_1")).toBe(false);
    expect(callMarkers(undefined).size).toBe(0);
  });

  it("splits the curve where the context was compacted", () => {
    expect(contextRuns(context)).toStrictEqual([
      { from: 0, to: 2 },
      { from: 3, to: 4 },
    ]);
    expect(
      contextRuns({ calls: [], compactions: [], cache_misses: [] }),
    ).toStrictEqual([]);
  });

  it("picks round axis steps and the nearest call", () => {
    expect(contextAxis(381_000)).toStrictEqual({
      ceiling: 400_000,
      step: 100_000,
    });
    expect(contextAxis(312_000)).toStrictEqual({
      ceiling: 400_000,
      step: 100_000,
    });
    expect(contextAxis(9_000)).toStrictEqual({ ceiling: 10_000, step: 2_500 });
    expect(contextAxis(0)).toStrictEqual({ ceiling: 1, step: 1 });
    expect(nearestCall(5, 0.49)).toBe(2);
    expect(nearestCall(5, -1)).toBe(0);
    expect(nearestCall(5, 2)).toBe(4);
    expect(nearestCall(1, 0.7)).toBe(0);
  });

  it("numbers calls by turn and across the conversation", () => {
    const places = callPlaces([
      root("req_a", 1),
      root("req_b", 1),
      root("req_c", 2),
      root("req_d", null),
    ]);
    expect(places.get("req_b")).toStrictEqual({
      turnIndex: 1,
      index: 2,
      ordinal: 2,
    });
    expect(places.get("req_c")).toStrictEqual({
      turnIndex: 2,
      index: 1,
      ordinal: 3,
    });
    expect(places.get("req_d")).toStrictEqual({
      turnIndex: null,
      index: 1,
      ordinal: 4,
    });
  });
});

describe("cache miss amounts and lines", () => {
  it("sums dollar amounts exactly", () => {
    expect(sumUSD(["0.1", "0.2"])).toBe("0.300000000");
    expect(sumUSD(["12.000000001", "0.999999999"])).toBe("13.000000000");
    expect(sumUSD([])).toBeNull();
    expect(formatExtraUSD("0.004")).toBe("< $0.01");
    expect(formatExtraUSD("14.3")).toBe("$14.30");
    expect(formatExtraUSD("0")).toBe("$0.00");
  });

  it("says why the cache missed and what it cost", () => {
    const miss = context.cache_misses[0]!;
    expect(cacheMissReason(miss)).toBe("AGENTS.md 在上一次调用后改了");
    expect(cacheMissCost(miss)).toBe("多花 $0.41");
    expect(
      cacheMissReason({
        ...miss,
        reason: "expired",
        changed: undefined,
        idle_ms: 720_000,
        ttl_ms: 300_000,
      }),
    ).toBe("距上一次调用 12m 00s，缓存已过期");
    const unknown = {
      ...miss,
      reason: "unknown" as const,
      changed: undefined,
      extra_usd: undefined,
    };
    expect(cacheMissReason(unknown)).toBeNull();
    expect(cacheMissCost(unknown)).toBe("重新写入 12万 Token");
  });
});

const globalRules: ContextItem = {
  kind: "rules",
  name: "CLAUDE.md",
  path: "~/.claude/CLAUDE.md",
  scope: "global",
  tokens: 600,
  hash: "fp1_global",
};
const projectRules = (hash: string): ContextItem => ({
  kind: "rules",
  name: "AGENTS.md",
  path: "~/proj/AGENTS.md",
  scope: "project",
  tokens: 3_580,
  hash,
});
const localRules: ContextItem = {
  kind: "rules",
  name: "CLAUDE.local.md",
  path: "~/proj/CLAUDE.local.md",
  scope: "local",
  tokens: 120,
  hash: "fp1_local",
};
const memoryIndex = (count: number, hash?: string): ContextItem => ({
  kind: "memory",
  name: "MEMORY.md",
  path: "~/.claude/projects/~-proj/memory/MEMORY.md",
  count,
  tokens: 900,
  ...(hash ? { hash } : {}),
});
const skills: ContextItem = {
  kind: "skills",
  count: 24,
  tokens: 1_200,
  hash: "fp1_skills",
};
const mcp: ContextItem = {
  kind: "mcp",
  count: 38,
  servers: ["paseo"],
  tokens: 2_400,
  hash: "fp1_mcp",
};

function carrying(
  id: string,
  turn: number | null,
  items: ContextItem[],
  extra: Partial<RequestContext> = {},
): RequestRecord {
  return { ...root(id, turn), context: { items, memory_writes: [], ...extra } };
}

describe("what calls carried", () => {
  it("lists a call's sources largest first, with the rest last", () => {
    expect(
      contextComposition({
        history: 1_000,
        tool_results: 0,
        tools: 300,
        system: 200,
        memory: 500,
        other: 2_000,
      }),
    ).toStrictEqual([
      { key: "history", tokens: 1_000, share: 50 },
      { key: "memory", tokens: 500, share: 25 },
      { key: "tools", tokens: 300, share: 15 },
      { key: "system", tokens: 200, share: 10 },
      { key: "other", tokens: 2_000, share: 100 },
    ]);
    expect(
      contextComposition({
        history: 0,
        tool_results: 0,
        tools: 0,
        system: 0,
        memory: 0,
        other: 0,
      }),
    ).toStrictEqual([]);
  });

  const turns = [
    carrying("req_1", 1, [
      globalRules,
      projectRules("fp1_a"),
      memoryIndex(60, "fp1_m1"),
      skills,
      mcp,
    ]),
    carrying("req_2", 1, [
      globalRules,
      projectRules("fp1_a"),
      memoryIndex(60, "fp1_m1"),
      skills,
      mcp,
    ]),
    // A title helper the curve leaves out; it still takes an ordinal.
    carrying("req_title", 1, [], { helper: true }),
    carrying("req_3", 2, [
      globalRules,
      projectRules("fp1_b"),
      memoryIndex(62, "fp1_m2"),
      skills,
      localRules,
      mcp,
    ]),
    // Without a fingerprint key the hashes are absent: nothing changed.
    carrying("req_4", 2, [
      globalRules,
      projectRules("fp1_b"),
      memoryIndex(63),
      skills,
      localRules,
      mcp,
    ]),
    root("req_5", 3),
  ];
  const calls: SessionContext = {
    calls: ["req_1", "req_2", "req_3", "req_4", "req_5"].map((id) => ({
      request_id: id,
      tokens: 50_000,
    })),
    compactions: [],
    cache_misses: [],
  };
  const places = callPlaces(turns);

  it("says how each carried file changed up to a call", () => {
    const latest = carriedItems(turns, calls, "req_4", places);
    expect(
      latest.map(({ item, change }) => [item.name ?? item.kind, change]),
    ).toStrictEqual([
      ["CLAUDE.md", { kind: "unchanged" }],
      ["AGENTS.md", { kind: "changed", ordinal: 4 }],
      ["MEMORY.md", { kind: "grew", entries: 3 }],
      ["skills", { kind: "unchanged" }],
      ["CLAUDE.local.md", { kind: "added", ordinal: 4 }],
    ]);
    expect(
      carriedItems(turns, calls, "req_2", places).map(
        ({ change }) => change.kind,
      ),
    ).toStrictEqual(["unchanged", "unchanged", "unchanged", "unchanged"]);
    // Calls that kept no record of what they carried list nothing.
    expect(carriedItems(turns, calls, "req_5", places)).toStrictEqual([]);
    expect(carriedItems(turns, calls, "req_title", places)).toStrictEqual([]);
  });

  it("names carried items and their changes", () => {
    expect(
      [globalRules, projectRules("fp1_a"), localRules].map(carriedKind),
    ).toStrictEqual(["全局规则", "项目规则", "个人项目规则"]);
    expect(carriedKind(memoryIndex(63))).toBe("自动记忆 63 条");
    expect(carriedKind({ kind: "memory" })).toBe("记忆");
    expect([carriedName(skills), carriedKind(skills)]).toStrictEqual([
      "Skills 目录",
      "24 个",
    ]);
    expect(carriedName({ kind: "rules" })).toBe("未命名文件");
    // MCP tools go by the servers they come from.
    expect([carriedName(mcp), carriedKind(mcp)]).toStrictEqual([
      "MCP 工具",
      "paseo · 38 个",
    ]);
    expect(carriedKind({ kind: "mcp", count: 2 })).toBe("2 个");
    expect(
      [
        { kind: "unchanged" } as const,
        { kind: "changed", ordinal: 158 } as const,
        { kind: "added", ordinal: 4 } as const,
        { kind: "grew", entries: 1 } as const,
      ].map(carriedChangeText),
    ).toStrictEqual([
      "没变过",
      "第 158 次调用起变了",
      "第 4 次调用起带上",
      "本对话新增 1 条",
    ]);
  });
});

describe("conversation notes from each call's record", () => {
  const reply: MemoryWrite = {
    kind: "memory",
    name: "MEMORY.md",
    path: "~/.claude/projects/~-proj/memory/MEMORY.md",
    title: "Reply in Chinese",
    entry: "reply-in-chinese.md",
  };
  const rules: MemoryWrite = {
    kind: "rules",
    name: "AGENTS.md",
    path: "~/proj/AGENTS.md",
  };

  it("notes a write on the first call that shows it, and asked summaries", () => {
    const markers = callMarkers(undefined, [
      carrying("req_1", 1, [], { memory_writes: [reply] }),
      // The same body sent again repeats the write.
      carrying("req_2", 1, [], { memory_writes: [reply] }),
      carrying("req_helper", 1, [], { helper: true, memory_writes: [rules] }),
      carrying("req_3", 2, [], { memory_writes: [reply, rules] }),
      carrying("req_4", 2, [], { summary: true }),
      root("req_5", 3),
    ]);
    expect(markers.get("req_1")).toStrictEqual({ memory: [reply] });
    expect(markers.has("req_2")).toBe(false);
    expect(markers.has("req_helper")).toBe(false);
    expect(markers.get("req_3")).toStrictEqual({ memory: [rules] });
    expect(markers.get("req_4")).toStrictEqual({ summary: true });
    expect(markers.has("req_5")).toBe(false);
  });

  it("words memory lines, repeated failures and changed files", () => {
    expect(memoryWriteLine(reply)).toBe("新增记忆「Reply in Chinese」");
    expect(memoryWriteLine(rules)).toBe("新增规则");
    expect(failedRepeatsText({ tool: "Bash", count: 4, command: true })).toBe(
      "同一条 Bash 命令连续失败 4 次",
    );
    expect(failedRepeatsText({ tool: "Read", count: 3 })).toBe(
      "同样的 Read 调用连续失败 3 次",
    );
    const miss = context.cache_misses[0]!;
    expect(
      ["skills", "rules", "memory", "tools", ""].map((changed) =>
        cacheMissReason({ ...miss, changed }),
      ),
    ).toStrictEqual([
      "Skills 目录在上一次调用后改了",
      "规则文件在上一次调用后改了",
      "记忆文件在上一次调用后改了",
      "工具定义在上一次调用后改了",
      null,
    ]);
  });
});
