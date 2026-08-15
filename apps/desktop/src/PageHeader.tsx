import type { ReactNode } from "react";
import { ArrowLeft } from "lucide-react";

import { SectionKicker } from "@/components/SectionKicker";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export function PageHeader({
  actions,
  back,
  description,
  eyebrow,
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
  eyebrow?: ReactNode;
  title: string;
  titleId?: string;
  variant?: "card" | "plain";
}) {
  return (
    <header
      className={cn(
        "flex min-w-0 shrink-0 items-end justify-between gap-6",
        variant === "card"
          ? "border-b bg-card px-4 pt-4 pb-3"
          : "mb-5 border-b pb-4",
      )}
      data-slot="page-header"
    >
      <div className="min-w-0">
        {back ? (
          <Button
            aria-label={back.label}
            className="-mt-1 mb-1.5 h-auto gap-1 px-0 py-0.5 text-micro text-muted-foreground no-underline hover:bg-transparent hover:text-foreground hover:no-underline"
            onClick={back.onClick}
            size="sm"
            type="button"
            variant="link"
          >
            <ArrowLeft aria-hidden="true" className="size-3" />
            {back.label}
          </Button>
        ) : null}
        {eyebrow ? <SectionKicker>{eyebrow}</SectionKicker> : null}
        <h1
          className="mt-1 text-xl font-semibold tracking-tight"
          id={titleId}
        >
          {title}
        </h1>
        {description ? (
          <p className="mt-1 max-w-[64ch] truncate text-xs text-text-secondary [&_code]:text-text-secondary">
            {description}
          </p>
        ) : null}
      </div>
      {actions ? (
        <div className="flex shrink-0 items-center gap-2">{actions}</div>
      ) : null}
    </header>
  );
}
