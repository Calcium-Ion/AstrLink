import { describe, expect, it } from "vitest";

import {
  parseAccessTokenMetadata,
  parseMetadataAttributes,
  parseMetadataValues,
  parsePluginActionResult,
  parsePluginClearResult,
  parsePluginDetail,
  parsePluginDryRun,
  parsePluginHostSettings,
  parsePluginJournal,
  parsePluginList,
  parsePluginMarket,
  parsePluginStatus,
  parsePluginTaskPage,
  parsePluginTestResult,
  parsePluginView,
  parsePluginStats,
  parseRequestPluginRecords,
  parseSessionMetadata,
  parseSessionPlugins,
} from "./plugin-model";
import {
  emptyTrajectoryFields,
  parseRequestRecord,
  type RequestRecord,
} from "./request-record-model";
import {
  inspectorChainRows,
  inspectorPart,
  trajectoryChipLabel,
} from "./request-trajectory-model";

const summary = {
  key: "powercontext",
  name: "PowerContext 记忆",
  version: "1.0.0",
  source: "builtin",
  file: "plugin.ts",
  enabled: true,
  state: "loaded",
  requested_capabilities: ["context.inject", "network"],
  granted_capabilities: ["network"],
  needs_network: true,
};

describe("plugin model", () => {
  it("reads a task plugin, its tasks and a marketplace", () => {
    const task = {
      icon: "Sora.Color",
      models: ["sora-2"],
      fetch_mode: "per_task",
      auth: "api_key",
      new_api: true,
      protocols: [{ name: "openai_responses", supports: ["stream", "sync"] }],
      routes: [{ method: "POST", path: "/sora/v1/videos", type: "submit" }],
    };
    const list = parsePluginList({
      directory: "/data/plugins",
      plugins: [
        {
          ...summary,
          key: "sora",
          kind: "task",
          task,
          providers: [
            {
              id: "task",
              name: "Sora",
              auth: { scheme: "bearer" },
              protocols: [
                { protocol: "openai.responses", stream: true, sync: true },
              ],
              models: ["sora-2"],
            },
          ],
          calls: 12,
          failures: 1,
          tasks: 4,
          running_tasks: 1,
        },
      ],
    });
    expect(list.plugins[0]).toMatchObject({
      kind: "task",
      task,
      calls: 12,
      tasks: 4,
      running_tasks: 1,
    });
    expect(() =>
      parsePluginList({
        directory: "/data/plugins",
        plugins: [{ ...summary, kind: "transform" }],
      }),
    ).toThrow();

    const page = parsePluginTaskPage({
      items: [
        {
          id: "video_1",
          plugin_key: "sora",
          service_id: "service_sora",
          surface: "openai_video",
          model: "sora-2",
          status: "SUCCESS",
          // Facts are text, numbers and switches; anything else is dropped.
          usage: { seconds: 8, size: "1280x720", hd: true, raw: { a: 1 } },
          artifacts: ["video"],
          created_at: "2026-10-25T08:00:00Z",
          updated_at: "2026-10-25T08:01:00Z",
        },
      ],
      next_cursor: "c1",
      running: 0,
    });
    expect(page.items[0].usage).toEqual({
      seconds: 8,
      size: "1280x720",
      hd: true,
    });
    expect(page.next_cursor).toBe("c1");

    const market = parsePluginMarket({
      index: "https://example.com/index.json",
      name: "Example",
      plugins: [
        {
          key: "sora",
          name: "Sora",
          latest: "1.1.0",
          versions: [
            {
              version: "1.1.0",
              base_url: "https://api.openai.com",
              link: "https://example.com/index.json#sora@1.1.0",
            },
          ],
          models: ["sora-2"],
          protocols: [{ name: "openai_video" }],
          routes: [],
          link: "https://example.com/index.json#sora@1.1.0",
          installed: "1.0.0",
        },
        {
          key: "relay",
          name: "Relay",
          latest: "1.0.0",
          versions: [
            { version: "1.0.0", link: "https://example.com/index.json#relay" },
          ],
          models: [],
          protocols: [],
          routes: [],
          link: "https://example.com/index.json#relay@1.0.0",
          unsupported: ["provider", "transform"],
        },
      ],
    });
    expect(market.plugins[1].unsupported).toEqual(["provider", "transform"]);
    expect(market.plugins[0]).toMatchObject({
      latest: "1.1.0",
      installed: "1.0.0",
      versions: [{ version: "1.1.0", base_url: "https://api.openai.com" }],
    });
    expect(() =>
      parsePluginMarket({ index: "x", name: "y", plugins: [{ key: "sora" }] }),
    ).toThrow();
  });

  it("reads the plugin list, failed plugins included", () => {
    const list = parsePluginList({
      directory: "/data/plugins",
      plugins: [
        summary,
        {
          ...summary,
          key: "team-rules",
          name: "team-rules",
          version: "",
          source: "directory",
          enabled: false,
          state: "failed",
          error: {
            code: "async",
            file: "plugin.ts",
            line: 12,
            column: 8,
            message: "async functions are not supported",
          },
          requested_capabilities: [],
          granted_capabilities: [],
          needs_network: false,
        },
      ],
    });
    expect(list.directory).toBe("/data/plugins");
    expect(list.plugins[1]?.error).toEqual({
      code: "async",
      file: "plugin.ts",
      line: 12,
      column: 8,
      message: "async functions are not supported",
    });
    expect(() =>
      parsePluginList({
        directory: "/d",
        plugins: [{ ...summary, source: "remote" }],
      }),
    ).toThrow("$.plugins[0].source");
  });

  it("reads a plugin's detail", () => {
    const detail = parsePluginDetail({
      ...summary,
      network_purpose: "访问 PowerContext 服务器",
      notice: "启用后会询问项目。",
      hooks: ["prepareTurn", "testConnection"],
      config: { serverUrl: "http://192.0.2.1:9090" },
      network_origins: [
        {
          origin: "http://192.0.2.1:9090",
          allow_plaintext: true,
          plaintext: true,
        },
      ],
      privacy_trusted: false,
      credentials: [
        {
          name: "token",
          label: "令牌",
          configured: true,
          hint: "3f2a",
          origin: "http://192.0.2.1:9090",
        },
      ],
      metadata: [
        {
          key: "project",
          label: "项目",
          pattern: "^[a-z]+$",
          marker: "memory-project",
        },
      ],
    });
    expect(detail.config).toEqual({ serverUrl: "http://192.0.2.1:9090" });
    expect(detail.credentials[0]).toEqual({
      name: "token",
      label: "令牌",
      configured: true,
      hint: "3f2a",
      origin: "http://192.0.2.1:9090",
    });
    expect(() =>
      parsePluginDetail({
        ...summary,
        hooks: [],
        config: [],
        network_origins: [],
        privacy_trusted: false,
        credentials: [],
        metadata: [],
      }),
    ).toThrow("$.config");
  });

  it("reads what the host keeps for a plugin and its host switches", () => {
    const detail = parsePluginDetail({
      ...summary,
      hooks: ["prepareSession", "onAction"],
      config: {},
      network_origins: [],
      privacy_trusted: false,
      credentials: [],
      metadata: [],
      excluded_clients: ["cursor"],
      workspace_binding: false,
      auto_refresh: true,
      lookup_enabled: false,
      tools: [
        { name: "search", description: "检索本项目的记忆。" },
        { name: "projects", description: "列出项目。", unbound: true },
      ],
      storage: [{ label: "项目编号", content: false, retention: "一直保留" }],
      host_storage: [
        { kind: "workspace_bindings", count: 2 },
        { kind: "resident_block" },
        // A kind a newer Core keeps is left out, not refused.
        { kind: "query_content" },
      ],
    });
    expect(detail.excluded_clients).toEqual(["cursor"]);
    expect(detail.workspace_binding).toBe(false);
    expect(detail.auto_refresh).toBe(true);
    expect(detail.lookup_enabled).toBe(false);
    expect(detail.tools).toEqual([
      { name: "search", description: "检索本项目的记忆。" },
      { name: "projects", description: "列出项目。", unbound: true },
    ]);
    expect(detail.storage).toEqual([
      { label: "项目编号", content: false, retention: "一直保留" },
    ]);
    expect(detail.host_storage).toEqual([
      { kind: "workspace_bindings", count: 2 },
      { kind: "resident_block" },
    ]);
    // An older Core sends none of them: the defaults of a new plugin.
    const older = parsePluginDetail({
      ...summary,
      hooks: [],
      config: {},
      network_origins: [],
      privacy_trusted: false,
      credentials: [],
      metadata: [],
    });
    expect(older.workspace_binding).toBe(true);
    expect(older.auto_refresh).toBe(false);
    expect(older.lookup_enabled).toBe(true);
    expect(older.tools).toEqual([]);
    expect(older.host_storage).toEqual([]);
  });

  it("reads the plugins' settings, their part in a session and a click", () => {
    expect(
      parsePluginHostSettings({
        paused: true,
        injection_budget_bytes: 16384,
        language: "ja",
      }),
    ).toEqual({
      paused: true,
      injection_budget_bytes: 16384,
      language: "ja",
      request_plugins: [],
    });
    expect(
      parsePluginHostSettings({
        paused: false,
        injection_budget_bytes: 16384,
        language: "zh-CN",
        request_plugins: [
          {
            id: "rpi_abcdefgh",
            name: "过滤",
            plugin: "request-filter",
            enabled: true,
            models: ["gpt-4o"],
            config: { keywords: "a" },
          },
        ],
      }).request_plugins,
    ).toEqual([
      {
        id: "rpi_abcdefgh",
        name: "过滤",
        plugin: "request-filter",
        enabled: true,
        models: ["gpt-4o"],
        config: { keywords: "a" },
      },
    ]);
    expect(() =>
      parsePluginHostSettings({
        paused: false,
        injection_budget_bytes: 16384,
        language: "fr",
      }),
    ).toThrow("$.language");
    const [part] = parseSessionPlugins({
      plugins: [
        {
          plugin: "powercontext",
          name: "PowerContext 记忆",
          paused: false,
          first_turn: 1,
          block: {
            text: "<project-context>…</project-context>",
            bytes: 38,
            trigger: "bound",
            language: "zh-CN",
            computed_at: "2026-10-21T10:00:00Z",
          },
          refresh_pending: true,
          lookup: '<lookup name="powercontext">…</lookup>',
        },
      ],
    });
    expect(part?.block?.trigger).toBe("bound");
    expect(part?.refresh_pending).toBe(true);
    expect(part?.lookup).toBe('<lookup name="powercontext">…</lookup>');
    expect(
      parsePluginActionResult({
        status: "ok",
        refresh: true,
        message: "下一个请求会重新装载记忆。",
      }),
    ).toEqual({
      status: "ok",
      refresh: true,
      message: "下一个请求会重新装载记忆。",
    });
    expect(parsePluginClearResult({ removed: 3 })).toBe(3);
    expect(
      parseMetadataValues({
        values: [
          {
            value: "astrlink",
            sessions: 3,
            last_used_at: "2026-10-21T00:00:00Z",
          },
        ],
      }),
    ).toEqual([
      { value: "astrlink", sessions: 3, last_used_at: "2026-10-21T00:00:00Z" },
    ]);
    const metadata = parseSessionMetadata({
      values: {},
      workspace: { name: "astrlink", values: { project: "astrlink" } },
      choices: { project: ["astrlink", "paseo"] },
    });
    expect(metadata.workspace).toEqual({
      name: "astrlink",
      values: { project: "astrlink" },
    });
    expect(metadata.choices).toEqual({ project: ["astrlink", "paseo"] });
    expect(parseSessionMetadata({ values: {} })).toEqual({ values: {} });
  });

  it("reads a plugin's account day by day", () => {
    const day = {
      date: "2026-10-21",
      load: {
        count: 2,
        by_trigger: { start: 1, compact: 1 },
        injected_bytes: 8192,
        requests: 5,
        wait_ms: { p50: 300, p95: 900 },
      },
      lookup: {
        ran: 3,
        refused: 1,
        counted: 3,
        with_hits: 2,
        sessions: 2,
        looking_up: 1,
      },
      binding: {
        workspace: 1,
        picked: 0,
        created: 1,
        declined: 0,
        unanswered: 0,
        changed: 0,
      },
      write: { ok: 4, failed: 0, reported: 1 },
      problems: { failed: 0, timeouts: 1, privacy: 0, over_budget: 0 },
      cache: {
        with: { requests: 4, input_tokens: 4000, cache_read_tokens: 3000 },
        without: { requests: 0, input_tokens: 0, cache_read_tokens: 0 },
      },
    };
    const stats = parsePluginStats({
      days: [day],
      total: { ...day, date: undefined },
    });
    expect(stats.days[0]).toEqual(day);
    expect(stats.total.date).toBeUndefined();
    expect(stats.total.lookup.ms).toBeUndefined();
    expect(() =>
      parsePluginStats({
        days: [],
        total: { ...day, write: { ok: -1, failed: 0, reported: 0 } },
      }),
    ).toThrow("$.total.write.ok");
  });

  it("reads a lookup by its size and the lookups a block offered", () => {
    const [lookup, ask, report] = parseRequestPluginRecords(
      [
        {
          plugin: "powercontext",
          plugin_version: "2.0.0",
          hook: "callTool",
          title: "记忆查询",
          status: "ok",
          summary: "检索 · 命中 2 条",
          elapsed_ms: 300,
          network: { requests: 2, denied: 0, errors: 0, wait_ms: 290 },
          lookup: {
            tool: "search",
            input_runes: 6,
            result_bytes: 512,
            hits: 2,
          },
        },
        {
          plugin: "powercontext",
          plugin_version: "2.0.0",
          hook: "prepareTurn",
          title: "询问项目",
          status: "ok",
          summary: "列出 20 个项目",
          elapsed_ms: 100,
          network: { requests: 1, denied: 0, errors: 0, wait_ms: 90 },
          commands: ["projects"],
        },
        {
          plugin: "powercontext",
          plugin_version: "2.1.0",
          hook: "callTool",
          title: "记忆上报",
          status: "ok",
          summary: "上报 15 字",
          elapsed_ms: 120,
          network: { requests: 1, denied: 0, errors: 0, wait_ms: 110 },
          lookup: {
            tool: "note",
            input_runes: 15,
            result_bytes: 60,
            writes: true,
          },
        },
      ],
      "$.plugin_records",
    );
    expect(lookup?.lookup).toEqual({
      tool: "search",
      input_runes: 6,
      result_bytes: 512,
      hits: 2,
    });
    expect(ask?.commands).toEqual(["projects"]);
    expect(report?.lookup).toEqual({
      tool: "note",
      input_runes: 15,
      result_bytes: 60,
      writes: true,
    });
    expect(() =>
      parseRequestPluginRecords(
        [
          {
            plugin: "powercontext",
            plugin_version: "2.0.0",
            hook: "callTool",
            title: "记忆查询",
            status: "ok",
            summary: "",
            elapsed_ms: 1,
            network: { requests: 0, denied: 0, errors: 0, wait_ms: 0 },
            lookup: { tool: "search", input_runes: -1, result_bytes: 0 },
          },
        ],
        "$.plugin_records",
      ),
    ).toThrow("$.plugin_records[0].lookup.input_runes");
  });

  it("reads why a block was computed and the buttons a record offers", () => {
    const [entry] = parseRequestPluginRecords(
      [
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
            { id: "refresh", label: "刷新记忆", confirm: "会重写缓存" },
          ],
        },
      ],
      "$.plugin_records",
    );
    expect(entry?.trigger).toBe("compact");
    expect(entry?.actions).toEqual([
      { id: "refresh", label: "刷新记忆", confirm: "会重写缓存" },
    ]);
  });

  it("reads the views Core cleaned and refuses anything else", () => {
    const view = parsePluginView(
      [
        {
          type: "kv",
          items: [
            { label: "项目", value: "astrlink" },
            { label: "Scope", value: "scp_1", mono: true },
          ],
        },
        {
          type: "table",
          columns: ["家族", "请求"],
          rows: [["atomic-memory", 4]],
        },
        { type: "badges", items: [{ text: "1.3.0", tone: "ok" }] },
        { type: "text", text: "两行\n文字" },
      ],
      "$.view",
    );
    expect(view).toHaveLength(4);
    expect(view[1]).toEqual({
      type: "table",
      columns: ["家族", "请求"],
      rows: [["atomic-memory", 4]],
    });
    expect(() =>
      parsePluginView([{ type: "html", html: "<b>x</b>" }], "$.view"),
    ).toThrow("$.view[0].type");
    expect(() =>
      parsePluginView(
        [{ type: "table", columns: ["a"], rows: [[{}]] }],
        "$.view",
      ),
    ).toThrow("$.view[0].rows[0][0]");
  });

  it("reads a development build, the journal and a dry run", () => {
    const list = parsePluginList({
      directory: "/data/plugins",
      plugins: [
        {
          ...summary,
          api_version: 2,
          development: {
            project: "/home/me/memory",
            built_at: "2026-10-23T12:00:00Z",
          },
        },
      ],
    });
    expect(list.plugins[0]?.api_version).toBe(2);
    expect(list.plugins[0]?.development).toEqual({
      project: "/home/me/memory",
      built_at: "2026-10-23T12:00:00Z",
    });
    const journal = parsePluginJournal({
      entries: [
        {
          seq: 3,
          time: "2026-10-23T12:00:01Z",
          hook: "observeTurn",
          level: "error",
          message: "threw",
          reason: "plugin_error",
          stack: "at observeTurn (src/plugin.ts:4:1)",
          dry_run: true,
        },
      ],
      timings: [
        {
          hook: "observeTurn",
          calls: 2,
          failures: 1,
          p50_ms: 1.5,
          p95_ms: 9,
          max_ms: 9,
          script_ms: 0.4,
        },
      ],
      latest: 3,
    });
    expect(journal.entries[0]?.stack).toContain("src/plugin.ts:4:1");
    expect(journal.timings[0]?.p95_ms).toBe(9);
    expect(journal.latest).toBe(3);
    expect(() =>
      parsePluginJournal({
        entries: [],
        timings: [
          {
            hook: "x",
            calls: 1,
            failures: 0,
            p50_ms: -1,
            p95_ms: 0,
            max_ms: 0,
            script_ms: 0,
          },
        ],
        latest: 0,
      }),
    ).toThrow();
    const run = parsePluginDryRun({
      error: { reason: "timeout", message: "ran out of time" },
      elapsed_ms: 10000,
      script_ms: 2,
      logs: [
        {
          time: "2026-10-23T12:00:01Z",
          hook: "describeStatus",
          level: "info",
          message: "hi",
          dry_run: true,
        },
      ],
    });
    expect(run.error).toEqual({
      reason: "timeout",
      message: "ran out of time",
    });
    expect(run.result).toBeUndefined();
    expect(run.logs[0]?.message).toBe("hi");
  });

  it("reads test results and status", () => {
    expect(
      parsePluginTestResult({
        status: "failed",
        summary: "the plugin ran JavaScript for longer than 200ms",
        reason: "timeout",
        elapsed_ms: 201,
        network: { requests: 0, denied: 0, errors: 0, wait_ms: 0 },
      }).reason,
    ).toBe("timeout");
    const status = parsePluginStatus({
      calls: 3,
      failures: { timeout: 1 },
      memory_limits: 0,
      timeouts: 1,
      network: { requests: 2, denied: 1, errors: 0, wait_ms: 40 },
      view: [{ type: "text", text: "ok" }],
    });
    expect(status.failures).toEqual({ timeout: 1 });
    expect(() =>
      parsePluginStatus({ ...status, failures: { timeout: -1 } }),
    ).toThrow("$.failures.timeout");
  });

  it("reads metadata attributes and token defaults", () => {
    const [attribute] = parseMetadataAttributes({
      attributes: [
        {
          key: "project",
          label: "项目",
          description: "",
          pattern: "^[a-z]+$",
          marker: "memory-project",
          owner: "powercontext",
          created_at: "2026-10-18T00:00:00Z",
          updated_at: "2026-10-18T00:00:00Z",
          header: "X-Astrlink-Meta-Project",
          owner_missing: true,
        },
      ],
    });
    expect(attribute?.owner_missing).toBe(true);
    expect(parseAccessTokenMetadata({ values: { env: "prod" } })).toEqual({
      env: "prod",
    });
    expect(() => parseAccessTokenMetadata({ values: { env: 1 } })).toThrow(
      "$.values.env",
    );
  });
});

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

