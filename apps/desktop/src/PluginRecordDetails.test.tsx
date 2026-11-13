// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({ runPluginAction: vi.fn() }));
const notify = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("./bridge", () => bridge);
vi.mock("./notify", () => ({ notify }));

import {
  PluginRecordDetails,
  RequestMetadataLine,
} from "./PluginRecordDetails";
import { installDialogAnimations } from "./lib/test-dialog-animations";
import { settleClosedLayer } from "./lib/test-layer-settle";
import {
  emptyTrajectoryFields,
  type RequestRecord,
} from "./request-record-model";

const record: RequestRecord = {
  id: "req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  parent_request_id: null,
  attempt_index: 1,
  child_count: 0,
  started_at: "2026-10-18T10:00:00Z",
  completed_at: "2026-10-18T10:00:02Z",
  status: "succeeded",
  input_protocol: "anthropic.messages",
  requested_model: "claude-opus-4-5",
  streaming: true,
  route_id: null,
  service_id: "service_01",
  local_access_token_id: null,
  http_status: 200,
  latency_ms: 2000,
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
};

/** Opens a record's line to show what waits behind it. */
async function open(row: Element | undefined) {
  const trigger = row?.querySelector<HTMLButtonElement>("button");
  if (!trigger) throw new Error("Missing the record's line");
  await act(async () => trigger.click());
}

