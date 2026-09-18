import { ToggleGroup } from "radix-ui";

import { cn } from "@/lib/utils";

export function SegmentedControl<T extends string>({
  label,
  options,
  value,
  onValueChange,
}: {
  label: string;
  options: readonly { value: T; label: string; count?: number }[];
  value: T;
  onValueChange: (value: T) => void;
}) {
  return (
    <ToggleGroup.Root
      aria-label={label}
      className="inline-flex max-w-full flex-wrap items-center gap-0.5 rounded-md bg-muted p-0.5"
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
            "inline-flex h-7 items-center justify-center gap-2 rounded-sm px-2.5 text-xs font-medium text-muted-foreground transition-colors",
            "hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring data-[state=on]:bg-background data-[state=on]:text-foreground",
          )}
          key={option.value}
          value={option.value}
        >
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
