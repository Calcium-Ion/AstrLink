import type { ReactNode } from "react";
import { BadgeAlert as TriangleAlert } from "@/components/icons";

import { Progress } from "@/components/ui/progress";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

/** Match one meter's line boxes without pulse or an invented window count. */
export function UsageMeterPlaceholder() {
  return (
    <div aria-hidden="true" className="grid min-w-0 gap-1.5">
      <div className="flex h-lh items-center justify-between text-xs">
        <span className="h-3 w-12 rounded-sm bg-muted" />
        <span className="h-3 w-8 rounded-sm bg-muted" />
      </div>
      <span className="h-1 rounded-full bg-muted" />
      <div className="flex h-lh items-center text-micro">
        <span className="h-2.5 w-20 rounded-sm bg-muted" />
      </div>
    </div>
  );
}

export function UsageMeter({
  label,
  caption,
  action,
  value,
  valueLabel,
  warning,
  tone,
}: {
  label: string;
  caption?: string | null;
  action?: ReactNode;
  value: number;
  valueLabel: string;
  warning?: string;
  tone: "success" | "warning" | "destructive";
}) {
  const percent = Number.isFinite(value) ? Math.max(0, value) : 0;
  return (
    <div className="grid min-w-0 gap-1.5">
      <div className="flex min-w-0 items-center justify-between gap-2 text-xs">
        <span className="min-w-0 truncate" title={label}>
          {label}
        </span>
        <span
          className={cn(
            "inline-flex shrink-0 items-center gap-1.5 font-medium tabular-nums",
            tone === "destructive" && "text-destructive",
            tone === "warning" && "text-warning-foreground",
          )}
        >
          {warning ? (
            <TooltipProvider delayDuration={200}>
              <Tooltip>
                <TooltipTrigger asChild>
                  <span
                    aria-label={warning}
                    className="rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    tabIndex={0}
                  >
                    <TriangleAlert aria-hidden="true" className="size-3" />
                  </span>
                </TooltipTrigger>
                <TooltipContent sideOffset={4}>{warning}</TooltipContent>
              </Tooltip>
            </TooltipProvider>
          ) : null}
          <span aria-hidden="true">{valueLabel}</span>
        </span>
      </div>
      <Progress
        aria-label={label}
        className="h-1"
        getValueLabel={() => valueLabel}
        tone={tone}
        value={Math.min(100, percent)}
      />
      {caption || action ? (
        <div className="flex min-w-0 items-center justify-between gap-2 text-micro text-muted-foreground">
          <span className="min-w-0">{caption}</span>
          {action}
        </div>
      ) : null}
    </div>
  );
}
