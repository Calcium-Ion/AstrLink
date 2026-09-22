import type { HTMLAttributes } from "react";

import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
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
  label,
  tone = "neutral",
  ...props
}: HTMLAttributes<HTMLSpanElement> & { label?: string; tone?: StatusTone }) {
  const dot = (
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

  if (!label) return dot;

  return (
    <TooltipProvider delayDuration={300}>
      <Tooltip>
        <TooltipTrigger asChild>
          <span
            aria-label={label}
            className="inline-flex size-4 shrink-0 items-center justify-center rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/30"
            role="img"
            tabIndex={0}
          >
            {dot}
          </span>
        </TooltipTrigger>
        <TooltipContent sideOffset={6}>{label}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}