describe("plugins in request records", () => {
  it("reads plugin events, metadata and plugin records", () => {
    const parsed = parseRequestRecord({
      ...record,
      turn_index: null,
      session_link: null,
      cursors: [],
      events: [
        {
          kind: "accepted",
          started_at: "2026-10-18T10:00:00Z",
          ended_at: null,
          status: "succeeded",
          summary: "claude-opus-4-5",
          attempt_index: 1,
        },
        {
          kind: "plugin",
          plugin: "powercontext",
          title: "记忆",
          started_at: "2026-10-18T10:00:00.1Z",
          ended_at: "2026-10-18T10:00:00.5Z",
          status: "succeeded",
          summary: "召回 3/9",
          attempt_index: 1,
        },
      ],
      metadata: {
        values: {
          project: {
            value: "astrlink",
            source: "session",
            set_by: "model_marker",
            turn: 2,
          },
        },
        events: [
          {
            key: "project",
            event: "ignored",
            value: "Bad",
            set_by: "header",
            reason: "invalid_value",
          },
        ],
      },
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
          view: [{ type: "kv", items: [{ label: "项目", value: "astrlink" }] }],
        },
      ],
    });
    expect(parsed.events[1]).toMatchObject({
      kind: "plugin",
      plugin: "powercontext",
      title: "记忆",
    });
    expect(parsed.metadata?.values.project).toEqual({
      value: "astrlink",
      source: "session",
      set_by: "model_marker",
      turn: 2,
    });
    expect(parsed.plugin_records?.[0]?.injection?.replayed_blocks).toBe(2);
    expect(() =>
      parseRequestRecord({
        ...record,
        events: [],
        metadata: { values: { project: { value: "x", source: "cookie" } } },
      }),
    ).toThrow("metadata.values.project.source");
  });

  it("puts plugin events in the gateway lane under the plugin's own name", () => {
    const rows = inspectorChainRows({
      ...record,
      events: [
        {
          kind: "accepted",
          started_at: "2026-10-18T10:00:00Z",
          ended_at: "2026-10-18T10:00:00Z",
          status: "succeeded",
          summary: "claude-opus-4-5",
          attempt_index: 1,
        },
        {
          kind: "plugin",
          plugin: "powercontext",
          title: "记忆",
          started_at: "2026-10-18T10:00:00.1Z",
          ended_at: "2026-10-18T10:00:00.5Z",
          status: "succeeded",
          summary: "召回 3/9",
          attempt_index: 1,
        },
      ],
    });
    const plugin = rows.find((row) => row.chip === "PLUGIN");
    expect(plugin).toMatchObject({
      lane: "gateway",
      plugin: "powercontext",
      title: "记忆",
      summary: "召回 3/9",
    });
    expect(trajectoryChipLabel("PLUGIN", plugin?.title)).toBe("记忆");
    expect(trajectoryChipLabel("PLUGIN")).toBe("插件");
    expect(trajectoryChipLabel("ROUTE", "记忆")).toBe("路由");
    expect(inspectorPart("PLUGIN")).toBe("plugin");
  });
});
