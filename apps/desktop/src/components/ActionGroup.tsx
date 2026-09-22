import type { HTMLAttributes } from "react";

import { cn } from "@/lib/utils";

/**
 * Right-aligned actions, including when a toolbar wraps. Supply children in
 * visual and keyboard order: unframed text/icons, rectangular controls/badges,
 * then framed buttons. Keep related controls (such as tabs) together.
 */
export function ActionGroup({
  className,
  ...props
}: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "ml-auto flex min-w-0 max-w-full flex-wrap items-center justify-end gap-2",
        className,
      )}
      data-slot="action-group"
      {...props}
    />
  );
}
