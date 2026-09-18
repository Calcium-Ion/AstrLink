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
}: {
  label: ReactNode;
  value: ReactNode;
  title?: string;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-2 bg-card px-4 py-4">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd
        className="min-w-0 text-xl leading-7 font-semibold tracking-tight text-foreground tabular-nums"
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
