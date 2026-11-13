import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export interface BarListItem {
  key: string;
  label: ReactNode;
  value: number;
  /** The figure at the end of the row; the value as is without it. */
  display?: ReactNode;
}

/**
 * A few named quantities side by side: one thin bar each in a single hue on
 * a lighter track of the same hue, the figure at the end. Every bar is
 * labelled, so the hue never carries identity.
 */
export function BarList({
  items,
  label,
  max,
  className,
}: {
  items: readonly BarListItem[];
  /** What the bars measure, for screen readers. */
  label: string;
  /** The value of a full bar; the largest value without it. */
  max?: number;
  className?: string;
}) {
  const top = max ?? Math.max(0, ...items.map((item) => item.value));
  return (
    <ul
      aria-label={label}
      className={cn("grid gap-1.5 text-xs", className)}
      data-slot="bar-list"
    >
      {items.map((item) => (
        <li
          className="grid grid-cols-[minmax(4.5rem,max-content)_minmax(3rem,1fr)_minmax(2.5rem,max-content)] items-center gap-2.5"
          key={item.key}
        >
          <span className="truncate text-muted-foreground">{item.label}</span>
          <span
            aria-hidden="true"
            className="h-1.5 overflow-hidden rounded-full bg-primary/12"
          >
            <span
              className="block h-full rounded-full bg-primary"
              style={{
                width:
                  top > 0 && item.value > 0
                    ? `${Math.max(3, Math.min(100, (item.value / top) * 100))}%`
                    : "0%",
              }}
            />
          </span>
          <span className="text-right tabular-nums">
            {item.display ?? item.value}
          </span>
        </li>
      ))}
    </ul>
  );
}
