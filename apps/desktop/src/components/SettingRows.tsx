import type { ReactNode } from "react";

import { HelpPopover } from "@/components/HelpPopover";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

/**
 * A titled group of setting rows: a small heading over rows divided by
 * hairlines, without a frame around them.
 */
export function SettingGroup({
  title,
  children,
  className,
}: {
  title?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={cn("grid min-w-0 gap-1", className)}>
      {title ? (
        <h3 className="text-xs font-medium text-muted-foreground">{title}</h3>
      ) : null}
      <div className="min-w-0 divide-y border-y">{children}</div>
    </section>
  );
}

/**
 * The width a setting's text field or select takes, so the controls of a
 * group line up. A number field stays narrow and a switch keeps its size.
 */
export const settingControlWidth = "w-full @[560px]:w-80";

/**
 * One setting: its name on the left, its controls on the right, the name
 * above them in a narrow container. An explanation sits behind the `?`
 * beside the name, never as text under the control.
 */
export function SettingRow({
  label,
  htmlFor,
  required = false,
  help,
  helpLabel,
  children,
  className,
}: {
  label: ReactNode;
  htmlFor?: string;
  required?: boolean;
  help?: ReactNode;
  /** Accessible name of the `?` trigger; defaults to the label text. */
  helpLabel?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "grid min-w-0 items-center gap-x-6 gap-y-1.5 py-2.5 @[560px]:grid-cols-[minmax(8rem,12rem)_minmax(0,1fr)]",
        className,
      )}
      data-slot="setting-row"
    >
      <div className="flex min-w-0 items-center gap-1">
        {htmlFor ? (
          <Label className="text-xs font-medium" htmlFor={htmlFor}>
            {label}
          </Label>
        ) : (
          <span className="text-xs font-medium">{label}</span>
        )}
        {required ? (
          <span aria-hidden="true" className="text-xs text-danger-foreground">
            *
          </span>
        ) : null}
        {help ? (
          <HelpPopover
            label={helpLabel ?? (typeof label === "string" ? label : "")}
          >
            {help}
          </HelpPopover>
        ) : null}
      </div>
      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
        {children}
      </div>
    </div>
  );
}
