import { describe, expect, it } from "vitest";

import {
  httpServicePreset,
  payAsYouGoPresetIDs,
  codingPlanPresetIDs,
  httpServicePresetIDs,
  httpServicePresetLabel,
  localConversionTargets,
  protocolEntryPath,
  supportsLocalConversion,
} from "./service-presets";

describe("HTTP service product presets", () => {
  it("configures new-api as a bearer-authenticated passthrough multi-protocol gateway", () => {
    const preset = httpServicePreset("newapi");

    expect(preset.kind).toBe("newapi");
    expect(preset.authScheme).toBe("bearer");
    expect(preset.baseURL).toBe("");
    expect(preset.capabilities).toHaveLength(8);
    expect(new Set(preset.capabilities.map(({ mode }) => mode))).toEqual(
      new Set(["native"]),
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
      "opencode_go", "opencode_zen", "kimi_coding", "glm_coding", "minimax_coding",
      "newapi",
      "openai_compatible",
      "openai",
      "anthropic",
      "gemini",
      "deepseek", "qwen", "moonshot", "glm", "minimax", "doubao", "xai",
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
        { protocol: "openai.models", mode: "native", streaming: false },
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
        { protocol: "openai.chat", mode: "native", streaming: true, convert_to: "google.generate_content" },
      ],
    });
  });

  it("keeps usage-based providers separate from coding plans and advertises only supported protocols", () => {
    expect(payAsYouGoPresetIDs).toContain("opencode_zen");
    expect(codingPlanPresetIDs).toContain("opencode_go");
    expect(payAsYouGoPresetIDs.some((kind) => codingPlanPresetIDs.includes(kind))).toBe(false);
    for (const [kind, baseURL, discovery] of [
      ["deepseek", "https://api.deepseek.com/v1", true],
      ["qwen", "https://dashscope.aliyuncs.com/compatible-mode/v1", false],
      ["moonshot", "https://api.moonshot.cn/v1", true],
      ["glm", "https://open.bigmodel.cn/api/paas/v4", false],
      ["minimax", "https://api.minimax.cn/v1", true],
      ["doubao", "https://ark.cn-beijing.volces.com/api/v3", false],
      ["xai", "https://api.x.ai/v1", true],
    ] as const) {
      const preset = httpServicePreset(kind);
      expect(payAsYouGoPresetIDs).toContain(kind);
      expect(preset).toMatchObject({ kind, baseURL, authScheme: "bearer", advancedOnStart: false });
      expect(preset.capabilities).toContainEqual({ protocol: "openai.chat", mode: "native", streaming: true });
      expect(preset.capabilities.some(({ protocol }) => protocol === "openai.models")).toBe(discovery);
      expect(preset.capabilities).toContainEqual({ protocol: "anthropic.messages", mode: "native", streaming: true });
      expect(preset.capabilities.some(({ protocol }) => protocol === "openai.responses")).toBe(kind !== "glm");
      expect(preset.capabilities.some(({ protocol }) => protocol === "openai.responses.compact")).toBe(kind === "xai");
      expect(preset.capabilities.some(({ protocol }) => protocol === "openai.completions")).toBe(kind === "xai");
      expect(preset.capabilities.every(({ mode, convert_to }) => mode === "native" && convert_to === undefined)).toBe(true);
    }
  });

  it("lists local conversion targets and enables only advertised edges", () => {
    expect(supportsLocalConversion("openai.chat")).toBe(true);
    expect(supportsLocalConversion("openai.models")).toBe(false);
    // Neither variant has an advertised edge, so offering them would only ever
    // render permanently disabled options.
    expect(supportsLocalConversion("openai.responses.compact")).toBe(false);
    expect(supportsLocalConversion("openai.completions")).toBe(false);
    expect(
      localConversionTargets("anthropic.messages", {
        available: true,
        edges: [
          {
            from: "anthropic.messages",
            to: "openai.chat",
            quality: "fair",
            streaming: true,
          },
        ],
      }).filter((target) => target.enabled),
    ).toEqual([
      { id: "openai.chat", enabled: true, quality: "fair", streaming: true },
    ]);
    expect(
      localConversionTargets("openai.chat", { available: false, edges: [] }).every(
        (target) => !target.enabled && target.quality === null,
      ),
    ).toBe(true);
  });

  it("maps protocol IDs to the inference-plane entry path", () => {
    expect(protocolEntryPath("anthropic.messages")).toBe("/v1/messages");
    expect(protocolEntryPath("openai.responses")).toBe("/v1/responses");
    expect(protocolEntryPath("openai.chat")).toBe("/v1/chat/completions");
    expect(protocolEntryPath("google.generate_content")).toBe(
      "/v1beta/models/:model:generateContent",
    );
    expect(
      protocolEntryPath("google.generate_content", { streaming: true }),
    ).toBe("/v1beta/models/:model:streamGenerateContent");
    expect(protocolEntryPath("vendor.custom")).toBe("vendor.custom");
  });

  it("opens advanced settings and requires an explicit capability for custom services", () => {
    expect(httpServicePreset("custom")).toMatchObject({
      authScheme: "bearer",
      capabilities: [],
      advancedOnStart: true,
    });
  });
});
