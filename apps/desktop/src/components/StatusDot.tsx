import type { HTMLAttributes } from "react";

import { cn } from "@/lib/utils";

export type StatusTone =
  | "blocked"
  | "negative"
  | "neutral"
  | "pending"
  | "positive";

const toneClasses: Record<StatusTone, string> = {
  blocked: "bg-blocked",
  negative: "bg-destructive",
  neutral: "bg-muted-foreground",
  pending: "bg-warning",
  positive: "bg-success",
};

export function StatusDot({
  className,
  tone = "neutral",
  ...props
}: HTMLAttributes<HTMLSpanElement> & { tone?: StatusTone }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "inline-block size-1.5 shrink-0 rounded-full",
        toneClasses[tone],
        className,
      )}
      data-tone={tone}
      {...props}
    />
  );
}
