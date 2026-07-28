import { describe, expect, it } from "vitest";

import {
  parseService,
  parseServicePage,
  parseServiceRecord,
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

  it("requires the connection variant selected by kind", () => {
    expect(() =>
      parseService({
        id: "service_wrong",
        name: "Wrong",
        kind: "codex_subscription",
        enabled: true,
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

  it("parses service records with strong ETags", () => {
    const record = parseServiceRecord({
      service: {
        id: "service_codex_work",
        name: "Codex work",
        kind: "codex_subscription",
        enabled: true,
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
});
