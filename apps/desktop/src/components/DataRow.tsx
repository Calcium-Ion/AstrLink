import type { HTMLAttributes, ReactNode } from "react";
import { Slot } from "radix-ui";

import { cn } from "@/lib/utils";

/**
 * One line of a hairline-separated list. Rows carry no fill or radius of their
 * own; the enclosing Panel supplies the boundary.
 */
export function DataRow({
  asChild = false,
  className,
  ...props
}: HTMLAttributes<HTMLDivElement> & { asChild?: boolean }) {
  const Comp = asChild ? Slot.Root : "div";

  return (
    <Comp
      className={cn(
        "flex min-w-0 items-center gap-3 border-b px-4 py-2.5 last:border-b-0",
        className,
      )}
      data-slot="data-row"
      {...props}
    />
  );
}

/** Label above value, the recurring pairing in overview and detail panels. */
export function DataField({
  className,
  label,
  value,
  ...props
}: Omit<HTMLAttributes<HTMLDivElement>, "children"> & {
  label: ReactNode;
  value: ReactNode;
}) {
  return (
    <div className={cn("flex min-w-0 flex-col gap-1", className)} {...props}>
      <span className="text-micro font-medium tracking-[0.06em] text-muted-foreground uppercase">
        {label}
      </span>
      <span className="min-w-0 text-sm">{value}</span>
    </div>
  );
}
