export type AgentToolId = "cursor" | "claude" | "codex" | "grok" | "pi";

/**
 * A skill AstrLink can install. Only `astrlink-debug` drives the AstrLink CLI,
 * so only it brings the CLI, its access rules, and the host guards.
 */
export type AgentSkillId = "astrlink-debug" | "redaction-placeholders";

/**
 * How the host is kept away from AstrLink's local files: enforced deny rules
 * (Claude Code), prompt-only global instructions (Codex), or the skill text
 * alone (hosts without a verified mechanism).
 */
export type AgentGuardKind = "deny_rules" | "instructions" | "skill_only";

/**
 * How the host lets agents run the AstrLink CLI without asking each time:
 * allow rules (Claude Code), a rules file that also lifts the sandbox (Codex),
 * a prompt on first use (hosts without a verified mechanism), or nothing at
 * all (hosts that run every command without asking, such as Pi).
 */
export type AgentCliAccessKind =
  | "allow_rules"
  | "exec_policy"
  | "prompt"
  | "unrestricted";

export interface AgentSkillStatus {
  id: AgentSkillId;
  installed: boolean;
  /** What installing this skill for the tool writes, apart from `shared_paths`. */
  preview_paths: string[];
}

export interface AgentToolStatus {
  id: AgentToolId;
  /** The WSL distribution the tool runs in; `null` for this computer. */
  wsl: string | null;
  detected: boolean;
  skills: AgentSkillStatus[];
  cli_access: AgentCliAccessKind;
  cli_access_installed: boolean;
  guard: AgentGuardKind;
  guard_installed: boolean;
}

export interface AgentInstallStatus {
  cli_binary: boolean;
  /** This computer's five tools, then the tools found in WSL distributions. */
  tools: AgentToolStatus[];
  shared_paths: string[];
  /** WSL distributions whose tools went unchecked because they were stopped. */
  wsl_unchecked: string[];
}

/** A tool selected for install, on this computer or in a WSL distribution. */
export interface AgentToolTarget {
  id: AgentToolId;
  wsl: string | null;
}

export interface AgentInstallReceiptSkill {
  id: AgentSkillId;
  version: string;
}

export interface AgentInstallReceipt {
  version: number;
  skills: AgentInstallReceiptSkill[];
  installed_at_unix: number;
  /** `null` when no selected skill drives the CLI. */
  cli_binary: string | null;
  files: string[];
  /** WSL distributions holding an install. */
  wsl: string[];
}

export const SKILL_IDS: readonly AgentSkillId[] = [
  "astrlink-debug",
  "redaction-placeholders",
];
const TOOL_IDS: readonly AgentToolId[] = [
  "cursor",
  "claude",
  "codex",
  "grok",
  "pi",
];
const GUARD_KINDS: readonly AgentGuardKind[] = [
  "deny_rules",
  "instructions",
  "skill_only",
];
const CLI_ACCESS_KINDS: readonly AgentCliAccessKind[] = [
  "allow_rules",
  "exec_policy",
  "prompt",
  "unrestricted",
];

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

function booleanAt(value: unknown, path: string): boolean {
  if (typeof value !== "boolean") invalid(path, "expected a boolean");
  return value;
}

function skillIdAt(value: unknown, path: string): AgentSkillId {
  if (!SKILL_IDS.includes(value as AgentSkillId))
    invalid(path, "unknown skill");
  return value as AgentSkillId;
}

function parseSkill(value: unknown, path: string): AgentSkillStatus {
  const root = objectAt(value, path);
  exactKeys(root, ["id", "installed", "preview_paths"], path);
  return {
    id: skillIdAt(root.id, `${path}.id`),
    installed: booleanAt(root.installed, `${path}.installed`),
    preview_paths: parsePaths(root.preview_paths, `${path}.preview_paths`),
  };
}

function parseTool(value: unknown, path: string): AgentToolStatus {
  const root = objectAt(value, path);
  exactKeys(
    root,
    [
      "id",
      "wsl",
      "detected",
      "skills",
      "cli_access",
      "cli_access_installed",
      "guard",
      "guard_installed",
    ],
    path,
  );
  if (!Array.isArray(root.skills))
    invalid(`${path}.skills`, "expected an array");
  if (!TOOL_IDS.includes(root.id as AgentToolId)) {
    invalid(`${path}.id`, "unknown tool");
  }
  if (!CLI_ACCESS_KINDS.includes(root.cli_access as AgentCliAccessKind)) {
    invalid(`${path}.cli_access`, "unknown CLI access kind");
  }
  if (!GUARD_KINDS.includes(root.guard as AgentGuardKind)) {
    invalid(`${path}.guard`, "unknown guard kind");
  }
  return {
    id: root.id as AgentToolId,
    wsl: root.wsl === null ? null : boundedString(root.wsl, `${path}.wsl`, 255),
    detected: booleanAt(root.detected, `${path}.detected`),
    skills: root.skills.map((skill, index) =>
      parseSkill(skill, `${path}.skills[${index}]`),
    ),
    cli_access: root.cli_access as AgentCliAccessKind,
    cli_access_installed: booleanAt(
      root.cli_access_installed,
      `${path}.cli_access_installed`,
    ),
    guard: root.guard as AgentGuardKind,
    guard_installed: booleanAt(root.guard_installed, `${path}.guard_installed`),
  };
}

function parseNames(value: unknown, path: string): string[] {
  if (!Array.isArray(value)) invalid(path, "expected an array");
  return value.map((item, index) =>
    boundedString(item, `${path}[${index}]`, 255),
  );
}

function parsePaths(value: unknown, path: string): string[] {
  if (!Array.isArray(value)) invalid(path, "expected an array");
  return value.map((item, index) =>
    boundedString(item, `${path}[${index}]`, 8192),
  );
}

export function parseAgentInstallStatus(value: unknown): AgentInstallStatus {
  const root = objectAt(value, "$");
  exactKeys(
    root,
    ["cli_binary", "tools", "shared_paths", "wsl_unchecked"],
    "$",
  );
  if (!Array.isArray(root.tools)) invalid("$.tools", "expected an array");
  return {
    cli_binary: booleanAt(root.cli_binary, "$.cli_binary"),
    tools: root.tools.map((tool, index) =>
      parseTool(tool, `$.tools[${index}]`),
    ),
    shared_paths: parsePaths(root.shared_paths, "$.shared_paths"),
    wsl_unchecked: parseNames(root.wsl_unchecked, "$.wsl_unchecked"),
  };
}

export function parseAgentInstallReceipt(value: unknown): AgentInstallReceipt {
  const root = objectAt(value, "$");
  exactKeys(
    root,
    ["version", "skills", "installed_at_unix", "cli_binary", "files", "wsl"],
    "$",
  );
  if (!Array.isArray(root.skills)) invalid("$.skills", "expected an array");
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
    skills: root.skills.map((value, index) => {
      const path = `$.skills[${index}]`;
      const skill = objectAt(value, path);
      exactKeys(skill, ["id", "version"], path);
      return {
        id: skillIdAt(skill.id, `${path}.id`),
        version: boundedString(skill.version, `${path}.version`),
      };
    }),
    installed_at_unix: root.installed_at_unix,
    cli_binary:
      root.cli_binary === null
        ? null
        : boundedString(root.cli_binary, "$.cli_binary", 8192),
    files: root.files.map((path, index) =>
      boundedString(path, `$.files[${index}]`, 8192),
    ),
    wsl: parseNames(root.wsl, "$.wsl"),
  };
}

export function toolLabelKey(
  id: AgentToolId,
): "cursor" | "claude" | "codex" | "grok" | "pi" {
  return id;
}
