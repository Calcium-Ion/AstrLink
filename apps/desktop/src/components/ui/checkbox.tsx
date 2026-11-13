import * as React from "react";
import { Check as CheckIcon, Minus as MinusIcon } from "@/components/icons";
import { Checkbox as CheckboxPrimitive } from "radix-ui";

import { cn } from "@/lib/utils";

function Checkbox({
  className,
  ...props
}: React.ComponentProps<typeof CheckboxPrimitive.Root>) {
  return (
    <CheckboxPrimitive.Root
      data-slot="checkbox"
      className={cn(
        "group/checkbox peer size-4 shrink-0 rounded-sm border border-input bg-card transition-colors outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-45 aria-invalid:border-destructive aria-invalid:ring-destructive/20 data-[state=checked]:border-primary-fill data-[state=checked]:bg-primary-fill data-[state=checked]:text-primary-fill-foreground data-[state=indeterminate]:border-primary-fill data-[state=indeterminate]:bg-primary-fill data-[state=indeterminate]:text-primary-fill-foreground",
        className,
      )}
      {...props}
    >
      <CheckboxPrimitive.Indicator
        data-slot="checkbox-indicator"
        className="grid place-content-center text-current transition-none"
      >
        <CheckIcon className="size-3.5 group-data-[state=indeterminate]/checkbox:hidden" />
        <MinusIcon className="hidden size-3.5 group-data-[state=indeterminate]/checkbox:block" />
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  );
}

/**
 * The checkbox look for a control that is itself the toggle, such as a list
 * row in selection mode. It is decorative; the control carries the state.
 */
function CheckboxMark({
  checked,
  className,
  disabled = false,
}: {
  checked: boolean;
  className?: string;
  disabled?: boolean;
}) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "grid size-4 shrink-0 place-content-center rounded-sm border border-input bg-card transition-colors data-[state=checked]:border-primary-fill data-[state=checked]:bg-primary-fill data-[state=checked]:text-primary-fill-foreground",
        disabled && "opacity-45",
        className,
      )}
      data-slot="checkbox-mark"
      data-state={checked ? "checked" : "unchecked"}
    >
      {/* The row hover would otherwise replay the tick on every checked row. */}
      {checked ? (
        <CheckIcon animateOnHover={false} className="size-3.5" />
      ) : null}
    </span>
  );
}

export { Checkbox, CheckboxMark };
