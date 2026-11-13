import { cn } from "@/lib/utils";

/**
 * One day of a strip: how often something happened, and whether the day is
 * marked, such as the day it began. Null leaves the day blank.
 */
export type DayStripDay = { count: number; marked?: boolean } | null;

const CELL = "size-2.25 shrink-0 rounded-[2px]";

function tone(count: number): string {
  return count >= 2 ? "bg-tide" : count === 1 ? "bg-tide/45" : "bg-muted";
}

/**
 * A row of small day cells, the oldest first: grey for a quiet day, light
 * for one, full for two or more. A marked day is outlined; blank days keep
 * their place, so strips line up by day.
 */
export function DayStrip({
  days,
  label,
  className,
}: {
  days: readonly DayStripDay[];
  label?: string;
  className?: string;
}) {
  return (
    <span
      aria-label={label}
      className={cn("flex items-center justify-end gap-0.75", className)}
      data-slot="day-strip"
      role={label ? "img" : undefined}
      title={label}
    >
      {days.map((day, index) => (
        <span
          aria-hidden="true"
          className={cn(
            CELL,
            day ? tone(day.count) : "bg-transparent",
            day?.marked &&
              "outline-[1.5px] outline-offset-1 outline-primary outline-solid",
          )}
          data-count={day?.count}
          data-marked={day?.marked || undefined}
          key={index}
        />
      ))}
    </span>
  );
}

/** One cell as the strip draws it, for a legend beside it. */
export function DayStripSwatch({
  count = 2,
  marked = false,
}: {
  count?: number;
  marked?: boolean;
}) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        CELL,
        "inline-block",
        tone(count),
        marked &&
          "mx-0.5 outline-[1.5px] outline-offset-1 outline-primary outline-solid",
      )}
    />
  );
}
