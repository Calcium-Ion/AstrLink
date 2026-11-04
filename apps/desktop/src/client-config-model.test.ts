import { describe, expect, it } from "vitest";

import {
  availableModels,
  clientConfigClients,
  configAttention,
  missingModels,
  parseClientCatalog,
  parseClientConfigApplyOutcome,
  parseClientConfigCopied,
  parseClientConfigOverview,
  parseClientConfigPreview,
  parseClientConfigSnippet,
  parseClientProxyCheck,
  reasoningEffortOptions,
  type ClientCatalog,
  type ClientCatalogEntry,
  type ClientConfigClient,
  type ClientConfigStatus,
} from "./client-config-model";

const home = "/Users/me";
const dirs: Record<ClientConfigClient, string> = {
  claude: ".claude",
  codex: ".codex",
  gemini: ".gemini",
  opencode: ".config/opencode",
  openclaw: ".openclaw",
  pi: ".pi/agent",
};
const files: Record<ClientConfigClient, string[]> = {
  claude: ["settings.json"],
  codex: ["config.toml"],
  gemini: [".env", "settings.json"],
  opencode: ["opencode.jsonc"],
  openclaw: ["openclaw.json"],
  pi: ["models.json", "settings.json"],
};

/** The six clients as the host lists them. */
const builtinEntries: ClientCatalogEntry[] = (
  [
    ["claude", "Claude Code", "anthropic.messages", false],
    ["codex", "Codex", "openai.responses", false],
    ["gemini", "Gemini CLI", "google.generate_content", false],
    ["opencode", "OpenCode", "openai.chat", true],
    ["openclaw", "OpenClaw", "openai.chat", true],
    ["pi", "Pi", "openai.responses", true],
  ] as const
).map(([id, name, protocol, list]) => ({
  id,
  name,
  kind: "file",
  targets: [],
  protocols: [protocol],
  list,
}));
const catalog: ClientCatalog = { clients: builtinEntries };

function status(
  client: ClientConfigClient,
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  const dir = `${home}/${dirs[client]}`;
  return {
    client,
    wsl: null,
    target: null,
    detected: true,
    home,
    dir,
    paths: files[client].map((file) => `${dir}/${file}`),
    exists: files[client].map(() => false),
    state: "not_configured",
    token_id: null,
    models: {},
    notes: [],
    paste_since_unix: null,
    paste_into: [],
    ...overrides,
  };
}

/** One home's six statuses, with overrides by client. */
function statuses(
  overrides: Partial<Record<ClientConfigClient, Record<string, unknown>>> = {},
): Record<string, unknown>[] {
  return clientConfigClients.map((client) => status(client, overrides[client]));
}

function parseStatuses(clients: unknown[]) {
  return parseClientConfigOverview(
    { clients, wsl_unchecked: [], wsl_localhost: false, installed: [] },
    catalog,
  ).clients;
}

function inWSL(name: string) {
  return statuses().map((item) => ({ ...item, wsl: name }));
}

