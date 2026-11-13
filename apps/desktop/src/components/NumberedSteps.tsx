import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

/** Steps the user takes elsewhere, numbered in order. */
export function NumberedSteps({
  steps,
  className,
}: {
  steps: readonly ReactNode[];
  className?: string;
}) {
  return (
    <ol className={cn("m-0 grid list-none gap-2 p-0 text-sm", className)}>
      {steps.map((step, index) => (
        <li key={index} className="flex min-w-0 items-start gap-2.5">
          <span
            aria-hidden="true"
            className="mt-px flex size-5 shrink-0 items-center justify-center rounded-full bg-primary/10 text-xs font-medium text-primary tabular-nums"
          >
            {index + 1}
          </span>
          <span className="min-w-0 leading-5">{step}</span>
        </li>
      ))}
    </ol>
  );
}
