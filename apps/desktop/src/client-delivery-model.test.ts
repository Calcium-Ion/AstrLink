import { describe, expect, it } from "vitest";

import type { ClientConfigStatus } from "./client-config-model";
import {
  autoFillClaude,
  defaultModel,
  checkedByDefault,
  clientMatches,
  draftModels,
  draftProblem,
  homeKey,
  initialDraft,
  leadPlace,
  modelCandidates,
  modelProtocol,
  newestGemini,
  newestModel,
  placeKey,
  protocolBaseURL,
  railGroupOf,
  requiredEntry,
} from "./client-delivery-model";
import type { ConversionEngineCapability } from "./core-model";
import type { Service } from "./service-model";

function service(overrides: Partial<Service>): Service {
  return {
    id: "service_a",
    name: "A",
    kind: "openai_compatible",
    enabled: true,
    models: [],
    capabilities: [],
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    ...overrides,
  };
}

const engine: ConversionEngineCapability = {
  name: "relaykit",
  version: null,
  available: true,
  edges: [
    {
      from: "openai.responses",
      to: "openai.chat",
      quality: "good",
      streaming: true,
    },
  ],
};

function status(overrides: Partial<ClientConfigStatus>): ClientConfigStatus {
  return {
    client: "claude",
    wsl: null,
    target: null,
    detected: true,
    home: "/Users/me",
    dir: "/Users/me/.claude",
    paths: ["/Users/me/.claude/settings.json"],
    exists: [true],
    state: "not_configured",
    token_id: null,
    models: {},
    notes: [],
    paste_since_unix: null,
    paste_into: [],
    ...overrides,
  };
}

describe("Claude Code model names", () => {
  it("picks each family's newest model, preferring an alias over its snapshot", () => {
    const models = [
      "claude-sonnet-4-5-20250929",
      "claude-sonnet-5",
      "claude-sonnet-5-20260801",
      "claude-opus-4-7",
      "claude-opus-5-5",
      "claude-3-5-haiku-20241022",
      "claude-haiku-4-5",
      "gpt-5.6",
    ];
    expect(newestModel(models, "sonnet")).toBe("claude-sonnet-5");
    expect(newestModel(models, "opus")).toBe("claude-opus-5-5");
    expect(newestModel(models, "haiku")).toBe("claude-haiku-4-5");
    expect(autoFillClaude(models)).toEqual({
      model: "claude-sonnet-5",
      haikuModel: "claude-haiku-4-5",
      sonnetModel: "claude-sonnet-5",
      opusModel: "claude-opus-5-5",
    });
    expect(autoFillClaude(["gpt-5.6"])).toEqual({
      model: "",
      haikuModel: "",
      sonnetModel: "",
      opusModel: "",
    });
  });

  it("reads the current lineup and Copilot's dotted ids", () => {
    const anthropic = [
      "claude-fable-5",
      "claude-fable-5-1",
      "claude-fable-5-2",
      "claude-haiku-4-5-20251001",
      "claude-haiku-5-5",
      "claude-opus-4-8",
      "claude-opus-5",
      "claude-opus-5-5",
      "claude-sonnet-4-6",
      "claude-sonnet-5",
      "claude-sonnet-5-5",
    ];
    expect(autoFillClaude(anthropic)).toEqual({
      model: "claude-sonnet-5-5",
      haikuModel: "claude-haiku-5-5",
      sonnetModel: "claude-sonnet-5-5",
      opusModel: "claude-opus-5-5",
    });
    expect(newestModel(anthropic, "fable")).toBe("claude-fable-5-2");
    const copilot = [
      "claude-fable-5.1",
      "claude-fable-5.2",
      "claude-haiku-4.5",
    ];
    expect(newestModel(copilot, "fable")).toBe("claude-fable-5.2");
    expect(newestModel(copilot, "haiku")).toBe("claude-haiku-4.5");
  });
});