describe("client-config status parsing", () => {
  it("accepts this computer's six clients, then each WSL home's", () => {
    const value = {
      clients: [...statuses(), ...inWSL("Ubuntu"), ...inWSL("Debian")],
      wsl_unchecked: ["Arch"],
      wsl_localhost: true,
      installed: [],
    };
    expect(parseClientConfigOverview(value, catalog)).toEqual(value);
    for (const clients of [
      inWSL("Ubuntu"),
      [...statuses(), ...inWSL("Ubuntu"), ...inWSL("Ubuntu")],
      [...statuses(), ...inWSL("Ubuntu").slice(0, 3)],
      [...statuses(), ...statuses()],
      statuses().slice(0, 3),
      statuses().reverse(),
    ]) {
      expect(() => parseStatuses(clients)).toThrow(
        "Invalid client-config IPC response",
      );
    }
  });

  it("accepts the models each client takes", () => {
    const parsed = parseStatuses(
      statuses({
        claude: {
          state: "configured",
          token_id: "token_01",
          models: {
            model: "main",
            haikuModel: "haiku",
            fableModel: "fable",
            subagentModel: "sub",
          },
        },
        codex: {
          state: "configured",
          token_id: "token_01",
          models: { model: "gpt-5", reasoningEffort: "xhigh" },
        },
        gemini: {
          state: "outdated",
          token_id: "token_01",
          models: { model: "gemini-3-pro" },
        },
        opencode: {
          state: "configured",
          token_id: "token_01",
          models: { model: "a", models: ["a", "b"] },
        },
        openclaw: {
          state: "modified",
          token_id: "token_02",
          models: { model: "a", models: [] },
        },
        pi: {
          state: "configured",
          token_id: "token_01",
          models: {
            model: "pi",
            models: ["pi"],
            contextWindow: 400000,
            maxTokens: 64000,
          },
        },
      }),
    );
    expect(parsed[1].models).toEqual({
      model: "gpt-5",
      reasoningEffort: "xhigh",
    });
    expect(parsed[5].models).toMatchObject({ contextWindow: 400000 });
    expect(parsed[5].exists).toEqual([false, false]);
  });

  it.each([
    ["an unexpected field", { token: "astr_x" }],
    ["a missing home", { home: undefined }],
    ["an empty home", { home: "" }],
    ["a multi-line dir", { dir: "/a\n/b" }],
    ["no config path", { paths: [], exists: [] }],
    ["one existence per path missing", { exists: [] }],
    ["a non-boolean existence", { exists: ["yes"] }],
    ["an unknown state", { state: "stale" }],
    ["a malformed token ID", { state: "configured", token_id: "X" }],
    ["a configured client without a token", { state: "modified" }],
    ["an unconfigured client with a token", { token_id: "token_01" }],
    ["a state CC Switch once had", { state: "cc_switch" }],
    ["models that are not an object", { models: ["gpt-5"] }],
    ["a tier outside Claude Code", { models: { opusModel: "x" } }],
    ["a list outside list clients", { models: { models: ["x"] } }],
    ["a malformed effort", { models: { reasoningEffort: "x-high" } }],
    ["a limit outside Pi", { models: { maxTokens: 1 } }],
    ["an empty model", { models: { model: "" } }],
    ["an overlong model", { models: { model: "x".repeat(257) } }],
    ["a revealed token", { dir: `/x/astr_${"A".repeat(43)}` }],
  ])("rejects %s", (_, overrides) => {
    const value = statuses({ codex: overrides });
    if ("home" in overrides && overrides.home === undefined) {
      delete value[1].home;
    }
    expect(() => parseStatuses(value)).toThrow(
      "Invalid client-config IPC response",
    );
  });

  it("rejects limits and lists Pi cannot take", () => {
    for (const models of [
      { model: "pi", contextWindow: 0 },
      { model: "pi", maxTokens: 100_000_001 },
      { model: "pi", maxTokens: "64000" },
      { model: "pi", models: Array.from({ length: 501 }, (_, i) => `m${i}`) },
      { model: "pi", models: [""] },
    ]) {
      expect(() =>
        parseStatuses(
          statuses({
            pi: { state: "configured", token_id: "token_01", models },
          }),
        ),
      ).toThrow("Invalid client-config IPC response");
    }
  });
});

