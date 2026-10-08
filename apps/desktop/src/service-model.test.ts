import { describe, expect, it } from "vitest";

import {
  activeServiceRisk,
  bestConversionTarget,
  hasPlanUsage,
  parseService,
  parseServicePage,
  parseServiceRecord,
  parseServiceModelProbe,
  parseSubscriptionRiskEvents,
  serviceStatusLabel,
  subscriptionRiskLabel,
  subscriptionNativeCapabilities,
  subscriptionConversionTargets,
  serviceBuiltinRedirects,
  supportsResponsesWebSocket,
  withClaudeCodeRedirects,
} from "./service-model";

const createdAt = "2026-07-28T12:00:00Z";

describe("service model", () => {
  it("fixes Alpha Search as native non-streaming only for Codex subscriptions", () => {
    for (const [kind, capabilities] of Object.entries(
      subscriptionNativeCapabilities,
    )) {
      expect(
        capabilities.filter(
          ({ protocol }) => protocol === "openai.alpha_search",
        ),
      ).toEqual(
        kind === "codex_subscription"
          ? [
              {
                protocol: "openai.alpha_search",
                mode: "native",
                streaming: false,
              },
            ]
          : [],
      );
    }
    for (const targets of Object.values(subscriptionConversionTargets)) {
      expect(targets).not.toContain("openai.alpha_search");
    }
  });

  it("parses HTTP and subscription services from one page", () => {
    const page = parseServicePage({
      items: [
        {
          id: "service_codex_personal",
          name: "Codex personal",
          kind: "codex_subscription",
          enabled: true,
          models: [],
          capabilities: [
            {
              protocol: "openai.responses",
              mode: "native",
              streaming: true,
            },
          ],
          subscription: {
            provider: "openai_codex",
            status: "connected",
            account_hint: "acct***01",
            credential_ref:
              "keyring://astrlink.subscription.openai_codex/service_codex_personal",
          },
          created_at: createdAt,
          updated_at: createdAt,
        },
        {
          id: "service_gateway",
          name: "new-api",
          kind: "newapi",
          enabled: true,
          models: ["gpt-5"],
          capabilities: [
            {
              protocol: "openai.responses",
              mode: "delegated",
              streaming: true,
            },
          ],
          http: {
            base_url: "https://gateway.example/v1",
            auth: { scheme: "bearer" },
            credential_ref: "local://service/service_gateway",
          },
          created_at: createdAt,
          updated_at: createdAt,
        },
      ],
      next_cursor: null,
    });

    expect(page.items.map((service) => service.kind)).toEqual([
      "codex_subscription",
      "newapi",
    ]);
    expect(page.items[0].subscription?.status).toBe("connected");
    expect(page.items[1].http?.base_url).toBe("https://gateway.example/v1");
  });

  it("parses Grok subscription services and binds them to the xai_grok provider", () => {
    const grok = parseService({
      id: "service_grok_personal",
      name: "Grok",
      kind: "grok_subscription",
      enabled: true,
      models: ["grok-4.5"],
      capabilities: [
        { protocol: "openai.responses", mode: "native", streaming: true },
        { protocol: "openai.chat", mode: "native", streaming: true },
        { protocol: "openai.models", mode: "native", streaming: false },
      ],
      subscription: {
        provider: "xai_grok",
        status: "connected",
        account_hint: "user***42",
        credential_ref: "keyring://astrlink/subscription/service_grok_personal",
      },
      created_at: createdAt,
      updated_at: createdAt,
    });
    expect(grok.kind).toBe("grok_subscription");
    expect(grok.subscription?.provider).toBe("xai_grok");
    expect(() =>
      parseService({
        id: "service_grok_personal",
        name: "Grok",
        kind: "grok_subscription",
        enabled: true,
        models: [],
        capabilities: [],
        subscription: { provider: "openai_codex", status: "disconnected" },
        created_at: createdAt,
        updated_at: createdAt,
      }),
    ).toThrow(/provider does not match service kind/);
  });

  it("parses Copilot subscription services and keeps Responses off WebSocket", () => {
    const copilot = parseService({
      id: "service_copilot_personal",
      name: "GitHub Copilot",
      kind: "copilot_subscription",
      enabled: true,
      models: ["claude-sonnet-4.6", "gpt-5.4"],
      capabilities: [
        { protocol: "anthropic.messages", mode: "native", streaming: true },
        { protocol: "openai.responses", mode: "native", streaming: true },
        { protocol: "openai.chat", mode: "native", streaming: true },
        { protocol: "openai.models", mode: "native", streaming: false },
      ],
      subscription: {
        provider: "github_copilot",
        status: "connected",
        account_hint: "5832***31",
        credential_ref: "local://subscription/service_copilot_personal",
      },
      created_at: createdAt,
      updated_at: createdAt,
    });
    expect(copilot.subscription?.provider).toBe("github_copilot");
    expect(supportsResponsesWebSocket(copilot)).toBe(false);
  });

  it("parses a provider's own redirect rules", () => {
    const service = (extra: Record<string, unknown>) =>
      parseService({
        id: "service_api_redirects",
        name: "API",
        kind: "openai",
        enabled: true,
        models: ["gpt-5.4"],
        capabilities: [
          { protocol: "openai.chat", mode: "native", streaming: true },
        ],
        http: {
          base_url: "https://api.example/v1",
          auth: { scheme: "none" },
        },
        created_at: createdAt,
        updated_at: createdAt,
        ...extra,
      });
    expect(service({}).model_redirects).toBeUndefined();
    expect(
      service({
        model_redirects: [{ from: "gpt-5", to: "gpt-5.4", enabled: true }],
      }).model_redirects,
    ).toEqual([{ from: "gpt-5", to: "gpt-5.4", enabled: true }]);
    expect(() =>
      service({
        model_redirects: [
          { from: "a", to: "b", enabled: true },
          { from: "b", to: "c", enabled: true },
        ],
      }),
    ).toThrow(/model_redirects/);
  });

  it("adds a Claude Code rule for each new Copilot Claude model without a built-in row", () => {
    const kept = { from: "claude-opus-4-6", to: "gpt-5.4", enabled: false };
    expect(serviceBuiltinRedirects("copilot_subscription")).toContainEqual({
      from: "claude-sonnet-4-6",
      defaultTo: "claude-sonnet-4.6",
      defaultEnabled: true,
    });
    expect(serviceBuiltinRedirects("openai")).toEqual([]);
    expect(
      withClaudeCodeRedirects(
        "copilot_subscription",
        [kept],
        ["claude-fable-5.9"],
        [
          "claude-fable-5.9",
          "claude-fable-6.0",
          "claude-sonnet-4.6",
          "claude-sonnet-4",
          "gpt-5.4",
        ],
      ),
    ).toEqual([
      kept,
      { from: "claude-fable-6-0", to: "claude-fable-6.0", enabled: true },
    ]);
    expect(
      withClaudeCodeRedirects("openai", [], [], ["claude-fable-6.0"]),
    ).toEqual([]);
  });

  it("accepts sealed account credentials and still reads keystore ones", () => {
    const account = (credential_ref: string) =>
      parseService({
        id: "service_codex_personal",
        name: "Codex",
        kind: "codex_subscription",
        enabled: true,
        models: [],
        capabilities: [],
        subscription: {
          provider: "openai_codex",
          status: "connected",
          credential_ref,
        },
        created_at: createdAt,
        updated_at: createdAt,
      });
    expect(
      account("local://subscription/service_codex_personal").subscription
        ?.credential_ref,
    ).toBe("local://subscription/service_codex_personal");
    expect(
      account("keyring://astrlink/subscription/service_codex_personal")
        .subscription?.credential_ref,
    ).toBe("keyring://astrlink/subscription/service_codex_personal");
    expect(() => account("local://service/service_codex_personal")).toThrow(
      /credential_ref/,
    );
  });

  it("rejects a leftover disabled_models field", () => {
    expect(() =>
      parseService({
        id: "service_gateway",
        name: "new-api",
        kind: "newapi",
        enabled: true,
        models: ["gpt-5"],
        disabled_models: ["gpt-4o"],
        capabilities: [],
        http: {
          base_url: "https://gateway.example/v1",
          auth: { scheme: "bearer" },
          credential_ref: "local://service/service_gateway",
        },
        created_at: createdAt,
        updated_at: createdAt,
      }),
    ).toThrow(/disabled_models: unexpected field/);
  });

  it("accepts a saved-key hint only next to a stored credential", () => {
    const service = (http: Record<string, unknown>) => ({
      id: "service_gateway",
      name: "new-api",
      kind: "newapi",
      enabled: true,
      models: [],
      capabilities: [],
      http: {
        base_url: "https://gateway.example/v1",
        auth: { scheme: "bearer" },
        ...http,
      },
      created_at: createdAt,
      updated_at: createdAt,
    });
    expect(
      parseService(
        service({
          credential_ref: "local://service/service_gateway",
          credential_hint: "…wxyz",
        }),
      ).http?.credential_hint,
    ).toBe("…wxyz");
    expect(() => parseService(service({ credential_hint: "…wxyz" }))).toThrow(
      /credential_hint/,
    );
    expect(() =>
      parseService(
        service({
          credential_ref: "local://service/service_gateway",
          credential_hint: "sk-test-0123456789wxyz",
        }),
      ),
    ).toThrow(/credential_hint/);
  });

  it("requires the connection variant selected by kind", () => {
    expect(() =>
      parseService({
        id: "service_wrong",
        name: "Wrong",
        kind: "codex_subscription",
        enabled: true,
        models: [],
        capabilities: [],
        http: {
          base_url: "https://example.com",
          auth: { scheme: "none" },
        },
        created_at: createdAt,
        updated_at: createdAt,
      }),
    ).toThrow(/requires only subscription/);
  });

  it("parses optional local conversion targets", () => {
    const service = parseService({
      id: "service_http",
      name: "HTTP",
      kind: "openai",
      enabled: true,
      models: [],
      capabilities: [
        {
          protocol: "anthropic.messages",
          mode: "native",
          streaming: true,
          convert_to: "openai.chat",
        },
      ],
      http: {
        base_url: "https://example.com",
        auth: { scheme: "none" },
      },
      created_at: createdAt,
      updated_at: createdAt,
    });
    expect(service.capabilities[0]).toEqual({
      protocol: "anthropic.messages",
      mode: "native",
      streaming: true,
      convert_to: "openai.chat",
    });
  });

  it("rejects retired capability-level model lists", () => {
    expect(() =>
      parseService({
        id: "service_http",
        name: "HTTP",
        kind: "openai",
        enabled: true,
        models: [],
        capabilities: [
          {
            protocol: "openai.responses",
            mode: "native",
            streaming: true,
            models: ["gpt-5"],
          },
        ],
        http: {
          base_url: "https://example.com",
          auth: { scheme: "none" },
        },
        created_at: createdAt,
        updated_at: createdAt,
      }),
    ).toThrow(/capabilities\[0\]\.models: unexpected field/);
  });

  it("parses service records with strong ETags", () => {
    const record = parseServiceRecord({
      service: {
        id: "service_codex_work",
        name: "Codex work",
        kind: "codex_subscription",
        enabled: true,
        models: [],
        capabilities: [],
        subscription: {
          provider: "openai_codex",
          status: "disconnected",
        },
        created_at: createdAt,
        updated_at: createdAt,
      },
      etag: `"sha256:${"a".repeat(64)}"`,
    });
    expect(record.service.id).toBe("service_codex_work");
  });

  it("parses bounded model probe results", () => {
    expect(
      parseServiceModelProbe({
        service_id: "service_gateway",
        protocol: "openai.models",
        model_ids: ["gpt-5", "gpt-4.1"],
      }),
    ).toEqual({
      service_id: "service_gateway",
      protocol: "openai.models",
      model_ids: ["gpt-5", "gpt-4.1"],
    });
    expect(() =>
      parseServiceModelProbe({
        protocol: "vendor.models",
        model_ids: [],
      }),
    ).toThrow(/unknown model discovery protocol/);
  });
});

