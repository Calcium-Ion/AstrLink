export type UpdateChannel = "stable" | "preview";
export interface UpdatePreferences {
  auto_check: boolean;
  auto_download: boolean;
  channel: UpdateChannel;
  official_mirror: boolean;
}
export const defaultUpdatePreferences = (): UpdatePreferences => ({
  auto_check: true,
  auto_download: true,
  channel: "stable",
  official_mirror: false,
});
export const UPDATE_PHASES = [
  "idle",
  "checking",
  "no_releases",
  "up_to_date",
  "available",
  "manual",
  "downloading",
  "ready",
  "installing",
  "error",
] as const;
export type UpdatePhase = (typeof UPDATE_PHASES)[number];
export interface UpdateRelease {
  version: string;
  notes: string;
  published_at: string | null;
  url: string;
}
export interface UpdateSnapshot {
  revision: number;
  current_version: string;
  latest_version: string | null;
  platform: string;
  arch: string;
  install_supported: boolean;
  configured: boolean;
  development: boolean;
  preferences: UpdatePreferences;
  phase: UpdatePhase;
  release: UpdateRelease | null;
  downloaded_bytes: number;
  total_bytes: number | null;
  download_source: "download_site" | "github" | null;
  last_checked_at: string | null;
  error_code: string | null;
  error_detail: string | null;
  /** When the host accepts the next check after a GitHub rate limit. */
  retry_at: string | null;
}
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("Invalid update IPC object");
  return value as Record<string, unknown>;
}
export function parseUpdatePreferences(value: unknown): UpdatePreferences {
  const v = object(value);
  if (
    Object.keys(v).sort().join() !==
      "auto_check,auto_download,channel,official_mirror" ||
    typeof v.auto_check !== "boolean" ||
    typeof v.auto_download !== "boolean" ||
    !["stable", "preview"].includes(String(v.channel)) ||
    typeof v.official_mirror !== "boolean"
  )
    throw new Error("Invalid update preferences IPC");
  return v as unknown as UpdatePreferences;
}
export function parseUpdateSnapshot(value: unknown): UpdateSnapshot {
  const v = object(value);
  if (!UPDATE_PHASES.includes(v.phase as UpdatePhase))
    throw new Error("Invalid update phase");
  for (const key of ["current_version", "platform", "arch"])
    if (typeof v[key] !== "string") throw new Error(`Invalid update ${key}`);
  for (const key of ["install_supported", "configured", "development"])
    if (typeof v[key] !== "boolean") throw new Error(`Invalid update ${key}`);
  if (
    v.download_source !== null &&
    !["download_site", "github"].includes(String(v.download_source))
  )
    throw new Error("Invalid update download_source");
  for (const key of ["revision", "downloaded_bytes", "total_bytes"]) {
    if (key === "total_bytes" && v[key] === null) continue;
    if (!Number.isSafeInteger(v[key]) || (v[key] as number) < 0)
      throw new Error(`Invalid update ${key}`);
  }
  if (
    v.latest_version !== undefined &&
    v.latest_version !== null &&
    typeof v.latest_version !== "string"
  )
    throw new Error("Invalid update latest_version");
  for (const key of [
    "last_checked_at",
    "error_code",
    "error_detail",
    "retry_at",
  ])
    if (v[key] !== null && typeof v[key] !== "string")
      throw new Error(`Invalid update ${key}`);
  parseUpdatePreferences(v.preferences);
  if (v.release !== null) {
    const r = object(v.release);
    for (const key of ["version", "notes", "url"])
      if (typeof r[key] !== "string")
        throw new Error(`Invalid update release ${key}`);
    if (r.published_at !== null && typeof r.published_at !== "string")
      throw new Error("Invalid update release date");
    const url = new URL(String(r.url));
    if (
      url.origin !== "https://github.com" ||
      !url.pathname.startsWith("/Calcium-Ion/AstrLink/releases/tag/")
    )
      throw new Error("Invalid update release URL");
  }
  return {
    ...(v as unknown as UpdateSnapshot),
    latest_version: (v.latest_version as string | null | undefined) ?? null,
  };
}
export const browserUpdateSnapshot = (): UpdateSnapshot => ({
  revision: 0,
  current_version: "—",
  latest_version: null,
  platform: "browser",
  arch: "—",
  install_supported: false,
  configured: false,
  development: true,
  preferences: defaultUpdatePreferences(),
  phase: "idle",
  release: null,
  downloaded_bytes: 0,
  total_bytes: null,
  download_source: null,
  last_checked_at: null,
  error_code: null,
  error_detail: null,
  retry_at: null,
});
export function updateBusy(snapshot: UpdateSnapshot): boolean {
  return ["checking", "downloading", "installing"].includes(snapshot.phase);
}
/** Whole seconds until the host accepts another check; 0 once it does. */
export function updateRetryWait(snapshot: UpdateSnapshot, now: number) {
  const until = snapshot.retry_at ? Date.parse(snapshot.retry_at) : NaN;
  return until > now ? Math.ceil((until - now) / 1000) : 0;
}
export type UpdateNotice = "available" | "manual" | "ready";
/** Mirrors the host's native-notification rule; `available` only counts while
 * it waits on the operator, since automatic download soon reaches `ready`. */
export function updateNotice(snapshot: UpdateSnapshot): UpdateNotice | null {
  if (!snapshot.release) return null;
  switch (snapshot.phase) {
    case "available":
      return snapshot.preferences.auto_download ? null : "available";
    case "manual":
    case "ready":
      return snapshot.phase;
    default:
      return null;
  }
}
