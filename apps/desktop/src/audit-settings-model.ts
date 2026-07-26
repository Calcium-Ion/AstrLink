export interface AuditSettings {
  request_body_enabled: boolean;
  response_content_enabled: boolean;
  request_body_max_bytes: number;
  response_content_max_bytes: number;
  metadata_retention_days: number;
  content_retention_days: number;
}

export interface AuditSettingsPatch {
  request_body_enabled?: boolean;
  response_content_enabled?: boolean;
  request_body_max_bytes?: number;
  response_content_max_bytes?: number;
  metadata_retention_days?: number;
  content_retention_days?: number;
  audit_risk_acknowledged?: boolean;
}

type JsonObject = Record<string, unknown>;

function invalid(path: string, message: string): never {
  throw new Error(`审计设置数据无效（${path}）：${message}`);
}

function objectAt(value: unknown, path: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalid(path, "应为对象");
  }
  return value as JsonObject;
}

function boolAt(value: unknown, path: string): boolean {
  if (typeof value !== "boolean") {
    return invalid(path, "应为布尔值");
  }
  return value;
}

function intAt(value: unknown, path: string): number {
  if (typeof value !== "number" || !Number.isInteger(value)) {
    return invalid(path, "应为整数");
  }
  return value;
}

export function parseAuditSettings(value: unknown): AuditSettings {
  const settings = objectAt(value, "$");
  return {
    request_body_enabled: boolAt(
      settings.request_body_enabled,
      "$.request_body_enabled",
    ),
    response_content_enabled: boolAt(
      settings.response_content_enabled,
      "$.response_content_enabled",
    ),
    request_body_max_bytes: intAt(
      settings.request_body_max_bytes,
      "$.request_body_max_bytes",
    ),
    response_content_max_bytes: intAt(
      settings.response_content_max_bytes,
      "$.response_content_max_bytes",
    ),
    metadata_retention_days: intAt(
      settings.metadata_retention_days,
      "$.metadata_retention_days",
    ),
    content_retention_days: intAt(
      settings.content_retention_days,
      "$.content_retention_days",
    ),
  };
}