describe("default model", () => {
  it("picks the newest Gemini text model over media and older lines", () => {
    const gemini = [
      "gemini-2.5-flash",
      "gemini-2.5-pro",
      "gemini-3-flash-preview",
      "gemini-3.1-pro-preview",
      "gemini-3.8-flash",
      "gemini-3.8-flash-lite-tts",
      "gemini-3.8-live",
      "gemini-4-argon",
      "gemini-4-carbon",
      "gemini-embedding-2",
      "gemini-flash-latest",
    ];
    expect(newestGemini(gemini)).toBe("gemini-4-argon");
    expect(newestGemini(["gemini-3.8-flash-lite", "gemini-3.8-flash"])).toBe(
      "gemini-3.8-flash",
    );
    expect(
      newestGemini(["gemini-3.8-flash", "gemini-4.5-live", "gemini-5-tts"]),
    ).toBe("gemini-3.8-flash");
    expect(newestGemini(["gemini-3.1-pro-preview", "gemini-3.1-pro"])).toBe(
      "gemini-3.1-pro",
    );
    expect(newestGemini(["gemini-embedding-001", "text-embedding"])).toBe(
      undefined,
    );
    expect(defaultModel(gemini)).toBe("gemini-4-argon");
  });

  it("moves a sorted Claude list to its newest main model and keeps others first", () => {
    expect(
      defaultModel([
        "claude-haiku-4-5",
        "claude-opus-5-5",
        "claude-sonnet-5-5",
      ]),
    ).toBe("claude-sonnet-5-5");
    expect(defaultModel(["claude-fable-5", "claude-fable-5-2"])).toBe(
      "claude-fable-5-2",
    );
    expect(defaultModel(["deepseek-v4", "claude-sonnet-5-5"])).toBe(
      "deepseek-v4",
    );
    expect(defaultModel(["claude-custom"])).toBe("claude-custom");
    expect(defaultModel([])).toBe("");
  });

  it("follows the provider's kind when the sorted list starts with an agent", () => {
    const google = [
      "antigravity-preview-09-2026",
      "deep-research-preview-04-2026",
      "gemini-2.5-flash",
      "gemini-3.8-flash",
    ];
    expect(defaultModel(google)).toBe("antigravity-preview-09-2026");
    expect(defaultModel(google, [{ kind: "gemini", models: google }])).toBe(
      "gemini-3.8-flash",
    );
    expect(
      defaultModel(google, [{ kind: "openai_compatible", models: google }]),
    ).toBe("antigravity-preview-09-2026");
    expect(
      initialDraft({ client: "gemini", models: {} }, google, [
        { kind: "gemini", models: google },
      ]).model,
    ).toBe("gemini-3.8-flash");
  });
});

