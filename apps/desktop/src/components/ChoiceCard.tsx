import type { ComponentProps, ReactNode } from "react";

import { Label } from "@/components/ui/label";
import { RadioGroupItem } from "@/components/ui/radio-group";
import { cn } from "@/lib/utils";

export function ChoiceCard({
  className,
  description,
  disabled,
  id,
  icon,
  label,
  layout = "row",
  selected,
  value,
  ...radioProps
}: {
  className?: string;
  description?: ReactNode;
  disabled?: boolean;
  id?: string;
  icon?: ReactNode;
  label: string;
  layout?: "row" | "tile";
  selected: boolean;
  value: string;
} & Pick<
  ComponentProps<typeof RadioGroupItem>,
  "onClick" | "aria-haspopup" | "aria-expanded"
>) {
  return (
    <Label
      className={cn(
        "flex min-w-0 cursor-pointer items-start gap-2 rounded-md border bg-card p-2.5 transition-colors hover:border-primary/40",
        layout === "tile" &&
          "relative flex-col items-center justify-center gap-2 px-2 py-3 text-center has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring/25",
        selected && "border-primary/50 bg-accent",
        disabled && "cursor-not-allowed opacity-50 hover:border-border",
        className,
      )}
      htmlFor={id}
    >
      <RadioGroupItem
        aria-label={label}
        className={cn("shrink-0", layout === "tile" && "sr-only")}
        disabled={disabled}
        id={id}
        value={value}
        {...radioProps}
      />
      {icon ? (
        <span aria-hidden="true" className="inline-flex shrink-0">
          {icon}
        </span>
      ) : null}
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
