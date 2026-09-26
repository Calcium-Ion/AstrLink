import { useT } from "../i18n";
import {
  isUsageRangePreset,
  USAGE_RANGE_PRESETS,
  type UsageRangePreset,
} from "../usage-range";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "./ui/select";

export function UsageRangeSelect({
  label,
  preset,
  onChange,
}: {
  label: string;
  preset: UsageRangePreset;
  onChange: (preset: UsageRangePreset) => void;
}) {
  const t = useT();
  return (
    <Select
      value={preset}
      onValueChange={(value) => {
        if (isUsageRangePreset(value)) onChange(value);
      }}
    >
      <SelectTrigger
        aria-label={label}
        size="sm"
        className="w-auto shrink-0 text-xs"
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent align="end">
        {USAGE_RANGE_PRESETS.map((value) => (
          <SelectItem key={value} value={value}>
            {t(`overview.range.${value}`)}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
