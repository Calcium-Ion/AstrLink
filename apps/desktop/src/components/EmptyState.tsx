import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export function EmptyState({
  action,
  className,
  description,
  title,
}: {
  action?: ReactNode;
  className?: string;
  description?: ReactNode;
  title: ReactNode;
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center gap-1.5 rounded-md border border-dashed px-6 py-10 text-center",
        className,
      )}
      data-slot="empty-state"
    >
      <strong className="text-sm font-medium text-foreground">{title}</strong>
      {description ? (
        <p className="max-w-[52ch] text-xs text-text-secondary">
          {description}
        </p>
      ) : null}
      {action ? <div className="mt-1.5">{action}</div> : null}
    </div>
  );
}
