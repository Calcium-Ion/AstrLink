import type { HTMLAttributes } from "react";

import { cn } from "@/lib/utils";

export function SectionKicker({
  className,
  ...props
}: HTMLAttributes<HTMLSpanElement>) {
  return (
    <span
      className={cn(
        "block text-[9.5px] font-extrabold tracking-[0.12em] text-accent-foreground uppercase",
        className,
      )}
      {...props}
    />
  );
}
