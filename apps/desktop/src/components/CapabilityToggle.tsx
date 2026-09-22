import { useId } from "react";

import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";

/** A labelled capability setting for service editors. */
export function CapabilityToggle({
  label,
  description,
  checked,
  disabled,
  size = "sm",
  onCheckedChange,
}: {
  label: string;
  description: string;
  checked: boolean;
  disabled?: boolean;
  size?: "sm" | "default";
  onCheckedChange: (checked: boolean) => void;
}) {
  const id = useId();
  return (
    <div className="flex w-full items-center justify-between gap-3">
      <div className="min-w-0">
        <label htmlFor={id} className={cn("cursor-pointer text-foreground", size === "sm" ? "text-xs font-medium" : "text-sm font-normal")}>
          {label}
        </label>
        <p id={`${id}-description`} className="mt-1 max-w-prose text-xs leading-relaxed text-muted-foreground">
          {description}
        </p>
      </div>
      <Switch id={id} size={size} aria-label={label} aria-describedby={`${id}-description`}
        checked={checked} disabled={disabled} onCheckedChange={onCheckedChange} />
    </div>
  );
}
