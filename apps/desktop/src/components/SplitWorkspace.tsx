import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

/** Full-height peer panels; narrow windows stack usable panels in an inner scroller. */
export function SplitWorkspace({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "grid h-full min-h-0 min-w-0 auto-rows-[minmax(360px,1fr)] gap-3 overflow-y-auto overscroll-contain @[720px]:auto-rows-auto @[720px]:grid-cols-2 @[720px]:grid-rows-[minmax(0,1fr)] @[720px]:overflow-hidden",
        className,
      )}
      data-slot="split-workspace"
      data-tab-scroller
      {...props}
    />
  );
}
