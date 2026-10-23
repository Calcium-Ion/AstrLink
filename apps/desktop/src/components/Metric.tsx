import type { HTMLAttributes, ReactNode } from "react";

import { cn } from "@/lib/utils";

/** A responsive summary band whose labels and values stay paired. */
export function MetricGroup({
  className,
  ...props
}: HTMLAttributes<HTMLDListElement>) {
  return (
    <dl
      className={cn(
        "grid grid-cols-2 gap-px bg-border @min-[640px]/workspace-surface:grid-cols-4",
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
  size?: "default" | "sm" | "compact";
  emphasis?: boolean;
  icon?: ReactNode;
  /** A short line under the value: a share, a split, or a caveat. */
  detail?: ReactNode;
}) {
  return (
    <div
      className={cn(
        "flex min-w-0 flex-col bg-card",
        size === "compact"
          ? "flex-row flex-wrap items-center justify-between gap-x-2 gap-y-0.5 px-3 py-1.5 @max-[800px]/workspace-surface:flex-col @max-[800px]/workspace-surface:items-start"
          : size === "sm"
            ? "gap-1 px-3 py-2.5"
            : "gap-2 px-4 py-4",
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
        <dd className="-mt-1 min-w-0 text-micro text-muted-foreground">
          {detail}
        </dd>
      ) : null}
    </div>
  );
}
