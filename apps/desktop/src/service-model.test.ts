import { describe, expect, it } from "vitest";

import {
  hasPlanUsage,
  parseService,
  parseServicePage,
  parseServiceRecord,
  parseServiceModelProbe,
} from "./service-model";

const createdAt = "2026-07-28T12:00:00Z";

describe("service model", () => {
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
    expect(page.items[1].http?.base_url).toBe(
      "https://gateway.example/v1",
    );
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
  it("covers connected subscriptions and API-key coding plans with a quota route", () => {
    const http = { base_url: "https://api.kimi.com/coding", auth: { scheme: "bearer" as const } };
    expect(hasPlanUsage({ kind: "kimi_coding", http })).toBe(true);
    expect(hasPlanUsage({ kind: "glm_coding", http })).toBe(true);
    expect(hasPlanUsage({ kind: "minimax_coding", http })).toBe(true);
    expect(hasPlanUsage({ kind: "opencode_go", http })).toBe(true);
    expect(hasPlanUsage({ kind: "opencode_zen", http })).toBe(false);
    expect(hasPlanUsage({ kind: "newapi", http })).toBe(false);
    expect(hasPlanUsage({ kind: "kimi_coding" })).toBe(false);
    expect(hasPlanUsage({ kind: "claude_subscription", subscription: { status: "connected" } })).toBe(true);
    expect(hasPlanUsage({ kind: "codex_subscription", subscription: { status: "disconnected" } })).toBe(false);
  });
});
