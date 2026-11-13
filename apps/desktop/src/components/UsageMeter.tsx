import type { ComponentProps, ReactNode } from "react";
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
  compact = false,
  className,
}: {
  label: string;
  caption?: string | null;
  action?: ReactNode;
  value: number;
  valueLabel: string;
  warning?: string;
  tone: "success" | "warning" | "destructive";
  compact?: boolean;
  className?: string;
}) {
  const percent = Number.isFinite(value) ? Math.max(0, value) : 0;
  const meter = (
    <div
      className={cn(
        "grid min-w-0",
        compact ? "gap-1" : "gap-1.5",
        compact &&
          "rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2",
        className,
      )}
      tabIndex={compact && (caption || warning) ? 0 : undefined}
    >
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
          {warning && compact ? (
            <TriangleAlert aria-label={warning} className="size-3" />
          ) : warning ? (
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
      {(!compact && caption) || action ? (
        <div className="flex min-w-0 items-center justify-between gap-2 text-micro text-muted-foreground">
          <span className="min-w-0">{!compact ? caption : null}</span>
          {action}
        </div>
      ) : null}
    </div>
  );
  if (!compact || (!caption && !warning)) return meter;
  return (
    <TooltipProvider delayDuration={200}>
      <Tooltip>
        <TooltipTrigger asChild>{meter}</TooltipTrigger>
        <TooltipContent sideOffset={4}>
          <p>
            {label} · {valueLabel}
          </p>
          {caption ? <p>{caption}</p> : null}
          {warning ? <p>{warning}</p> : null}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

/**
 * Column template shared by `UsageMeterRow`s (label, bar, value, caption), so
 * the columns line up across every row and group header placed inside it.
 * Without `captions` the caption column and its gap are dropped.
 */
export function UsageMeterGrid({
  captions = true,
  className,
  ...props
}: ComponentProps<"div"> & { captions?: boolean }) {
  return (
    <div
      className={cn(
        "grid items-center gap-x-2.5",
        captions
          ? "grid-cols-[auto_minmax(0,1fr)_auto_auto]"
          : "grid-cols-[auto_minmax(0,1fr)_auto]",
        className,
      )}
      {...props}
    />
  );
}

/** One meter on a single line of a `UsageMeterGrid`, for dense lists. */
export function UsageMeterRow({
  label,
  accessibleLabel = label,
  caption,
  captionDetail = caption,
  value,
  valueLabel,
  valueText,
  warning,
  tone,
  className,
}: {
  label: string;
  /** Names the bar when the visible label leans on a group header. */
  accessibleLabel?: string;
  caption?: string | null;
  /** Unabbreviated caption for the hover title. */
  captionDetail?: string | null;
  value: number;
  valueLabel: string;
  /** Visible value when a column header already names the unit. */
  valueText?: string;
  warning?: string;
  /** `default` is a plain share in brand ink, for counts that carry no status. */
  tone: "default" | "success" | "warning" | "destructive";
  className?: string;
}) {
  const percent = Number.isFinite(value) ? Math.max(0, value) : 0;
  return (
    <div
      className={cn(
        "col-span-full grid min-h-5 grid-cols-subgrid items-center text-xs",
        className,
      )}
      title={[accessibleLabel, valueLabel, captionDetail, warning]
        .filter(Boolean)
        .join(" · ")}
    >
      <span className="min-w-0 truncate text-text-secondary">{label}</span>
      <Progress
        aria-label={accessibleLabel}
        className="h-1.5"
        getValueLabel={() => valueLabel}
        tone={tone}
        value={Math.min(100, percent)}
      />
      <span
        className={cn(
          "inline-flex items-center justify-end gap-1 font-medium tabular-nums",
          tone === "destructive" && "text-destructive",
          tone === "warning" && "text-warning-foreground",
        )}
      >
        {warning ? (
          <TriangleAlert aria-label={warning} className="size-3" />
        ) : null}
        <span aria-hidden="true">{valueText ?? valueLabel}</span>
      </span>
      {caption ? (
        <span className="text-right text-micro text-muted-foreground tabular-nums">
          {caption}
        </span>
      ) : null}
    </div>
  );
}

/**
 * One meter inside a line of text, such as a list row: the label, a short
 * bar and the value. It is built from phrasing elements, so it can sit in a
 * button that opens the row.
 */
export function UsageMeterInline({
  label,
  value,
  valueLabel,
  title,
  tone,
  className,
}: {
  label: string;
  value: number;
  valueLabel: string;
  /** What the value measures against, for the hover title. */
  title?: string;
  /** `default` is a plain share in brand ink. */
  tone: "default" | "warning" | "destructive";
  className?: string;
}) {
  const percent = Number.isFinite(value) ? Math.max(0, value) : 0;
  const filled = Math.min(100, percent);
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap",
        className,
      )}
      data-slot="usage-meter-inline"
      title={title}
    >
      <span>{label}</span>
      <span
        aria-label={label}
        aria-valuemax={100}
        aria-valuemin={0}
        aria-valuenow={Math.round(filled)}
        aria-valuetext={valueLabel}
        className={cn(
          "relative block h-1 w-14 overflow-hidden rounded-full",
          tone === "default" ? "bg-primary/20" : "bg-muted",
        )}
        role="meter"
      >
        <span
          className={cn(
            "absolute inset-y-0 left-0 rounded-full transition-[width]",
            tone === "default" && "bg-primary",
            tone === "warning" && "bg-warning",
            tone === "destructive" && "bg-destructive",
          )}
          style={{ width: `${filled}%` }}
        />
      </span>
      <span
        aria-hidden="true"
        className={cn(
          "font-medium tabular-nums text-foreground",
          tone === "warning" && "text-warning-foreground",
          tone === "destructive" && "text-destructive",
        )}
      >
        {valueLabel}
      </span>
    </span>
  );
}
