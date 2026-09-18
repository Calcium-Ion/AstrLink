import { cn } from "@/lib/utils";

import {
  formatCompactNumber,
  formatExactNumber,
} from "../format-compact-number";

export function CompactCount({
  className,
  placeholder = "—",
  value,
}: {
  className?: string;
  placeholder?: string;
  value?: number | null;
}) {
  if (value == null || !Number.isFinite(value)) {
    return <span className={cn("tabular-nums", className)}>{placeholder}</span>;
  }
  const text = formatCompactNumber(value);
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
