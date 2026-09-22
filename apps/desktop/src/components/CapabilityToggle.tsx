import { useId } from "react";

import { Switch } from "@/components/ui/switch";

/** A labelled capability setting for service editors. */
export function CapabilityToggle({
  label,
  description,
  checked,
  disabled,
  onCheckedChange,
}: {
  label: string;
  description: string;
  checked: boolean;
  disabled?: boolean;
  onCheckedChange: (checked: boolean) => void;
}) {
  const id = useId();
  return (
    <div className="flex w-full items-center justify-between gap-3">
      <div className="min-w-0">
        <label htmlFor={id} className="cursor-pointer text-xs font-medium text-foreground">
          {label}
        </label>
        <p id={`${id}-description`} className="mt-1 max-w-prose text-xs leading-relaxed text-muted-foreground">
          {description}
        </p>
      </div>
      <Switch id={id} size="sm" aria-label={label} aria-describedby={`${id}-description`}
        checked={checked} disabled={disabled} onCheckedChange={onCheckedChange} />
    </div>
  );
}