describe("client catalog parsing", () => {
  const pair: ClientCatalogEntry = {
    id: "workbuddy",
    name: "WorkBuddy",
    kind: "file",
    targets: ["cn", "intl"],
    protocols: ["openai.chat", "openai.responses"],
    list: true,
  };
  const listed: ClientCatalogEntry[] = [
    {
      id: "cherry_studio",
      name: "Cherry Studio",
      kind: "link",
      targets: [],
      protocols: ["openai.chat"],
      list: false,
    },
    {
      id: "trae",
      name: "TRAE",
      kind: "guide",
      targets: [],
      protocols: ["anthropic.messages", "openai.responses", "openai.chat"],
      list: false,
    },
    {
      id: "comate",
      name: "文心快码 Comate",
      kind: "unsupported",
      targets: [],
      protocols: [],
      list: false,
    },
  ];
  const full: ClientCatalog = {
    clients: [...builtinEntries, pair, ...listed],
  };

  it("accepts every kind of client in the host's order", () => {
    expect(parseClientCatalog(full)).toEqual(full);
    expect(parseClientCatalog({ clients: [] })).toEqual({ clients: [] });
  });

  it.each([
    ["an unexpected field", { icon: "x" }],
    ["a malformed id", { id: "Work-Buddy" }],
    ["a one-letter id", { id: "w" }],
    ["an empty name", { name: "" }],
    ["an overlong name", { name: "W".repeat(65) }],
    ["an unknown kind", { kind: "plugin" }],
    ["a single target", { targets: ["cn"] }],
    ["a repeated target", { targets: ["cn", "cn"] }],
    ["a malformed target", { targets: ["cn", "Intl"] }],
    ["an unknown protocol", { protocols: ["gemini.generate"] }],
    ["a repeated protocol", { protocols: ["openai.chat", "openai.chat"] }],
    ["a written client without a protocol", { protocols: [] }],
    ["a list flag that is not a boolean", { list: "yes" }],
  ])("rejects %s", (_, overrides) => {
    expect(() =>
      parseClientCatalog({
        clients: [...builtinEntries, { ...pair, ...overrides }],
      }),
    ).toThrow("Invalid client-config IPC response");
  });

  it("rejects targets outside written clients and repeated ids", () => {
    for (const clients of [
      [{ ...listed[1], targets: ["cn", "intl"] }],
      [builtinEntries[0], builtinEntries[0]],
      Array.from({ length: 201 }, (_, index) => ({
        ...pair,
        id: `client_${index}`,
      })),
    ]) {
      expect(() => parseClientCatalog({ clients })).toThrow(
        "Invalid client-config IPC response",
      );
    }
  });

  const targetStatus = (target: string | null, wsl: string | null = null) => ({
    ...status("codex"),
    client: "workbuddy",
    wsl,
    target,
    dir: "/Users/me/.workbuddy",
    paths: ["/Users/me/.workbuddy/models.json"],
    exists: [false],
  });
  const homeOf = (wsl: string | null) => [
    ...statuses().map((item) => ({ ...item, wsl })),
    targetStatus("cn", wsl),
    targetStatus("intl", wsl),
  ];
  const parseFull = (
    clients: unknown[],
    installed: unknown[] = [],
  ): ReturnType<typeof parseClientConfigOverview> =>
    parseClientConfigOverview(
      { clients, wsl_unchecked: [], wsl_localhost: false, installed },
      full,
    );

  it("reads one status per target, and the link clients found", () => {
    const parsed = parseFull(
      [...homeOf(null), ...homeOf("Ubuntu")],
      ["cherry_studio"],
    );
    expect(
      parsed.clients
        .filter((item) => item.client === "workbuddy")
        .map(({ wsl, target }) => [wsl, target]),
    ).toEqual([
      [null, "cn"],
      [null, "intl"],
      ["Ubuntu", "cn"],
      ["Ubuntu", "intl"],
    ]);
    expect(parsed.installed).toEqual(["cherry_studio"]);
    // A list client of the catalog takes a list.
    expect(
      parseFull([
        ...statuses(),
        {
          ...targetStatus("cn"),
          state: "configured",
          token_id: "token_01",
          models: { model: "a", models: ["a", "b"] },
        },
        targetStatus("intl"),
      ]).clients[6].models,
    ).toEqual({ model: "a", models: ["a", "b"] });
  });

  it("rejects targets out of order and clients the catalog does not write", () => {
    const local = homeOf(null);
    for (const clients of [
      [...local.slice(0, 6), local[7], local[6]],
      [...local.slice(0, 6), targetStatus(null), targetStatus("intl")],
      [...local.slice(0, 6), targetStatus("cn"), targetStatus("hk")],
      [...local.slice(0, 5), { ...local[5], target: "cn" }, ...local.slice(6)],
      local.slice(0, 7),
      [...local, { ...status("codex"), client: "trae" }],
    ]) {
      expect(() => parseFull(clients)).toThrow(
        "Invalid client-config IPC response",
      );
    }
    for (const installed of [["trae"], ["cherry_studio", "cherry_studio"]]) {
      expect(() => parseFull(local, installed)).toThrow(
        "Invalid client-config IPC response",
      );
    }
  });

  it("matches each result to its asked target", () => {
    const asked = [
      { client: "workbuddy", wsl: null, target: "cn", action: "write" },
      { client: "workbuddy", wsl: null, target: "intl", action: "write" },
    ] as const;
    const result = (target: string) => ({
      client: "workbuddy",
      wsl: null,
      target,
      action: "write",
      ok: true,
      error: null,
    });
    const value = { status: "done", results: [result("cn"), result("intl")] };
    expect(parseClientConfigApplyOutcome(value, asked)).toEqual(value);
    expect(() =>
      parseClientConfigApplyOutcome(
        { status: "done", results: [result("intl"), result("cn")] },
        asked,
      ),
    ).toThrow("Invalid client-config IPC response");
  });
});

