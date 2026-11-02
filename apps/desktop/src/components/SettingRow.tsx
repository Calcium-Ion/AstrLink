import { useId, type ReactNode } from "react";

import { DataRow } from "@/components/DataRow";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";

/**
 * One setting in a Panel: its name and a one-line explanation on the left,
 * the control on the right. Pass `htmlFor` when the control is a single
 * labelable element so clicking the name reaches it; a dependent setting
 * sits `inset` under the one it depends on. A control too wide to leave the
 * name a readable column wraps onto its own line.
 */
export function SettingRow({
  children,
  className,
  disabled = false,
  hint,
  hintId,
  htmlFor,
  inset = false,
  label,
}: {
  children: ReactNode;
  className?: string;
  disabled?: boolean;
  hint?: ReactNode;
  hintId?: string;
  htmlFor?: string;
  inset?: boolean;
  label: ReactNode;
}) {
  return (
    <DataRow
      className={cn(
        "min-h-12 flex-wrap justify-between",
        inset && "pl-8",
        disabled && "opacity-60",
        className,
      )}
    >
      <div className="min-w-56 flex-1">
        {htmlFor ? (
          <Label
            className="block cursor-pointer text-sm leading-normal font-normal"
            htmlFor={htmlFor}
          >
            {label}
          </Label>
        ) : (
          <span className="block text-sm">{label}</span>
        )}
        {hint ? (
          <p className="mt-0.5 text-xs text-muted-foreground" id={hintId}>
            {hint}
          </p>
        ) : null}
      </div>
      <div className="ml-auto flex shrink-0 items-center gap-2">{children}</div>
    </DataRow>
  );
}

/** A SettingRow whose control is one Switch, named and described by the row. */
export function SettingSwitchRow({
  checked,
  disabled = false,
  hint,
  inset,
  label,
  onCheckedChange,
}: {
  checked: boolean;
  disabled?: boolean;
  hint?: ReactNode;
  inset?: boolean;
  label: ReactNode;
  onCheckedChange: (checked: boolean) => void;
}) {
  const id = useId();
  const hintId = hint ? `${id}-hint` : undefined;

  return (
    <SettingRow
      disabled={disabled}
      hint={hint}
      hintId={hintId}
      htmlFor={id}
      inset={inset}
      label={label}
    >
      <Switch
        aria-describedby={hintId}
        checked={checked}
        disabled={disabled}
        id={id}
        onCheckedChange={onCheckedChange}
      />
    </SettingRow>
  );
}
