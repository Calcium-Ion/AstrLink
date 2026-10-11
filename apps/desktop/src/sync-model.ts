/**
 * The configuration-sync read model. Every field mirrors contract/sync.go, and
 * every parser refuses a response the interface cannot render rather than
 * showing a half-read configuration.
 */

export type SyncTriggerMode = "interval" | "cron" | "manual";
export type SyncRemoteKind = "webdav" | "s3";
export type SyncS3Addressing = "path" | "suffix";

export const syncTriggerModes: SyncTriggerMode[] = [
  "interval",
  "cron",
  "manual",
];

export const syncS3Addressings: SyncS3Addressing[] = ["path", "suffix"];

/** The folder a package is written under when a WebDAV remote has none. */
export const defaultSyncFolder = "astrlink";

export const minSyncIntervalMinutes = 5;
export const minSyncDebounceSeconds = 5;
export const maxSyncDebounceSeconds = 3600;
export const maxSyncDeviceNameLength = 40;
/** The engine's own lower bound for a stored passphrase. */
export const minSyncPassphraseLength = 12;

export interface SyncTriggerSettings {
  mode: SyncTriggerMode;
  interval_minutes: number;
  cron: string;
  debounce_seconds: number;
}

export interface SyncScope {
  services: boolean;
  credentials: boolean;
  policies: boolean;
  pricing: boolean;
}

export interface SyncRemote {
  kind: "" | SyncRemoteKind;
  endpoint: string;
  path: string;
  region: string;
  username: string;
  password: string;
  access_key: string;
  secret_key: string;
  addressing: SyncS3Addressing;
}

export interface SyncConfig {
  enabled: boolean;
  device_name: string;
  trigger: SyncTriggerSettings;
  scope: SyncScope;
  remote: SyncRemote;
  /**
   * Whether a remote password / secret key is stored. The read never returns
   * the secrets themselves, so a form cannot round-trip them; leaving a field
   * blank keeps the stored value.
   */
  remote_password_set: boolean;
  remote_secret_key_set: boolean;
}

export interface SyncConflictNotice {
  kind: string;
  name: string;
  resolution: string;
  at: string;
}

export interface SyncStatus {
  enabled: boolean;
  passphrase_set: boolean;
  remote_configured: boolean;
  configured: boolean;
  running: boolean;
  /**
   * This device's identity in the remote lock. The interface shows
   * device_name instead.
   */
  device_id: string;
  device_name: string;
  host_name: string;
  pending_changes: number;
  last_synced_at?: string;
  next_run_at?: string;
  last_error?: string;
  conflicts: SyncConflictNotice[];
}

export interface SyncPassphraseResult {
  passphrase_set: boolean;
}

export function defaultSyncConfig(): SyncConfig {
  return {
    enabled: false,
    device_name: "",
    trigger: {
      mode: "interval",
      interval_minutes: 60,
      cron: "",
      debounce_seconds: 30,
    },
    scope: {
      services: true,
      credentials: true,
      policies: true,
      pricing: true,
    },
    remote: {
      kind: "",
      endpoint: "",
      path: defaultSyncFolder,
      region: "",
      username: "",
      password: "",
      access_key: "",
      secret_key: "",
      addressing: "path",
    },
    remote_password_set: false,
    remote_secret_key_set: false,
  };
}

function object(value: unknown, path: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error(`${path}: expected an object`);
  return value as Record<string, unknown>;
}

function text(value: unknown, path: string): string {
  if (typeof value !== "string") throw new Error(`${path}: expected a string`);
  return value;
}

function flag(value: unknown, path: string): boolean {
  if (typeof value !== "boolean")
    throw new Error(`${path}: expected a boolean`);
  return value;
}

function integer(
  value: unknown,
  path: string,
  min: number,
  max: number,
): number {
  if (
    typeof value !== "number" ||
    !Number.isSafeInteger(value) ||
    value < min ||
    value > max
  )
    throw new Error(`${path}: expected an integer from ${min} through ${max}`);
  return value;
}

function timestamp(value: unknown, path: string): string {
  const parsed = text(value, path);
  if (!Number.isFinite(Date.parse(parsed)))
    throw new Error(`${path}: expected a timestamp`);
  return parsed;
}