describe("client-config change preview parsing", () => {
  const preview = {
    files: [
      {
        path: "/Users/me/.gemini/.env",
        created: false,
        deleted: false,
        changes: 3,
        unit: "line",
        lines: [
          { kind: "context", text: "# Gemini CLI" },
          { kind: "remove", text: "GEMINI_API_KEY=AIza…k2Q" },
          { kind: "add", text: "GEMINI_API_KEY=astr_…K8Q2" },
          { kind: "gap", text: "" },
          { kind: "context", text: "\tindented" },
        ],
      },
    ],
    replaced: 1,
    restored: 0,
  };

  it("accepts files with their changed lines", () => {
    expect(parseClientConfigPreview(preview)).toEqual(preview);
    expect(
      parseClientConfigPreview({ files: [], replaced: 0, restored: 2 }),
    ).toEqual({ files: [], replaced: 0, restored: 2 });
  });

  it.each([
    ["an unknown line kind", { lines: [{ kind: "moved", text: "a" }] }],
    ["a line that spans lines", { lines: [{ kind: "add", text: "a\nb" }] }],
    [
      "a revealed token",
      { lines: [{ kind: "add", text: `KEY=astr_${"A".repeat(43)}` }] },
    ],
    ["a file both created and deleted", { created: true, deleted: true }],
    ["an unknown unit", { unit: "byte" }],
    ["a negative count", { changes: -1 }],
    ["an unexpected field", { diff: "" }],
  ])("rejects %s", (_, overrides) => {
    expect(() =>
      parseClientConfigPreview({
        ...preview,
        files: [{ ...preview.files[0], ...overrides }],
      }),
    ).toThrow("Invalid client-config IPC response");
  });
});