describe("hasPlanUsage", () => {
  it("covers connected subscriptions and API-key services with a quota route", () => {
    const http = {
      base_url: "https://api.kimi.com/coding",
      auth: { scheme: "bearer" as const },
    };
    expect(hasPlanUsage({ kind: "kimi_coding", http })).toBe(true);
    expect(hasPlanUsage({ kind: "glm_coding", http })).toBe(true);
    expect(hasPlanUsage({ kind: "minimax_coding", http })).toBe(true);
    expect(hasPlanUsage({ kind: "opencode_go", http })).toBe(true);
    expect(hasPlanUsage({ kind: "opencode_zen", http })).toBe(false);
    expect(hasPlanUsage({ kind: "newapi", http })).toBe(true);
    expect(hasPlanUsage({ kind: "openai_compatible", http })).toBe(false);
    expect(hasPlanUsage({ kind: "kimi_coding" })).toBe(false);
    expect(
      hasPlanUsage({
        kind: "claude_subscription",
        subscription: { status: "connected" },
      }),
    ).toBe(true);
    expect(
      hasPlanUsage({
        kind: "codex_subscription",
        subscription: { status: "disconnected" },
      }),
    ).toBe(false);
  });
});

function claudeService(subscription: Record<string, unknown>) {
  return {
    id: "service_claude_personal",
    name: "Claude personal",
    kind: "claude_subscription",
    enabled: true,
    models: [],
    capabilities: [
      { protocol: "anthropic.messages", mode: "native", streaming: true },
    ],
    subscription: {
      provider: "claude_code",
      status: "connected",
      ...subscription,
    },
    created_at: createdAt,
    updated_at: createdAt,
  };
}

