import type { ReactNode } from "react";

import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";

/**
 * A phrase that opens its own editor: an outline button reading like part of a
 * sentence ("任务类型 是 编码、架构"), with the editor in a popover.
 *
 * `label` carries the muted part (field and operator) and `value` the part the
 * eye should land on. `invalid` marks a phrase that cannot be saved yet, such as
 * an option list with nothing chosen. Used for routing rule conditions and for
 * the options of a classification dimension.
 */
export function ConditionChip({
  label,
  value,
  icon,
  invalid = false,
  disabled = false,
  open,
  onOpenChange,
  children,
  contentClassName,
  className,
  "aria-label": ariaLabel,
  ...props
}: {
  label?: ReactNode;
  value: ReactNode;
  /** A small leading mark, e.g. the classifier icon on classifier fields. */
  icon?: ReactNode;
  invalid?: boolean;
  disabled?: boolean;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  /** The editor shown in the popover. */
  children: ReactNode;
  contentClassName?: string;
  className?: string;
  "aria-label": string;
} & Record<`data-${string}`, string | number | boolean | undefined>) {
  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger asChild disabled={disabled}>
        <Button
          type="button"
          variant="outline"
          size="sm"
          aria-label={ariaLabel}
          aria-invalid={invalid || undefined}
          className={cn(
            "h-7 max-w-full min-w-0 gap-1 px-2 font-normal [&>svg:not([class*='size-'])]:size-3",
            invalid &&
              "border-destructive/60 text-danger-foreground hover:text-danger-foreground",
            className,
          )}
          data-slot="condition-chip"
          {...props}
        >
          {icon}
          {label ? (
            <span
              className={cn(
                "shrink-0",
                invalid ? "text-danger-foreground" : "text-muted-foreground",
              )}
            >
              {label}
            </span>
          ) : null}
          <span className="min-w-0 truncate font-medium">{value}</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        sideOffset={4}
        className={cn("grid w-72 gap-3 text-xs", contentClassName)}
      >
        {children}
      </PopoverContent>
    </Popover>
  );
}