describe("client-config apply parsing", () => {
  const asked = [
    { client: "gemini", wsl: null, target: null, action: "write" },
    { client: "pi", wsl: "Ubuntu", target: null, action: "remove" },
  ] as const;

  it("accepts confirmations for the asked items", () => {
    const value = {
      status: "needs_confirmation",
      items: [
        {
          client: "gemini",
          wsl: null,
          target: null,
          dir: "/Users/me/.gemini",
          remove: false,
          keys: [
            {
              path: "/Users/me/.gemini/.env",
              key: "GEMINI_API_KEY",
              current: null,
            },
            {
              path: "/Users/me/.gemini/settings.json",
              key: "security.auth.selectedType",
              current: "oauth-personal",
            },
          ],
          gateway: null,
          token_id: null,
        },
        {
          client: "pi",
          wsl: "Ubuntu",
          target: null,
          dir: "/home/me/.pi/agent",
          remove: true,
          keys: [],
          gateway: null,
          token_id: null,
        },
      ],
    };
    expect(parseClientConfigApplyOutcome(value, asked)).toEqual(value);
    // Another token or gateway is reason enough on its own.
    expect(
      parseClientConfigApplyOutcome(
        {
          status: "needs_confirmation",
          items: [
            {
              ...value.items[0],
              keys: [],
              gateway: "http://localhost:8317",
              token_id: "token_02",
            },
          ],
        },
        asked,
      ).status,
    ).toBe("needs_confirmation");
  });

  it.each([
    ["no item", []],
    [
      "an item that was not asked",
      [
        {
          client: "codex",
          wsl: null,
          target: null,
          dir: "/x",
          remove: false,
          keys: [],
          gateway: null,
          token_id: "token_02",
        },
      ],
    ],
    [
      "a write shown as a removal",
      [
        {
          client: "gemini",
          wsl: null,
          target: null,
          dir: "/x",
          remove: true,
          keys: [],
          gateway: null,
          token_id: null,
        },
      ],
    ],
    [
      "a write without a reason",
      [
        {
          client: "gemini",
          wsl: null,
          target: null,
          dir: "/x",
          remove: false,
          keys: [],
          gateway: null,
          token_id: null,
        },
      ],
    ],
    [
      "a revealed token",
      [
        {
          client: "gemini",
          wsl: null,
          target: null,
          dir: "/x",
          remove: false,
          keys: [
            { path: "/x/.env", key: "K", current: `astr_${"A".repeat(43)}` },
          ],
          gateway: null,
          token_id: null,
        },
      ],
    ],
  ])("rejects a confirmation with %s", (_, items) => {
    expect(() =>
      parseClientConfigApplyOutcome(
        { status: "needs_confirmation", items },
        asked,
      ),
    ).toThrow("Invalid client-config IPC response");
  });

  it("accepts one result per asked item, in order", () => {
    const value = {
      status: "done",
      results: [
        {
          client: "gemini",
          wsl: null,
          target: null,
          action: "write",
          ok: true,
          error: null,
        },
        {
          client: "pi",
          wsl: "Ubuntu",
          target: null,
          action: "remove",
          ok: false,
          error: {
            message: "invalid JSON",
            path: "/home/me/.pi/agent/settings.json",
            line: 12,
          },
        },
      ],
    };
    expect(parseClientConfigApplyOutcome(value, asked)).toEqual(value);
    for (const results of [
      value.results.slice(0, 1),
      [...value.results].reverse(),
      [{ ...value.results[0], ok: false }],
      [value.results[0], { ...value.results[1], ok: true }],
      [value.results[0], { ...value.results[1], action: "write" }],
      [
        value.results[0],
        { ...value.results[1], error: { message: "", path: null, line: null } },
      ],
    ]) {
      expect(() =>
        parseClientConfigApplyOutcome({ status: "done", results }, asked),
      ).toThrow("Invalid client-config IPC response");
    }
    for (const outcome of [{ status: "applied" }, null]) {
      expect(() => parseClientConfigApplyOutcome(outcome, asked)).toThrow(
        "Invalid client-config IPC response",
      );
    }
  });
});

