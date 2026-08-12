import type { HTMLAttributes } from "react";

import { cn } from "@/lib/utils";

export type StatusTone = "negative" | "neutral" | "pending" | "positive";

const toneClasses: Record<StatusTone, string> = {
  negative:
    "bg-destructive shadow-[0_0_0_3px_color-mix(in_srgb,var(--destructive)_14%,transparent)]",
  neutral: "bg-muted-foreground",
  pending:
    "bg-warning shadow-[0_0_0_3px_color-mix(in_srgb,var(--warning)_15%,transparent)]",
  positive:
    "bg-success shadow-[0_0_0_3px_color-mix(in_srgb,var(--success)_14%,transparent)]",
};

export function StatusDot({
  className,
  tone = "neutral",
  ...props
}: HTMLAttributes<HTMLSpanElement> & { tone?: StatusTone }) {
  return (
    <span
      aria-hidden="true"
      className={cn("inline-block size-[7px] shrink-0 rounded-full", toneClasses[tone], className)}
      {...props}
    />
  );
}
