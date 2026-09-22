import { useEffect, useId, useRef, useState } from "react";

import { Check, ChevronDown } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Popover, PopoverAnchor, PopoverContent } from "@/components/ui/popover";
import { cn } from "@/lib/utils";

/** Editable selection: opening shows every option; only typing filters the list. */
export function Combobox({
  id, value, options, onValueChange, disabled, placeholder, maxLength,
  emptyMessage, "aria-label": ariaLabel,
}: {
  id?: string;
  value: string;
  options: readonly string[];
  onValueChange: (value: string) => void;
  disabled?: boolean;
  placeholder?: string;
  maxLength?: number;
  emptyMessage: string;
  "aria-label": string;
}) {
  const generatedId = useId();
  const listId = `${generatedId}-options`;
  const anchorRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(-1);
  const filtered = options.filter(option => option.toLowerCase().includes(query.toLowerCase()));
  const expanded = open && !disabled;

  function showOptions() {
    if (disabled) return;
    setQuery("");
    setActiveIndex(options.indexOf(value));
    setOpen(true);
  }

  function choose(option: string) {
    onValueChange(option);
    setOpen(false);
    inputRef.current?.focus({ preventScroll: true });
  }

  useEffect(() => {
    if (disabled) setOpen(false);
  }, [disabled]);

  useEffect(() => {
    if (!expanded || activeIndex < 0) return;
    const list = listRef.current;
    const option = list?.querySelector<HTMLElement>(`[data-option-index="${activeIndex}"]`);
    if (!list || !option) return;
    // Scroll only the popup, preserving the dialog and workspace positions.
    const bounds = list.getBoundingClientRect();
    const row = option.getBoundingClientRect();
    if (row.top < bounds.top) list.scrollTop -= bounds.top - row.top;
    else if (row.bottom > bounds.bottom) list.scrollTop += row.bottom - bounds.bottom;
  }, [expanded, activeIndex]);

  return <Popover open={expanded} onOpenChange={setOpen}>
    <PopoverAnchor asChild>
      <div ref={anchorRef} className="relative min-w-0" data-slot="combobox">
        <Input ref={inputRef} id={id ?? generatedId} role="combobox" aria-label={ariaLabel}
          aria-autocomplete="list" aria-expanded={expanded} aria-controls={expanded ? listId : undefined}
          aria-activedescendant={expanded && filtered[activeIndex] !== undefined ? `${listId}-${activeIndex}` : undefined}
          autoComplete="off" className="pr-9" value={value} disabled={disabled} placeholder={placeholder} maxLength={maxLength}
          onClick={() => { if (!expanded) showOptions(); }}
          onChange={event => {
            onValueChange(event.target.value);
            setQuery(event.target.value);
            setActiveIndex(-1);
            setOpen(true);
          }}
          onKeyDown={event => {
            if (event.nativeEvent.isComposing) return;
            if (event.key === "ArrowDown" || event.key === "ArrowUp") {
              event.preventDefault();
              if (!expanded) {
                showOptions();
                if (!options.includes(value)) setActiveIndex(event.key === "ArrowDown" ? 0 : options.length - 1);
              } else {
                setActiveIndex(index => event.key === "ArrowDown"
                  ? Math.min(index + 1, filtered.length - 1)
                  : index < 0 ? filtered.length - 1 : Math.max(0, index - 1));
              }
            } else if (event.key === "Enter" && expanded) {
              event.preventDefault();
              if (filtered[activeIndex] !== undefined) choose(filtered[activeIndex]);
              else setOpen(false);
            } else if (event.key === "Tab") {
              setOpen(false);
            }
          }} />
        <Button type="button" variant="ghost" size="icon-sm" tabIndex={-1}
          className="absolute top-1/2 right-0.5 -translate-y-1/2 text-muted-foreground"
          aria-label={ariaLabel} aria-haspopup="listbox" aria-expanded={expanded} aria-controls={expanded ? listId : undefined}
          disabled={disabled} onMouseDown={event => event.preventDefault()}
          onClick={() => {
            inputRef.current?.focus({ preventScroll: true });
            if (expanded) setOpen(false);
            else showOptions();
          }}>
          <ChevronDown aria-hidden="true" />
        </Button>
      </div>
    </PopoverAnchor>
    <PopoverContent ref={listRef} id={listId} role="listbox" aria-label={ariaLabel} align="start" sideOffset={4}
      className="z-110 max-h-[min(16rem,var(--radix-popover-content-available-height))] w-(--radix-popover-trigger-width) overscroll-contain p-1"
      onOpenAutoFocus={event => event.preventDefault()} onCloseAutoFocus={event => event.preventDefault()}
      onInteractOutside={event => { if (anchorRef.current?.contains(event.target as Node)) event.preventDefault(); }}
      onEscapeKeyDown={event => { event.preventDefault(); setOpen(false); }}>
      {filtered.length ? filtered.map((option, index) => <div key={option} id={`${listId}-${index}`}
        role="option" aria-selected={value === option} data-option-index={index} title={option}
        className={cn("flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 text-sm select-none", index === activeIndex && "bg-accent text-accent-foreground")}
        onPointerMove={() => setActiveIndex(index)} onMouseDown={event => event.preventDefault()} onClick={() => choose(option)}>
        <span className="min-w-0 flex-1 break-all">{option}</span>
        {value === option ? <Check aria-hidden="true" className="size-3.5 shrink-0" /> : null}
      </div>) : <div role="status" className="px-2 py-1.5 text-xs text-muted-foreground">{emptyMessage}</div>}
    </PopoverContent>
  </Popover>;
}