describe("client-config snippets and proxy checks", () => {
  it("accepts one block per file, showing only the token hint", () => {
    const value = [
      { path: "~/.gemini/.env", contents: "GEMINI_API_KEY=astr_…K8Q2\n" },
      { path: "~/.gemini/settings.json", contents: "{}\n" },
    ];
    expect(parseClientConfigSnippet(value)).toEqual(value);
    for (const snippet of [
      [],
      [{ path: "~/.env", contents: "  \n" }],
      [{ path: "", contents: "a" }],
      [{ path: "~/.env", contents: `KEY=astr_${"A".repeat(43)}` }],
      [{ path: "~/.env", contents: "a", extra: 1 }],
      "text",
    ]) {
      expect(() => parseClientConfigSnippet(snippet)).toThrow(
        "Invalid client-config IPC response",
      );
    }
    expect(parseClientConfigCopied(false)).toBe(false);
    expect(() => parseClientConfigCopied("true")).toThrow(
      "Invalid client-config IPC response",
    );
  });

  it("accepts each proxy route, naming the proxy by host and port", () => {
    const value = {
      client: { route: "direct" },
      numeric: { route: "blocked", proxy: "127.0.0.1:7892" },
    };
    expect(parseClientProxyCheck(value)).toEqual(value);
    for (const check of [
      { client: { route: "direct" } },
      { client: { route: "tunnel" }, numeric: null },
      { client: { route: "direct", proxy: "127.0.0.1:7892" }, numeric: null },
      { client: { route: "blocked" }, numeric: null },
    ]) {
      expect(() => parseClientProxyCheck(check)).toThrow(
        "Invalid client-config IPC response",
      );
    }
  });
});

describe("reasoning effort options", () => {
  it("offers the model's official levels Codex takes, else the common ones", () => {
    expect(reasoningEffortOptions(["low", "high", "turbo"], "")).toEqual([
      "low",
      "high",
    ]);
    expect(reasoningEffortOptions(undefined, "")).toEqual([
      "minimal",
      "low",
      "medium",
      "high",
      "xhigh",
    ]);
    // The config's own value stays visible.
    expect(reasoningEffortOptions(["low"], "max")).toEqual(["low", "max"]);
  });
});

describe("missing models", () => {
  const services = [
    { enabled: true, models: ["a", "b"] },
    { enabled: false, models: ["c"] },
    { enabled: true, models: ["d"], subscription: { status: "disconnected" } },
  ];
  const redirects = [
    { from: "alias", to: "a", enabled: true },
    { from: "off", to: "a", enabled: false },
    { from: "dead", to: "c", enabled: true },
  ];

  it("serves usable services' models and redirects to them", () => {
    expect([...availableModels(services, redirects)].sort()).toEqual([
      "a",
      "alias",
      "b",
    ]);
  });

  it("names a config's models the gateway no longer serves", () => {
    const available = availableModels(services, redirects);
    const config = parseStatuses(
      statuses({
        opencode: {
          state: "configured",
          token_id: "token_01",
          models: { model: "a", models: ["a", "c", "d", "alias"] },
        },
      }),
    )[3];
    expect(missingModels(config, available)).toEqual(["c", "d"]);
    expect(configAttention(config, "token_01", available)).toEqual({
      kind: "model_missing",
      missing: ["c", "d"],
    });
    // Unknown until the catalog loads; another token's config is not ours.
    expect(configAttention(config, "token_01", null)).toBeNull();
    expect(configAttention(config, "token_02", available)).toBeNull();
  });

  it("ranks what a config needs", () => {
    const [claude, codex, gemini] = parseStatuses(
      statuses({
        claude: { state: "invalid", token_id: "token_01" },
        codex: {
          state: "modified",
          token_id: "token_01",
          models: { model: "gone" },
        },
        gemini: { state: "outdated", token_id: "token_01", models: {} },
      }),
    ) as ClientConfigStatus[];
    const available = new Set<string>();
    expect(configAttention(claude, "token_01", available)?.kind).toBe(
      "invalid",
    );
    expect(configAttention(codex, "token_01", available)?.kind).toBe(
      "modified",
    );
    expect(configAttention(gemini, "token_01", available)?.kind).toBe(
      "outdated",
    );
  });
});
