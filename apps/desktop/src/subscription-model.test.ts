import { describe, expect, it } from "vitest";

import {
  parseAuthorizationSession,
  parseBeginCodexAuthorizationResult,
} from "./subscription-model";

const timestamps = {
  created_at: "2026-07-28T08:00:00Z",
  updated_at: "2026-07-28T08:05:00Z",
};

describe("subscription IPC contract", () => {
  it("parses pending authorization sessions with https URLs", () => {
    const session = parseAuthorizationSession({
      id: "authorization_01",
      provider: "openai_codex",
      status: "pending",
      flow: "browser",
      service_id: "service_codex_01",
      authorization_url: "https://auth.example/oauth/authorize",
      expires_at: "2026-07-28T08:10:00Z",
      created_at: timestamps.created_at,
      updated_at: timestamps.updated_at,
    });
    expect(session.status).toBe("pending");
    expect(session.authorization_url).toBe(
      "https://auth.example/oauth/authorize",
    );
  });

  it("allows loopback http authorization URLs for pending sessions", () => {
    expect(
      parseAuthorizationSession({
        id: "authorization_01",
        provider: "openai_codex",
        status: "pending",
        flow: "browser",
        service_id: "service_codex_01",
        authorization_url: "http://127.0.0.1:8765/oauth/callback",
        expires_at: "2026-07-28T08:10:00Z",
        created_at: timestamps.created_at,
        updated_at: timestamps.updated_at,
      }).authorization_url,
    ).toBe("http://127.0.0.1:8765/oauth/callback");
  });

  it("parses begin authorization outcomes without token fields", () => {
    const session = parseBeginCodexAuthorizationResult({
      kind: "session",
      session: {
        id: "authorization_01",
        provider: "openai_codex",
        status: "pending",
        flow: "browser",
        service_id: "service_codex_01",
        authorization_url: "https://auth.example/oauth/authorize",
        expires_at: "2026-07-28T08:10:00Z",
        created_at: timestamps.created_at,
        updated_at: timestamps.updated_at,
      },
    });
    expect(session.kind).toBe("session");
    expect(session.session.id).toBe("authorization_01");
  });

  it("accepts Grok Device Code sessions and rejects other Grok flows", () => {
    const grok = {
      id: "authorization_grok",
      provider: "xai_grok",
      status: "pending",
      flow: "device_code",
      service_id: "service_grok_01",
      device_code: {
        verification_url:
          "https://accounts.x.ai/oauth2/device?user_code=GROK-CODE",
        user_code: "GROK-CODE",
      },
      expires_at: "2026-07-28T08:15:00Z",
      created_at: timestamps.created_at,
      updated_at: timestamps.updated_at,
    };
    expect(parseAuthorizationSession(grok).provider).toBe("xai_grok");
    expect(() =>
      parseAuthorizationSession({
        ...grok,
        flow: "browser",
        device_code: undefined,
        authorization_url: "https://auth.x.ai/oauth2/authorize",
      }),
    ).toThrow(/unsupported by provider/);
    expect(() =>
      parseAuthorizationSession({ ...grok, flow: "authorization_code" }),
    ).toThrow(/unsupported by provider/);
  });

  it("accepts Droid Device Code sessions and rejects other Droid flows", () => {
    const droid = {
      id: "authorization_droid",
      provider: "factory_droid",
      status: "pending",
      flow: "device_code",
      service_id: "service_droid_01",
      device_code: {
        verification_url: "https://auth.factory.ai/device?user_code=DROI-D001",
        user_code: "DROI-D001",
      },
      expires_at: "2026-10-10T08:15:00Z",
      created_at: timestamps.created_at,
      updated_at: timestamps.updated_at,
    };
    expect(parseAuthorizationSession(droid).provider).toBe("factory_droid");
    expect(() =>
      parseAuthorizationSession({
        ...droid,
        flow: "browser",
        device_code: undefined,
        authorization_url: "https://auth.factory.ai/oauth/authorize",
      }),
    ).toThrow(/unsupported by provider/);
  });

  it("accepts Copilot Device Code sessions and rejects other Copilot flows", () => {
    const copilot = {
      id: "authorization_copilot",
      provider: "github_copilot",
      status: "pending",
      flow: "device_code",
      service_id: "service_copilot_01",
      device_code: {
        verification_url: "https://github.com/login/device",
        user_code: "COPI-LOT1",
      },
      expires_at: "2026-10-05T08:15:00Z",
      created_at: timestamps.created_at,
      updated_at: timestamps.updated_at,
    };
    expect(parseAuthorizationSession(copilot).provider).toBe("github_copilot");
    expect(() =>
      parseAuthorizationSession({
        ...copilot,
        flow: "browser",
        device_code: undefined,
        authorization_url: "https://github.com/login/oauth/authorize",
      }),
    ).toThrow(/unsupported by provider/);
  });

  it("parses Device Code sessions and rejects mixed or terminal instructions", () => {
    const pending = {
      id: "authorization_02",
      provider: "openai_codex",
      status: "pending",
      flow: "device_code",
      service_id: "service_codex_01",
      device_code: {
        verification_url: "https://auth.openai.com/codex/device",
        user_code: "ABCD-EFGH",
      },
      expires_at: "2026-07-28T08:15:00Z",
      created_at: timestamps.created_at,
      updated_at: timestamps.updated_at,
    };
    expect(parseAuthorizationSession(pending).device_code?.user_code).toBe(
      "ABCD-EFGH",
    );
    expect(() =>
      parseAuthorizationSession({
        ...pending,
        authorization_url: "https://auth.openai.com/oauth/authorize",
      }),
    ).toThrow("must not include authorization_url");
    expect(() =>
      parseAuthorizationSession({
        ...pending,
        status: "completed",
      }),
    ).toThrow("terminal session must not include login instructions");
  });

  it("rejects remote http authorization URLs", () => {
    expect(() =>
      parseAuthorizationSession({
        id: "authorization_01",
        provider: "openai_codex",
        status: "pending",
        flow: "browser",
        service_id: "service_codex_01",
        authorization_url: "http://auth.example/oauth/authorize",
        expires_at: "2026-07-28T08:10:00Z",
        created_at: timestamps.created_at,
        updated_at: timestamps.updated_at,
      }),
    ).toThrow("authorization URL http is only allowed on loopback");
  });
});
