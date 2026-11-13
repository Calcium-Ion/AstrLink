// @vitest-environment happy-dom

import { beforeEach, describe, expect, it } from "vitest";

import type { Service } from "./service-model";
import {
  SERVICE_TERMINAL_STORAGE_KEY,
  parseTerminalChoices,
  readTerminalMemory,
  rememberTerminalSession,
  serviceModelOptions,
  suggestedClient,
  suggestedModel,
} from "./service-terminal-model";

function service(overrides: Partial<Service> = {}): Service {
  return {
    id: "service_glm",
    name: "GLM",
    kind: "openai_compatible",
    enabled: true,
    models: ["glm-5", "glm-4.6"],
    capabilities: [
      { protocol: "anthropic.messages", mode: "native", streaming: true },
    ],
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    ...overrides,
  };
}

describe("service terminal model", () => {
  beforeEach(() => localStorage.clear());

  it("parses only the terminal apps the desktop knows", () => {
    expect(
      parseTerminalChoices([
        { id: "terminal", name: "Terminal" },
        { id: "warp", name: "Warp" },
      ]),
    ).toEqual([
      { id: "terminal", name: "Terminal" },
      { id: "warp", name: "Warp" },
    ]);
    expect(() =>
      parseTerminalChoices([{ id: "hyper", name: "Hyper" }]),
    ).toThrow();
    expect(() => parseTerminalChoices({})).toThrow();
  });

  it("offers the provider's models and the names its redirects take", () => {
    expect(
      serviceModelOptions(
        service({
          model_redirects: [
            { from: "claude-sonnet-5-5", to: "glm-5", enabled: true },
            { from: "claude-haiku-5-5", to: "glm-4.6", enabled: false },
            { from: "glm-5", to: "glm-5", enabled: true },
          ],
        }),
      ),
    ).toEqual(["glm-5", "glm-4.6", "claude-sonnet-5-5"]);
  });

  it("suggests the client a subscription or its models fit", () => {
    expect(suggestedClient(service({ kind: "codex_subscription" }))).toBe(
      "codex",
    );
    expect(suggestedClient(service({ kind: "claude_subscription" }))).toBe(
      "claude",
    );
    expect(suggestedClient(service({ models: ["gpt-5.5", "o3"] }))).toBe(
      "codex",
    );
    expect(
      suggestedClient(service({ models: ["gpt-5.5", "claude-opus-5-5"] })),
    ).toBe("claude");
    expect(suggestedClient(service())).toBe("claude");
  });

  it("starts from the client's own model only where the provider serves it", () => {
    const memory = readTerminalMemory();
    expect(
      suggestedModel(
        service({ models: ["claude-opus-5-5"] }),
        "claude",
        memory,
      ),
    ).toBe("");
    expect(suggestedModel(service(), "claude", memory)).toBe("glm-5");
    expect(
      suggestedModel(service({ models: ["gpt-5.5"] }), "codex", memory),
    ).toBe("");
    expect(suggestedModel(service({ models: [] }), "codex", memory)).toBe("");
    // A Gemini provider lists its oldest model first; start on its newest.
    expect(
      suggestedModel(
        service({
          kind: "gemini",
          models: [
            "gemini-2.5-flash",
            "gemini-3-pro-image",
            "gemini-3.8-flash",
          ],
        }),
        "claude",
        memory,
      ),
    ).toBe("gemini-3.8-flash");
  });

  it("remembers each session's choices for the next one", () => {
    let memory = readTerminalMemory();
    for (const directory of ["/a", "/b", "/c", "/d", "/e", "/a", "/f"]) {
      memory = rememberTerminalSession(memory, {
        service_id: "service_glm",
        client: "claude",
        directory,
        model: null,
        terminal: "warp",
      });
    }
    memory = rememberTerminalSession(memory, {
      service_id: "service_glm",
      client: "codex",
      directory: "/f",
      model: "glm-4.6",
      terminal: "iterm2",
    });
    const saved = readTerminalMemory();
    expect(saved).toEqual(memory);
    expect(saved.directories).toEqual(["/f", "/a", "/e", "/d", "/c"]);
    expect(saved.terminal).toBe("iterm2");
    expect(saved.clients).toEqual({ service_glm: "codex" });
    // A blank model is a choice too: the client's own.
    expect(suggestedModel(service(), "claude", saved)).toBe("");
    expect(suggestedModel(service(), "codex", saved)).toBe("glm-4.6");
  });

  it("ignores malformed saved choices", () => {
    localStorage.setItem(
      SERVICE_TERMINAL_STORAGE_KEY,
      JSON.stringify({
        terminal: "hyper",
        directories: ["/ok", 3],
        clients: { a: "pi", b: "codex" },
        models: { "a:claude": 1, "b:codex": "gpt-5.5" },
      }),
    );
    expect(readTerminalMemory()).toEqual({
      terminal: null,
      directories: ["/ok"],
      clients: { b: "codex" },
      models: { "b:codex": "gpt-5.5" },
    });
    localStorage.setItem(SERVICE_TERMINAL_STORAGE_KEY, "{");
    expect(readTerminalMemory().directories).toEqual([]);
  });
});