describe("PluginRecordDetails", () => {
  let container: HTMLDivElement;
  let reactRoot: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.append(container);
    reactRoot = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => reactRoot.unmount());
    container.remove();
  });

  it("puts what each plugin did on its line and the rest behind it", async () => {
    await act(async () => {
      reactRoot.render(
        <PluginRecordDetails
          record={{
            ...record,
            plugin_records: [
              {
                plugin: "powercontext",
                plugin_version: "1.0.0",
                hook: "prepareTurn",
                title: "记忆",
                status: "ok",
                summary: "召回 3/9",
                elapsed_ms: 412,
                injection: {
                  placement: "turn_message_prefix",
                  injected_bytes: 1843,
                  replayed_blocks: 2,
                  dropped_items: 0,
                },
                network: { requests: 3, denied: 0, errors: 0, wait_ms: 402 },
                observe: "queued",
                view: [
                  { type: "kv", items: [{ label: "项目", value: "astrlink" }] },
                ],
                truncated: true,
              },
              {
                plugin: "team-rules",
                plugin_version: "",
                hook: "prepareSession",
                title: "团队规则",
                status: "skipped",
                summary: "",
                skipped_reason: "short_query",
                elapsed_ms: 1,
                network: { requests: 0, denied: 0, errors: 0, wait_ms: 0 },
              },
              {
                plugin: "team-rules",
                plugin_version: "1.0.0",
                hook: "observeTurn",
                title: "团队规则写入",
                status: "failed",
                summary: "写入失败：连不上",
                skipped_reason: "plugin_error",
                elapsed_ms: 30,
                network: { requests: 1, denied: 0, errors: 1, wait_ms: 30 },
              },
            ],
          }}
        />,
      );
    });
    const [memory, rules, failed] = document.querySelectorAll(
      '[data-testid="plugin-record"]',
    );
    // The line: what the plugin did, then what it added; a capture that
    // went as usual says nothing.
    expect(memory?.textContent).toContain("记忆412 ms召回 3/9");
    expect(memory?.textContent).toContain("本轮用户消息开头 +1.8 KB");
    expect(memory?.textContent).not.toContain("已排队");
    expect(
      memory?.querySelector('[data-testid="plugin-record-details"]'),
    ).toBeNull();
    // Behind it: the sizes, the network, the plugin's view and its hook.
    await open(memory);
    const details = memory?.querySelector(
      '[data-testid="plugin-record-details"]',
    );
    expect(details?.textContent).toContain(
      "本轮用户消息开头 · 1.8 KB · 沿用之前的 2 段",
    );
    expect(details?.textContent).toContain("3 个请求 · 等待 402 ms");
    expect(details?.textContent).not.toContain("拒绝");
    expect(details?.textContent).toContain("astrlink");
    expect(details?.textContent).toContain("超出上限，已截断");
    expect(details?.textContent).toContain("powercontext 1.0.0 · prepareTurn");
    // A record without a summary says why it was skipped.
    expect(rules?.textContent).toContain("已跳过");
    expect(rules?.textContent).toContain("问题太短");
    // A plugin that sent nothing shows no network line.
    await open(rules);
    expect(rules?.textContent).not.toContain("个请求");
    // A failure opens by itself.
    expect(failed?.textContent).toContain("失败");
    expect(failed?.textContent).toContain("插件出错");
    expect(
      failed?.querySelector('[data-testid="plugin-record-details"]')
        ?.textContent,
    ).toContain("1 个请求 · 等待 30 ms · 失败 1");
  });

  it("does not call a block skipped when the request still carries the lookup usage", async () => {
    await act(async () => {
      reactRoot.render(
        <PluginRecordDetails
          record={{
            ...record,
            plugin_records: [
              {
                plugin: "powercontext",
                plugin_version: "2.1.1",
                hook: "prepareSession",
                title: "记忆装载",
                status: "skipped",
                summary: "项目还没有画像和主题",
                trigger: "bound",
                elapsed_ms: 21,
                injection: {
                  placement: "system_append",
                  injected_bytes: 1229,
                  replayed_blocks: 0,
                  dropped_items: 0,
                  usage_bytes: 1229,
                },
                network: { requests: 5, denied: 0, errors: 0, wait_ms: 5 },
              },
            ],
          }}
        />,
      );
    });
    const [load] = document.querySelectorAll('[data-testid="plugin-record"]');
    expect(load?.textContent).not.toContain("已跳过");
    expect(load?.textContent).toContain("项目还没有画像和主题");
    expect(load?.textContent).toContain("只附上查询方法 · 系统提示词 +1.2 KB");
    await open(load);
    expect(
      load?.querySelector('[data-testid="plugin-record-details"]')?.textContent,
    ).toContain("系统提示词 · 1.2 KB · 其中查询方法 1.2 KB");
  });

  it("shows a turn waiting for its project and what the capture wrote", async () => {
    await act(async () => {
      reactRoot.render(
        <PluginRecordDetails
          record={{
            ...record,
            plugin_records: [
              {
                plugin: "powercontext",
                plugin_version: "1.0.0",
                hook: "prepareTurn",
                title: "记忆",
                status: "skipped",
                summary: "召回 0/9 · 1 个检索失败",
                skipped_reason: "search_failed",
                elapsed_ms: 2400,
                network: { requests: 5, denied: 0, errors: 1, wait_ms: 2400 },
                observe: "awaiting_reply",
              },
              {
                plugin: "powercontext",
                plugin_version: "1.0.0",
                hook: "observeTurn",
                title: "记忆写入",
                status: "ok",
                summary: "已写入 · 已整理成记忆",
                elapsed_ms: 16500,
                network: { requests: 2, denied: 0, errors: 0, wait_ms: 16480 },
              },
            ],
          }}
        />,
      );
    });
    const [turn, write] = document.querySelectorAll(
      '[data-testid="plugin-record"]',
    );
    expect(turn?.textContent).toContain("召回 0/9 · 1 个检索失败");
    expect(turn?.textContent).toContain(
      "有检索失败，没有命中 · 写入：等回复确定项目",
    );
    await act(async () => {
      reactRoot.render(
        <PluginRecordDetails
          record={{
            ...record,
            plugin_records: [
              {
                plugin: "powercontext",
                plugin_version: "2.1.0",
                hook: "prepareTurn",
                title: "询问项目",
                status: "ok",
                summary: "列出 2 个项目",
                elapsed_ms: 90,
                network: { requests: 1, denied: 0, errors: 0, wait_ms: 80 },
                observe: "held_unbound",
              },
            ],
          }}
        />,
      );
    });
    expect(
      document.querySelector('[data-testid="plugin-record"]')?.textContent,
    ).toContain("写入：先记下，会话选定项目后补写");
    expect(write?.textContent).toContain("记忆写入");
    expect(write?.textContent).toContain("已写入 · 已整理成记忆");
  });

  it("shows the lookups the model ran by their size, and the ones offered", async () => {
    await act(async () => {
      reactRoot.render(
        <PluginRecordDetails
          record={{
            ...record,
            plugin_records: [
              {
                plugin: "powercontext",
                plugin_version: "2.0.0",
                hook: "prepareTurn",
                title: "询问项目",
                status: "ok",
                summary: "列出 20 个项目（共 35 个）",
                elapsed_ms: 120,
                network: { requests: 2, denied: 0, errors: 0, wait_ms: 110 },
                commands: ["projects"],
              },
              {
                plugin: "powercontext",
                plugin_version: "2.0.0",
                hook: "callTool",
                title: "记忆查询",
                status: "ok",
                summary: "检索 · 命中 3 条",
                elapsed_ms: 820,
                network: { requests: 6, denied: 0, errors: 0, wait_ms: 800 },
                lookup: {
                  tool: "search",
                  input_runes: 12,
                  result_bytes: 2150,
                  hits: 3,
                },
              },
              {
                plugin: "powercontext",
                plugin_version: "2.0.0",
                hook: "callTool",
                title: "PowerContext 记忆",
                status: "skipped",
                summary: "",
                skipped_reason: "turn_limit",
                elapsed_ms: 0,
                network: { requests: 0, denied: 0, errors: 0, wait_ms: 0 },
                lookup: { tool: "read", input_runes: 4, result_bytes: 0 },
              },
              {
                plugin: "powercontext",
                plugin_version: "2.1.0",
                hook: "callTool",
                title: "记忆上报",
                status: "ok",
                summary: "上报 15 字",
                elapsed_ms: 140,
                network: { requests: 1, denied: 0, errors: 0, wait_ms: 130 },
                lookup: {
                  tool: "note",
                  input_runes: 15,
                  result_bytes: 72,
                  writes: true,
                },
              },
              {
                plugin: "powercontext",
                plugin_version: "2.1.0",
                hook: "callTool",
                title: "PowerContext 记忆",
                status: "skipped",
                summary: "",
                skipped_reason: "turn_limit",
                elapsed_ms: 0,
                network: { requests: 0, denied: 0, errors: 0, wait_ms: 0 },
                lookup: {
                  tool: "note",
                  input_runes: 3,
                  result_bytes: 0,
                  writes: true,
                },
              },
            ],
          }}
        />,
      );
    });
    const [ask, search, refused, report, tooMany] = document.querySelectorAll(
      '[data-testid="plugin-record"]',
    );
    await open(ask);
    expect(ask?.textContent).toContain("附上查询projects");
    expect(search?.textContent).toContain("检索 · 命中 3 条");
    await open(search);
    expect(
      search?.querySelector('[data-testid="plugin-record-lookup"]')
        ?.textContent,
    ).toBe("search · 参数 12 字 · 结果 2.1 KB · 3 条结果");
    expect(refused?.textContent).toContain("这一轮已经查满 3 次");
    await open(refused);
    expect(
      refused?.querySelector('[data-testid="plugin-record-lookup"]')
        ?.textContent,
    ).toBe("read · 参数 4 字");
    // A report says so, and its limits are its own.
    await open(report);
    expect(report?.textContent).toContain("上报note · 参数 15 字 · 结果 72 B");
    expect(tooMany?.textContent).toContain("这一轮已经上报满 2 次");
    expect(tooMany?.textContent).not.toContain("查满");
  });

  it("names the extension point and says what its call did", async () => {
    const base = {
      plugin: "gate",
      plugin_version: "1.0.0",
      title: "Gate",
      status: "ok" as const,
      summary: "",
      elapsed_ms: 3,
      network: { requests: 0, denied: 0, errors: 0, wait_ms: 0 },
    };
    await act(async () => {
      reactRoot.render(
        <PluginRecordDetails
          record={{
            ...record,
            plugin_records: [
              {
                ...base,
                hook: "onRequest",
                effects: ["tag"],
                tags: ["night", "cheap"],
              },
              {
                ...base,
                hook: "route",
                effects: ["order"],
                summary: "Beta is cheaper",
              },
              { ...base, hook: "beforeForward", effects: ["system", "params"] },
              {
                ...base,
                hook: "onStreamEvent",
                effects: ["stream"],
                events: 5,
                failures: 1,
                summary: "",
              },
              {
                ...base,
                hook: "onRequest",
                status: "skipped",
                skipped_reason: "circuit_open",
              },
            ],
          }}
        />,
      );
    });
    const rows = [
      ...document.querySelectorAll('[data-testid="plugin-record"]'),
    ];
    const point = (row: Element) =>
      row.querySelector('[data-testid="plugin-record-point"]')?.textContent;
    const headline = (row: Element) =>
      row.querySelector('[data-testid="plugin-record-headline"]')?.textContent;
    expect(rows.map(point)).toEqual([
      "入口",
      "路由",
      "转发前",
      "流式改写",
      "入口",
    ]);
    expect(headline(rows[0]!)).toBe("标签：night、cheap");
    expect(headline(rows[1]!)).toBe("Beta is cheaper");
    expect(rows[1]?.textContent).toContain("调整了提供商顺序");
    expect(headline(rows[2]!)).toBe("追加了系统提示词、改了参数");
    expect(headline(rows[3]!)).toBe("改写了流式事件");
    expect(rows[3]?.textContent).toContain(
      "处理了 5 条事件，1 条出错后原样放过",
    );
    expect(headline(rows[4]!)).toBe("失败太多，暂时停用");
  });

  it("says so when no plugin ran", async () => {
    await act(async () => {
      reactRoot.render(<PluginRecordDetails record={record} />);
    });
    expect(container.textContent).toBe("这个请求没有插件记录。");
  });

  it("says why a block was loaded and runs the plugin's buttons after asking", async () => {
    const removeAnimations = installDialogAnimations();
    bridge.runPluginAction.mockResolvedValue({
      status: "ok",
      refresh: true,
      message: "这个会话的下一个请求会重新装载记忆。",
    });
    await act(async () => {
      reactRoot.render(
        <PluginRecordDetails
          record={{
            ...record,
            session_id: "session_1",
            plugin_records: [
              {
                plugin: "powercontext",
                plugin_version: "2.0.0",
                hook: "prepareSession",
                title: "记忆装载",
                status: "ok",
                summary: "画像 · 主题 20 个 · 4.2 KB",
                elapsed_ms: 310,
                network: { requests: 2, denied: 0, errors: 0, wait_ms: 300 },
                trigger: "compact",
                actions: [
                  { id: "refresh", label: "刷新记忆", confirm: "会重写缓存。" },
                ],
              },
              {
                plugin: "team-rules",
                plugin_version: "1.0.0",
                hook: "prepareSession",
                title: "Team rules",
                status: "skipped",
                summary: "",
                elapsed_ms: 0,
                network: { requests: 0, denied: 0, errors: 0, wait_ms: 0 },
                skipped_reason: "over_budget",
              },
            ],
          }}
        />,
      );
    });
    expect(container.textContent).toContain("compact 后刷新");
    expect(container.textContent).toContain("超出注入总预算");
    // The plugin's buttons wait behind the record's line.
    await open(
      container.querySelector('[data-testid="plugin-record"]') ?? undefined,
    );
    const action = container.querySelector<HTMLButtonElement>(
      "[data-testid=plugin-record-action]",
    );
    expect(action?.textContent).toBe("刷新记忆");
    await act(async () => action?.click());
    // The plugin asked for a confirmation: nothing runs before it.
    expect(bridge.runPluginAction).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("会重写缓存。");
    const confirm = [
      ...document.querySelectorAll<HTMLButtonElement>(
        "[role=alertdialog] button",
      ),
    ].find((button) => button.textContent === "刷新记忆");
    await act(async () => confirm?.click());
    await settleClosedLayer();
    expect(bridge.runPluginAction).toHaveBeenCalledWith(
      "powercontext",
      "refresh",
      "session_1",
    );
    expect(notify.success).toHaveBeenCalledWith(
      "这个会话的下一个请求会重新装载记忆。",
    );
    removeAnimations();
  });

  it("puts a request's metadata on one line with where each value came from", async () => {
    await act(async () => {
      reactRoot.render(
        <RequestMetadataLine
          metadata={{
            values: {
              project: {
                value: "astrlink",
                source: "session",
                set_by: "model_marker",
                turn: 2,
              },
              env: { value: "prod", source: "token" },
            },
            events: [
              {
                key: "env",
                event: "ignored",
                value: "Prod",
                set_by: "header",
                reason: "invalid_value",
              },
              {
                key: "project",
                event: "bound",
                value: "astrlink",
                set_by: "model_marker",
                normalized: "AstrLink",
              },
              {
                key: "team",
                event: "bound",
                value: "infra",
                set_by: "user_marker",
                new: true,
              },
            ],
          }}
        />,
      );
    });
    const line = document.querySelector('[data-testid="request-metadata"]');
    // A session binding says how it was set.
    expect(line?.textContent).toContain(
      "project=astrlink (模型标记 · 第 2 轮)",
    );
    expect(line?.textContent).toContain(
      "project: 绑定为 astrlink (模型标记 · 原写法 AstrLink)",
    );
    expect(line?.textContent).toContain(
      "team: 绑定为 infra (用户标记 · 新建项目)",
    );
    expect(line?.textContent).toContain("env=prod (令牌默认值)");
    expect(line?.textContent).toContain("已忽略 env=Prod：值不符合校验规则");
    await act(async () => {
      reactRoot.render(
        <RequestMetadataLine metadata={{ values: {}, events: [] }} />,
      );
    });
    expect(
      document.querySelector('[data-testid="request-metadata"]'),
    ).toBeNull();
  });
});
