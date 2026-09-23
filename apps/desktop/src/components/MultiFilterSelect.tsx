import { useMemo, useState } from "react";

import { ChevronDown } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";

export interface MultiFilterOption {
  value: string;
  label: string;
}

/** Compact searchable multi-select for list toolbars. */
export function MultiFilterSelect({
  allLabel,
  ariaLabel,
  className,
  clearLabel,
  disabled,
  emptyMessage,
  label,
  onChange,
  options,
  searchPlaceholder,
  selectAllLabel,
  selectedCountLabel,
  value,
}: {
  allLabel: string;
  ariaLabel: string;
  className?: string;
  clearLabel: string;
  disabled?: boolean;
  emptyMessage: string;
  label: string;
  onChange: (value: string[]) => void;
  options: readonly MultiFilterOption[];
  searchPlaceholder: string;
  selectAllLabel: string;
  selectedCountLabel: (count: number) => string;
  value: string[];
}) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const selected = useMemo(() => new Set(value), [value]);
  const filtered = useMemo(() => {
    const query = search.trim().toLocaleLowerCase();
    if (!query) return options;
    return options.filter((option) => option.label.toLocaleLowerCase().includes(query));
  }, [options, search]);
  const allSelected = options.length > 0 && options.every((option) => selected.has(option.value));
  const triggerText = value.length === 0
    ? allLabel
    : value.length === 1
      ? options.find((option) => option.value === value[0])?.label ?? selectedCountLabel(1)
      : selectedCountLabel(value.length);

  function toggle(option: string): void {
    onChange(selected.has(option)
      ? value.filter((item) => item !== option)
      : [...value, option]);
  }

  function toggleAll(): void {
    onChange(allSelected ? [] : options.map((option) => option.value));
  }

  return (
    <Popover
      open={open && !disabled}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setSearch("");
      }}
    >
      <PopoverTrigger asChild>
        <Button
          aria-expanded={open && !disabled}
          aria-haspopup="listbox"
          aria-label={ariaLabel}
          className={cn("min-w-0 justify-between text-xs", className)}
          disabled={disabled}
          size="sm"
          type="button"
          variant="outline"
        >
          <span className="flex min-w-0 items-center gap-2">
            <span className="shrink-0 font-normal text-muted-foreground">{label}</span>
            <span className="min-w-0 truncate" title={triggerText}>{triggerText}</span>
          </span>
          <ChevronDown aria-hidden="true" className="size-3.5 opacity-60" />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        className="w-(--radix-popover-trigger-width) min-w-56 p-2"
        onOpenAutoFocus={(event) => event.preventDefault()}
      >
        <Input
          aria-label={`${ariaLabel} search`}
          autoComplete="off"
          onChange={(event) => setSearch(event.currentTarget.value)}
          placeholder={searchPlaceholder}
          value={search}
        />
        <div className="mt-2 flex items-center justify-between gap-2 border-b pb-2">
          <Button
            className="px-1.5 text-xs"
            disabled={options.length === 0}
            onClick={toggleAll}
            size="xs"
            type="button"
            variant="ghost"
          >
            {allSelected ? clearLabel : selectAllLabel}
          </Button>
          <span className="text-micro text-muted-foreground tabular-nums">
            {selectedCountLabel(value.length)}
          </span>
        </div>
        <div className="mt-1 max-h-56 overflow-y-auto" role="listbox" aria-label={ariaLabel}>
          {filtered.length > 0 ? filtered.map((option) => (
            <label
              className="flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm hover:bg-accent"
              key={option.value}
            >
              <Checkbox
                aria-label={option.label}
                checked={selected.has(option.value)}
                onCheckedChange={() => toggle(option.value)}
              />
              <span className="min-w-0 truncate" title={option.label}>{option.label}</span>
            </label>
          )) : (
            <p className="px-2 py-3 text-xs text-muted-foreground">{emptyMessage}</p>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}
