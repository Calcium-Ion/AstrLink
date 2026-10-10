import type { ServiceKind } from "./service-model";

export type BuiltinToolKind = "web_search" | "image_generation";
export interface BuiltinTool {
  enabled: boolean;
  backend: "upstream" | "external" | "service_images" | "";
  service_id?: string;
  model?: string;
  base_url?: string;
}
export type BuiltinTools = Record<BuiltinToolKind, BuiltinTool>;

/**
 * Provider kinds that generate images with their own key: through the OpenAI
 * Images endpoints, or through MiniMax's image API.
 */
export const builtinImagesServiceKinds: readonly ServiceKind[] = [
  "newapi",
  "openai",
  "openai_compatible",
  "minimax",
  "minimax_coding",
];

/** Image models suggested for providers whose model list may omit them. */
export const builtinImageModels: Partial<
  Record<ServiceKind, readonly string[]>
> = {
  minimax: ["image-01", "image-01-live"],
  minimax_coding: ["image-01", "image-01-live"],
};

const imageModelPattern =
  /image|dall-e|flux|seedream|stable-diffusion|sdxl|sd3|cogview|wanx|t2i|recraft|ideogram|kolors|jimeng|hidream/i;

/**
 * Reports whether a model ID names an image generation model. Provider model
 * lists carry only IDs, so the Images API picker goes by known model names.
 */
export function isImageGenerationModel(model: string): boolean {
  return imageModelPattern.test(model) && !/video/i.test(model);
}

/**
 * Reports whether a chat model can run the Responses image_generation tool,
 * which only OpenAI's GPT and o-series models offer.
 */
export function runsImageGenerationTool(model: string): boolean {
  return (
    /(^|\/)(gpt-\d|o\d)/i.test(model) &&
    !/realtime|audio|transcribe|tts|search/i.test(model) &&
    !isImageGenerationModel(model)
  );
}

export function defaultBuiltinTools(): BuiltinTools {
  return {
    web_search: { enabled: false, backend: "upstream" },
    image_generation: { enabled: false, backend: "upstream" },
  };
}

export function parseBuiltinTools(value: unknown): BuiltinTools {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw Error("invalid builtin tools");
  const tools = value as Record<string, unknown>;
  if (
    Object.keys(tools).length !== 2 ||
    !("web_search" in tools) ||
    !("image_generation" in tools)
  )
    throw Error("invalid builtin tools fields");
  for (const kind of ["web_search", "image_generation"] as const) {
    const value = tools[kind];
    if (!value || typeof value !== "object" || Array.isArray(value))
      throw Error("invalid builtin tool");
    const tool = value as Record<string, unknown>;
    if (
      typeof tool.enabled !== "boolean" ||
      !["", "upstream", "external", "service_images"].includes(
        tool.backend as string,
      ) ||
      (tool.backend === "service_images" && kind !== "image_generation")
    )
      throw Error("invalid builtin tool backend");
    for (const [key, value] of Object.entries(tool)) {
      if (key === "enabled") continue;
      if (
        !["backend", "service_id", "model", "base_url"].includes(key) ||
        typeof value !== "string"
      )
        throw Error("invalid builtin tool field");
    }
    const model = tool.model as string | undefined;
    if (
      model &&
      ([...model].length > 256 ||
        [...model].some((character) => {
          const code = character.codePointAt(0)!;
          return code < 32 || code === 127;
        }))
    )
      throw Error("invalid builtin tool model");
    if (
      tool.service_id &&
      !/^[a-z][a-z0-9_-]{2,95}$/.test(tool.service_id as string)
    )
      throw Error("invalid builtin tool provider");
    if (tool.base_url) {
      const url = new URL(tool.base_url as string);
      if (
        (tool.base_url as string).length > 2048 ||
        !["http:", "https:"].includes(url.protocol) ||
        !url.hostname ||
        url.username ||
        url.password ||
        url.search ||
        url.hash
      )
        throw Error("invalid builtin tool API URL");
    }
    if (
      tool.enabled &&
      (!tool.backend ||
        ((tool.backend === "upstream" || tool.backend === "service_images") &&
          (!tool.service_id || !tool.model)) ||
        (tool.backend === "external" &&
          (!tool.base_url || (kind === "image_generation" && !tool.model))))
    )
      throw Error("builtin tool configuration is incomplete");
  }
  return structuredClone(tools) as BuiltinTools;
}
