import { describe, expect, it } from "vitest";
import {
  defaultBuiltinTools,
  isImageGenerationModel,
  parseBuiltinTools,
  runsImageGenerationTool,
} from "./builtin-tools-model";

describe("builtin tools configuration", () => {
  it("defaults off and accepts both independent backend types", () => {
    const tools = defaultBuiltinTools();
    expect(tools.web_search.enabled).toBe(false);
    tools.web_search = {
      enabled: true,
      backend: "external",
      base_url: "https://search.example",
    };
    tools.image_generation = {
      enabled: true,
      backend: "upstream",
      service_id: "service_images",
      model: "conversation-model",
    };
    expect(parseBuiltinTools(tools)).toEqual(tools);
  });
  it("rejects incomplete enabled tools, embedded credentials and unknown fields", () => {
    const tools = defaultBuiltinTools();
    for (const web_search of [
      { enabled: true, backend: "external" },
      { enabled: true, backend: "upstream", model: "main" },
      {
        enabled: false,
        backend: "external",
        base_url: "https://key:secret@example.com",
      },
      { enabled: false, backend: "upstream", secret: "private" },
    ])
      expect(() => parseBuiltinTools({ ...tools, web_search })).toThrow();
  });
  it("limits the provider Images API to image generation with a model", () => {
    const tools = defaultBuiltinTools();
    const image_generation = {
      enabled: true,
      backend: "service_images" as const,
      service_id: "newapi_main",
      model: "gpt-image-1",
    };
    expect(parseBuiltinTools({ ...tools, image_generation })).toEqual({
      ...tools,
      image_generation,
    });
    expect(() =>
      parseBuiltinTools({ ...tools, web_search: image_generation }),
    ).toThrow();
    expect(() =>
      parseBuiltinTools({
        ...tools,
        image_generation: { ...image_generation, model: "" },
      }),
    ).toThrow();
  });
  it("recognizes image generation models by name", () => {
    for (const model of [
      "gpt-image-1",
      "gpt-image-2-all",
      "dall-e-3",
      "imagen-4.0-generate-001",
      "gemini-2.5-flash-image",
      "black-forest-labs/FLUX.1-dev",
      "doubao-seedream-4-0-250828",
      "qwen-image",
      "wan2.5-t2i-preview",
      "image-01",
    ])
      expect(isImageGenerationModel(model)).toBe(true);
    for (const model of [
      "gpt-5",
      "claude-sonnet-5",
      "gemini-2.5-flash",
      "MiniMax-M3",
      "kling-v1-image2video",
    ])
      expect(isImageGenerationModel(model)).toBe(false);
  });
  it("runs the Responses image tool only on OpenAI chat models", () => {
    for (const model of ["gpt-5", "gpt-4.1-mini", "o3", "openai/gpt-5.1-codex"])
      expect(runsImageGenerationTool(model)).toBe(true);
    for (const model of [
      "claude-sonnet-5",
      "gemini-2.5-flash",
      "grok-4",
      "gpt-image-1",
      "gpt-oss-120b",
      "gpt-4o-mini-tts",
    ])
      expect(runsImageGenerationTool(model)).toBe(false);
  });
});
