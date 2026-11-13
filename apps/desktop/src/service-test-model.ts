import { serviceModelFormats, type Service } from "./service-model";

export const serviceTestProtocols = [
  "openai.responses",
  "openai.chat",
  "openai.completions",
  "anthropic.messages",
  "google.generate_content",
] as const;
export type ServiceTestProtocol = (typeof serviceTestProtocols)[number];
export interface ServiceTestInput {
  protocol: ServiceTestProtocol;
  model: string;
  stream: boolean;
  prompt?: string;
}
/** A file a test produced, as a task plugin's provider generates one. */
export interface ServiceTestMedia {
  kind: "image" | "video" | "audio" | "file";
  /** The gateway's link to it, relative in the web console. */
  url: string;
  mime_type?: string;
  /** The plugin's key for the file, such as image-1. */
  name: string;
}

const mediaKinds = ["image", "video", "audio", "file"] as const;

/**
 * Whether a link is a task's file on the gateway: an http address, or a
 * path, under /v1/tasks/<id>/artifacts/. Nothing else loads in a preview.
 */
function gatewayFileLink(url: string): boolean {
  let parsed: URL;
  try {
    parsed = new URL(url, "http://gateway.invalid");
  } catch {
    return false;
  }
  return (
    (parsed.protocol === "http:" || parsed.protocol === "https:") &&
    !parsed.username &&
    !parsed.password &&
    parsed.pathname.startsWith("/v1/tasks/") &&
    parsed.pathname.includes("/artifacts/")
  );
}

function parseMedia(value: unknown): ServiceTestMedia[] {
  if (!Array.isArray(value)) throw new Error("Invalid provider test media");
  return value.map((entry) => {
    const item = (entry ?? {}) as Record<string, unknown>;
    if (
      !mediaKinds.includes(item.kind as ServiceTestMedia["kind"]) ||
      typeof item.url !== "string" ||
      !gatewayFileLink(item.url) ||
      typeof item.name !== "string" ||
      (item.mime_type !== undefined && typeof item.mime_type !== "string")
    )
      throw new Error("Invalid provider test media");
    return {
      kind: item.kind as ServiceTestMedia["kind"],
      url: item.url,
      name: item.name,
      ...(typeof item.mime_type === "string" && item.mime_type
        ? { mime_type: item.mime_type }
        : {}),
    };
  });
}

export interface ServiceTestResult extends ServiceTestInput {
  service_id: string;
  ok: boolean;
  status_code: number;
  duration_ms: number;
  response_headers_ms?: number | null;
  first_token_ms?: number | null;
  output: string;
  raw_response?: string;
  raw_response_truncated?: boolean;
  response_content_type?: string;
  error_code?: string;
  message?: string;
  /** What a task plugin's provider generated: images, videos, music. */
  media?: ServiceTestMedia[];
}

export function testableCapabilities(
  service: Service,
  model = service.models[0] ?? "",
) {
  if (service.kind === "vertex") {
    const preferred = vertexTestProtocol(service, model);
    return serviceModelFormats(service.kind, service.http?.auth.scheme)
      .map((protocol) => ({
        protocol,
        mode: "native" as const,
        streaming: true,
      }))
      .sort(
        (a, b) =>
          Number(b.protocol === preferred) - Number(a.protocol === preferred),
      );
  }
  const capabilities = service.capabilities.filter(
    (capability) =>
      !capability.convert_to &&
      serviceTestProtocols.includes(capability.protocol as ServiceTestProtocol),
  );
  return capabilities;
}

/** A native connection-test recommendation, never an inference routing default. */
export function vertexTestProtocol(
  service: Service,
  model: string,
): ServiceTestProtocol | undefined {
  const explicit = service.model_protocols?.find(
    (entry) => entry.model === model,
  )?.protocol;
  if (explicit)
    return serviceModelFormats("vertex", service.http?.auth.scheme).includes(
      explicit,
    )
      ? explicit
      : undefined;
  if (/^gemini-[A-Za-z0-9._@-]+$/.test(model)) return "google.generate_content";
  if (service.http?.auth.scheme === "google_api_key") return undefined;
  if (/^claude-[A-Za-z0-9._@-]+$/.test(model)) return "anthropic.messages";
  if (/^[A-Za-z0-9][A-Za-z0-9._@-]*\/[A-Za-z0-9][A-Za-z0-9._@-]*$/.test(model))
    return "openai.chat";
  return undefined;
}

export function parseServiceTestResult(value: unknown): ServiceTestResult {
  if (!value || typeof value !== "object")
    throw new Error("Invalid provider test result");
  const item = value as Record<string, unknown>;
  if (
    typeof item.service_id !== "string" ||
    typeof item.model !== "string" ||
    !serviceTestProtocols.includes(item.protocol as ServiceTestProtocol) ||
    typeof item.stream !== "boolean" ||
    typeof item.ok !== "boolean" ||
    typeof item.output !== "string" ||
    !Number.isInteger(item.status_code) ||
    Number(item.status_code) < 0 ||
    Number(item.status_code) > 599 ||
    !Number.isSafeInteger(item.duration_ms) ||
    Number(item.duration_ms) < 0 ||
    (item.error_code !== undefined && typeof item.error_code !== "string") ||
    (item.message !== undefined && typeof item.message !== "string") ||
    (item.raw_response !== undefined &&
      typeof item.raw_response !== "string") ||
    (item.raw_response_truncated !== undefined &&
      typeof item.raw_response_truncated !== "boolean") ||
    (item.response_content_type !== undefined &&
      typeof item.response_content_type !== "string")
  )
    throw new Error("Invalid provider test result");
  for (const key of ["response_headers_ms", "first_token_ms"]) {
    if (
      item[key] != null &&
      (!Number.isSafeInteger(item[key]) ||
        Number(item[key]) < 0 ||
        Number(item[key]) > Number(item.duration_ms))
    ) {
      throw new Error("Invalid provider test timing");
    }
  }
  if (
    item.first_token_ms != null &&
    (item.stream !== true ||
      item.response_headers_ms == null ||
      Number(item.first_token_ms) < Number(item.response_headers_ms))
  ) {
    throw new Error("Invalid provider test timing");
  }
  const result = item as unknown as ServiceTestResult;
  return item.media === undefined
    ? result
    : { ...result, media: parseMedia(item.media) };
}
