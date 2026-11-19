import type {
  TrajectoryChip,
  TrajectoryTone,
} from "./request-trajectory-model";

/**
 * Phase colours are shared by the list rows, the timeline marks and the
 * inspector tabs, and the inspector runs in its own window. Keeping the scale
 * here is what stops the two windows from drifting apart.
 */
const chipClass: Record<TrajectoryChip, string> = {
  TURN: "bg-foreground text-background",
  CLIENT: "bg-primary text-primary-foreground",
  REDIRECT: "bg-accent-foreground text-primary-foreground",
  POLICY: "bg-warning text-primary-foreground",
  COMPRESS: "bg-text-secondary text-primary-foreground",
  ROUTE: "bg-tide text-primary-foreground",
  UPSTREAM: "bg-warning text-primary-foreground",
  RETRY: "bg-warning-wash text-warning-foreground",
  RESTORE: "bg-violet text-primary-foreground",
  RESULT: "bg-success text-primary-foreground",
  PLUGIN: "bg-violet text-primary-foreground",
};

const subtleChipClass: Record<TrajectoryChip, string> = {
  TURN: "bg-muted text-foreground",
  CLIENT: "bg-accent text-primary",
  REDIRECT: "bg-secondary text-accent-foreground",
  POLICY: "bg-muted text-muted-foreground",
  COMPRESS: "bg-muted text-text-secondary",
  ROUTE: "bg-tide-wash text-accent-foreground",
  UPSTREAM: "bg-warning-wash text-warning-foreground",
  RETRY: "bg-warning-wash text-warning-foreground",
  RESTORE: "bg-violet-wash text-violet-foreground",
  RESULT: "bg-success-wash text-success-foreground",
  PLUGIN: "bg-violet-wash text-violet-foreground",
};

/** The solid scale's fills, for marks too small to carry a wash. */
const chipDotFill: Record<TrajectoryChip, string> = {
  TURN: "bg-foreground",
  CLIENT: "bg-primary",
  REDIRECT: "bg-accent-foreground",
  POLICY: "bg-warning",
  COMPRESS: "bg-text-secondary",
  ROUTE: "bg-tide",
  UPSTREAM: "bg-warning",
  RETRY: "bg-warning",
  RESTORE: "bg-violet",
  RESULT: "bg-success",
  PLUGIN: "bg-violet",
};

const failedPhaseChips = new Set<TrajectoryChip>([
  "TURN",
  "RESULT",
  "UPSTREAM",
  "RETRY",
]);

export function chipToneClass(
  chip: TrajectoryChip,
  tone: TrajectoryTone,
  appearance: "solid" | "subtle" = "solid",
): string {
  if (tone === "failed" && failedPhaseChips.has(chip)) {
    if (appearance === "subtle") return "bg-danger-wash text-danger-foreground";
    return "bg-destructive text-destructive-foreground";
  }
  if (tone === "blocked" && (chip === "RESULT" || chip === "POLICY")) {
    if (appearance === "subtle")
      return "bg-blocked-wash text-blocked-foreground";
    return "bg-blocked text-primary-foreground";
  }
  if (
    tone === "cancelled" &&
    (chip === "RESULT" || chip === "CLIENT" || chip === "TURN")
  ) {
    return "bg-warning-wash text-warning-foreground";
  }
  if (tone === "pending" && chip === "RESULT") {
    if (appearance === "subtle")
      return "bg-warning-wash text-warning-foreground";
    return "bg-warning text-primary-foreground";
  }
  return appearance === "subtle" ? subtleChipClass[chip] : chipClass[chip];
}

/**
 * A tab's status mark: the phase colour the list row and timeline use, so a
 * tab matches the row that opened it. Failures and waits override it on the
 * same phases that turn red or amber in the list.
 */
export function chipDotClass(
  chip: TrajectoryChip,
  tone: TrajectoryTone,
): string {
  if (tone === "failed" && failedPhaseChips.has(chip)) return "bg-destructive";
  if (tone === "blocked" && (chip === "RESULT" || chip === "POLICY"))
    return "bg-blocked";
  if (
    (tone === "pending" && failedPhaseChips.has(chip)) ||
    (tone === "cancelled" &&
      (chip === "RESULT" || chip === "CLIENT" || chip === "TURN"))
  )
    return "bg-warning";
  return chipDotFill[chip];
}
