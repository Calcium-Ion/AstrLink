import { describe, expect, it } from "vitest";

import {
  detectKinds,
  existingMatch,
  importModels,
  insecureAddress,
  normalizedAddress,
  parseLocalImportOpened,
  parseLocalImportRead,
  planCandidate,
  probeFailure,
  saveAddress,
  servicesSharingAddresses,
  type LocalImportCandidate,
} from "./local-import-model";
import type { Service } from "./service-model";

const session = "0123456789abcdef0123456789abcdef";

function candidate(
  overrides: Partial<LocalImportCandidate> = {},
): LocalImportCandidate {
  return {
    id: "c0",
    name: null,
    address: "https://api.relayhub.example/v1",
    protocols: ["openai.responses"],
    models: [],
    key: { status: "found", hint: "…k2Qe" },
    sources: [{ tool: "codex", wsl: null }],
    ...overrides,
  };
}

function service(
  id: string,
  baseURL: string,
  credentialHint?: string,
): Service {
  return {
    id,
    name: id,
    kind: "newapi",
    enabled: true,
    models: [],
    capabilities: [],
    http: {
      base_url: baseURL,
      auth: { scheme: "bearer" },
      ...(credentialHint ? { credential_hint: credentialHint } : {}),
    },
    created_at: "2026-10-31T00:00:00Z",
    updated_at: "2026-10-31T00:00:00Z",
  };
}

describe("local import IPC", () => {
  it("parses the located sources", () => {
    expect(
      parseLocalImportOpened({
        session,
        sources: [
          {
            id: "s0",
            tool: "codex",
            wsl: null,
            paths: ["~/.codex/config.toml", "~/.codex/auth.json"],
          },
          {
            id: "s1",
            tool: "pi",
            wsl: "Ubuntu",
            paths: ["~/.pi/agent/models.json"],
          },
        ],
      }),
    ).toEqual({
      session,
      sources: [
        {
          id: "s0",
          tool: "codex",
          wsl: null,
          paths: ["~/.codex/config.toml", "~/.codex/auth.json"],
        },
        {
          id: "s1",
          tool: "pi",
          wsl: "Ubuntu",
          paths: ["~/.pi/agent/models.json"],
        },
      ],
    });
    expect(() =>
      parseLocalImportOpened({ session: "short", sources: [] }),
    ).toThrow("$.session");
    expect(() =>
      parseLocalImportOpened({
        session,
        sources: [{ id: "s0", tool: "cursor", wsl: null, paths: [] }],
      }),
    ).toThrow("$.sources[0].tool");
  });

  it("accepts key hints but never a key", () => {
    const read = {
      candidates: [
        {
          id: "c0",
          name: "Relay Hub",
          address: "https://api.relayhub.example/v1",
          protocols: ["openai.responses"],
          models: ["relay-large"],
          key: { status: "found", hint: "…k2Qe" },
          sources: [{ tool: "codex", wsl: null }],
        },
        {
          id: "c1",
          name: null,
          address: "https://api.deepseek.com/v1",
          protocols: [],
          models: [],
          key: { status: "env", name: "DEEPSEEK_API_KEY" },
          sources: [{ tool: "pi", wsl: "Ubuntu" }],
        },
      ],
      skipped: [
        {
          reason: "chatgpt_login",
          tool: "codex",
          wsl: null,
          name: null,
          path: null,
          detail: null,
        },
      ],
    };
    expect(parseLocalImportRead(read).candidates[1].key).toEqual({
      status: "env",
      name: "DEEPSEEK_API_KEY",
    });

    const withKey = structuredClone(read);
    withKey.candidates[0].key = {
      status: "found",
      hint: "fake-relay-key-k2Qe",
    };
    expect(() => parseLocalImportRead(withKey)).toThrow(
      "$.candidates[0].key.hint",
    );
    const extra = structuredClone(read) as {
      candidates: Record<string, unknown>[];
    };
    extra.candidates[0].secret = "fake-relay-key-k2Qe";
    expect(() => parseLocalImportRead(extra)).toThrow("$.candidates[0].secret");
    const reason = structuredClone(read);
    reason.skipped[0].reason = "other";
    expect(() => parseLocalImportRead(reason)).toThrow("$.skipped[0].reason");
  });
});