describe("model candidates", () => {
  const services = [
    service({
      id: "service_chat",
      name: "Chat",
      models: ["chat-model", "vendor/model"],
      capabilities: [
        { protocol: "openai.chat", mode: "native", streaming: true },
      ],
    }),
    service({
      id: "service_off",
      enabled: false,
      models: ["off-model"],
      capabilities: [
        { protocol: "openai.responses", mode: "native", streaming: true },
      ],
    }),
    service({
      id: "service_sub",
      models: ["sub-model"],
      capabilities: [
        { protocol: "openai.responses", mode: "native", streaming: true },
      ],
      subscription: {
        provider: "openai_codex",
        status: "disconnected",
        credential_ref: "local://subscription/service_sub",
      } as Service["subscription"],
    }),
  ];
  const catalog = {
    services,
    redirects: [
      { from: "alias", to: "chat-model", enabled: true },
      { from: "astrlink/auto", to: "chat-model", enabled: true },
      { from: "off-alias", to: "chat-model", enabled: false },
    ],
  };

  it("offers models a usable service takes natively or through a conversion", () => {
    expect(modelCandidates(catalog, ["openai.responses"], engine)).toEqual([
      "chat-model",
      "vendor/model",
      "alias",
    ]);
    expect(modelCandidates(catalog, ["openai.responses"], null)).toEqual([]);
    expect(modelCandidates(catalog, ["openai.chat"], null)).toEqual([
      "chat-model",
      "vendor/model",
      "alias",
    ]);
  });

  it("leaves out models Gemini CLI cannot put in its URL path", () => {
    expect(
      modelCandidates(
        {
          ...catalog,
          services: services.map((item) => ({
            ...item,
            capabilities: [
              {
                protocol: "google.generate_content",
                mode: "native" as const,
                streaming: true,
              },
            ],
          })),
        },
        ["google.generate_content"],
        null,
      ),
    ).toEqual(["chat-model", "alias"]);
  });

  it("offers what any of a client's protocols reaches, in service order", () => {
    expect(
      modelCandidates(catalog, ["openai.responses", "openai.chat"], null),
    ).toEqual(["chat-model", "vendor/model", "alias"]);
    expect(
      modelCandidates(
        catalog,
        ["google.generate_content", "openai.chat"],
        null,
      ),
    ).toEqual(["chat-model", "vendor/model", "alias"]);
  });

  it("names the entry a model needs", () => {
    expect(
      requiredEntry(catalog, "chat-model", "openai.responses", engine),
    ).toMatchObject({ service: { id: "service_chat" }, target: "openai.chat" });
    expect(requiredEntry(catalog, "chat-model", "openai.chat", engine)).toBe(
      undefined,
    );
  });
});

describe("drafts and default checks", () => {
  it("starts from the config's models, else the first candidate", () => {
    expect(
      initialDraft({ client: "codex", models: {} }, ["a", "b"]).model,
    ).toBe("a");
    expect(
      initialDraft(
        { client: "codex", models: { model: "b", reasoningEffort: "high" } },
        ["a", "b"],
      ),
    ).toMatchObject({ model: "b", reasoningEffort: "high" });
    expect(
      initialDraft({ client: "gemini", models: {} }, [
        "gemini-2.5-pro",
        "gemini-4-carbon",
      ]).model,
    ).toBe("gemini-4-carbon");
    expect(
      initialDraft({ client: "gemini", models: { model: "gemini-2.5-pro" } }, [
        "gemini-2.5-pro",
        "gemini-4-carbon",
      ]).model,
    ).toBe("gemini-2.5-pro");
    // Claude Code fills its tiers only while the config holds none.
    expect(
      initialDraft({ client: "claude", models: { opusModel: "x" } }, [
        "claude-sonnet-5",
      ]),
    ).toMatchObject({ model: "", sonnetModel: "", opusModel: "x" });
  });

  it("writes only filled fields and blocks a client without its model", () => {
    const draft = initialDraft({ client: "pi", models: {} }, ["a"]);
    const pi = { id: "pi", list: true, protocols: ["openai.responses"] };
    expect(
      draftModels(
        pi,
        { ...draft, contextWindow: "400000" },
        ["a", "b"],
        () => "openai.responses",
      ),
    ).toEqual({ model: "a", models: ["a", "b"], contextWindow: 400000 });
    expect(draftProblem("pi", { ...draft, maxTokens: "12k" })).toBe("limits");
    expect(draftProblem("gemini", { ...draft, model: " " })).toBe("model");
    expect(draftProblem("claude", { ...draft, model: "" })).toBeNull();
  });

  it.each([
    ["an installed client nobody set up", {}, false],
    ["a missing client", { detected: false }, false],
    [
      "this token's config",
      { state: "configured", token_id: "token_01" },
      true,
    ],
    ["a port to update", { state: "outdated", token_id: "token_01" }, true],
    ["an edited config", { state: "modified", token_id: "token_01" }, false],
    [
      "another token's config",
      { state: "configured", token_id: "token_02" },
      false,
    ],
    ["an unreadable file", { state: "invalid" }, false],
    ["a client that is open", { notes: ["running"] }, false],
  ] as const)("checks %s by default: %s", (_, overrides, expected) => {
    expect(
      checkedByDefault(
        status(overrides as Partial<ClientConfigStatus>),
        "token_01",
        false,
      ),
    ).toBe(expected);
  });

  it("checks what needs attention when the dialog opens on it", () => {
    expect(
      checkedByDefault(
        status({ state: "modified", token_id: "token_01" }),
        "token_01",
        true,
      ),
    ).toBe(true);
    expect(
      checkedByDefault(status({ state: "invalid" }), "token_01", true),
    ).toBe(false);
  });
});

