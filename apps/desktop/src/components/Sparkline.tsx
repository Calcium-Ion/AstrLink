import { useRef, useState } from "react";

import { ChartTooltipCard } from "@/components/ChartTooltip";
import { cn } from "@/lib/utils";

export interface SparklinePoint {
  /** The period, such as a day, as the tooltip names it. */
  label: string;
  value: number;
}

/**
 * A run of thin columns under a stat tile's number, one per period: the last,
 * current one in the accent and the rest in the quiet ink, an empty period a
 * hairline on the baseline. Hovering a column shows its value and period; the
 * page keeps the same numbers in a table.
 */
export function Sparkline({
  points,
  format,
  label,
  tone = "primary",
  className,
}: {
  /** Oldest first. */
  points: readonly SparklinePoint[];
  format: (value: number) => string;
  /** What the columns count, for screen readers. */
  label: string;
  /** warning for counts of trouble. */
  tone?: "primary" | "warning";
  className?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [hovered, setHovered] = useState<number | null>(null);
  const max = Math.max(0, ...points.map((point) => point.value));
  const point = hovered === null ? undefined : points[hovered];
  const width = ref.current?.getBoundingClientRect().width ?? 0;
  return (
    <div
      aria-label={`${label}: ${points
        .map((item) => `${item.label} ${format(item.value)}`)
        .join("; ")}`}
      className={cn("relative flex h-6 items-end gap-0.5", className)}
      data-slot="sparkline"
      onPointerLeave={() => setHovered(null)}
      ref={ref}
      role="img"
    >
      {points.map((item, index) => (
        <span
          className="flex h-full min-w-0 flex-1 items-end justify-center"
          key={item.label}
          onPointerEnter={() => setHovered(index)}
        >
          <span
            className={cn(
              "w-full max-w-[18px] rounded-t-[2px] transition-opacity",
              item.value <= 0
                ? "bg-border"
                : index === points.length - 1
                  ? tone === "warning"
                    ? "bg-warning"
                    : "bg-primary"
                  : tone === "warning"
                    ? "bg-warning/45"
                    : "bg-muted-foreground/35",
              hovered !== null && hovered !== index && "opacity-60",
            )}
            data-value={item.value}
            style={{
              height:
                item.value > 0 && max > 0
                  ? `${Math.max(14, (item.value / max) * 100)}%`
                  : "1px",
            }}
          />
        </span>
      ))}
      {point ? (
        <ChartTooltipCard
          active
          coordinate={{
            x: ((hovered ?? 0) + 0.5) * (width / points.length),
            y: -6,
          }}
          originRef={ref}
          placementKey={`${hovered}`}
        >
          <span className="font-medium tabular-nums">
            {format(point.value)}
          </span>
          <span className="text-muted-foreground">{point.label}</span>
        </ChartTooltipCard>
      ) : null}
    </div>
  );
}
