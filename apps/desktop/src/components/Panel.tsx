import type { HTMLAttributes, ReactNode } from "react";
import { Slot } from "radix-ui";

import { cn } from "@/lib/utils";
import { ActionGroup } from "@/components/ActionGroup";

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
        <ActionGroup className="shrink-0 gap-1.5">{actions}</ActionGroup>
      ) : null}
    </div>
  );
}

/** Fill the panel and keep long content inside its own scrolling region. */
export function PanelBody({
  className,
  ...props
}: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "relative min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain p-4",
        className,
      )}
      data-slot="panel-body"
      data-tab-scroller
      {...props}
    />
  );
}

/** A wrapping footer keeps supporting details beside the panel's actions. */
export function PanelFooter({
  actions,
  children,
  className,
  ...props
}: HTMLAttributes<HTMLDivElement> & { actions?: ReactNode }) {
  return (
    <div
      className={cn(
        "mt-auto flex min-w-0 flex-wrap items-center justify-between gap-3 border-t bg-muted/40 px-4 py-3",
        className,
      )}
      data-slot="panel-footer"
      {...props}
    >
      {children ? <div className="min-w-0">{children}</div> : null}
      {actions ? <ActionGroup>{actions}</ActionGroup> : null}
    </div>
  );
}
