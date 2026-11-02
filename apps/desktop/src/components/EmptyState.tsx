import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

/**
 * Says why a place is empty. The `inline` variant is one dashed band with an
 * icon beside the words, for an empty part that sits above other content.
 */
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
  variant?: "default" | "page" | "inline";
}) {
  const Heading = variant === "page" ? "h2" : "strong";
  if (variant === "inline") {
    return (
      <div
        className={cn(
          "flex min-w-0 items-center gap-3 rounded-lg border border-dashed px-3.5 py-3 text-left",
          className,
        )}
        data-slot="empty-state"
      >
        {illustration ? (
          <div
            className="grid size-8.5 shrink-0 place-items-center rounded-md bg-muted text-text-secondary"
            data-slot="empty-state-illustration"
          >
            {illustration}
          </div>
        ) : null}
        <div className="min-w-0 flex-1">
          <Heading
            className="block text-sm font-semibold text-foreground"
            id={titleId}
          >
            {title}
          </Heading>
          {description ? (
            <p className="text-xs text-text-secondary">{description}</p>
          ) : null}
        </div>
        {action ? <div className="shrink-0">{action}</div> : null}
      </div>
    );
  }
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
      {illustration ? (
        <div className="mb-3 max-w-full" data-slot="empty-state-illustration">
          {illustration}
        </div>
      ) : null}
      <Heading
        className={cn(
          "text-foreground",
          variant === "page" ? "text-xl font-semibold" : "text-sm font-medium",
        )}
        id={titleId}
      >
        {title}
      </Heading>
      {description ? (
        <p
          className={cn(
            "max-w-[52ch] text-text-secondary",
            variant === "page" ? "text-sm" : "text-xs",
          )}
        >
          {description}
        </p>
      ) : null}
      {action ? <div className="mt-1.5">{action}</div> : null}
    </div>
  );
}
