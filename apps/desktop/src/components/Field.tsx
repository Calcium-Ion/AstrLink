import type { ReactNode } from "react";

import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

/**
 * Label + control + optional hint. Pages used to hand-roll this stack with
 * sub-scale font sizes, which is what made dense forms feel cramped; routing
 * them through one component keeps every form field on the token scale.
 */
export function Field({
  children,
  className,
  hint,
  htmlFor,
  label,
}: {
  children: ReactNode;
  className?: string;
  hint?: ReactNode;
  htmlFor?: string;
  label: ReactNode;
}) {
  return (
    <Label
      className={cn("flex min-w-0 flex-col items-stretch gap-1.5", className)}
      htmlFor={htmlFor}
    >
      <span className="text-xs font-medium text-text-secondary">{label}</span>
      {children}
      {hint ? (
        <span className="text-xs font-normal text-muted-foreground">
          {hint}
        </span>
      ) : null}
    </Label>
  );
}
