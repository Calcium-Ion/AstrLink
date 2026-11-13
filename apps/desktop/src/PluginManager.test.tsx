// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  listPlugins: vi.fn(),
  reloadPlugins: vi.fn(),
  getPlugin: vi.fn(),
  getPluginStatus: vi.fn(),
  getPluginStats: vi.fn(),
  getPluginCreditStars: vi.fn(),
  updatePlugin: vi.fn(),
  testPlugin: vi.fn(),
  savePluginCredential: vi.fn(),
  deletePluginCredential: vi.fn(),
  getPluginHostSettings: vi.fn(),
  updatePluginHostSettings: vi.fn(),
  clearPluginData: vi.fn(),
  getPluginJournal: vi.fn(),
  dryRunPlugin: vi.fn(),
  listenPluginApprovalFinished: vi.fn(),
  openPluginApproval: vi.fn(),
  pluginApprovalOf: vi.fn(),
  previewPluginInstall: vi.fn(),
  installPlugin: vi.fn(),
  checkPluginUpdate: vi.fn(),
  rollbackPlugin: vi.fn(),
  uninstallPlugin: vi.fn(),
  pluginInstallFailureOf: vi.fn(),
  getPluginMarket: vi.fn(),
  findPluginByCode: vi.fn(),
  getPluginMarketDoc: vi.fn(),
  listPluginTasks: vi.fn(),
  clearPluginTasks: vi.fn(),
  getUsageSummary: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import { PluginManager } from "./PluginManager";
import type {
  PluginDayStats,
  PluginDetail,
  PluginInstallReview,
  PluginList,
  PluginMarket,
  PluginStats,
  PluginTask,
} from "./plugin-model";
import type { Service } from "./service-model";
import {
  finishExitAnimations,
  installDialogAnimations,
} from "./lib/test-dialog-animations";
import { settleClosedLayer } from "./lib/test-layer-settle";
import { emptyUsageSummary, resolveUsageWindow } from "./usage-range";

