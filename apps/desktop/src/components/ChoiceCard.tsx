import type { ReactNode } from "react";

import { Label } from "@/components/ui/label";
import { RadioGroupItem } from "@/components/ui/radio-group";
import { cn } from "@/lib/utils";

export function ChoiceCard({
  className,
  description,
  disabled,
  label,
  selected,
  value,
}: {
  className?: string;
  description?: ReactNode;
  disabled?: boolean;
  label: string;
  selected: boolean;
  value: string;
}) {
  return (
    <Label
      className={cn(
        "flex min-w-0 cursor-pointer items-start gap-2 rounded-md border bg-card p-2.5 transition-colors hover:border-primary/40",
        selected && "border-primary/50 bg-accent",
        className,
      )}
    >
      <RadioGroupItem aria-label={label} disabled={disabled} value={value} />
      <span className="grid min-w-0 gap-0.5">
        <strong className="text-sm font-medium">{label}</strong>
        {description ? (
          <small className="text-xs font-normal text-muted-foreground">
            {description}
          </small>
        ) : null}
      </span>
    </Label>
  );
}
