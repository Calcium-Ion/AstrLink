import type { ReactNode } from "react";
import { ArrowLeft } from "@/components/icons";

import { Button } from "@/components/ui/button";
import { ActionGroup } from "@/components/ActionGroup";
import { cn } from "@/lib/utils";

export function PageHeader({
  actions,
  actionsClassName,
  back,
  className,
  description,
  headingLevel = 1,
  title,
  titleGroupClassName,
  titleId,
  titleSuffix,
  variant = "compact",
}: {
  actions?: ReactNode;
  actionsClassName?: string;
  back?: {
    label: string;
    onClick: () => void;
  };
  // Lets a page tighten the default spacing when its own toolbar follows.
  className?: string;
  description?: ReactNode;
  headingLevel?: 1 | 2;
  title: string;
  titleGroupClassName?: string;
  titleId?: string;
  titleSuffix?: ReactNode;
  variant?: "card" | "plain" | "compact";
}) {
  const compact = variant === "compact";
  const Heading = headingLevel === 1 ? "h1" : "h2";
  return (
    <header
      className={cn(
        "flex min-w-0 shrink-0 justify-between",
        compact ? "mb-3 items-center gap-3 border-b py-2" : "items-end gap-6",
        !compact && variant === "card"
          ? "border-b bg-card px-4 pt-4 pb-3"
          : !compact
            ? "mb-5 border-b pb-4"
            : null,
        className,
      )}
      data-slot="page-header"
    >
      <div
        className={cn(
          "min-w-0",
          compact && "flex items-center gap-2",
          titleGroupClassName,
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
        <Heading
          className={cn(
            "text-sm font-semibold tracking-tight",
            compact && "truncate",
          )}
          id={titleId}
          title={title}
        >
          {title}
        </Heading>
        {titleSuffix}
        {compact || !description ? null : (
          <p className="mt-1 max-w-[64ch] truncate text-xs text-text-secondary [&_code]:text-text-secondary">
            {description}
          </p>
        )}
      </div>
      {actions ? (
        <ActionGroup className={cn("shrink-0", actionsClassName)}>
          {actions}
        </ActionGroup>
      ) : null}
    </header>
  );
}
