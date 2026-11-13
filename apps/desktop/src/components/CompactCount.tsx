import { cn } from "@/lib/utils";

import {
  formatCompactNumber,
  formatExactNumber,
} from "../format-compact-number";

export function CompactCount({
  className,
  placeholder = "—",
  short = false,
  value,
}: {
  className?: string;
  placeholder?: string;
  /** Fewer fraction digits as the number grows; see `formatCompactNumber`. */
  short?: boolean;
  value?: number | null;
}) {
  if (value == null || !Number.isFinite(value)) {
    return <span className={cn("tabular-nums", className)}>{placeholder}</span>;
  }
  const text = formatCompactNumber(value, { short });
  const exact = formatExactNumber(value);
  return (
    <span
      className={cn("tabular-nums", className)}
      title={exact === text ? undefined : exact}
    >
      {text}
    </span>
  );
}