describe("subscription risk", () => {
  const now = new Date("2026-07-28T12:30:00Z");

  it("parses suspended and cooling risks", () => {
    const suspended = parseService(
      claudeService({
        risk: {
          state: "suspended",
          code: "organization_disabled",
          message: "This organization has been disabled.",
          http_status: 403,
          observed_at: createdAt,
          occurrences: 2,
        },
      }),
    );
    expect(suspended.subscription?.risk).toEqual({
      state: "suspended",
      code: "organization_disabled",
      message: "This organization has been disabled.",
      http_status: 403,
      observed_at: createdAt,
      occurrences: 2,
    });

    const cooling = parseService(
      claudeService({
        risk: {
          state: "cooling",
          code: "rate_limit_5h",
          observed_at: createdAt,
          paused_until: "2026-07-28T15:00:00Z",
        },
      }),
    );
    expect(cooling.subscription?.risk?.paused_until).toBe(
      "2026-07-28T15:00:00Z",
    );
  });

  it("rejects malformed risks", () => {
    const base = {
      state: "suspended",
      code: "account_deactivated",
      observed_at: createdAt,
    };
    for (const [risk, message] of [
      [{ ...base, extra: true }, /risk\.extra: unexpected field/],
      [{ ...base, state: "banned" }, /risk\.state: unknown risk state/],
      [{ state: "suspended", observed_at: createdAt }, /risk\.code: missing/],
      [{ ...base, code: "Bad-Code" }, /risk\.code: invalid risk code/],
      [{ ...base, observed_at: "yesterday" }, /risk\.observed_at/],
      [
        { ...base, paused_until: "2026-07-28T15:00:00Z" },
        /only cooling may set paused_until/,
      ],
      [{ ...base, state: "cooling" }, /risk\.paused_until/],
      [{ ...base, http_status: 99 }, /risk\.http_status/],
      [{ ...base, http_status: 403.5 }, /risk\.http_status/],
      [{ ...base, occurrences: 0 }, /risk\.occurrences/],
      [{ ...base, message: "x".repeat(241) }, /risk\.message/],
      [
        { ...base, message: `Bearer ${"a".repeat(24)}` },
        /must not contain credential material/,
      ],
    ] as const) {
      expect(() => parseService(claudeService({ risk }))).toThrow(message);
    }
  });

  it("treats an elapsed cooling risk as inactive", () => {
    const cooling = (pausedUntil: string) =>
      parseService(
        claudeService({
          risk: {
            state: "cooling",
            code: "forbidden",
            observed_at: createdAt,
            paused_until: pausedUntil,
          },
        }),
      );
    expect(activeServiceRisk(cooling("2026-07-28T13:00:00Z"), now)?.code).toBe(
      "forbidden",
    );
    expect(
      activeServiceRisk(cooling("2026-07-28T12:30:00Z"), now),
    ).toBeUndefined();
    const suspended = parseService(
      claudeService({
        risk: {
          state: "suspended",
          code: "repeated_forbidden",
          observed_at: createdAt,
        },
      }),
    );
    expect(activeServiceRisk(suspended, now)?.state).toBe("suspended");
    expect(activeServiceRisk(parseService(claudeService({})), now)).toBe(
      undefined,
    );
  });

  it("labels known and unknown risk codes", () => {
    expect(subscriptionRiskLabel("organization_disabled", "suspended")).toBe(
      "组织已被上游停用",
    );
    expect(subscriptionRiskLabel("rate_limit_7d", "cooling")).toBe(
      "已达 7 天用量上限",
    );
    expect(subscriptionRiskLabel("brand_new_signal", "suspended")).toBe(
      "上游风控已暂停此账号",
    );
    expect(subscriptionRiskLabel("brand_new_signal", "cooling")).toBe(
      "上游暂时限制了此账号",
    );
    expect(subscriptionRiskLabel(undefined, "cleared")).toBe("已恢复调度");
  });

  it("adds the active risk and the reauthorization reason to the status label", () => {
    const suspended = parseService(
      claudeService({
        risk: {
          state: "suspended",
          code: "oauth_not_allowed",
          observed_at: createdAt,
        },
      }),
    );
    expect(serviceStatusLabel(suspended, now)).toBe(
      "已连接 · 上游不允许此账号通过 OAuth 访问",
    );

    const expired = parseService(
      claudeService({
        status: "needs_reauth",
        last_error: {
          code: "refresh_failed",
          message: "Refresh token was revoked.",
        },
      }),
    );
    expect(serviceStatusLabel(expired, now)).toBe(
      "需要重新登录：Refresh token was revoked.",
    );
    expect(
      serviceStatusLabel(
        parseService(
          claudeService({
            status: "error",
            last_error: { code: "upstream_error", message: "boom" },
          }),
        ),
        now,
      ),
    ).toBe("连接异常");
  });
});

