"use client";

import * as React from "react";
import { Progress as ProgressPrimitive } from "radix-ui";

import { cn } from "@/lib/utils";

function Progress({
  className,
  value,
  tone = "default",
  ...props
}: React.ComponentProps<typeof ProgressPrimitive.Root> & {
  tone?: "default" | "success" | "warning" | "destructive";
}) {
  return (
    <ProgressPrimitive.Root
      data-slot="progress"
      className={cn(
        "relative h-2 w-full overflow-hidden rounded-full bg-primary/20",
        tone !== "default" && "bg-muted",
        tone === "success" && "[&_[data-slot=progress-indicator]]:bg-success",
        tone === "warning" && "[&_[data-slot=progress-indicator]]:bg-warning",
        tone === "destructive" &&
          "[&_[data-slot=progress-indicator]]:bg-destructive",
        className,
      )}
      value={value}
      {...props}
    >
      <ProgressPrimitive.Indicator
        data-slot="progress-indicator"
        className="h-full w-full flex-1 bg-primary transition-all"
        style={{ transform: `translateX(-${100 - (value ?? 0)}%)` }}
      />
    </ProgressPrimitive.Root>
  );
}

const circularProgressTones = {
  default: "[&_[data-slot=progress-indicator]]:stroke-primary",
  success: "[&_[data-slot=progress-indicator]]:stroke-success",
  warning: "[&_[data-slot=progress-indicator]]:stroke-warning",
  destructive: "[&_[data-slot=progress-indicator]]:stroke-destructive",
};

function CircularProgress({
  className,
  value,
  tone = "default",
  ...props
}: Omit<
  React.ComponentProps<typeof ProgressPrimitive.Root>,
  "asChild" | "children" | "max" | "value"
> & {
  value: number;
  tone?: keyof typeof circularProgressTones;
}) {
  const percent = Number.isFinite(value) ? Math.max(0, value) : 0;
  const progress = Math.min(100, percent);
  const circumference = 2 * Math.PI * 10;

  return (
    <ProgressPrimitive.Root
      data-slot="circular-progress"
      className={cn(
        "inline-flex shrink-0 items-center gap-2 text-xs font-medium text-foreground tabular-nums",
        circularProgressTones[tone],
        className,
      )}
      value={progress}
      max={100}
      getValueLabel={() => `${Math.round(percent)}%`}
      {...props}
    >
      <svg
        aria-hidden="true"
        className="size-5 shrink-0 -rotate-90"
        viewBox="0 0 24 24"
        fill="none"
        strokeWidth={2}
      >
        <circle
          data-slot="progress-track"
          className="stroke-border"
          cx={12}
          cy={12}
          r={10}
        />
        <circle
          data-slot="progress-indicator"
          className="transition-[stroke-dashoffset] duration-300 motion-reduce:transition-none"
          cx={12}
          cy={12}
          r={10}
          strokeLinecap="round"
          strokeDasharray={circumference}
          strokeDashoffset={circumference * (1 - progress / 100)}
          opacity={progress > 0 ? 1 : 0}
        />
      </svg>
      <span aria-hidden="true" className="min-w-8 text-right">
        {Math.round(percent)}%
      </span>
    </ProgressPrimitive.Root>
  );
}

export { Progress, CircularProgress };
