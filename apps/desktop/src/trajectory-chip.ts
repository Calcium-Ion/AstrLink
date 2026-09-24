import type {
  TrajectoryChip,
  TrajectoryTone,
} from "./request-trajectory-model";

/**
 * Phase colours are shared by the list rows, the timeline marks and the
 * inspector header, and the inspector now runs in its own window. Keeping the
 * scale here is what stops the two windows from drifting apart.
 */
const chipClass: Record<TrajectoryChip, string> = {
  TURN: "bg-foreground text-background",
  CLIENT: "bg-primary text-primary-foreground",
  REDIRECT: "bg-accent-foreground text-primary-foreground",
  POLICY: "bg-warning text-primary-foreground",
  ROUTE: "bg-tide text-primary-foreground",
  UPSTREAM: "bg-warning text-primary-foreground",
  RETRY: "bg-warning-wash text-warning-foreground",
  RESTORE: "bg-violet text-primary-foreground",
  RESULT: "bg-success text-primary-foreground",
};

const subtleChipClass: Record<TrajectoryChip, string> = {
  TURN: "bg-muted text-foreground",
  CLIENT: "bg-accent text-primary",
  REDIRECT: "bg-secondary text-accent-foreground",
  POLICY: "bg-muted text-muted-foreground",
  ROUTE: "bg-tide-wash text-accent-foreground",
  UPSTREAM: "bg-warning-wash text-warning-foreground",
  RETRY: "bg-warning-wash text-warning-foreground",
  RESTORE: "bg-violet-wash text-violet-foreground",
  RESULT: "bg-success-wash text-success-foreground",
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

/** Badge shared by the inspector header and the list rows. */
export const CHIP_BADGE_CLASS =
  "inline-flex h-5 items-center justify-center rounded-sm px-1 text-micro font-semibold tracking-wide";