describe("parseSubscriptionRiskEvents", () => {
  const event = {
    id: 7,
    service_id: "service_claude_personal",
    kind: "suspended",
    code: "organization_disabled",
    message: "This organization has been disabled.",
    http_status: 403,
    observed_at: createdAt,
  };

  it("parses a newest-first event page for one service", () => {
    expect(
      parseSubscriptionRiskEvents(
        {
          items: [
            {
              id: 8,
              service_id: "service_claude_personal",
              kind: "cleared",
              observed_at: "2026-07-28T13:00:00Z",
            },
            event,
            {
              id: 6,
              service_id: "service_claude_personal",
              kind: "cooling",
              code: "rate_limit_5h",
              observed_at: "2026-07-28T11:00:00Z",
              paused_until: "2026-07-28T16:00:00Z",
            },
          ],
        },
        "service_claude_personal",
      ).map((item) => item.kind),
    ).toEqual(["cleared", "suspended", "cooling"]);
  });

  it("rejects malformed events and events of another service", () => {
    for (const [value, message] of [
      [{ items: [event], next_cursor: null }, /\$\.next_cursor/],
      [{ items: [{ ...event, id: 0 }] }, /items\[0\]\.id/],
      [{ items: [{ ...event, id: "7" }] }, /items\[0\]\.id/],
      [{ items: [{ ...event, kind: "banned" }] }, /unknown risk event kind/],
      [{ items: [{ ...event, service_id: "Bad" }] }, /service_id/],
      [{ items: [{ ...event, extra: 1 }] }, /unexpected field/],
      [
        { items: [{ ...event, service_id: "service_other" }] },
        /belongs to another service/,
      ],
      [{ items: Array.from({ length: 51 }, () => event) }, /at most 50/],
    ] as const) {
      expect(() =>
        parseSubscriptionRiskEvents(value, "service_claude_personal"),
      ).toThrow(message);
    }
  });
});

