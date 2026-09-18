import type { ReactNode } from "react";
import { ChevronRight } from "@/components/icons";
import { cn } from "@/lib/utils";

export function HelpDisclosure({
  title,
  children,
  open,
  tone = "neutral",
}: {
  title: string;
  children: ReactNode;
  open?: boolean;
  tone?: "neutral" | "warning";
}) {
  return (
    <details className="group/details min-w-0 text-xs" open={open}>
      <summary
        className={cn(
          "flex w-fit cursor-pointer list-none items-center gap-1.5 rounded-sm transition-colors hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring [&::-webkit-details-marker]:hidden",
          tone === "warning"
            ? "text-warning-foreground"
            : "text-muted-foreground",
        )}
      >
        <ChevronRight
          aria-hidden="true"
          className="size-3.5 shrink-0 transition-transform group-open/details:rotate-90"
        />
        {title}
      </summary>
      <div className="mt-3 grid min-w-0 gap-3 text-xs leading-relaxed text-muted-foreground">
        {children}
      </div>
    </details>
  );
}
