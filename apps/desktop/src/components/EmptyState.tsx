import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export function EmptyState({
  action,
  className,
  description,
  illustration,
  title,
  titleId,
  variant = "default",
}: {
  action?: ReactNode;
  className?: string;
  description?: ReactNode;
  illustration?: ReactNode;
  title: ReactNode;
  titleId?: string;
  variant?: "default" | "page";
}) {
  const Heading = variant === "page" ? "h2" : "strong";
  return (
    <div
      className={cn(
        "flex min-w-0 flex-col items-center justify-center text-center",
        variant === "page"
          ? "gap-3 px-4 py-8"
          : "gap-1.5 rounded-md border border-dashed px-6 py-10",
        className,
      )}
      data-slot="empty-state"
    >
      {illustration ? <div className="mb-3 max-w-full" data-slot="empty-state-illustration">{illustration}</div> : null}
      <Heading
        className={cn("text-foreground", variant === "page" ? "text-xl font-semibold" : "text-sm font-medium")}
        id={titleId}
      >
        {title}
      </Heading>
      {description ? (
        <p className={cn("max-w-[52ch] text-text-secondary", variant === "page" ? "text-sm" : "text-xs")}>
          {description}
        </p>
      ) : null}
      {action ? <div className="mt-1.5">{action}</div> : null}
    </div>
  );
}
