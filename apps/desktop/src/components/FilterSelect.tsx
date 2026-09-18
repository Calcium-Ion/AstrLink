import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

/** Compact, labelled select for list toolbars; long values stay inside the control. */
export function FilterSelect({
  ariaLabel,
  className,
  disabled,
  label,
  value,
  onChange,
  options,
}: {
  ariaLabel: string;
  className?: string;
  disabled?: boolean;
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: Array<{ label: string; value: string }>;
}) {
  return (
    <Select
      disabled={disabled}
      onValueChange={(next) => onChange(next === "__all__" ? "" : next)}
      value={value || "__all__"}
    >
      <SelectTrigger
        aria-label={ariaLabel}
        className={cn("min-w-0 text-xs", className)}
        size="sm"
        title={options.find((option) => option.value === value)?.label}
      >
        <span className="flex min-w-0 items-center gap-2">
          <span className="shrink-0 font-normal text-muted-foreground">{label}</span>
          <span className="min-w-0 truncate">
            <SelectValue />
          </span>
        </span>
      </SelectTrigger>
      <SelectContent>
        {options.map((option) => (
          <SelectItem key={option.value || "__all__"} value={option.value || "__all__"}>
            {option.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
