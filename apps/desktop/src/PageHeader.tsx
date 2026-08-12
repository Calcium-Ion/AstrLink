import type { ReactNode } from "react";
import { ArrowLeft } from "lucide-react";

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
        "flex min-w-0 shrink-0 items-start justify-between gap-5",
        variant === "card"
          ? "border-b bg-card px-[22px] pt-5 pb-[18px]"
          : "mb-4",
      )}
      data-slot="page-header"
    >
      <div className="min-w-0">
        {back ? (
          <Button
            aria-label={back.label}
            className="-mt-[3px] mb-[7px] h-auto gap-[5px] px-0 py-[3px] text-[11px] font-bold no-underline hover:bg-transparent hover:no-underline"
            onClick={back.onClick}
            size="sm"
            type="button"
            variant="link"
          >
            <ArrowLeft aria-hidden="true" className="size-3.5" />
            {back.label}
          </Button>
        ) : null}
        {eyebrow ? (
          <span className="block text-[9.5px] font-extrabold tracking-[0.12em] text-accent-foreground uppercase">
            {eyebrow}
          </span>
        ) : null}
        <h1
          className="mt-[5px] text-xl font-[750] tracking-[-0.025em]"
          id={titleId}
        >
          {title}
        </h1>
        {description ? (
          <p className="mt-1.5 max-w-[680px] overflow-hidden text-[10.5px] leading-[1.55] text-text-secondary text-ellipsis [&_code]:text-text-secondary">
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
