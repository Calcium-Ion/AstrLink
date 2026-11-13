import { useMemo, useRef, useState } from "react";

import { ChevronDown } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { selectTriggerClassName } from "@/components/ui/select";
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
  everyLabel,
  label,
  noneLabel,
  onChange,
  options,
  searchable = true,
  searchPlaceholder,
  selectAllLabel,
  selectedCountLabel,
  selectedText,
  value,
}: {
  allLabel: string;
  ariaLabel: string;
  className?: string;
  clearLabel: string;
  disabled?: boolean;
  emptyMessage: string;
  /** The trigger's text with every option selected; the count without it. */
  everyLabel?: string;
  label: string;
  /**
   * The trigger's text with nothing selected, where that means none rather
   * than all; allLabel without it.
   */
  noneLabel?: string;
  onChange: (value: string[]) => void;
  options: readonly MultiFilterOption[];
  /** A short list leaves the search out; the panel opens on its first box. */
  searchable?: boolean;
  searchPlaceholder?: string;
  /** Offers selecting every option; without it the panel only clears. */
  selectAllLabel?: string;
  selectedCountLabel: (count: number) => string;
  /**
   * The trigger's text for some of the options, such as their names; the
   * count, or the one option's name, without it.
   */
  selectedText?: (selected: MultiFilterOption[]) => string;
  value: string[];
}) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const searchRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const selected = useMemo(() => new Set(value), [value]);
  const filtered = useMemo(() => {
    const query = search.trim().toLocaleLowerCase();
    if (!query) return options;
    return options.filter((option) =>
      option.label.toLocaleLowerCase().includes(query),
    );
  }, [options, search]);
  const allSelected =
    options.length > 0 && options.every((option) => selected.has(option.value));
  const triggerText =
    value.length === 0
      ? (noneLabel ?? allLabel)
      : allSelected && everyLabel !== undefined
        ? everyLabel
        : selectedText
          ? selectedText(options.filter((option) => selected.has(option.value)))
          : value.length === 1
            ? (options.find((option) => option.value === value[0])?.label ??
              selectedCountLabel(1))
            : selectedCountLabel(value.length);

  function toggle(option: string): void {
    onChange(
      selected.has(option)
        ? value.filter((item) => item !== option)
        : [...value, option],
    );
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
        {/* Drawn as a select trigger so it lines up with the selects beside it. */}
        <button
          aria-expanded={open && !disabled}
          aria-haspopup="listbox"
          aria-label={ariaLabel}
          className={cn(selectTriggerClassName, "min-w-0 text-xs", className)}
          data-size="sm"
          disabled={disabled}
          type="button"
        >
          <span className="flex min-w-0 items-center gap-2">
            {label ? (
              <span className="shrink-0 text-muted-foreground">{label}</span>
            ) : null}
            <span className="min-w-0 truncate" title={triggerText}>
              {triggerText}
            </span>
          </span>
          <ChevronDown aria-hidden="true" className="size-4 opacity-50" />
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        className="w-(--radix-popover-trigger-width) min-w-56 p-2"
        // Keep focus inside the panel: Radix's default autofocus target is the
        // content wrapper, so let it open unfocused and move focus to the search
        // input instead. Otherwise Tab would walk out to the trigger's siblings.
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          if (searchable) searchRef.current?.focus();
          else
            listRef.current
              ?.querySelector<HTMLElement>('[role="checkbox"]')
              ?.focus();
        }}
      >
        {searchable ? (
          <Input
            aria-label={`${ariaLabel} search`}
            autoComplete="off"
            className="mb-2"
            onChange={(event) => setSearch(event.currentTarget.value)}
            placeholder={searchPlaceholder}
            ref={searchRef}
            value={search}
          />
        ) : null}
        <div className="flex items-center justify-between gap-2 border-b pb-2">
          {selectAllLabel === undefined ? (
            <Button
              className="px-1.5 text-xs"
              disabled={value.length === 0}
              onClick={() => onChange([])}
              size="xs"
              type="button"
              variant="ghost"
            >
              {clearLabel}
            </Button>
          ) : (
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
          )}
          <span className="text-micro text-muted-foreground tabular-nums">
            {selectedCountLabel(value.length)}
          </span>
        </div>
        <div
          className="mt-1 max-h-56 overflow-y-auto"
          ref={listRef}
          role="listbox"
          aria-label={ariaLabel}
        >
          {filtered.length > 0 ? (
            filtered.map((option) => (
              <label
                className="flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm hover:bg-accent"
                key={option.value}
              >
                <Checkbox
                  aria-label={option.label}
                  checked={selected.has(option.value)}
                  onCheckedChange={() => toggle(option.value)}
                />
                <span className="min-w-0 truncate" title={option.label}>
                  {option.label}
                </span>
              </label>
            ))
          ) : (
            <p className="px-2 py-3 text-xs text-muted-foreground">
              {emptyMessage}
            </p>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}