export function parseSyncConfig(value: unknown): SyncConfig {
  const config = object(value, "sync_config");
  const trigger = object(config.trigger, "sync_config.trigger");
  const scope = object(config.scope, "sync_config.scope");
  const remote = object(config.remote, "sync_config.remote");
  const mode = text(trigger.mode, "sync_config.trigger.mode");
  if (mode !== "interval" && mode !== "cron" && mode !== "manual")
    throw new Error("sync_config.trigger.mode: unknown trigger");
  const kind = text(remote.kind, "sync_config.remote.kind");
  if (kind !== "" && kind !== "webdav" && kind !== "s3")
    throw new Error("sync_config.remote.kind: unknown remote");
  const addressing = text(
    remote.addressing ?? "path",
    "sync_config.remote.addressing",
  );
  if (addressing !== "path" && addressing !== "suffix")
    throw new Error("sync_config.remote.addressing: unknown addressing");
  return {
    enabled: flag(config.enabled, "sync_config.enabled"),
    device_name: text(config.device_name ?? "", "sync_config.device_name"),
    trigger: {
      mode,
      interval_minutes: integer(
        trigger.interval_minutes ?? 0,
        "sync_config.trigger.interval_minutes",
        0,
        60 * 24 * 30,
      ),
      cron: text(trigger.cron ?? "", "sync_config.trigger.cron"),
      debounce_seconds: integer(
        trigger.debounce_seconds ?? 0,
        "sync_config.trigger.debounce_seconds",
        0,
        maxSyncDebounceSeconds,
      ),
    },
    scope: {
      services: flag(scope.services, "sync_config.scope.services"),
      credentials: flag(scope.credentials, "sync_config.scope.credentials"),
      policies: flag(scope.policies, "sync_config.scope.policies"),
      pricing: flag(scope.pricing, "sync_config.scope.pricing"),
    },
    remote: {
      kind,
      endpoint: text(remote.endpoint ?? "", "sync_config.remote.endpoint"),
      path: text(remote.path ?? "", "sync_config.remote.path"),
      region: text(remote.region ?? "", "sync_config.remote.region"),
      username: text(remote.username ?? "", "sync_config.remote.username"),
      password: text(remote.password ?? "", "sync_config.remote.password"),
      access_key: text(
        remote.access_key ?? "",
        "sync_config.remote.access_key",
      ),
      secret_key: text(
        remote.secret_key ?? "",
        "sync_config.remote.secret_key",
      ),
      addressing,
    },
    remote_password_set: flag(
      config.remote_password_set,
      "sync_config.remote_password_set",
    ),
    remote_secret_key_set: flag(
      config.remote_secret_key_set,
      "sync_config.remote_secret_key_set",
    ),
  };
}

/** The body a save sends: the write-only flags are read-only, so they are dropped. */
export function syncConfigPayload(config: SyncConfig): unknown {
  const {
    remote_password_set: _p,
    remote_secret_key_set: _s,
    ...rest
  } = config;
  return rest;
}

export type SyncConfigIssue =
  | "interval"
  | "cron"
  | "debounce"
  | "webdav_endpoint"
  | "s3_endpoint"
  | "remote_kind"
  | "device_name";

/** Mirrors contract.ValidateSyncConfig so the interface refuses it first. */
export function syncConfigIssue(config: SyncConfig): SyncConfigIssue | null {
  if (
    config.trigger.mode === "interval" &&
    config.trigger.interval_minutes < minSyncIntervalMinutes
  )
    return "interval";
  if (config.trigger.mode === "cron" && config.trigger.cron.trim() === "")
    return "cron";
  const debounce = config.trigger.debounce_seconds;
  if (
    debounce !== 0 &&
    (debounce < minSyncDebounceSeconds || debounce > maxSyncDebounceSeconds)
  )
    return "debounce";
  if (config.remote.kind === "webdav" && config.remote.endpoint.trim() === "")
    return "webdav_endpoint";
  if (
    config.remote.kind === "s3" &&
    (config.remote.endpoint.trim() === "" || config.remote.path.trim() === "")
  )
    return "s3_endpoint";
  if (config.enabled && config.remote.kind === "") return "remote_kind";
  if ([...config.device_name.trim()].length > maxSyncDeviceNameLength)
    return "device_name";
  return null;
}

export function parseSyncStatus(value: unknown): SyncStatus {
  const status = object(value, "sync_status");
  const conflicts = status.conflicts ?? [];
  if (!Array.isArray(conflicts))
    throw new Error("sync_status.conflicts: expected a list");
  return {
    enabled: flag(status.enabled, "sync_status.enabled"),
    passphrase_set: flag(status.passphrase_set, "sync_status.passphrase_set"),
    remote_configured: flag(
      status.remote_configured,
      "sync_status.remote_configured",
    ),
    configured: flag(status.configured, "sync_status.configured"),
    running: flag(status.running, "sync_status.running"),
    device_id: text(status.device_id ?? "", "sync_status.device_id"),
    device_name: text(status.device_name ?? "", "sync_status.device_name"),
    host_name: text(status.host_name ?? "", "sync_status.host_name"),
    pending_changes: integer(
      status.pending_changes ?? 0,
      "sync_status.pending_changes",
      0,
      Number.MAX_SAFE_INTEGER,
    ),
    ...(status.last_synced_at === undefined || status.last_synced_at === null
      ? {}
      : {
          last_synced_at: timestamp(
            status.last_synced_at,
            "sync_status.last_synced_at",
          ),
        }),
    ...(status.next_run_at === undefined || status.next_run_at === null
      ? {}
      : {
          next_run_at: timestamp(status.next_run_at, "sync_status.next_run_at"),
        }),
    ...(typeof status.last_error === "string" && status.last_error !== ""
      ? { last_error: status.last_error }
      : {}),
    conflicts: conflicts.map((item, index) => {
      const path = `sync_status.conflicts[${index}]`;
      const notice = object(item, path);
      return {
        kind: text(notice.kind, `${path}.kind`),
        name: text(notice.name, `${path}.name`),
        resolution: text(notice.resolution, `${path}.resolution`),
        at: timestamp(notice.at, `${path}.at`),
      };
    }),
  };
}

export function parseSyncPassphraseResult(
  value: unknown,
): SyncPassphraseResult {
  const result = object(value, "sync_passphrase");
  return { passphrase_set: flag(result.passphrase_set, "sync_passphrase") };
}