describe("protocols per model", () => {
  const both = service({
    id: "service_both",
    models: ["claude-sonnet-5", "gpt-5.6", "glm-5"],
    capabilities: [
      { protocol: "anthropic.messages", mode: "native", streaming: true },
      { protocol: "openai.responses", mode: "native", streaming: true },
      { protocol: "openai.chat", mode: "native", streaming: true },
    ],
  });
  const chat = service({
    id: "service_chat",
    models: ["chat-only"],
    capabilities: [
      { protocol: "openai.chat", mode: "native", streaming: true },
    ],
  });
  const converted = service({
    id: "service_conv",
    models: ["conv-model"],
    capabilities: [
      { protocol: "openai.chat", mode: "native", streaming: true },
      {
        protocol: "openai.responses",
        mode: "native",
        streaming: true,
        convert_to: "openai.chat",
      },
    ],
  });
  const catalog = { services: [both, chat, converted], redirects: [] };
  const all = ["anthropic.messages", "openai.responses", "openai.chat"];

  it("writes a model with its family's protocol when the gateway serves it natively", () => {
    expect(modelProtocol(catalog, "claude-sonnet-5", all, engine)).toBe(
      "anthropic.messages",
    );
    expect(modelProtocol(catalog, "gpt-5.6", all, engine)).toBe(
      "openai.responses",
    );
    expect(modelProtocol(catalog, "glm-5", all, engine)).toBe("openai.chat");
    // Without its family's protocol, Chat comes next.
    expect(
      modelProtocol(
        catalog,
        "claude-sonnet-5",
        ["openai.responses", "openai.chat"],
        engine,
      ),
    ).toBe("openai.chat");
  });

  it("prefers a native entry over one that converts or one to enable", () => {
    expect(modelProtocol(catalog, "chat-only", all, engine)).toBe(
      "openai.chat",
    );
    expect(
      modelProtocol(
        catalog,
        "conv-model",
        ["openai.responses", "openai.chat"],
        engine,
      ),
    ).toBe("openai.chat");
    // With only a converted entry, it takes that one.
    expect(
      modelProtocol(
        catalog,
        "conv-model",
        ["anthropic.messages", "openai.responses"],
        engine,
      ),
    ).toBe("openai.responses");
    // A model no protocol reaches keeps the client's first.
    expect(
      modelProtocol(
        catalog,
        "unknown",
        ["anthropic.messages", "openai.responses"],
        null,
      ),
    ).toBe("anthropic.messages");
    // Gemini cannot carry a model with "/" in its path.
    expect(
      modelProtocol(
        {
          services: [
            service({
              models: ["v/m"],
              capabilities: [
                {
                  protocol: "google.generate_content",
                  mode: "native",
                  streaming: true,
                },
              ],
            }),
          ],
          redirects: [],
        },
        "v/m",
        ["google.generate_content", "openai.chat"],
        null,
      ),
    ).toBe("openai.chat");
  });

  it("names each written model's protocol for a client that takes several", () => {
    const draft = initialDraft({ client: "workbuddy", models: {} }, ["glm-5"]);
    const entry = {
      id: "workbuddy",
      list: true,
      protocols: ["openai.chat", "openai.responses"],
    };
    expect(
      draftModels(entry, draft, ["glm-5", "gpt-5.6"], (model) =>
        modelProtocol(catalog, model, entry.protocols, engine),
      ),
    ).toEqual({
      model: "glm-5",
      models: ["glm-5", "gpt-5.6"],
      modelProtocols: { "glm-5": "openai.chat", "gpt-5.6": "openai.responses" },
    });
    // One protocol needs no names; a client without a list writes one model.
    expect(
      draftModels(
        { id: "kimi_code", list: false, protocols: ["openai.chat"] },
        draft,
        ["glm-5", "gpt-5.6"],
        () => "openai.chat",
      ),
    ).toEqual({ model: "glm-5" });
  });

  it("gives OpenAI protocols the /v1 address", () => {
    const url = "http://127.0.0.1:8317";
    expect(protocolBaseURL("openai.chat", url)).toBe(`${url}/v1`);
    expect(protocolBaseURL("openai.responses", url)).toBe(`${url}/v1`);
    expect(protocolBaseURL("anthropic.messages", url)).toBe(url);
    expect(protocolBaseURL("google.generate_content", url)).toBe(url);
  });
});

