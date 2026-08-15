import type { HTMLAttributes, ReactNode } from "react";
import { Slot } from "radix-ui";

import { cn } from "@/lib/utils";

/**
 * A hairline-bounded region on the paper surface. Pages should reach for this
 * instead of assembling `border bg-card rounded-*` by hand, so every panel in
 * the app shares one geometry.
 */
export function Panel({
  asChild = false,
  className,
  tone = "card",
  ...props
}: HTMLAttributes<HTMLDivElement> & {
  asChild?: boolean;
  tone?: "card" | "inset";
}) {
  const Comp = asChild ? Slot.Root : "div";

  return (
    <Comp
      className={cn(
        "min-w-0 overflow-hidden rounded-md border",
        tone === "inset" ? "bg-muted" : "bg-card",
        className,
      )}
      data-slot="panel"
      {...props}
    />
  );
}

/** Sticky-free header band for a Panel: title on the left, actions on the right. */
export function PanelHeader({
  actions,
  children,
  className,
  ...props
}: Omit<HTMLAttributes<HTMLDivElement>, "children"> & {
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div
      className={cn(
        "flex min-w-0 items-start justify-between gap-3 border-b px-4 py-3",
        className,
      )}
      data-slot="panel-header"
      {...props}
    >
      <div className="min-w-0">{children}</div>
      {actions ? (
        <div className="flex shrink-0 flex-wrap items-center justify-end gap-1.5">
          {actions}
        </div>
      ) : null}
    </div>
  );
}