describe("addresses and kinds", () => {
  it("normalizes the address the way the host merges", () => {
    expect(normalizedAddress("https://Relay.example.com/v1/")).toBe(
      "https://relay.example.com",
    );
    expect(normalizedAddress("https://relay.example.com:443/")).toBe(
      "https://relay.example.com",
    );
    expect(normalizedAddress("https://relay.example.com/claude/v1")).toBe(
      "https://relay.example.com/claude",
    );
    expect(normalizedAddress("not a url")).toBeNull();
  });

  it("flags plain http to another machine", () => {
    expect(insecureAddress("http://192.168.1.2:3000")).toBe(true);
    expect(insecureAddress("http://127.0.0.1:3425")).toBe(false);
    expect(insecureAddress("http://localhost:3456")).toBe(false);
    expect(insecureAddress("https://relay.example.com")).toBe(false);
  });

  it("detects presets by host and path, coding plans first", () => {
    expect(detectKinds("https://open.bigmodel.cn/api/anthropic")).toEqual([
      "glm_coding",
      "glm",
    ]);
    expect(detectKinds("https://open.bigmodel.cn/api/coding/paas/v4")).toEqual([
      "glm_coding",
    ]);
    expect(detectKinds("https://open.bigmodel.cn/api/paas/v4/")).toEqual([
      "glm",
    ]);
    expect(detectKinds("https://api.minimaxi.com/anthropic")).toEqual([
      "minimax_coding",
      "minimax",
    ]);
    expect(detectKinds("https://api.kimi.com/coding/")).toEqual([
      "kimi_coding",
    ]);
    expect(detectKinds("https://api.deepseek.com/anthropic")).toEqual([
      "deepseek",
    ]);
    expect(detectKinds("https://api.openai.com/v1")).toEqual(["openai"]);
    expect(detectKinds("https://api.anthropic.com")).toEqual(["anthropic"]);
    expect(detectKinds("http://127.0.0.1:3425")).toEqual(["magpie"]);
    expect(detectKinds("https://api.relayhub.example/v1")).toEqual(["newapi"]);
  });

  it("saves the preset path on the host the tool used", () => {
    expect(
      saveAddress("https://open.bigmodel.cn/api/anthropic", "glm_coding"),
    ).toBe("https://open.bigmodel.cn/api/coding/paas/v4");
    expect(saveAddress("https://api.z.ai/api/anthropic", "glm")).toBe(
      "https://api.z.ai/api/paas/v4",
    );
    // The previous Kimi domain stays: the key only goes where it was used.
    expect(saveAddress("https://api.kimi.com/coding", "kimi_coding")).toBe(
      "https://api.kimi.com/coding",
    );
    expect(saveAddress("https://api.relayhub.example/v1/", "newapi")).toBe(
      "https://api.relayhub.example/v1",
    );
  });
});

describe("planning", () => {
  it("keeps a relay to the formats the tools used, plus its model list", () => {
    const plan = planCandidate(
      candidate({
        name: "Relay Hub",
        protocols: ["openai.responses", "anthropic.messages"],
        models: ["relay-large"],
      }),
      "newapi",
      [],
    );
    expect(plan).toMatchObject({
      kind: "newapi",
      defaultName: "Relay Hub",
      baseURL: "https://api.relayhub.example/v1",
      auth: { scheme: "bearer" },
      keyIncluded: true,
      needsKey: false,
      models: ["relay-large"],
    });
    expect(plan?.capabilities.map((capability) => capability.protocol)).toEqual(
      ["openai.responses", "anthropic.messages", "openai.models"],
    );
  });

  it("gives a preset its own formats, address and models", () => {
    const plan = planCandidate(
      candidate({
        address: "https://open.bigmodel.cn/api/anthropic",
        protocols: ["anthropic.messages"],
        key: { status: "env", name: "GLM_KEY" },
      }),
      "glm_coding",
      [],
    );
    expect(plan).toMatchObject({
      kind: "glm_coding",
      baseURL: "https://open.bigmodel.cn/api/coding/paas/v4",
      needsKey: true,
      models: ["glm-5.3", "glm-5.3-flash"],
    });
    expect(plan?.capabilities.map((capability) => capability.protocol)).toEqual(
      expect.arrayContaining([
        "openai.responses",
        "anthropic.messages",
        "openai.chat",
      ]),
    );
  });

  it("tells a repeat from a provider at the same address", () => {
    const found = candidate({ address: "https://api.relayhub.example" });
    const services = [
      service("svc_other", "https://elsewhere.example"),
      service("svc_relay", "https://api.relayhub.example/v1/"),
    ];
    expect(
      servicesSharingAddresses([found], services).map((item) => item.id),
    ).toEqual(["svc_relay"]);
    expect(
      existingMatch(found, services, new Map([["svc_relay", "…k2Qe"]])),
    ).toEqual({ service: services[1], sameKey: true });
    expect(
      existingMatch(found, services, new Map([["svc_relay", "…zzzz"]])),
    ).toEqual({ service: services[1], sameKey: false });
    expect(
      existingMatch(
        candidate({ address: "https://new.example" }),
        services,
        new Map(),
      ),
    ).toBeNull();
    // A preset's saved address counts as the same place.
    const glm = candidate({
      address: "https://open.bigmodel.cn/api/anthropic",
    });
    expect(
      existingMatch(
        glm,
        [service("svc_glm", "https://open.bigmodel.cn/api/coding/paas/v4")],
        new Map([["svc_glm", "…k2Qe"]]),
      )?.sameKey,
    ).toBe(true);
  });

  it("never saves an empty model list", () => {
    const relay = candidate({ models: ["relay-large"] });
    const plan = planCandidate(relay, "newapi", [])!;
    expect(importModels(["a", "relay-large"], relay, plan)).toEqual({
      models: ["a", "relay-large"],
      origin: "fetched",
    });
    expect(importModels(["a"], relay, plan)).toEqual({
      models: ["a", "relay-large"],
      origin: "fetched",
    });
    expect(importModels(null, relay, plan)).toEqual({
      models: ["relay-large"],
      origin: "config",
    });
    const glm = candidate({
      address: "https://open.bigmodel.cn/api/anthropic",
    });
    expect(
      importModels([], glm, planCandidate(glm, "glm_coding", [])!),
    ).toEqual({ models: ["glm-5.3", "glm-5.3-flash"], origin: "preset" });
    const bare = candidate();
    expect(
      importModels([], bare, planCandidate(bare, "newapi", [])!),
    ).toBeNull();
  });

  it("names why a model list failed without its message", () => {
    expect(probeFailure(new Error("upstream model discovery timed out"))).toBe(
      "timeout",
    );
    expect(
      probeFailure(new Error("upstream model discovery failed (HTTP 401)")),
    ).toBe("auth");
    expect(
      probeFailure(new Error("upstream model discovery failed (HTTP 404)")),
    ).toBe("notFound");
    expect(probeFailure("connection refused")).toBe("other");
  });
});
