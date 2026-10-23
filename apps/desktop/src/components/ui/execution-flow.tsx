import type { ReactNode } from "react";

import { ThinkingLoader } from "@/components/ui/thinking-loader";
import { cn } from "@/lib/utils";

import "./execution-flow.css";

/** Connected phase nodes for a compact, virtualizable execution flow. */
export function ExecutionNode({
  children,
  first,
  last,
  pending,
  label,
  className,
}: {
  children: ReactNode;
  first: boolean;
  last: boolean;
  pending: boolean;
  label: string;
  className?: string;
}) {
  return (
    <span
      aria-hidden="true"
      className="relative flex h-full items-center justify-center"
    >
      {!first ? (
        <span className="absolute top-0 bottom-1/2 border-l border-border" />
      ) : null}
      {!last ? (
        <span className="absolute top-1/2 bottom-0 border-l border-border" />
      ) : null}
      <span
        className={cn(
          "relative z-1 flex size-6 shrink-0 items-center justify-center rounded-full border border-current/15 bg-card font-mono text-micro font-semibold shadow-xs transition-shadow group-hover:ring-3 group-hover:ring-current/10 motion-reduce:transition-none [&_svg]:size-3",
          first && "size-7 rounded-lg",
          className,
        )}
      >
        {pending ? <ThinkingLoader compact label={label} /> : children}
      </span>
    </span>
  );
}

/** A phase's actual interval within its own call, including zero-time ticks. */
export function ExecutionSpan({
  offset,
  duration,
  label,
  title,
  pending,
  className,
}: {
  offset?: number;
  duration?: number;
  label: string;
  title: string;
  pending: boolean;
  className?: string;
}) {
  const timed = offset !== undefined && duration !== undefined;
  return (
    <span className="flex min-w-0 items-center gap-2" title={title}>
      <span
        aria-hidden="true"
        className="relative hidden h-6 min-w-0 flex-1 overflow-hidden rounded bg-muted/40 @min-[560px]/trajectory:block"
      >
        {[0.25, 0.5, 0.75].map((position) => (
          <span
            key={position}
            className="absolute inset-y-0 border-l border-border/60"
            style={{ left: `${position * 100}%` }}
          />
        ))}
        {timed ? (
          <span
            data-slot="execution-span"
            className={cn(
              "absolute inset-y-1.5 min-w-0.5 rounded-sm opacity-65 transition-[width,left,opacity] duration-500 group-hover:opacity-100 group-data-[selected=true]:opacity-100 motion-reduce:transition-none",
              pending && "execution-span-pending opacity-100",
              className,
            )}
            style={{
              left: `min(${offset * 100}%, calc(100% - 2px))`,
              width: `${duration * 100}%`,
            }}
          />
        ) : null}
      </span>
      <span className="w-14 shrink-0 text-right font-mono text-micro tabular-nums text-muted-foreground">
        {label}
      </span>
    </span>
  );
}
