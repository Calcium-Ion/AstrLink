import type { HTMLAttributes, ReactNode } from "react";

import { cn } from "@/lib/utils";

/**
 * A responsive summary band whose labels and values stay paired. `inline`
 * sets four bare metrics in equal columns divided by hairlines, for a panel
 * that already has its own frame; it folds to two by two when its nearest
 * `@container` is narrower than 35rem.
 */
export function MetricGroup({
  className,
  variant = "tiles",
  ...props
}: HTMLAttributes<HTMLDListElement> & { variant?: "tiles" | "inline" }) {
  return (
    <dl
      className={cn(
        variant === "inline"
          ? "grid grid-cols-2 gap-y-3 [&>div:nth-child(even)]:border-l [&>div:nth-child(even)]:pl-4 @min-[35rem]:grid-cols-4 @min-[35rem]:[&>div:nth-child(3)]:border-l @min-[35rem]:[&>div:nth-child(3)]:pl-4"
          : "grid grid-cols-2 gap-px bg-border @min-[640px]/workspace-surface:grid-cols-4",
        className,
      )}
      data-slot="metric-group"
      {...props}
    />
  );
}

export function Metric({
  className,
  label,
  value,
  title,
  size = "default",
  emphasis = false,
  icon,
  detail,
}: {
  className?: string;
  label: ReactNode;
  value: ReactNode;
  title?: string;
  /**
   * `inline` drops the tile, for a `MetricGroup` of that variant, and keeps
   * the value and the detail on one line each.
   */
  size?: "default" | "sm" | "compact" | "inline";
  emphasis?: boolean;
  icon?: ReactNode;
  /** A short line under the value: a share, a split, or a caveat. */
  detail?: ReactNode;
}) {
  return (
    <div
      className={cn(
        "flex min-w-0 flex-col",
        size === "inline" ? "gap-0.5" : "bg-card",
        size === "compact"
          ? "flex-row flex-wrap items-center justify-between gap-x-2 gap-y-0.5 px-3 py-1.5 @max-[800px]/workspace-surface:flex-col @max-[800px]/workspace-surface:items-start"
          : size === "sm"
            ? "gap-1 px-3 py-2.5"
            : size === "default"
              ? "gap-2 px-4 py-4"
              : null,
        className,
      )}
    >
      <dt className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
        {label}
        {icon ? (
          <span aria-hidden="true" className="text-primary [&_svg]:size-4">
            {icon}
          </span>
        ) : null}
      </dt>
      <dd
        className={cn(
          "min-w-0 font-semibold tracking-tight tabular-nums",
          size === "inline" && "truncate",
          size === "compact"
            ? "text-sm leading-5"
            : size === "sm"
              ? "text-lg leading-6"
              : "text-xl leading-7",
          emphasis ? "text-primary" : "text-foreground",
        )}
        title={title}
      >
        {value}
      </dd>
      {detail ? (
        <dd
          className={cn(
            "min-w-0 text-micro text-muted-foreground",
            size === "inline" ? "truncate" : "-mt-1",
          )}
        >
          {detail}
        </dd>
      ) : null}
    </div>
  );
}
