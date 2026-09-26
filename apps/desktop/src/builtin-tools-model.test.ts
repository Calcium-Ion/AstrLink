import { describe, expect, it } from "vitest";
import { defaultBuiltinTools, parseBuiltinTools } from "./builtin-tools-model";

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
});
