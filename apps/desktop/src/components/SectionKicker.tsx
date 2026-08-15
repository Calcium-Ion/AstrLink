import type { HTMLAttributes } from "react";

import { cn } from "@/lib/utils";

export function SectionKicker({
  className,
  ...props
}: HTMLAttributes<HTMLSpanElement>) {
  return (
    <span
      className={cn(
        "block text-micro font-medium tracking-[0.08em] text-muted-foreground uppercase",
        className,
      )}
      {...props}
    />
  );
}
