import type { Service } from "./service-model";

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
}

export function testableCapabilities(service: Service) {
  return service.capabilities.filter(
    (capability) =>
      !capability.convert_to &&
      serviceTestProtocols.includes(capability.protocol as ServiceTestProtocol),
  );
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
  return item as unknown as ServiceTestResult;
}
