import type { ReactNode } from "react";
import { ArrowLeft } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export function PageHeader({
  actions,
  back,
  description,
  title,
  titleId,
  variant = "plain",
}: {
  actions?: ReactNode;
  back?: {
    label: string;
    onClick: () => void;
  };
  description?: ReactNode;
  title: string;
  titleId?: string;
  variant?: "card" | "plain" | "compact";
}) {
  const compact = variant === "compact";
  return (
    <header
      className={cn(
        "flex min-w-0 shrink-0 justify-between",
        compact
          ? "mb-3 items-center gap-3 border-b py-2"
          : "items-end gap-6",
        !compact && variant === "card"
          ? "border-b bg-card px-4 pt-4 pb-3"
          : !compact
            ? "mb-5 border-b pb-4"
            : null,
      )}
      data-slot="page-header"
    >
      <div
        className={cn(
          "min-w-0",
          compact && "flex items-center gap-2",
        )}
      >
        {back ? (
          <Button
            aria-label={back.label}
            className={cn(
              "h-auto gap-1 px-0 py-0.5 text-micro text-muted-foreground no-underline hover:bg-transparent hover:text-foreground hover:no-underline has-[>svg]:px-0",
              !compact && "-mt-1 mb-1.5",
            )}
            onClick={back.onClick}
            size="sm"
            type="button"
            variant="link"
          >
            <ArrowLeft aria-hidden="true" className="size-3" />
            {back.label}
          </Button>
        ) : null}
        {compact && back ? (
          <span aria-hidden="true" className="text-border">
            /
          </span>
        ) : null}
        <h1
          className={cn(
            "font-semibold tracking-tight",
            compact ? "truncate text-sm" : "text-xl",
          )}
          id={titleId}
        >
          {title}
        </h1>
        {compact || !description ? null : (
          <p className="mt-1 max-w-[64ch] truncate text-xs text-text-secondary [&_code]:text-text-secondary">
            {description}
          </p>
        )}
      </div>
      {actions ? (
        <div className="flex shrink-0 items-center gap-2">{actions}</div>
      ) : null}
    </header>
  );
}
