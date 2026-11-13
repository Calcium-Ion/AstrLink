import { useId, useRef, useState } from "react";

import { Check, ChevronDown } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { selectTriggerClassName } from "@/components/ui/select";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { currentLocale, useT } from "@/i18n";
import { cn } from "@/lib/utils";
import {
  customRangeProblem,
  isCustomUsageRange,
  localDayKey,
  parseDayKey,
  resolveUsageWindow,
  usageRangeKey,
  type CustomUsageRange,
  type UsageRange,
  type UsageRangePreset,
} from "@/usage-range";

/**
 * Picks a usage range: one of `presets`, or whole days typed in below them.
 * With `allLabel` it also offers no range, which `value` reports as null.
 * Without `custom` it offers the presets only.
 */
export function UsageRangePicker({
  align = "end",
  allLabel,
  ariaLabel,
  className,
  custom = true,
  disabled,
  label,
  onChange,
  presets,
  value,
}: {
  align?: "start" | "end";
  allLabel?: string;
  ariaLabel: string;
  className?: string;
  custom?: boolean;
  disabled?: boolean;
  /** A muted prefix in the trigger, as the list filters beside it show. */
  label?: string;
  onChange: (value: UsageRange | null) => void;
  presets: readonly UsageRangePreset[];
  value: UsageRange | null;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<CustomUsageRange>(() =>
    customDraft(value),
  );
  const [checked, setChecked] = useState(false);
  const selectedRef = useRef<HTMLButtonElement>(null);
  const fieldId = useId();
  const today = localDayKey(new Date());
  const problem = customRangeProblem(draft, new Date());
  const selectedKey = value === null ? null : usageRangeKey(value);
  const triggerText =
    value === null
      ? (allLabel ?? "")
      : isCustomUsageRange(value)
        ? customRangeLabel(value, t("usageRange.today"))
        : t(`overview.range.${value}`);

  const choose = (next: UsageRange | null) => {
    setOpen(false);
    onChange(next);
  };
  const option = (
    key: string | null,
    text: string,
    next: UsageRange | null,
  ) => {
    const selected = key === selectedKey;
    return (
      <button
        aria-pressed={selected}
        className="flex w-full items-center justify-between gap-2 rounded-sm px-2 py-1.5 text-left text-sm outline-none hover:bg-accent focus-visible:bg-accent"
        key={key ?? "all"}
        onClick={() => choose(next)}
        ref={selected ? selectedRef : undefined}
        type="button"
      >
        <span className="truncate">{text}</span>
        {selected ? (
          <Check aria-hidden="true" className="size-3.5 shrink-0" />
        ) : null}
      </button>
    );
  };

  return (
    <Popover
      open={open && !disabled}
      onOpenChange={(next) => {
        setOpen(next);
        if (next) {
          setDraft(customDraft(value));
          setChecked(false);
        }
      }}
    >
      <PopoverTrigger asChild>
        {/* Drawn as a select trigger so it lines up with the selects beside it. */}
        <button
          aria-label={ariaLabel}
          className={cn(selectTriggerClassName, "min-w-0 text-xs", className)}
          data-size="sm"
          disabled={disabled}
          title={triggerText}
          type="button"
        >
          <span className="flex min-w-0 items-center gap-2">
            {label ? (
              <span className="shrink-0 text-muted-foreground">{label}</span>
            ) : null}
            <span className="min-w-0 truncate">{triggerText}</span>
          </span>
          <ChevronDown aria-hidden="true" className="size-4 opacity-50" />
        </button>
      </PopoverTrigger>
      <PopoverContent
        align={align}
        aria-label={ariaLabel}
        className="w-72 p-1.5"
        data-slot="usage-range-picker"
        onOpenAutoFocus={(event) => {
          if (!selectedRef.current) return;
          event.preventDefault();
          selectedRef.current.focus();
        }}
      >
        <div className="grid">
          {allLabel ? option(null, allLabel, null) : null}
          {presets.map((preset) =>
            option(preset, t(`overview.range.${preset}`), preset),
          )}
        </div>
        {custom ? (
          <form
            aria-labelledby={`${fieldId}-custom`}
            className="mt-1.5 grid gap-2 border-t px-2 pt-2.5 pb-1"
            noValidate
            onSubmit={(event) => {
              event.preventDefault();
              setChecked(true);
              if (problem === null) choose({ ...draft });
            }}
          >
            <span
              className="text-micro font-medium text-muted-foreground"
              id={`${fieldId}-custom`}
            >
              {t("usageRange.custom")}
            </span>
            <div className="grid grid-cols-2 gap-2">
              <label className="grid min-w-0 gap-1 text-micro text-muted-foreground">
                {t("usageRange.start")}
                <Input
                  aria-invalid={checked && problem !== null}
                  className="h-7 px-1.5 text-xs"
                  max={today}
                  onChange={(event) => {
                    const from = event.currentTarget.value;
                    setDraft((current) => ({ ...current, from }));
                  }}
                  type="date"
                  value={draft.from}
                />
              </label>
              <label className="grid min-w-0 gap-1 text-micro text-muted-foreground">
                {t("usageRange.end")}
                <Input
                  aria-invalid={checked && problem !== null}
                  className="h-7 px-1.5 text-xs"
                  max={today}
                  min={draft.from || undefined}
                  onChange={(event) => {
                    const to = event.currentTarget.value;
                    setDraft((current) => ({ ...current, to }));
                  }}
                  type="date"
                  value={draft.to}
                />
              </label>
            </div>
            {checked && problem !== null ? (
              <p className="text-micro text-danger-foreground" role="alert">
                {t(`usageRange.problem.${problem}`)}
              </p>
            ) : null}
            <Button className="justify-self-end" size="sm" type="submit">
              {t("usageRange.apply")}
            </Button>
          </form>
        ) : null}
      </PopoverContent>
    </Popover>
  );
}

/** Opens the custom fields on the days the current range covers. */
function customDraft(value: UsageRange | null): CustomUsageRange {
  if (value !== null && isCustomUsageRange(value)) return { ...value };
  const window = resolveUsageWindow(value ?? "30d", new Date());
  return {
    from: localDayKey(new Date(window.from)),
    // The window ends at an exclusive midnight, so its last day is before it.
    to: localDayKey(new Date(new Date(window.to).getTime() - 1)),
  };
}

const rangeFormats = new Map<string, Intl.DateTimeFormat>();

/** `10月1日 – 10月16日`; the year shows once the range leaves this year. */
export function customRangeLabel(
  range: CustomUsageRange,
  todayLabel: string,
): string {
  const from = parseDayKey(range.from);
  const to = parseDayKey(range.to);
  if (!from || !to) return `${range.from} ~ ${range.to}`;
  const today = localDayKey(new Date());
  if (range.from === today && range.to === today) return todayLabel;
  const thisYear = new Date().getFullYear();
  const withYear =
    from.getFullYear() !== thisYear || to.getFullYear() !== thisYear;
  const locale = currentLocale();
  const key = `${locale}:${withYear}`;
  let format = rangeFormats.get(key);
  if (!format) {
    format = new Intl.DateTimeFormat(locale, {
      ...(withYear ? { year: "numeric" } : {}),
      month: "short",
      day: "numeric",
    });
    rangeFormats.set(key, format);
  }
  return format.formatRange(from, to);
}
