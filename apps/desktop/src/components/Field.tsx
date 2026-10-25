import { useId, type ReactNode } from "react";

import { HelpPopover } from "@/components/HelpPopover";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

/**
 * Label + control + optional hint. Pages used to hand-roll this stack with
 * sub-scale font sizes, which is what made dense forms feel cramped; routing
 * them through one component keeps every form field on the token scale.
 *
 * Set `group` when the control is a set of buttons: a wrapping <label> would
 * forward clicks on its text to the first button.
 *
 * `tooltip` moves an explanation into a `?` beside the label instead of a text
 * block under the control. Hints that push a control row taller than its
 * neighbours are what made the dense grids look ragged, so a field that takes
 * `tooltip` should drop its `hint`. Set `inDialog` when the field sits in a
 * dialog, so its `?` opens above the dialog rather than beneath it.
 */
export function Field({
  children,
  className,
  group = false,
  hint,
  htmlFor,
  inDialog = false,
  label,
  tooltip,
  tooltipLabel,
}: {
  children: ReactNode;
  className?: string;
  group?: boolean;
  hint?: ReactNode;
  htmlFor?: string;
  inDialog?: boolean;
  label: ReactNode;
  tooltip?: ReactNode;
  /** Accessible name of the `?` trigger; defaults to the label text. */
  tooltipLabel?: string;
}) {
  const labelID = useId();
  const content = (
    <>
      <span className="flex min-w-0 items-center gap-1">
        <span
          className="min-w-0 text-xs font-medium text-text-secondary"
          id={group ? labelID : undefined}
        >
          {label}
        </span>
        {tooltip ? (
          <HelpPopover
            label={tooltipLabel ?? (typeof label === "string" ? label : "")}
            inDialog={inDialog}
          >
            {tooltip}
          </HelpPopover>
        ) : null}
      </span>
      {children}
      {hint ? (
        <span className="text-xs font-normal text-muted-foreground">
          {hint}
        </span>
      ) : null}
    </>
  );
  const layout = "flex min-w-0 flex-col items-stretch gap-1.5";
  if (group) {
    return (
      <div
        aria-labelledby={labelID}
        className={cn(layout, "text-xs leading-none font-medium", className)}
        role="group"
      >
        {content}
      </div>
    );
  }
  return (
    <Label className={cn(layout, className)} htmlFor={htmlFor}>
      {content}
    </Label>
  );
}
