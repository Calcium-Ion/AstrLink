import { describe, expect, it } from "vitest";

import { endpointPreset } from "./endpoint-presets";

describe("endpoint product presets", () => {
  it("configures new-api as a bearer-authenticated delegated multi-protocol gateway", () => {
    const preset = endpointPreset("newapi");

    expect(preset.kind).toBe("newapi");
    expect(preset.authScheme).toBe("bearer");
    expect(preset.baseURL).toBe("");
    expect(preset.capabilities).toHaveLength(8);
    expect(new Set(preset.capabilities.map(({ mode }) => mode))).toEqual(
      new Set(["delegated"]),
    );
    expect(preset.capabilities.map(({ protocol }) => protocol)).toEqual([
      "openai.responses",
      "openai.responses.compact",
      "anthropic.messages",
      "google.generate_content",
      "openai.chat",
      "openai.completions",
      "openai.models",
      "google.models",
    ]);
  });

  it("uses conservative per-key matrices for Sub2API-style subscriptions", () => {
    expect(endpointPreset("subscription_openai")).toMatchObject({
      kind: "custom",
      authScheme: "bearer",
      capabilities: [
        { protocol: "openai.responses", mode: "delegated", streaming: true },
        { protocol: "openai.chat", mode: "delegated", streaming: true },
        { protocol: "openai.models", mode: "delegated", streaming: false },
      ],
    });
    expect(endpointPreset("subscription_anthropic")).toMatchObject({
      kind: "custom",
      authScheme: "bearer",
      capabilities: [
        { protocol: "anthropic.messages", mode: "delegated", streaming: true },
        // Sub2API returns this route for Anthropic groups, although its item
        // shape is closer to Anthropic than strict OpenAI model metadata.
        { protocol: "openai.models", mode: "delegated", streaming: false },
      ],
    });
    expect(endpointPreset("subscription_gemini")).toMatchObject({
      kind: "custom",
      authScheme: "bearer",
      capabilities: [
        {
          protocol: "google.generate_content",
          mode: "delegated",
          streaming: true,
        },
        { protocol: "google.models", mode: "delegated", streaming: false },
      ],
    });
  });

  it("keeps OpenAI-compatible subscriptions intentionally narrow and native", () => {
    expect(endpointPreset("openai_compatible")).toMatchObject({
      kind: "openai_compatible",
      baseURL: "",
      authScheme: "bearer",
      capabilities: [
        { protocol: "openai.chat", mode: "native", streaming: true },
        {
          protocol: "openai.completions",
          mode: "native",
          streaming: true,
        },
        { protocol: "openai.models", mode: "native", streaming: false },
      ],
    });
  });

  it("uses exact provider-specific auth and native capabilities for official services", () => {
    expect(endpointPreset("openai")).toMatchObject({
      baseURL: "https://api.openai.com/v1",
      authScheme: "bearer",
      capabilities: [
        { protocol: "openai.responses", mode: "native", streaming: true },
        {
          protocol: "openai.responses.compact",
          mode: "native",
          streaming: false,
        },
        { protocol: "openai.chat", mode: "native", streaming: true },
        {
          protocol: "openai.completions",
          mode: "native",
          streaming: true,
        },
        { protocol: "openai.models", mode: "native", streaming: false },
      ],
    });
    expect(endpointPreset("anthropic")).toMatchObject({
      baseURL: "https://api.anthropic.com",
      authScheme: "anthropic_api_key",
      capabilities: [
        {
          protocol: "anthropic.messages",
          mode: "native",
          streaming: true,
        },
      ],
    });
    expect(endpointPreset("gemini")).toMatchObject({
      baseURL: "https://generativelanguage.googleapis.com",
      authScheme: "google_api_key",
      capabilities: [
        {
          protocol: "google.generate_content",
          mode: "native",
          streaming: true,
        },
        { protocol: "google.models", mode: "native", streaming: false },
      ],
    });
  });

  it("opens advanced settings and requires an explicit capability for custom services", () => {
    expect(endpointPreset("custom")).toMatchObject({
      authScheme: "bearer",
      capabilities: [],
      advancedOnStart: true,
    });
  });
});
