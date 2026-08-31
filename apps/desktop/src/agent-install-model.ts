export type AgentToolId = "cursor" | "claude" | "codex";

export interface AgentToolStatus {
  id: AgentToolId;
  detected: boolean;
  skill_installed: boolean;
  mcp_installed: boolean;
}

export interface AgentInstallStatus {
  canonical_skill: boolean;
  mcp_binary: boolean;
  mcp_command: string | null;
  tools: AgentToolStatus[];
  preview_paths: string[];
}

export interface AgentInstallReceipt {
  version: number;
  bundle: string;
  bundle_version: string;
  installed_at_unix: number;
  mcp_binary: string;
  files: string[];
}

const TOOL_IDS: readonly AgentToolId[] = ["cursor", "claude", "codex"];

function invalid(path: string, detail: string): never {
  throw new Error(`Invalid AstrLink agent-install IPC at ${path}: ${detail}`);
}

function objectAt(value: unknown, path: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalid(path, "expected an object");
  }
  return value as Record<string, unknown>;
}

function exactKeys(
  value: Record<string, unknown>,
  expected: readonly string[],
  path: string,
): void {
  const keys = new Set(expected);
  for (const key of Object.keys(value)) {
    if (!keys.has(key)) invalid(`${path}.${key}`, "unexpected field");
  }
  for (const key of expected) {
    if (!Object.hasOwn(value, key)) invalid(`${path}.${key}`, "missing field");
  }
}

function boundedString(value: unknown, path: string, max = 4096): string {
  if (typeof value !== "string" || value.length === 0 || value.length > max) {
    return invalid(path, "expected a bounded non-empty string");
  }
  return value;
}

function nullableString(value: unknown, path: string): string | null {
  if (value === null) return null;
  return boundedString(value, path);
}

function booleanAt(value: unknown, path: string): boolean {
  if (typeof value !== "boolean") invalid(path, "expected a boolean");
  return value;
}

function parseTool(value: unknown, path: string): AgentToolStatus {
  const root = objectAt(value, path);
  exactKeys(root, ["id", "detected", "skill_installed", "mcp_installed"], path);
  if (!TOOL_IDS.includes(root.id as AgentToolId)) {
    invalid(`${path}.id`, "unknown tool");
  }
  return {
    id: root.id as AgentToolId,
    detected: booleanAt(root.detected, `${path}.detected`),
    skill_installed: booleanAt(root.skill_installed, `${path}.skill_installed`),
    mcp_installed: booleanAt(root.mcp_installed, `${path}.mcp_installed`),
  };
}

export function parseAgentInstallStatus(value: unknown): AgentInstallStatus {
  const root = objectAt(value, "$");
  exactKeys(
    root,
    ["canonical_skill", "mcp_binary", "mcp_command", "tools", "preview_paths"],
    "$",
  );
  if (!Array.isArray(root.tools)) invalid("$.tools", "expected an array");
  if (!Array.isArray(root.preview_paths)) {
    invalid("$.preview_paths", "expected an array");
  }
  return {
    canonical_skill: booleanAt(root.canonical_skill, "$.canonical_skill"),
    mcp_binary: booleanAt(root.mcp_binary, "$.mcp_binary"),
    mcp_command: nullableString(root.mcp_command, "$.mcp_command"),
    tools: root.tools.map((tool, index) => parseTool(tool, `$.tools[${index}]`)),
    preview_paths: root.preview_paths.map((path, index) =>
      boundedString(path, `$.preview_paths[${index}]`, 8192),
    ),
  };
}

export function parseAgentInstallReceipt(value: unknown): AgentInstallReceipt {
  const root = objectAt(value, "$");
  exactKeys(
    root,
    [
      "version",
      "bundle",
      "bundle_version",
      "installed_at_unix",
      "mcp_binary",
      "files",
    ],
    "$",
  );
  if (!Array.isArray(root.files)) invalid("$.files", "expected an array");
  if (typeof root.version !== "number" || !Number.isInteger(root.version)) {
    invalid("$.version", "expected an integer");
  }
  if (
    typeof root.installed_at_unix !== "number" ||
    !Number.isFinite(root.installed_at_unix)
  ) {
    invalid("$.installed_at_unix", "expected a number");
  }
  return {
    version: root.version,
    bundle: boundedString(root.bundle, "$.bundle"),
    bundle_version: boundedString(root.bundle_version, "$.bundle_version"),
    installed_at_unix: root.installed_at_unix,
    mcp_binary: boundedString(root.mcp_binary, "$.mcp_binary", 8192),
    files: root.files.map((path, index) =>
      boundedString(path, `$.files[${index}]`, 8192),
    ),
  };
}

export function toolLabelKey(id: AgentToolId): "cursor" | "claude" | "codex" {
  return id;
}
