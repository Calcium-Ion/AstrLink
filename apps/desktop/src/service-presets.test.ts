import { describe, expect, it } from "vitest";

import {
  httpServicePreset,
  httpServicePresetIDs,
  httpServicePresetLabel,
} from "./service-presets";

describe("HTTP service product presets", () => {
  it("configures new-api as a bearer-authenticated delegated multi-protocol gateway", () => {
    const preset = httpServicePreset("newapi");

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

  it("does not expose API-key services as Codex, Claude, or Gemini subscriptions", () => {
    expect(httpServicePresetIDs).toEqual([
      "newapi",
      "openai_compatible",
      "openai",
      "anthropic",
      "gemini",
      "custom",
    ]);
    expect(
      httpServicePresetIDs.map(httpServicePresetLabel).join(" "),
    ).not.toContain("订阅");
  });

  it("keeps OpenAI-compatible API services intentionally narrow and native", () => {
    expect(httpServicePreset("openai_compatible")).toMatchObject({
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
    expect(httpServicePreset("openai")).toMatchObject({
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
    expect(httpServicePreset("anthropic")).toMatchObject({
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
    expect(httpServicePreset("gemini")).toMatchObject({
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
    expect(httpServicePreset("custom")).toMatchObject({
      authScheme: "bearer",
      capabilities: [],
      advancedOnStart: true,
    });
  });
});