describe("bestConversionTarget", () => {
  const edges = [
    {
      from: "openai.chat",
      to: "anthropic.messages",
      quality: "good" as const,
      streaming: true,
    },
    {
      from: "openai.chat",
      to: "google.generate_content",
      quality: "fair" as const,
      streaming: true,
    },
    {
      from: "openai.chat",
      to: "openai.responses",
      quality: "fair" as const,
      streaming: true,
    },
  ];
  it.each([
    {
      name: "quality before target order",
      upstream: ["openai.responses", "anthropic.messages"],
      edges,
      available: true,
      expected: "anthropic.messages",
    },
    {
      name: "fixed order breaks fair ties",
      upstream: ["google.generate_content", "openai.responses"],
      edges,
      available: true,
      expected: "openai.responses",
    },
    {
      name: "fixed order breaks good ties",
      upstream: [
        "google.generate_content",
        "openai.responses",
        "anthropic.messages",
      ],
      edges: edges.map((edge) => ({ ...edge, quality: "good" as const })),
      available: true,
      expected: "openai.responses",
    },
    {
      name: "discouraged excluded",
      upstream: ["anthropic.messages"],
      edges: [{ ...edges[0], quality: "discouraged" as const }],
      available: true,
      expected: undefined,
    },
    {
      name: "unadvertised edge excluded",
      upstream: ["openai.responses"],
      edges: [edges[0]],
      available: true,
      expected: undefined,
    },
    {
      name: "target must be upstream",
      upstream: ["openai.models"],
      edges,
      available: true,
      expected: undefined,
    },
    {
      name: "unavailable engine",
      upstream: ["anthropic.messages"],
      edges,
      available: false,
      expected: undefined,
    },
    {
      name: "wrong source excluded",
      upstream: ["anthropic.messages"],
      edges: [{ ...edges[0], from: "openai.responses" }],
      available: true,
      expected: undefined,
    },
  ])("$name", ({ upstream, edges, available, expected }) => {
    expect(
      bestConversionTarget("openai.chat", upstream, { available, edges }),
    ).toBe(expected);
  });
  it("does not infer edges without a snapshot", () => {
    expect(
      bestConversionTarget("openai.chat", ["openai.responses"], null),
    ).toBeUndefined();
    expect(
      bestConversionTarget("openai.chat", ["openai.responses"]),
    ).toBeUndefined();
  });
});
