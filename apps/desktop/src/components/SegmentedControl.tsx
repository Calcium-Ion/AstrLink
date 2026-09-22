import { ToggleGroup } from "radix-ui";
import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export function SegmentedControl<T extends string>({
  label,
  options,
  value,
  onValueChange,
  disabled = false,
  variant = "default",
}: {
  label: string;
  options: readonly {
    value: T;
    label: string;
    count?: number;
    icon?: ReactNode;
  }[];
  value: T;
  onValueChange: (value: T) => void;
  disabled?: boolean;
  variant?: "default" | "line";
}) {
  return (
    <ToggleGroup.Root
      aria-label={label}
      disabled={disabled}
      className={cn(
        "inline-flex max-w-full flex-wrap items-center",
        variant === "line" ? "gap-4" : "gap-0.5 rounded-md bg-muted p-0.5",
      )}
      onValueChange={(next) => {
        const option = options.find((item) => item.value === next);
        if (option) onValueChange(option.value);
      }}
      type="single"
      value={value}
    >
      {options.map((option) => (
        <ToggleGroup.Item
          className={cn(
            "inline-flex h-7 items-center justify-center gap-2 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-45",
            variant === "line"
              ? "border-b-2 border-transparent px-0.5 data-[state=on]:border-primary data-[state=on]:text-primary"
              : "rounded-sm px-2.5 data-[state=on]:bg-background data-[state=on]:text-foreground",
          )}
          key={option.value}
          value={option.value}
        >
          {option.icon ? (
            <span aria-hidden="true" className="[&_svg]:size-3.5">
              {option.icon}
            </span>
          ) : null}
          {option.label}
          {option.count !== undefined ? (
            <span className="min-w-3 text-micro text-muted-foreground tabular-nums">
              {option.count}
            </span>
          ) : null}
        </ToggleGroup.Item>
      ))}
    </ToggleGroup.Root>
  );
}
