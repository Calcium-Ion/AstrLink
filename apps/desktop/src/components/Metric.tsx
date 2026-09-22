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
  label,
  value,
  title,
  size = "default",
  emphasis = false,
  icon,
}: {
  label: ReactNode;
  value: ReactNode;
  title?: string;
  size?: "default" | "sm";
  emphasis?: boolean;
  icon?: ReactNode;
}) {
  return (
    <div
      className={cn(
        "flex min-w-0 flex-col bg-card",
        size === "sm" ? "gap-1 px-3 py-2.5" : "gap-2 px-4 py-4",
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
          size === "sm" ? "text-lg leading-6" : "text-xl leading-7",
          emphasis ? "text-primary" : "text-foreground",
        )}
        title={title}
      >
        {value}
      </dd>
    </div>
  );
}

/** Paired numbers wrap between values so each number keeps its unit. */
export function MetricValuePair({
  first,
  second,
}: {
  first: ReactNode;
  second: ReactNode;
}) {
  return (
    <span className="inline-flex max-w-full flex-wrap items-baseline">
      <span className="whitespace-nowrap">{first}</span>
      <span className="whitespace-nowrap"> / {second}</span>
    </span>
  );
}
