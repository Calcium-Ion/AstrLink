import type { HTMLAttributes } from "react";

import { cn } from "@/lib/utils";

import "./activity-slot.css";

/**
 * The bordered slot that shows what a worker is busy with, as on a job queue
 * board: a band sweeps across it while work runs, hatching marks a pause.
 */
export function ActivitySlot({
  className,
  state,
  ...props
}: HTMLAttributes<HTMLSpanElement> & { state: "running" | "idle" }) {
  return (
    <span
      className={cn("activity-slot", className)}
      data-slot="activity-slot"
      data-state={state}
      {...props}
    />
  );
}