const memory: PluginDetail = {
  limits: [],
  ui_enabled: true,
  key: "powercontext",
  name: "PowerContext 记忆",
  version: "1.0.0",
  source: "builtin",
  file: "plugin.ts",
  enabled: false,
  state: "loaded",
  requested_capabilities: [
    "context.inject",
    "request.read",
    "network",
    "storage",
    "ui.views",
  ],
  granted_capabilities: [],
  needs_network: true,
  network_purpose: "访问 PowerContext 服务器",
  notice: "启用后，新会话的前几轮会请模型询问项目。",
  hooks: [
    "prepareSession",
    "prepareTurn",
    "observeTurn",
    "describeStatus",
    "testConnection",
  ],
  config: {
    serverUrl: "http://192.0.2.1:9090",
    quotas: { "atomic-memory": 4 },
  },
  network_origins: [
    {
      origin: "http://192.0.2.1:9090",
      allow_plaintext: true,
      plaintext: true,
      setting: "serverUrl",
    },
  ],
  privacy_trusted: false,
  turn_wait_ms: 2500,
  credentials: [
    {
      name: "token",
      label: "PowerContext 令牌",
      configured: true,
      hint: "abcd",
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
  settings: [
    {
      key: "serverUrl",
      label: "服务器地址",
      type: "server",
      group: "连接",
      required: true,
      credential: "token",
    },
    {
      key: "quotas.atomic-memory",
      label: "原子记忆",
      type: "number",
      group: "召回",
      row: "每轮条数",
      default: 4,
      min: 0,
      max: 20,
      unit: "条",
    },
    {
      key: "quotas.topic-memory",
      label: "主题记忆",
      type: "number",
      group: "召回",
      row: "每轮条数",
      default: 3,
      min: 0,
      max: 20,
      unit: "条",
    },
    { key: "$wait", group: "召回" },
    {
      key: "askProject",
      label: "没有项目时询问",
      type: "switch",
      group: "会话",
      default: true,
      help: "会话还没有项目时附上的询问提示词",
    },
    { key: "$trusted", group: "隐私" },
    {
      key: "minQueryChars",
      label: "最短问题",
      type: "number",
      advanced: true,
      default: 8,
      min: 0,
      max: 200,
      unit: "字",
    },
    { key: "$origins", advanced: true },
  ],
  credits: [
    {
      name: "PowerContext",
      owner: "OceanBase",
      license: "Apache-2.0",
      url: "https://github.com/oceanbase/powercontext",
    },
  ],
  excluded_clients: [],
  workspace_binding: true,
  auto_refresh: false,
  lookup_enabled: true,
  tools: [],
  storage: [
    {
      label: "项目在服务器上的编号",
      content: false,
      retention: "一直保留，可在插件页清除",
    },
  ],
  host_storage: [
    { kind: "session_bindings" },
    { kind: "workspace_bindings", count: 2 },
    { kind: "plugin_sessions", count: 5 },
    { kind: "request_records" },
    { kind: "resident_block" },
    { kind: "plugin_values", count: 3 },
  ],
};

const status = {
  calls: 4,
  failures: { timeout: 1 },
  memory_limits: 0,
  timeouts: 1,
  network: { requests: 3, denied: 1, errors: 0, wait_ms: 20 },
  view: [
    {
      type: "kv" as const,
      items: [{ label: "最近写入", value: "astrlink：已写入 · 已整理成记忆" }],
    },
  ],
  last_test: {
    status: "ok" as const,
    summary: "PowerContext 1.3.0 · 190 ms",
    elapsed_ms: 190,
    network: { requests: 2, denied: 0, errors: 0, wait_ms: 180 },
    view: [
      {
        type: "kv" as const,
        items: [{ label: "可检索", value: "原子记忆 / 主题记忆" }],
      },
    ],
    tested_at: "2026-10-19T08:00:00Z",
  },
  bindings: [
    {
      key: "project",
      label: "项目",
      values: [
        {
          value: "astrlink",
          sessions: 2,
          last_used_at: "2026-10-19T07:58:00Z",
        },
        { value: "none", sessions: 1 },
      ],
    },
  ],
};

function day(date: string, change: Partial<PluginDayStats> = {}) {
  const empty: PluginDayStats = {
    date,
    load: { count: 0, by_trigger: {}, injected_bytes: 0, requests: 0 },
    lookup: {
      ran: 0,
      refused: 0,
      counted: 0,
      with_hits: 0,
      sessions: 0,
      looking_up: 0,
    },
    binding: {
      workspace: 0,
      picked: 0,
      created: 0,
      declined: 0,
      unanswered: 0,
      changed: 0,
    },
    write: { ok: 0, failed: 0, reported: 0 },
    problems: { failed: 0, timeouts: 0, privacy: 0, over_budget: 0 },
    cache: {
      with: { requests: 0, input_tokens: 0, cache_read_tokens: 0 },
      without: { requests: 0, input_tokens: 0, cache_read_tokens: 0 },
    },
  };
  return { ...empty, ...change };
}

const busyDay = day("2026-10-21", {
  load: {
    count: 4,
    by_trigger: { start: 2, bound: 1, compact: 1 },
    injected_bytes: 61440,
    requests: 12,
    wait_ms: { p50: 310, p95: 1200 },
  },
  binding: {
    workspace: 2,
    picked: 1,
    created: 1,
    declined: 0,
    unanswered: 1,
    changed: 1,
  },
  write: { ok: 9, failed: 1, reported: 2 },
  problems: { failed: 1, timeouts: 2, privacy: 0, over_budget: 0 },
  cache: {
    with: { requests: 10, input_tokens: 100000, cache_read_tokens: 82000 },
    without: { requests: 5, input_tokens: 50000, cache_read_tokens: 30000 },
  },
});

const stats: PluginStats = {
  days: [
    ...[15, 16, 17, 18, 19, 20].map((date) => day(`2026-10-${date}`)),
    busyDay,
  ],
  total: { ...busyDay, date: undefined },
};

const list: PluginList = {
  directory: "/data/plugins",
  plugins: [
    memory,
    {
      key: "team-rules",
      name: "team-rules",
      version: "",
      source: "directory",
      file: "plugin.ts",
      enabled: false,
      state: "failed",
      error: {
        code: "async",
        file: "plugin.ts",
        line: 12,
        column: 3,
        message: "async functions are not supported",
      },
      requested_capabilities: [],
      granted_capabilities: [],
      needs_network: false,
    },
  ],
};

const helloReview = {
  token: "token-1",
  expires_at: "2026-10-23T13:00:00Z",
  source: {
    kind: "github",
    repository: "acme/hello",
    url: "https://github.com/acme/hello",
    ref: "v1.1.0",
    ref_kind: "tag" as const,
    commit: "0123456789abcdef0123456789abcdef01234567",
    committed_at: "2026-10-01T12:00:00Z",
  },
  key: "hello",
  name: "Hello",
  version: "1.1.0",
  api_version: 2,
  capabilities: ["storage"],
  hooks: ["prepareTurn"],
  plugin_bytes: 2048,
  lockfile: "package-lock.json",
  dependencies: [
    { name: "tiny-lib", version: "2.0.0", bytes: 4096, scripts: true },
  ],
  dependency_bytes: 4096,
  warnings: [{ code: "install_scripts_skipped", package: "tiny-lib@2.0.0" }],
};

/** What a plugin's detail has beyond its summary, all empty. */
const emptyDetail = {
  hooks: [],
  config: {},
  network_origins: [],
  privacy_trusted: false,
  turn_wait_ms: 2500,
  credentials: [],
  metadata: [],
  settings: [],
  credits: [],
  excluded_clients: [],
  workspace_binding: true,
  auto_refresh: false,
  lookup_enabled: true,
  tools: [],
  storage: [],
  host_storage: [],
  limits: [],
  ui_enabled: true,
};

const soraTask: PluginTask = {
  icon: "Sora.Color",
  description: "OpenAI Sora 视频生成",
  author: "QuantumNous",
  models: ["sora-2", "sora-2-pro"],
  fetch_mode: "per_task",
  auth: "api_key",
  base_url: "https://api.openai.com",
  new_api: true,
  protocols: [
    { name: "openai_responses", supports: ["stream", "sync", "background"] },
    { name: "openai_video" },
  ],
  routes: [],
};

const soraProvider = {
  id: "task",
  name: "Sora",
  base_url: "https://api.openai.com",
  auth: { scheme: "bearer" as const },
  protocols: [{ protocol: "openai.videos", stream: false, sync: true }],
  models: ["sora-2", "sora-2-pro"],
};

/** A task plugin of new-api's marketplace, enabled, with a task running. */
const sora: PluginDetail = {
  ...emptyDetail,
  key: "sora",
  name: "Sora",
  version: "1.1.0",
  source: "directory",
  file: "plugin.js",
  enabled: true,
  state: "loaded",
  kind: "task",
  task: soraTask,
  providers: [soraProvider],
  tasks: 3,
  running_tasks: 1,
  requested_capabilities: [],
  granted_capabilities: [],
  needs_network: false,
};

const soraService = {
  id: "service_sora",
  name: "Sora 主账号",
  kind: "plugin",
  enabled: true,
  models: ["sora-2"],
  capabilities: [],
  http: { base_url: "https://api.openai.com", auth: { scheme: "bearer" } },
  plugin: { key: "sora", provider: "task" },
  created_at: "2026-10-25T08:00:00Z",
  updated_at: "2026-10-25T08:00:00Z",
} as unknown as Service;

const market: PluginMarket = {
  index:
    "https://raw.githubusercontent.com/QuantumNous/new-api-plugins/main/index.json",
  name: "New API Official Plugins",
  plugins: [
    {
      key: "alibaba",
      name: "Alibaba Bailian",
      description: "阿里云百炼图片与视频生成",
      icon: "AlibabaCloud.Color",
      latest: "1.4.2",
      versions: [
        {
          version: "1.4.2",
          base_url: "https://dashscope.aliyuncs.com",
          auth: "api_key",
          link: "https://raw.githubusercontent.com/QuantumNous/new-api-plugins/main/index.json#alibaba@1.4.2",
        },
      ],
      models: ["wan2.5-t2v-preview", "qwen-image"],
      protocols: [{ name: "openai_video" }, { name: "openai_image" }],
      routes: [],
      link: "https://raw.githubusercontent.com/QuantumNous/new-api-plugins/main/index.json#alibaba@1.4.2",
    },
    {
      key: "sora",
      name: "Sora",
      description: "OpenAI Sora 视频生成",
      latest: "1.1.0",
      versions: [
        {
          version: "1.1.0",
          link: "https://raw.githubusercontent.com/QuantumNous/new-api-plugins/main/index.json#sora@1.1.0",
        },
      ],
      models: ["sora-2", "sora-2-pro"],
      protocols: [{ name: "openai_video" }],
      routes: [],
      link: "https://raw.githubusercontent.com/QuantumNous/new-api-plugins/main/index.json#sora@1.1.0",
      installed: "1.1.0",
    },
  ],
};

const alibabaReview = {
  token: "token-market",
  expires_at: "2026-10-25T13:00:00Z",
  source: {
    kind: "market",
    repository: "New API Official Plugins",
    url: "https://raw.githubusercontent.com/QuantumNous/new-api-plugins/main/plugins/tasks/alibaba/1.4.2/plugin.js",
    ref: "1.4.2",
    ref_kind: "version",
    commit: "4f2c",
    index: market.index,
    key: "alibaba",
  },
  key: "alibaba",
  name: "Alibaba Bailian",
  version: "1.4.2",
  description: "阿里云百炼图片与视频生成",
  author: "QuantumNous",
  api_version: 1,
  capabilities: [],
  hooks: [],
  plugin_bytes: 30720,
  dependencies: [],
  dependency_bytes: 0,
  kind: "task",
  task: {
    models: ["wan2.5-t2v-preview", "qwen-image"],
    fetch_mode: "per_task",
    auth: "api_key",
    base_url: "https://dashscope.aliyuncs.com",
    protocols: [{ name: "openai_video" }, { name: "openai_image" }],
    routes: [],
  },
} as unknown as PluginInstallReview;

function button(label: string, root: ParentNode = document): HTMLButtonElement {
  const match = [...root.querySelectorAll("button")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!(match instanceof HTMLButtonElement))
    throw new Error(`Missing button: ${label}`);
  return match;
}

async function click(element: HTMLElement) {
  await act(async () => {
    element.click();
  });
}

async function openTab(label: string) {
  const tab = [
    ...document.querySelectorAll<HTMLButtonElement>('[role="tab"]'),
  ].find((candidate) => candidate.textContent?.trim() === label);
  if (!tab) throw new Error(`Missing tab: ${label}`);
  await act(async () => {
    tab.dispatchEvent(
      new MouseEvent("mousedown", { bubbles: true, button: 0 }),
    );
  });
}

async function chooseOption(selector: string, option: string) {
  const trigger = document.querySelector<HTMLButtonElement>(selector);
  if (trigger === null) throw new Error(`Missing select trigger: ${selector}`);
  await act(async () => {
    trigger.dispatchEvent(
      new PointerEvent("pointerdown", {
        bubbles: true,
        button: 0,
        pointerType: "mouse",
      }),
    );
    await Promise.resolve();
  });
  const item = [
    ...document.querySelectorAll<HTMLElement>('[role="option"]'),
  ].find((candidate) => candidate.textContent?.trim() === option);
  if (!item) throw new Error(`Missing select option: ${option}`);
  await act(async () => {
    item.click();
    await Promise.resolve();
  });
}

/** The control a setting row's label names. */
function control<T extends HTMLElement>(label: string): T {
  const name = [...document.querySelectorAll("label")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  const target = name?.htmlFor ? document.getElementById(name.htmlFor) : null;
  if (!target) throw new Error(`Missing the control of ${label}`);
  return target as T;
}

async function type(
  element: HTMLInputElement | HTMLTextAreaElement,
  value: string,
) {
  const prototype =
    element instanceof HTMLTextAreaElement
      ? HTMLTextAreaElement.prototype
      : HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(prototype, "value")?.set;
  await act(async () => {
    setter?.call(element, value);
    element.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

let approvalFinished: ((finished: unknown) => void) | null = null;

describe("PluginManager", () => {
  let container: HTMLDivElement;
  let reactRoot: Root;
  let removeAnimations: () => void;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    removeAnimations = installDialogAnimations();
    bridgeMocks.listPlugins.mockResolvedValue(list);
    bridgeMocks.getUsageSummary.mockRejectedValue(new Error("no usage"));
    bridgeMocks.getPluginCreditStars.mockResolvedValue({
      "https://github.com/oceanbase/powercontext": 1367,
    });
    bridgeMocks.getPlugin.mockImplementation(async (key: string) =>
      key === memory.key
        ? memory
        : {
            ...list.plugins[1],
            hooks: [],
            config: {},
            network_origins: [],
            privacy_trusted: false,
            turn_wait_ms: 2500,
            credentials: [],
            metadata: [],
            settings: [],
            credits: [],
            excluded_clients: [],
            workspace_binding: true,
            auto_refresh: false,
            lookup_enabled: true,
            tools: [],
            storage: [],
            host_storage: [],
            limits: [],
            ui_enabled: true,
          },
    );
    bridgeMocks.getPluginStatus.mockResolvedValue(status);
    bridgeMocks.getPluginStats.mockResolvedValue(stats);
    bridgeMocks.listenPluginApprovalFinished.mockImplementation(
      async (handler: (finished: unknown) => void) => {
        approvalFinished = handler;
        return () => {
          approvalFinished = null;
        };
      },
    );
    bridgeMocks.pluginApprovalOf.mockReturnValue(null);
    bridgeMocks.pluginInstallFailureOf.mockImplementation(
      (error: { failure?: unknown }) => error?.failure ?? null,
    );
    bridgeMocks.openPluginApproval.mockResolvedValue(true);
    bridgeMocks.getPluginHostSettings.mockResolvedValue({
      paused: false,
      injection_budget_bytes: 16384,
      language: "zh-CN",
    });
    container = document.createElement("div");
    document.body.append(container);
    reactRoot = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => reactRoot.unmount());
    removeAnimations();
    container.remove();
  });

  async function render() {
    await act(async () => {
      reactRoot.render(<PluginManager coreSessionKey="session-1" isReady />);
    });
  }

  /** Renders the list and opens a plugin's page from its row. */
  async function renderOpen(key = "powercontext") {
    await render();
    const row = document.querySelector<HTMLElement>(
      `[data-testid="plugin-row"][data-plugin-key="${key}"]`,
    );
    if (!row) throw new Error(`Missing the row of ${key}`);
    await click(row);
  }

  /** Opens the dialog of the settings of every plugin. */
  async function openHostSettings() {
    await click(button("插件设置"));
    const dialog = document.querySelector(
      '[data-testid="plugin-host-dialog"]',
    ) as HTMLElement | null;
    if (!dialog) throw new Error("Missing the plugin settings dialog");
    return dialog;
  }

  it("goes back to the list when the sidebar opens the page again", async () => {
    const first = { kind: "plugins" };
    await act(async () => {
      reactRoot.render(
        <PluginManager coreSessionKey="session-1" isReady visit={first} />,
      );
    });
    const row = document.querySelector<HTMLElement>(
      '[data-testid="plugin-row"][data-plugin-key="powercontext"]',
    );
    await click(row!);
    expect(document.querySelector('[data-testid="plugin-detail"]')).not.toBe(
      null,
    );
    // A render of the same visit keeps the plugin open.
    await act(async () => {
      reactRoot.render(
        <PluginManager coreSessionKey="session-1" isReady visit={first} />,
      );
    });
    expect(document.querySelector('[data-testid="plugin-detail"]')).not.toBe(
      null,
    );
    await act(async () => {
      reactRoot.render(
        <PluginManager
          coreSessionKey="session-1"
          isReady
          visit={{ kind: "plugins" }}
        />,
      );
    });
    expect(document.querySelector('[data-testid="plugin-detail"]')).toBe(null);
    expect(
      document.querySelectorAll('[data-testid="plugin-row"]'),
    ).toHaveLength(2);
  });

  it("says the gateway is not running instead of loading forever", async () => {
    await act(async () => {
      reactRoot.render(
        <PluginManager coreSessionKey="session-1" isReady={false} />,
      );
    });
    expect(bridgeMocks.listPlugins).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("网关没有运行");
    expect(document.body.textContent).not.toContain("正在加载插件");
    // Once the gateway is ready the list loads; when it stops again the list
    // stays, marked as the last one read.
    await act(async () => {
      reactRoot.render(<PluginManager coreSessionKey="session-1" isReady />);
    });
    expect(
      document.querySelectorAll('[data-testid="plugin-row"]'),
    ).toHaveLength(2);
    await act(async () => {
      reactRoot.render(
        <PluginManager coreSessionKey="session-1" isReady={false} />,
      );
    });
    expect(
      document.querySelectorAll('[data-testid="plugin-row"]'),
    ).toHaveLength(2);
    expect(document.body.textContent).toContain(
      "网关尚未就绪，当前显示上次读取的插件。",
    );
  });

  it("lists plugins and shows why one failed to load", async () => {
    await render();
    const rows = document.querySelectorAll('[data-testid="plugin-row"]');
    expect(rows).toHaveLength(2);
    expect(rows[0]?.textContent).toContain("PowerContext 记忆");
    expect(rows[0]?.textContent).toContain("内置");
    expect(rows[1]?.textContent).toContain(
      "plugin.ts 第 12 行 · async 需要 apiVersion 2",
    );
    // The row says where the plugin comes from, never its key.
    expect(rows[0]?.textContent).toContain("内置");
    expect(document.body.textContent).not.toContain("powercontext · 1.0.0");
    await click(rows[1] as HTMLElement);
    expect(document.body.textContent).toContain(
      "async functions are not supported",
    );
    expect(document.querySelector('[role="tab"]')).toBeNull();
  });

  it("shows the connection, the bound projects, its own status and its credits", async () => {
    await renderOpen();
    const connection = document.querySelector(
      '[data-testid="plugin-connection"]',
    );
    // The plugin joined to AstrLink, switched off, with its server under it
    // and the last test on the link.
    const link = connection?.querySelector('[data-testid="plugin-link"]');
    expect(link?.getAttribute("data-state")).toBe("off");
    expect(link?.textContent).toContain("已停用");
    expect(link?.textContent).toContain("PowerContext 1.3.0 · 190 ms");
    expect(link?.textContent).toContain("192.0.2.1:9090明文 HTTP");
    expect(connection?.textContent).toContain("原子记忆 / 主题记忆");
    const bindings = document.querySelector('[data-testid="plugin-bindings"]');
    expect(bindings?.textContent).toContain("已绑定的项目");
    expect(bindings?.textContent).toContain("astrlink");
    expect(bindings?.textContent).toContain("不纳入");
    expect(
      document.querySelector('[data-testid="plugin-own-status"]')?.textContent,
    ).toContain("已整理成记忆");
    // The last 7 days as a tile for each part the plugin has, with its days
    // as small columns: no lookups here, so no tile for them.
    const effects = document.querySelector('[data-testid="plugin-effects"]');
    const tiles = [
      ...(effects?.querySelectorAll('[data-testid="plugin-effect-tile"]') ??
        []),
    ].map((tile) => tile.textContent);
    expect(tiles).toEqual([
      "装载消耗约 2.05万 token装载 4 次 · 等待约 310 ms · 刷新 1 次",
      "写入9 条Agent 上报 2 条 · 失败 1 条",
      "问题3失败 1 · 超时 2",
    ]);
    const sparklines = effects?.querySelectorAll('[data-slot="sparkline"]');
    expect(sparklines).toHaveLength(3);
    const columns = sparklines?.[0]?.querySelectorAll("[data-value]") ?? [];
    expect(columns).toHaveLength(7);
    // Today, on the right, carries the load.
    expect(columns[6]?.getAttribute("data-value")).toBe("20480");
    expect(columns[0]?.getAttribute("data-value")).toBe("0");
    // How sessions got their project, only the ways that happened.
    const ways = document.querySelector(
      '[data-testid="plugin-effect-bindings"]',
    );
    expect(ways?.textContent).toBe(
      "绑定方式按工作区自动2选了已有的1新建1没回答1自动绑定后改掉 1 次",
    );
    expect(
      document.querySelector('[data-testid="plugin-effect-cache"]')
        ?.textContent,
    ).toBe("缓存命中开了记忆82%其他会话60%");
    expect(effects?.textContent).not.toContain("查询");
    // The projects it is built on: a line, with GitHub's stars and mark.
    const credits = document.querySelector('[data-testid="plugin-credits"]');
    expect(credits?.textContent).toContain("使用了开源项目 PowerContext");
    expect(
      credits?.querySelector('[data-slot="github-repo-badge"]')?.textContent,
    ).toContain("1,367");
    expect(credits?.textContent).not.toContain("官方");
    const repository = credits?.querySelector("a");
    expect(repository?.getAttribute("href")).toBe(
      "https://github.com/oceanbase/powercontext",
    );
    expect(repository?.getAttribute("aria-label")).toBe(
      "在 GitHub 上查看 PowerContext，1,367 颗星",
    );
    expect(repository?.getAttribute("title")).toBe(
      "oceanbase/powercontext · Apache-2.0",
    );
    expect(bridgeMocks.getPluginCreditStars).toHaveBeenCalledWith(
      "powercontext",
    );
    // What the plugin does stays behind the ? by its name.
    expect(document.body.textContent).not.toContain(
      "启用后，新会话的前几轮会请模型询问项目。",
    );
    await click(
      document.querySelector(
        'button[aria-label="关于这个插件"]',
      ) as HTMLElement,
    );
    const about = document.querySelector('[data-testid="plugin-about"]');
    expect(about?.textContent).toContain(
      "启用后，新会话的前几轮会请模型询问项目。",
    );
    expect(about?.textContent).toContain("界面视图");
  });

  it("says when the last 7 days have no records, and shows lookups where there are", async () => {
    bridgeMocks.getPluginStats.mockResolvedValueOnce({
      days: [day("2026-10-21")],
      total: day(""),
    });
    await renderOpen();
    expect(
      document.querySelector('[data-testid="plugin-effects"]')?.textContent,
    ).toContain("最近 7 天还没有记录。");
    bridgeMocks.getPlugin.mockResolvedValue({
      ...memory,
      tools: [{ name: "search", description: "检索本项目的记忆。" }],
    });
    bridgeMocks.getPluginStats.mockResolvedValueOnce({
      ...stats,
      total: {
        ...stats.total,
        lookup: {
          ran: 8,
          refused: 1,
          counted: 8,
          with_hits: 6,
          ms: { p50: 640, p95: 2100 },
          sessions: 5,
          looking_up: 2,
        },
      },
    });
    await click(
      document.querySelector('button[aria-label="刷新"]') as HTMLElement,
    );
    expect(
      document.querySelector('[data-testid="plugin-effects"]')?.textContent,
    ).toContain("查询8 次命中 75% · 2 个会话用过 · 拒绝 1 次");
  });

  it("lists the account day by day on the status page, today first", async () => {
    await renderOpen();
    await openTab("运行状态");
    const daily = document.querySelector(
      '[data-testid="plugin-daily-effects"]',
    );
    const rows = daily?.querySelectorAll("tbody tr") ?? [];
    expect(rows).toHaveLength(7);
    expect(rows[0]?.textContent).toContain("2026-10-21");
    expect(rows[0]?.textContent).toContain("60.0 KB");
    expect(rows[0]?.textContent).toContain("9（失败 1）");
    expect(rows[0]?.textContent).toContain("82% / 60%");
    expect(rows[6]?.textContent).toContain("2026-10-15");
    const head = daily?.querySelector("thead")?.textContent;
    expect(head).toContain("消耗");
    expect(head).not.toContain("查询");
    // Days without activity stay, greyed.
    expect(rows[0]?.getAttribute("data-active")).toBe("true");
    expect(rows[1]?.getAttribute("data-active")).toBe("false");
    expect(rows[1]?.className).toContain("text-muted-foreground");
  });

  it("shows hook timings, the log and a dry run on the develop tab", async () => {
    bridgeMocks.getPlugin.mockResolvedValue({
      ...memory,
      development: {
        project: "/home/me/powercontext",
        built_at: "2026-10-23T12:00:00Z",
      },
    });
    bridgeMocks.getPluginJournal.mockResolvedValue({
      entries: [
        {
          seq: 1,
          time: "2026-10-23T12:00:01Z",
          hook: "prepareTurn",
          level: "info",
          message: "recalled 3 memories",
        },
        {
          seq: 2,
          time: "2026-10-23T12:00:02Z",
          hook: "observeTurn",
          level: "error",
          message: "threw TypeError: x is undefined",
          reason: "plugin_error",
          stack:
            "TypeError: x is undefined\n    at observeTurn (src/plugin.ts:40:3)",
        },
      ],
      timings: [
        {
          hook: "prepareTurn",
          calls: 12,
          failures: 1,
          p50_ms: 42.5,
          p95_ms: 310,
          max_ms: 1250,
          script_ms: 3.2,
        },
      ],
      latest: 2,
      breakers: [{ hook: "onRequest", until: "2026-10-23T12:05:00Z" }],
    });
    bridgeMocks.dryRunPlugin.mockResolvedValue({
      result: [{ type: "text", text: "ok" }],
      elapsed_ms: 12.3,
      script_ms: 1.5,
      logs: [
        {
          seq: 0,
          time: "2026-10-23T12:00:03Z",
          hook: "describeStatus",
          level: "info",
          message: "status read",
          dry_run: true,
        },
      ],
    });
    await renderOpen();
    // A development build says so instead of where it came from.
    expect(document.body.textContent).toContain("开发版 · 1.0.0");
    await openTab("开发");
    expect(bridgeMocks.getPluginJournal).toHaveBeenCalledWith(
      "powercontext",
      0,
    );
    const develop = document.querySelector('[data-testid="plugin-develop"]');
    expect(
      develop?.querySelector('[data-testid="plugin-development"]')?.textContent,
    ).toContain("powercontext");
    expect(
      develop?.querySelector('[data-testid="plugin-breaker"]')?.textContent,
    ).toContain("「入口」最近失败太多");
    const timings = develop?.querySelector('[data-testid="plugin-timings"]');
    expect(timings?.textContent).toContain("prepareTurn");
    expect(timings?.textContent).toContain("43 ms");
    expect(timings?.textContent).toContain("310 ms");
    expect(timings?.textContent).toContain("1.25 s");
    const log = develop?.querySelector('[data-testid="plugin-log"]');
    const lines = log?.querySelectorAll("li") ?? [];
    // Newest first, failures with their stack.
    expect(lines[0]?.textContent).toContain("错误");
    expect(lines[0]?.textContent).toContain("src/plugin.ts:40:3");
    expect(lines[1]?.textContent).toContain("recalled 3 memories");

    const run = develop?.querySelector('[data-testid="plugin-dry-run"]');
    const args = run?.querySelector("textarea") as HTMLTextAreaElement;
    expect(JSON.parse(args.value)).toEqual([{ language: "zh-CN" }]);
    await click(button("运行", run as ParentNode));
    expect(bridgeMocks.dryRunPlugin).toHaveBeenCalledWith(
      "powercontext",
      "describeStatus",
      [{ language: "zh-CN" }],
    );
    const outcome = run?.querySelector(
      '[data-testid="plugin-dry-run-outcome"]',
    );
    expect(outcome?.textContent).toContain('"text": "ok"');
    expect(outcome?.textContent).toContain("status read");
  });

  it("sets the time limits of the plugin's extension points", async () => {
    bridgeMocks.getPlugin.mockResolvedValue({
      ...memory,
      hooks: [...memory.hooks, "onRequest", "onStreamEvent"],
      limits: [
        {
          hook: "onRequest",
          default_ms: 1000,
          max_ms: 5000,
          plugin_ms: 2000,
          effective_ms: 2000,
        },
        {
          hook: "onStreamEvent",
          default_ms: 50,
          max_ms: 1000,
          ms: 200,
          effective_ms: 200,
        },
      ],
      fail_closed_requested: true,
    });
    bridgeMocks.updatePlugin.mockResolvedValue(memory);
    await renderOpen();
    await openTab("设置");
    const limits = document.querySelector('[data-testid="plugin-limits"]');
    const inputs = [...(limits?.querySelectorAll("input") ?? [])];
    expect(limits?.textContent).toContain("入口");
    expect(limits?.textContent).toContain("流式改写");
    expect(inputs.map((input) => [input.placeholder, input.value])).toEqual([
      ["2000", ""],
      ["50", "200"],
    ]);
    await type(inputs[0] as HTMLInputElement, "3000");
    await type(inputs[1] as HTMLInputElement, "");
    const failClosed = [
      ...document.querySelectorAll<HTMLButtonElement>('[role="switch"]'),
    ].find(
      (item) =>
        item
          .closest("[data-setting-row]")
          ?.textContent?.includes("出错时拦下请求") ??
        item.parentElement?.parentElement?.textContent?.includes(
          "出错时拦下请求",
        ),
    );
    if (!failClosed) throw new Error("Missing the fail-closed switch");
    await click(failClosed);
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      hook_limits_ms: { onRequest: 3000, onStreamEvent: 0 },
      fail_closed: true,
    });
  });

  it("orders the plugins and lists them in that order", async () => {
    bridgeMocks.getPluginHostSettings.mockResolvedValue({
      paused: false,
      injection_budget_bytes: 16384,
      language: "zh-CN",
      order: ["team-rules"],
    });
    bridgeMocks.updatePluginHostSettings.mockImplementation(async (update) => ({
      paused: false,
      injection_budget_bytes: 16384,
      language: "zh-CN",
      ...update,
    }));
    await render();
    const rows = () =>
      [...document.querySelectorAll('[data-testid="plugin-row"]')].map(
        (row) => row.getAttribute("data-key") ?? row.textContent,
      );
    expect(rows()[0]).toContain("team-rules");
    await openHostSettings();
    await click(button("调整顺序"));
    const ordered = [
      ...document.querySelectorAll('[data-testid="plugin-order-row"]'),
    ];
    expect(ordered.map((row) => row.textContent)).toEqual([
      expect.stringContaining("team-rules"),
      expect.stringContaining("PowerContext 记忆"),
    ]);
    await click(
      button("保存", ordered[0]?.closest('[role="dialog"]') as HTMLElement),
    );
    expect(bridgeMocks.updatePluginHostSettings).toHaveBeenCalledWith({
      order: ["team-rules", "powercontext"],
    });
  });

  it("enables a plugin at once with what it declares", async () => {
    bridgeMocks.updatePlugin.mockResolvedValueOnce({
      ...memory,
      enabled: true,
      granted_capabilities: memory.requested_capabilities,
    });
    await renderOpen();
    await openTab("设置");
    await click(button("启用"));
    // A plugin is trusted code: its install review listed what it
    // declares, and enabling it asks nothing more.
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      enabled: true,
    });
    expect(
      document.querySelector('[data-testid="plugin-awaiting-approval"]'),
    ).toBeNull();
    expect(document.body.textContent).not.toContain("无法保存修改");
    const link = document.querySelector('[data-testid="plugin-link"]');
    expect(link?.getAttribute("data-state")).not.toBe("off");
  });

  it("installs a plugin from a GitHub link once the confirmation window agrees", async () => {
    bridgeMocks.previewPluginInstall.mockResolvedValue(helloReview);
    bridgeMocks.installPlugin.mockRejectedValueOnce(
      new Error('{"error":{"code":"approval_pending","message":"waits"}}'),
    );
    await render();
    await click(button("安装插件"));
    const dialog = document.querySelector(
      '[data-testid="plugin-install-dialog"]',
    ) as HTMLElement;
    expect(dialog).not.toBeNull();
    await type(
      document.getElementById("plugin-install-url") as HTMLInputElement,
      "https://github.com/acme/hello",
    );
    await click(button("检查", dialog));
    expect(bridgeMocks.previewPluginInstall).toHaveBeenCalledWith(
      "https://github.com/acme/hello",
    );
    expect(dialog.textContent).toContain("标签 v1.1.0");
    expect(dialog.textContent).toContain("依赖 1 个");
    expect(dialog.textContent).toContain("没有运行：tiny-lib@2.0.0");

    await click(button("安装", dialog));
    expect(bridgeMocks.installPlugin).toHaveBeenCalledWith("token-1", false);
    expect(
      document.querySelector('[data-testid="plugin-install-waiting"]'),
    ).not.toBeNull();

    // The confirmation window installed it: the page shows it.
    const hello = {
      ...memory,
      key: "hello",
      name: "Hello",
      source: "directory" as const,
      installed: helloReview.source,
    };
    bridgeMocks.listPlugins.mockResolvedValue({
      ...list,
      plugins: [...list.plugins, hello],
    });
    bridgeMocks.getPlugin.mockResolvedValue(hello);
    await act(async () => {
      approvalFinished?.({ id: "approval-1", key: "hello", approved: true });
    });
    await act(async () => {});
    expect(
      document.querySelector('[data-testid="plugin-install-dialog"]'),
    ).toBeNull();
    expect(bridgeMocks.getPlugin).toHaveBeenCalledWith("hello");
    expect(
      document.querySelector('[data-testid="plugin-source"]')?.textContent,
    ).toContain("acme/hello");
  });

  it("says why a plugin cannot be installed, with the build's messages", async () => {
    bridgeMocks.previewPluginInstall.mockRejectedValue({
      failure: {
        reason: "build_failed",
        messages: ['src/plugin.ts:3:7: Expected ";"'],
      },
    });
    await render();
    await click(button("安装插件"));
    const dialog = document.querySelector(
      '[data-testid="plugin-install-dialog"]',
    ) as HTMLElement;
    await type(
      document.getElementById("plugin-install-url") as HTMLInputElement,
      "acme/hello",
    );
    await click(button("检查", dialog));
    expect(dialog.textContent).toContain("构建失败。");
    expect(dialog.textContent).toContain("src/plugin.ts:3:7");
    expect(button("安装", dialog).disabled).toBe(true);
  });

  it("checks a plugin installed from source for updates, and uninstalls it after asking", async () => {
    const hello = {
      ...memory,
      key: "hello",
      name: "Hello",
      source: "override" as const,
      installed: helloReview.source,
      versions: [
        {
          id: "20261020T080000Z-0000abcd",
          version: "1.0.0",
          replaced_at: "2026-10-20T08:00:00Z",
        },
      ],
    };
    bridgeMocks.listPlugins.mockResolvedValue({ ...list, plugins: [hello] });
    bridgeMocks.getPlugin.mockResolvedValue(hello);
    bridgeMocks.checkPluginUpdate.mockResolvedValue({
      available: true,
      ref: "v1.2.0",
      ref_kind: "tag",
      version: "1.2.0",
      committed_at: "2026-10-22T08:00:00Z",
      notes: "stores things",
      url: "https://github.com/acme/hello/tree/v1.2.0",
    });
    bridgeMocks.previewPluginInstall.mockResolvedValue(helloReview);
    bridgeMocks.uninstallPlugin.mockResolvedValue(list);
    await renderOpen("hello");
    const source = document.querySelector(
      '[data-testid="plugin-source"]',
    ) as HTMLElement;
    expect(source.textContent).toContain("标签 v1.1.0");
    expect(source.textContent).not.toContain(helloReview.source.commit);
    await click(button("检查更新", source));
    expect(bridgeMocks.checkPluginUpdate).toHaveBeenCalledWith("hello");
    expect(source.textContent).toContain("有新版本：1.2.0");
    await click(button("查看并更新", source));
    // The update is reviewed as an install is.
    expect(bridgeMocks.previewPluginInstall).toHaveBeenCalledWith(
      "https://github.com/acme/hello/tree/v1.2.0",
    );
    await click(button("取消"));
    await settleClosedLayer();

    await act(async () => {
      document
        .querySelector<HTMLButtonElement>('[aria-label="更多操作"]')
        ?.dispatchEvent(
          new PointerEvent("pointerdown", {
            bubbles: true,
            button: 0,
            pointerType: "mouse",
          }),
        );
    });
    const uninstall = [
      ...document.querySelectorAll<HTMLElement>('[role="menuitem"]'),
    ].find((item) => item.textContent?.trim() === "卸载");
    if (!uninstall) throw new Error("Missing the uninstall item");
    await click(uninstall);
    const confirm = document.querySelector(
      '[role="alertdialog"]',
    ) as HTMLElement;
    expect(confirm.textContent).toContain("内置的同名插件会恢复");
    expect(bridgeMocks.uninstallPlugin).not.toHaveBeenCalled();
    await click(button("卸载", confirm));
    expect(bridgeMocks.uninstallPlugin).toHaveBeenCalledWith("hello");
  });

  it("offers a row of switches as one choice of several", async () => {
    bridgeMocks.getPlugin.mockResolvedValue({
      ...memory,
      settings: [
        ...memory.settings,
        ...["atomic", "topics", "skills"].map((key, index) => ({
          key: `scopes.${key}`,
          label: ["原子记忆", "主题", "技能"][index],
          type: "switch" as const,
          group: "查询",
          row: "查询范围",
          default: true,
        })),
      ],
    });
    bridgeMocks.updatePlugin.mockResolvedValue(memory);
    await renderOpen();
    await openTab("设置");
    const scopes = document.querySelector<HTMLButtonElement>(
      'button[aria-label="查询范围"]',
    );
    if (!scopes) throw new Error("Missing the scope choice");
    expect(scopes.textContent).toBe("全部");
    // No switch of its own stays on the row.
    expect(
      scopes
        .closest('[data-slot="setting-row"]')
        ?.querySelector('[role="switch"]'),
    ).toBeNull();
    await act(async () => {
      scopes.dispatchEvent(
        new PointerEvent("pointerdown", { bubbles: true, button: 0 }),
      );
      scopes.click();
    });
    // Three options need no search.
    expect(
      document.querySelector('input[aria-label="查询范围 search"]'),
    ).toBeNull();
    const skills = document.querySelector<HTMLButtonElement>(
      'button[role="checkbox"][aria-label="技能"]',
    );
    if (!skills) throw new Error("Missing the skills option");
    await click(skills);
    expect(scopes.textContent).toBe("原子记忆、主题");
    await act(async () => {
      document.dispatchEvent(
        new KeyboardEvent("keydown", { bubbles: true, key: "Escape" }),
      );
    });
    await settleClosedLayer();
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith(
      "powercontext",
      expect.objectContaining({
        config: expect.objectContaining({ scopes: { skills: false } }),
      }),
    );
  });

  it("keeps the settings of every plugin in one dialog, their help behind ?", async () => {
    await render();
    const dialog = await openHostSettings();
    const controls = dialog.querySelector(
      '[data-testid="plugin-host-controls"]',
    );
    expect(controls?.textContent).toContain("暂停所有插件");
    expect(controls?.textContent).toContain("注入总预算");
    // No explanation under the controls: each is behind its ?.
    expect(controls?.textContent).not.toContain("暂停期间所有插件都不运行");
    const help = controls?.querySelector<HTMLButtonElement>(
      'button[aria-label="关于「暂停所有插件」"]',
    );
    if (!help) throw new Error("Missing the help of the pause");
    await click(help);
    expect(document.body.textContent).toContain("暂停期间所有插件都不运行");
  });

  it("saves the settings form as one configuration", async () => {
    bridgeMocks.updatePlugin.mockResolvedValue(memory);
    await renderOpen();
    await openTab("设置");
    const form = document.querySelector('[data-testid="plugin-settings"]');
    expect(form?.textContent).toContain("每轮条数");
    expect(control<HTMLInputElement>("主题记忆").value).toBe("3");
    expect(button("保存").disabled).toBe(true);
    await type(control<HTMLInputElement>("原子记忆"), "25");
    await click(button("保存"));
    expect(document.body.textContent).toContain(
      "「每轮条数 · 原子记忆」要在 0 到 20 之间",
    );
    expect(bridgeMocks.updatePlugin).not.toHaveBeenCalled();
    await type(control<HTMLInputElement>("原子记忆"), "6");
    await click(control<HTMLButtonElement>("没有项目时询问"));
    expect(document.body.textContent).toContain("有改动未保存");
    await click(button("保存"));
    // Untouched defaults stay out; the consented server keeps its consent.
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      config: {
        serverUrl: "http://192.0.2.1:9090",
        quotas: { "atomic-memory": 6 },
        askProject: false,
      },
      plaintext_consent: ["http://192.0.2.1:9090"],
    });
  });

  it("asks again for plain HTTP when the server moves", async () => {
    bridgeMocks.updatePlugin.mockResolvedValue(memory);
    await renderOpen();
    await openTab("设置");
    await type(
      control<HTMLInputElement>("服务器地址"),
      "http://192.0.2.9:9090",
    );
    await click(button("保存"));
    expect(document.body.textContent).toContain(
      "「服务器地址」是明文 HTTP 地址，勾选「允许明文 HTTP」才能保存",
    );
    expect(bridgeMocks.updatePlugin).not.toHaveBeenCalled();
    const consent = [
      ...document.querySelectorAll<HTMLButtonElement>('[role="checkbox"]'),
    ].find((box) => box.parentElement?.textContent?.includes("允许明文 HTTP"));
    if (!consent) throw new Error("Missing the plain HTTP consent");
    await click(consent);
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      config: {
        serverUrl: "http://192.0.2.9:9090",
        quotas: { "atomic-memory": 4 },
      },
      plaintext_consent: ["http://192.0.2.9:9090"],
    });
  });

  it("takes an origin allowed before the form as the server's", async () => {
    // A server allowed on the old network tab has no setting mark.
    bridgeMocks.getPlugin.mockResolvedValue({
      ...memory,
      network_origins: [
        {
          origin: "http://192.0.2.1:9090",
          allow_plaintext: true,
          plaintext: true,
        },
      ],
    });
    bridgeMocks.updatePlugin.mockResolvedValue(memory);
    await renderOpen();
    await openTab("设置");
    await click(button("更多设置"));
    expect(
      document.querySelectorAll('[data-testid="plugin-origin"]'),
    ).toHaveLength(0);
    const consent = [
      ...document.querySelectorAll<HTMLButtonElement>('[role="checkbox"]'),
    ].find((box) => box.parentElement?.textContent?.includes("允许明文 HTTP"));
    expect(consent?.getAttribute("aria-checked")).toBe("true");
    await type(control<HTMLInputElement>("原子记忆"), "5");
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      config: {
        serverUrl: "http://192.0.2.1:9090",
        quotas: { "atomic-memory": 5 },
      },
      plaintext_consent: ["http://192.0.2.1:9090"],
    });
  });

  it("sends a new token to its server", async () => {
    bridgeMocks.savePluginCredential.mockResolvedValue(memory);
    await renderOpen();
    await openTab("设置");
    expect(document.body.textContent).toContain("已配置 ···· abcd");
    await click(button("更换"));
    await type(control<HTMLInputElement>("PowerContext 令牌"), "pc-new-token");
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).not.toHaveBeenCalled();
    expect(bridgeMocks.savePluginCredential).toHaveBeenCalledWith(
      "powercontext",
      "token",
      { secret: "pc-new-token", origin: "http://192.0.2.1:9090" },
    );
  });

  it("edits the same configuration as JSON", async () => {
    bridgeMocks.updatePlugin.mockResolvedValue(memory);
    await renderOpen();
    await openTab("设置");
    await click(button("更多设置"));
    expect(control<HTMLInputElement>("最短问题").value).toBe("8");
    await click(button("以 JSON 编辑"));
    const editor = document.querySelector<HTMLTextAreaElement>(
      '[role="dialog"] textarea',
    );
    if (!editor) throw new Error("Missing the JSON editor");
    expect(JSON.parse(editor.value)).toEqual(memory.config);
    await type(editor, "[1, 2]");
    await click(button("应用"));
    expect(document.body.textContent).toContain("配置必须是一个 JSON 对象。");
    await type(
      editor,
      '{"serverUrl":"http://192.0.2.1:9090","quotas":{"atomic-memory":7},"extra":true}',
    );
    await click(button("应用"));
    await finishExitAnimations();
    expect(control<HTMLInputElement>("原子记忆").value).toBe("7");
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      config: {
        serverUrl: "http://192.0.2.1:9090",
        quotas: { "atomic-memory": 7 },
        extra: true,
      },
      plaintext_consent: ["http://192.0.2.1:9090"],
    });
  });

  it("pauses every plugin and sets their injection budget from the header", async () => {
    bridgeMocks.updatePluginHostSettings.mockImplementation(
      async (update: Record<string, unknown>) => ({
        paused: false,
        injection_budget_bytes: 16384,
        language: "zh-CN",
        ...update,
      }),
    );
    await render();
    await openHostSettings();
    const controls = document.querySelector(
      '[data-testid="plugin-host-controls"]',
    );
    expect(controls?.textContent).toContain("16 KB");
    expect(document.body.textContent).not.toContain("所有插件已暂停");
    await click(
      document.querySelector(
        'button[aria-label="暂停所有插件"]',
      ) as HTMLElement,
    );
    expect(bridgeMocks.updatePluginHostSettings).toHaveBeenCalledWith({
      paused: true,
    });
    expect(document.body.textContent).toContain(
      "所有插件已暂停，请求原样发出。",
    );
    await chooseOption("#plugin-host-budget", "32 KB");
    await settleClosedLayer();
    expect(bridgeMocks.updatePluginHostSettings).toHaveBeenLastCalledWith({
      injection_budget_bytes: 32768,
    });
  });

  it("voids the lookup credentials it lists only after asking", async () => {
    bridgeMocks.getPlugin.mockResolvedValue({
      ...memory,
      host_storage: [
        ...memory.host_storage,
        { kind: "lookup_credentials", count: 2 },
      ],
    });
    bridgeMocks.clearPluginData.mockResolvedValue(2);
    await renderOpen();
    const item = [
      ...document.querySelectorAll('[data-testid="plugin-stored-item"]'),
    ].find((entry) => entry.textContent?.includes("查询凭证")) as HTMLElement;
    // Grouped where they are kept.
    expect(
      item.closest('[data-testid="plugin-stored-place"]')?.textContent,
    ).toContain("只在内存里");
    expect(item.textContent).toContain("2 条");
    await click(button("作废", item));
    expect(bridgeMocks.clearPluginData).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("作废查询凭证？");
    expect(document.body.textContent).toContain("提示词缓存要重写一次");
    await click(
      button(
        "作废",
        document.querySelector('[role="alertdialog"]') as HTMLElement,
      ),
    );
    await finishExitAnimations();
    expect(bridgeMocks.clearPluginData).toHaveBeenCalledWith(
      "powercontext",
      "lookups",
    );
  });

  it("lists the plugin's lookups behind the switch that turns them off", async () => {
    bridgeMocks.getPlugin.mockResolvedValue({
      ...memory,
      tools: [
        { name: "search", description: "按一句话检索本项目的记忆。" },
        {
          name: "projects",
          description: "按关键词列出项目。",
          unbound: true,
        },
        {
          name: "note",
          description: "把值得记住的项目信息记进记忆。",
          writes: true,
        },
      ],
      settings: [...memory.settings, { key: "$lookup", group: "查询" }],
    });
    bridgeMocks.updatePlugin.mockResolvedValue(memory);
    await renderOpen();
    await openTab("设置");
    const lookup = control<HTMLButtonElement>("允许 Agent 查询");
    expect(lookup.getAttribute("aria-checked")).toBe("true");
    const help = document.querySelector<HTMLButtonElement>(
      'button[aria-label="关于允许 Agent 查询"]',
    );
    if (!help) throw new Error("Missing the lookup help");
    await click(help);
    expect(document.body.textContent).toContain(
      "search：按一句话检索本项目的记忆。",
    );
    expect(document.body.textContent).toContain(
      "note（上报）：把值得记住的项目信息记进记忆。",
    );
    await act(async () => {
      document.dispatchEvent(
        new KeyboardEvent("keydown", { bubbles: true, key: "Escape" }),
      );
    });
    await settleClosedLayer();
    await click(lookup);
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      lookup_enabled: false,
    });
  });

  it("offers no lookup switch for a plugin without lookups", async () => {
    await renderOpen();
    await openTab("设置");
    expect(document.body.textContent).not.toContain("允许 Agent 查询");
  });

  it("lists what is kept for the plugin and clears it after asking", async () => {
    bridgeMocks.clearPluginData.mockResolvedValue(2);
    await renderOpen();
    const stored = document.querySelector('[data-testid="plugin-stored"]');
    expect(stored?.textContent).toContain("工作区和值的对应");
    expect(stored?.textContent).toContain("2 条");
    expect(stored?.textContent).toContain("当前装载块");
    // A column for each place, each thing with how much and its button;
    // only what holds memory text says so.
    const places = [
      ...document.querySelectorAll('[data-testid="plugin-stored-place"]'),
    ].map((place) => place.querySelector("h4")?.textContent);
    expect(places).toEqual(["AstrLink 数据库", "只在内存里", "请求记录"]);
    const block = [
      ...document.querySelectorAll('[data-testid="plugin-stored-item"]'),
    ].find((item) => item.textContent?.includes("当前装载块"));
    expect(block?.textContent).toBe("当前装载块含记忆内容");
    // What each one is, how long it stays and what the plugin declares
    // all wait behind the card's one ?.
    expect(stored?.querySelectorAll("button[aria-label^='关于']")).toHaveLength(
      1,
    );
    expect(stored?.textContent).not.toContain("网关重启就没了");
    await click(
      stored?.querySelector('button[aria-label="关于数据存储"]') as HTMLElement,
    );
    const help = document.querySelector('[data-testid="plugin-stored-help"]');
    expect(help?.textContent).toContain(
      "当前装载块会话系统提示词里正在用的那份项目记忆。保留：网关重启就没了。",
    );
    expect(help?.textContent).toContain(
      "项目在服务器上的编号（一直保留，可在插件页清除）",
    );
    await act(async () => {
      document.dispatchEvent(
        new KeyboardEvent("keydown", { bubbles: true, key: "Escape" }),
      );
    });
    await settleClosedLayer();
    const items = [
      ...document.querySelectorAll('[data-testid="plugin-stored-item"]'),
    ];
    const workspaces = items.find((item) =>
      item.textContent?.includes("工作区和值的对应"),
    ) as HTMLElement;
    await click(button("清除", workspaces));
    expect(bridgeMocks.clearPluginData).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("清除工作区记录？");
    await click(
      button(
        "清除",
        document.querySelector('[role="alertdialog"]') as HTMLElement,
      ),
    );
    await finishExitAnimations();
    expect(bridgeMocks.clearPluginData).toHaveBeenCalledWith(
      "powercontext",
      "workspaces",
    );
    // The counts come back from Core afresh.
    expect(bridgeMocks.getPlugin).toHaveBeenCalledTimes(2);
  });

  it("asks before automatic refresh and saves the host's switches", async () => {
    bridgeMocks.updatePlugin.mockResolvedValue(memory);
    await renderOpen();
    await openTab("设置");
    expect(
      control<HTMLButtonElement>("按工作区自动绑定").getAttribute(
        "aria-checked",
      ),
    ).toBe("true");
    await click(control<HTMLButtonElement>("自动刷新"));
    expect(document.body.textContent).toContain("打开自动刷新？");
    await click(
      button(
        "打开",
        document.querySelector('[role="alertdialog"]') as HTMLElement,
      ),
    );
    await finishExitAnimations();
    expect(
      control<HTMLButtonElement>("自动刷新").getAttribute("aria-checked"),
    ).toBe("true");
    await click(control<HTMLButtonElement>("按工作区自动绑定"));
    const clients = document.querySelector<HTMLButtonElement>(
      'button[aria-label="对哪些客户端生效"]',
    );
    expect(clients?.textContent).toContain("全部客户端");
    await act(async () => {
      clients?.dispatchEvent(
        new PointerEvent("pointerdown", { bubbles: true, button: 0 }),
      );
      clients?.click();
    });
    const cursor = document.querySelector<HTMLButtonElement>(
      'button[role="checkbox"][aria-label="Cursor"]',
    );
    if (!cursor) throw new Error("Missing the Cursor option");
    await click(cursor);
    await act(async () => {
      document.dispatchEvent(
        new KeyboardEvent("keydown", { bubbles: true, key: "Escape" }),
      );
    });
    await settleClosedLayer();
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      excluded_clients: ["cursor"],
      workspace_binding: false,
      auto_refresh: true,
    });
  });

  it("lets the user choose how long a request waits for the plugin", async () => {
    bridgeMocks.updatePlugin.mockResolvedValue({
      ...memory,
      turn_wait_ms: 3500,
    });
    await renderOpen();
    await openTab("设置");
    const wait = control<HTMLButtonElement>("最长等待");
    expect(wait.textContent).toContain("2.5 秒");
    await chooseOption(`#${CSS.escape(wait.id)}`, "3.5 秒");
    await settleClosedLayer();
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      turn_wait_ms: 3500,
    });
  });

  it("offers no wait for a plugin that adds nothing to requests", async () => {
    bridgeMocks.getPlugin.mockResolvedValue({
      ...memory,
      hooks: ["observeTurn", "testConnection"],
      settings: [],
    });
    await renderOpen();
    await openTab("设置");
    expect(document.body.textContent).not.toContain("最长等待");
    // Without settings of its own the gateway's still show.
    expect(document.body.textContent).toContain("受信任");
  });

  it("shows the connection test it ran", async () => {
    bridgeMocks.getPlugin.mockResolvedValue({ ...memory, enabled: true });
    bridgeMocks.getPluginStatus.mockResolvedValueOnce({
      ...status,
      last_test: undefined,
    });
    bridgeMocks.testPlugin.mockResolvedValue(status.last_test);
    await renderOpen();
    const link = () => document.querySelector('[data-testid="plugin-link"]');
    expect(link()?.getAttribute("data-state")).toBe("idle");
    expect(link()?.textContent).toContain("还没测试");
    await click(button("测试连接"));
    expect(bridgeMocks.testPlugin).toHaveBeenCalledWith("powercontext");
    // The overview reads the test back with what it changed in the counts.
    expect(bridgeMocks.getPluginStatus).toHaveBeenCalledTimes(2);
    expect(link()?.getAttribute("data-state")).toBe("connected");
    expect(link()?.textContent).toContain("连接正常");
  });

  it("keeps the plugin's own status with the connection, once each", async () => {
    bridgeMocks.getPluginStatus.mockResolvedValueOnce({
      ...status,
      view: [
        {
          type: "kv" as const,
          items: [
            { label: "可检索", value: "原子记忆" },
            { label: "最近写入", value: "astrlink：已写入" },
          ],
        },
      ],
    });
    await renderOpen();
    const connection = document.querySelector(
      '[data-testid="plugin-connection"]',
    );
    // A row the status repeats shows once, as the status says it now.
    expect(connection?.textContent?.match(/可检索/g)).toHaveLength(1);
    expect(connection?.textContent).toContain(
      "可检索原子记忆最近写入astrlink：已写入",
    );
  });

  it("says the plugin's status could not be read, and why behind a ?", async () => {
    bridgeMocks.getPluginStatus.mockResolvedValueOnce({
      ...status,
      view: undefined,
      view_error: "threw TypeError: boom",
    });
    await renderOpen();
    expect(
      document.querySelector('[data-testid="plugin-status-error"]')
        ?.textContent,
    ).toBe("没读出来");
    expect(document.body.textContent).not.toContain("threw TypeError");
    await click(
      document.querySelector(
        'button[aria-label="插件返回的错误"]',
      ) as HTMLElement,
    );
    expect(document.body.textContent).toContain("threw TypeError: boom");
  });

  it("lists what a rescan finds and opens a plugin from its row", async () => {
    bridgeMocks.listPlugins.mockResolvedValue({
      directory: "/data/plugins",
      plugins: [],
    });
    bridgeMocks.reloadPlugins.mockResolvedValue(list);
    await render();
    expect(document.body.textContent).toContain("还没有插件");
    await click(
      document.querySelector('button[aria-label="重新扫描"]') as HTMLElement,
    );
    expect(
      document.querySelectorAll('[data-testid="plugin-row"]'),
    ).toHaveLength(2);
    expect(bridgeMocks.getPlugin).not.toHaveBeenCalled();
    await click(
      document.querySelector(
        '[data-testid="plugin-row"][data-plugin-key="powercontext"]',
      ) as HTMLElement,
    );
    expect(bridgeMocks.getPlugin).toHaveBeenCalledWith("powercontext");
    expect(
      document.querySelector('[data-testid="plugin-overview"]'),
    ).not.toBeNull();
    // Back to the list.
    await click(button("插件"));
    expect(
      document.querySelectorAll('[data-testid="plugin-row"]'),
    ).toHaveLength(2);
  });

  it("says the overview needs the views grant", async () => {
    bridgeMocks.getPluginStatus.mockResolvedValue({
      calls: 0,
      failures: {},
      memory_limits: 0,
      timeouts: 0,
      network: { requests: 0, denied: 0, errors: 0, wait_ms: 0 },
    });
    await renderOpen();
    expect(
      document.querySelector('[data-testid="plugin-overview"]')?.textContent,
    ).toContain("启用时授予「界面视图」，这里才会显示插件提供的概览。");
  });

  describe("the star map", () => {
    afterEach(() => {
      localStorage.removeItem("astrlink.plugins.view");
    });

    async function renderMap(
      plugins: PluginList["plugins"],
      services: Service[] = [],
    ) {
      bridgeMocks.listPlugins.mockResolvedValue({ ...list, plugins });
      const fallback = bridgeMocks.getPlugin.getMockImplementation();
      bridgeMocks.getPlugin.mockImplementation(async (key: string) =>
        key === "sora" ? sora : fallback?.(key),
      );
      bridgeMocks.getPluginMarket.mockResolvedValue(market);
      await act(async () => {
        reactRoot.render(
          <PluginManager
            coreSessionKey="session-1"
            isReady
            services={services}
          />,
        );
      });
    }

    const stars = () => [
      ...document.querySelectorAll<HTMLElement>('[data-testid="plugin-star"]'),
    ];

    it("draws enhancement plugins left of the gateway and provider plugins right of it", async () => {
      const idle = { ...sora, running_tasks: 0 };
      await renderMap([...list.plugins, idle], [soraService]);
      expect(
        stars().map((star) => [star.dataset.pluginKey, star.dataset.side]),
      ).toEqual([
        ["powercontext", "left"],
        ["team-rules", "left"],
        ["sora", "right"],
      ]);
      // A star says what its plugin is doing, else its state.
      expect(stars()[0].textContent).toContain("已停用");
      expect(stars()[1].textContent).toContain("加载失败");
      expect(stars()[2].textContent).toContain("1 个提供商在用");
      await renderMap([...list.plugins, idle]);
      expect(stars()[2].textContent).toContain("还没有提供商在用");
      // A star opens its plugin; the gateway the settings of every plugin.
      await click(
        document.querySelector(
          '[data-testid="plugin-star-hub"]',
        ) as HTMLElement,
      );
      expect(
        document.querySelector('[data-testid="plugin-host-dialog"]'),
      ).not.toBe(null);
      await act(async () => {
        document.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
        );
      });
      await finishExitAnimations();
      await settleClosedLayer();
      await click(stars()[2]);
      expect(
        document.querySelector('[data-testid="plugin-task-overview"]'),
      ).not.toBe(null);
    });

    it("searches the stars and the list, and keeps the list once chosen", async () => {
      await renderMap([...list.plugins, sora]);
      await type(
        document.querySelector<HTMLInputElement>(
          'input[placeholder="搜索插件或模型"]',
        )!,
        "sora-2-pro",
      );
      // The stars that do not match fade; the rows go.
      expect(
        stars()
          .filter((star) => star.dataset.faded === undefined)
          .map((star) => star.dataset.pluginKey),
      ).toEqual(["sora"]);
      expect(
        [
          ...document.querySelectorAll<HTMLElement>(
            '[data-testid="plugin-row"]',
          ),
        ].map((row) => row.dataset.pluginKey),
      ).toEqual(["sora"]);
      await click(button("列表"));
      expect(document.querySelector('[data-testid="plugin-star-map"]')).toBe(
        null,
      );
      expect(localStorage.getItem("astrlink.plugins.view")).toBe("list");
      await act(async () => reactRoot.unmount());
      reactRoot = createRoot(container);
      await renderMap([...list.plugins, sora]);
      expect(document.querySelector('[data-testid="plugin-star-map"]')).toBe(
        null,
      );
      await click(button("星图"));
      expect(
        document.querySelector('[data-testid="plugin-star-map"]'),
      ).not.toBe(null);
    });

    it("leads an empty side to where its plugins come from", async () => {
      await renderMap([sora]);
      const enhancer = document.querySelector<HTMLElement>(
        '[data-testid="plugin-star-add"][data-side="left"]',
      );
      expect(enhancer?.textContent).toBe("从 GitHub 安装");
      await click(enhancer!);
      expect(
        document.querySelector('[data-testid="plugin-install-dialog"]'),
      ).not.toBe(null);
      await act(async () => reactRoot.unmount());
      reactRoot = createRoot(container);
      await renderMap([memory]);
      const provider = document.querySelector<HTMLElement>(
        '[data-testid="plugin-star-add"][data-side="right"]',
      );
      expect(provider?.textContent).toBe("去插件市场");
      await click(provider!);
      expect(document.querySelector('[data-testid="plugin-market"]')).not.toBe(
        null,
      );
    });

    it("lights each plugin with what it did over the last 30 days", async () => {
      const summary = emptyUsageSummary(resolveUsageWindow("30d", new Date()));
      summary.by_plugin = [
        {
          plugin: "powercontext",
          requests: 40,
          failed_requests: 2,
          tasks: 0,
          failed_tasks: 0,
        },
        {
          plugin: "sora",
          requests: 0,
          failed_requests: 0,
          tasks: 5,
          failed_tasks: 0,
        },
      ];
      bridgeMocks.getUsageSummary.mockResolvedValue(summary);
      await renderMap(
        [
          { ...memory, enabled: true },
          { ...sora, running_tasks: 0 },
        ],
        [soraService],
      );
      expect(bridgeMocks.getUsageSummary.mock.calls[0][0].preset).toBe("30d");
      expect(stars()[0].textContent).toContain("42 次");
      expect(stars()[1].textContent).toContain("5 个任务");
      expect(
        document.querySelector('[data-testid="plugin-star-map"]')?.textContent,
      ).toContain("近 30 天：处理 42 次请求，生成 5 个任务");
      // A planet is as large as its work, a task counting as ten requests;
      // sparks run to each plugin at work.
      const size = (key: string) =>
        parseFloat(
          document.querySelector<HTMLElement>(
            `[data-plugin-key="${key}"] .star-stage-sphere`,
          )!.style.width,
        );
      expect(size("sora")).toBeGreaterThan(size("powercontext"));
      expect(document.querySelectorAll(".star-stage-spark")).toHaveLength(2);
    });
  });

  describe("task plugins and the marketplace", () => {
    beforeEach(() => {
      bridgeMocks.listPlugins.mockResolvedValue({
        ...list,
        plugins: [...list.plugins, sora],
      });
      const fallback = bridgeMocks.getPlugin.getMockImplementation();
      bridgeMocks.getPlugin.mockImplementation(async (key: string) =>
        key === "sora" ? sora : fallback?.(key),
      );
      bridgeMocks.listPluginTasks.mockResolvedValue({
        items: [
          {
            id: "video_1",
            plugin_key: "sora",
            service_id: "service_sora",
            surface: "openai_video",
            action: "generate",
            model: "sora-2",
            status: "SUCCESS",
            usage: { seconds: 8 },
            created_at: "2026-10-25T08:00:00Z",
            updated_at: "2026-10-25T08:01:00Z",
            finished_at: "2026-10-25T08:01:00Z",
          },
          {
            id: "video_2",
            plugin_key: "sora",
            service_id: "service_sora",
            surface: "openai_video",
            model: "sora-2-pro",
            status: "IN_PROGRESS",
            progress: "40%",
            created_at: "2026-10-25T08:02:00Z",
            updated_at: "2026-10-25T08:02:30Z",
          },
          {
            id: "resp_3",
            plugin_key: "sora",
            service_id: "service_sora",
            surface: "openai_responses",
            model: "sora-2",
            status: "FAILURE",
            fail_reason: "content policy",
            created_at: "2026-10-25T07:00:00Z",
            updated_at: "2026-10-25T07:00:10Z",
            finished_at: "2026-10-25T07:00:10Z",
          },
        ],
        running: 1,
      });
      bridgeMocks.clearPluginTasks.mockResolvedValue(2);
      bridgeMocks.getPluginMarket.mockResolvedValue(market);
    });

    async function renderTasks(
      onAddProvider = vi.fn(),
      onOpenService = vi.fn(),
    ) {
      await act(async () => {
        reactRoot.render(
          <PluginManager
            coreSessionKey="session-1"
            isReady
            onAddProvider={onAddProvider}
            onOpenService={onOpenService}
            services={[soraService]}
          />,
        );
      });
    }

    it("opens a task plugin's page: its numbers, endpoints, models and providers", async () => {
      const onAddProvider = vi.fn();
      const onOpenService = vi.fn();
      await renderTasks(onAddProvider, onOpenService);
      const row = document.querySelector<HTMLElement>(
        '[data-testid="plugin-row"][data-plugin-key="sora"]',
      );
      // The list puts it with the providers, its models on the right.
      expect(row?.textContent).toContain("2 个模型");
      await click(row!);
      expect(
        [...document.querySelectorAll('[role="tab"]')].map(
          (tab) => tab.textContent,
        ),
      ).toEqual(["概览", "任务", "运行状态"]);
      const overview = document.querySelector(
        '[data-testid="plugin-task-overview"]',
      ) as HTMLElement;
      const metrics = [
        ...overview.querySelectorAll('[data-slot="metric-group"] dt'),
      ].map((dt) => [dt.textContent, dt.nextElementSibling?.textContent]);
      expect(metrics).toEqual([
        ["模型", "2"],
        ["进行中", "1"],
        ["全部任务", "3"],
        ["提供商", "1"],
      ]);
      const endpoints = overview.querySelector(
        '[data-testid="plugin-task-endpoints"]',
      )?.textContent;
      expect(endpoints).toContain("/v1/responses");
      expect(endpoints).toContain("流式 · 同步 · 后台");
      expect(endpoints).toContain("/v1/videos");
      expect(overview.textContent).toContain("sora-2-pro");
      expect(overview.textContent).toContain("逐个查询");
      await click(button("添加提供商"));
      expect(onAddProvider).toHaveBeenCalledWith(
        expect.objectContaining({
          plugin: "sora",
          pluginName: "Sora",
          provider: soraProvider,
          task: soraTask,
        }),
      );
      const provider = [
        ...overview.querySelectorAll<HTMLButtonElement>(
          '[data-testid="plugin-task-providers"] button',
        ),
      ].find((item) => item.textContent?.includes("Sora 主账号"));
      await click(provider!);
      expect(onOpenService).toHaveBeenCalledWith("service_sora");
    });

    it("lists a task plugin's tasks and clears the finished ones after asking", async () => {
      await renderTasks();
      await click(
        document.querySelector(
          '[data-testid="plugin-row"][data-plugin-key="sora"]',
        ) as HTMLElement,
      );
      await openTab("任务");
      const tasks = document.querySelector(
        '[data-testid="plugin-tasks"]',
      ) as HTMLElement;
      expect(tasks.textContent).toContain("1 个进行中");
      const rows = [
        ...tasks.querySelectorAll('[data-testid="plugin-task-row"]'),
      ];
      expect(
        rows.map((row) => row.querySelector("[data-tone]")?.textContent),
      ).toEqual(["完成", "生成中 40%", "失败"]);
      expect(rows[0].textContent).toContain("视频 · generate");
      expect(rows[2].textContent).toContain("content policy");
      await click(button("清除已完成的任务"));
      const dialog = document.querySelector(
        '[role="alertdialog"]',
      ) as HTMLElement;
      expect(dialog.textContent).toContain("进行中的任务不受影响");
      expect(bridgeMocks.clearPluginTasks).not.toHaveBeenCalled();
      await click(button("清除已完成的任务", dialog));
      await finishExitAnimations();
      expect(bridgeMocks.clearPluginTasks).toHaveBeenCalledWith("sora");
      expect(bridgeMocks.listPluginTasks).toHaveBeenCalledTimes(2);
    });

    it("lists a plugin that needs a new-api part AstrLink does not run as not compatible", async () => {
      bridgeMocks.getPluginMarket.mockResolvedValue({
        ...market,
        plugins: [
          ...market.plugins,
          {
            ...market.plugins[0],
            key: "relay",
            name: "Relay",
            installed: undefined,
            unsupported: ["transform"],
            link: market.index + "#relay@1.0.0",
          },
        ],
      });
      await renderTasks();
      await openTab("插件市场");
      const card = [
        ...document.querySelectorAll<HTMLElement>(
          '[data-testid="plugin-market-card"]',
        ),
      ].find((item) => item.textContent?.includes("Relay"))!;
      const badge = card.querySelector<HTMLElement>("[data-tone]")!;
      expect(badge.textContent).toBe("不兼容");
      expect(badge.title).toBe(
        "需要 new-api 的请求处理插件，AstrLink 还不能运行。",
      );
      expect(card.querySelector("button")).toBeNull();
      await click(card);
      const entry = document.querySelector(
        '[data-testid="plugin-market-entry"]',
      ) as HTMLElement;
      expect(entry.textContent).toContain("不兼容");
      expect(
        [...entry.querySelectorAll("button")].map((item) => item.textContent),
      ).not.toContain("安装");
      expect(bridgeMocks.previewPluginInstall).not.toHaveBeenCalled();
    });

    it("browses the marketplace and installs from it after the review", async () => {
      bridgeMocks.previewPluginInstall.mockResolvedValue(alibabaReview);
      bridgeMocks.installPlugin.mockResolvedValue({
        ...sora,
        key: "alibaba",
        name: "Alibaba Bailian",
      });
      await renderTasks();
      await openTab("插件市场");
      const cards = () =>
        [
          ...document.querySelectorAll<HTMLElement>(
            '[data-testid="plugin-market-card"]',
          ),
        ].map((card) => card);
      expect(cards()).toHaveLength(2);
      // An installed version says so instead of offering itself again.
      expect(cards()[1].textContent).toContain("已安装");
      // The search reaches the models too.
      await type(
        document.querySelector<HTMLInputElement>(
          'input[placeholder="搜索插件、模型或编码"]',
        )!,
        "wan2.5",
      );
      expect(cards().map((card) => card.textContent)).toEqual([
        expect.stringContaining("Alibaba Bailian"),
      ]);
      await click(cards()[0]);
      const entry = document.querySelector(
        '[data-testid="plugin-market-entry"]',
      ) as HTMLElement;
      expect(entry.textContent).toContain("阿里云百炼图片与视频生成");
      expect(entry.textContent).toContain("/v1/images/generations");
      await click(button("安装", entry));
      await finishExitAnimations();
      expect(bridgeMocks.previewPluginInstall).toHaveBeenCalledWith(
        market.plugins[0].link,
      );
      const review = document.querySelector(
        '[data-testid="plugin-install-review"]',
      ) as HTMLElement;
      expect(review.textContent).toContain("任务插件");
      expect(
        review.querySelector('[data-testid="plugin-install-source"]')
          ?.textContent,
      ).toBe("New API Official Plugins / alibaba版本 1.4.2 · 已校验");
      expect(
        review.querySelector('[data-testid="plugin-install-task"]')
          ?.textContent,
      ).toContain("/v1/videos");
      const dialog = document.querySelector(
        '[data-testid="plugin-install-dialog"]',
      ) as HTMLElement;
      await click(button("安装", dialog));
      expect(bridgeMocks.installPlugin).toHaveBeenCalledWith(
        "token-market",
        false,
      );
    });

    it("opens the community marketplace, finds a plugin by its code and shows its usage guide", async () => {
      const communityIndex =
        "https://www.newapi.ai/api/v1/community/index.json";
      const lookup =
        "https://www.newapi.ai/api/v1/community/resolve?code=30000003";
      const echo = {
        ...market.plugins[1],
        key: "someone_echo",
        name: "Echo",
        description: "回声视频",
        versions: [
          {
            version: "1.1.0",
            readme: true,
            link: `${communityIndex}#someone_echo@1.1.0`,
          },
        ],
        link: `${communityIndex}#someone_echo@1.1.0`,
        installed: "1.1.0",
        revoked: { reason: "泄露密钥" },
        author: { login: "someone", url: "https://github.com/someone" },
        recommended: true,
        code: "20000002",
      };
      const hidden = {
        ...echo,
        key: "other_hidden",
        name: "Hidden",
        code: "30000003",
        installed: undefined,
        revoked: undefined,
        recommended: undefined,
        author: undefined,
        versions: [
          {
            version: "1.1.0",
            readme: true,
            link: `${lookup}#other_hidden@1.1.0`,
          },
        ],
        link: `${lookup}#other_hidden@1.1.0`,
      };
      bridgeMocks.getPluginMarket.mockImplementation(async (index?: string) =>
        index === communityIndex
          ? {
              index: communityIndex,
              name: "New API Community",
              plugins: [echo],
            }
          : market,
      );
      bridgeMocks.findPluginByCode.mockResolvedValue({
        index: lookup,
        name: "New API Community",
        plugins: [hidden],
      });
      bridgeMocks.getPluginMarketDoc.mockResolvedValue({
        markdown: "# Hidden\n\n先在官网申请密钥。",
        language: "zh-CN",
      });
      try {
        await renderTasks();
        await openTab("插件市场");
        await chooseOption('[aria-label="插件市场来源"]', "社区市场");
        expect(bridgeMocks.getPluginMarket).toHaveBeenLastCalledWith(
          communityIndex,
        );
        const cards = () => [
          ...document.querySelectorAll<HTMLElement>(
            '[data-testid="plugin-market-card"]',
          ),
        ];
        expect(cards()).toHaveLength(1);
        expect(cards()[0].textContent).toContain("作者 someone");
        expect(cards()[0].textContent).toContain("推荐");
        // The version installed now was taken down, and says why.
        const badge = cards()[0].querySelector<HTMLElement>("[data-tone]")!;
        expect(badge.textContent).toBe("已下架");
        expect(badge.title).toContain("泄露密钥");
        // A code the listing does not have is looked up on the site.
        await type(
          document.querySelector<HTMLInputElement>(
            'input[placeholder="搜索插件、模型或编码"]',
          )!,
          "30000003",
        );
        await act(async () => {
          await Promise.resolve();
        });
        expect(bridgeMocks.findPluginByCode).toHaveBeenCalledWith("30000003");
        expect(cards().map((card) => card.textContent)).toEqual([
          expect.stringContaining("Hidden"),
        ]);
        await click(cards()[0]);
        await act(async () => {
          await Promise.resolve();
        });
        expect(bridgeMocks.getPluginMarketDoc).toHaveBeenCalledWith({
          index: lookup,
          key: "other_hidden",
          version: "1.1.0",
          doc: "readme",
        });
        const entry = document.querySelector(
          '[data-testid="plugin-market-entry"]',
        ) as HTMLElement;
        expect(
          entry.querySelector('[data-testid="plugin-market-guide"]')
            ?.textContent,
        ).toContain("先在官网申请密钥。");
        expect(entry.textContent).toContain("30000003");
        await click(button("安装", entry));
        await finishExitAnimations();
        expect(bridgeMocks.previewPluginInstall).toHaveBeenCalledWith(
          hidden.link,
        );
      } finally {
        localStorage.removeItem("astrlink.plugins.marketIndex");
      }
    });
  });
});