describe("rail places and groups", () => {
  const overview = {
    clients: [
      status({ client: "claude" }),
      status({ client: "codex", detected: false }),
      status({
        client: "gemini",
        detected: false,
        state: "configured",
        token_id: "token_01",
      }),
      status({ client: "workbuddy", target: "cn", detected: false }),
      status({ client: "workbuddy", target: "intl" }),
      status({ client: "codex", wsl: "Ubuntu" }),
    ],
    installed: ["cherry_studio"],
  };

  it("keys places by client, home and target", () => {
    expect(placeKey({ client: "codex", wsl: null, target: null })).toBe(
      "codex::",
    );
    expect(
      placeKey({ client: "workbuddy", wsl: "Ubuntu", target: "intl" }),
    ).toBe("workbuddy:Ubuntu:intl");
    expect(homeKey({ client: "workbuddy", wsl: "Ubuntu" })).toBe(
      "workbuddy:Ubuntu",
    );
  });

  it("groups a client by what AstrLink found and what it can do", () => {
    const group = (
      id: string,
      kind: "file" | "link" | "guide" | "unsupported",
    ) => railGroupOf({ id, kind }, overview);
    expect(group("claude", "file")).toBe("installed");
    // Found in WSL only, or holding a config, still counts as installed.
    expect(group("codex", "file")).toBe("installed");
    expect(group("gemini", "file")).toBe("installed");
    // One installed target is enough.
    expect(group("workbuddy", "file")).toBe("installed");
    expect(group("pi", "file")).toBe("missing");
    expect(group("cherry_studio", "link")).toBe("installed");
    expect(group("aionui", "link")).toBe("missing");
    expect(group("trae", "guide")).toBe("guide");
    expect(group("cursor", "unsupported")).toBe("unsupported");
  });

  it("matches a search by name or id, ignoring case, spaces and separators", () => {
    const entry = { id: "deepseek_harness", name: "DeepSeek Harness" };
    for (const query of [
      "",
      "deep",
      "SEEK H",
      "deepseekharness",
      "seek_har",
      "harness-",
    ]) {
      expect(clientMatches(entry, query)).toBe(true);
    }
    expect(clientMatches(entry, "kimi")).toBe(false);
  });

  it("shows a home's state from the place that matters most", () => {
    const cn = status({ client: "workbuddy", target: "cn", detected: false });
    const intl = status({ client: "workbuddy", target: "intl" });
    const owned = status({
      client: "workbuddy",
      target: "intl",
      state: "configured",
      token_id: "token_01",
    });
    const other = status({
      client: "workbuddy",
      target: "cn",
      state: "configured",
      token_id: "token_02",
    });
    const never = () => false;
    expect(leadPlace([cn, intl], "token_01", never)).toBe(intl);
    expect(leadPlace([other, owned], "token_01", never)).toBe(owned);
    expect(leadPlace([cn, other], "token_01", never)).toBe(other);
    expect(leadPlace([cn, intl], "token_01", (item) => item === cn)).toBe(cn);
    expect(leadPlace([], "token_01", never)).toBeUndefined();
  });
});
