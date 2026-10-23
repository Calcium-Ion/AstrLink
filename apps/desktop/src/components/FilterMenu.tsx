import {
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";

import { ChevronDown, X } from "@/components/icons";
import { Button } from "@/components/ui/button";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";

// Adapted from Affect UI's free Filter Menu: grouped controls and removable chips.
// https://www.affectui.com/r/filter-menu.json
// Copyright (c) 2026 Florian Höller (adfects). MIT: LICENSES/affectui-MIT.txt.
// Shared controls replace its checkbox-only options and use AstrLink's tokens.
export interface FilterMenuGroup {
  id: string;
  label: string;
  count: number;
  content: ReactNode;
}

/** Groups custom filter controls without replacing their search or keyboard handling. */
export function FilterMenu({
  activeCount,
  clearLabel,
  groups,
  onClear,
  selectedCountLabel,
  title,
}: {
  activeCount: number;
  clearLabel: string;
  groups: readonly FilterMenuGroup[];
  onClear: () => void;
  selectedCountLabel: (count: number) => string;
  title: string;
}) {
  const [open, setOpen] = useState(
    () => new Set(groups.map((group) => group.id)),
  );
  const listRef = useRef<HTMLDivElement>(null);
  const id = useId();

  function setGroupOpen(groupId: string, expanded: boolean) {
    setOpen((current) => {
      const next = new Set(current);
      if (expanded) next.add(groupId);
      else next.delete(groupId);
      return next;
    });
  }

  return (
    <div data-slot="filter-menu">
      <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
        <span className="text-xs font-medium">{title}</span>
        <Button
          disabled={activeCount === 0}
          onClick={onClear}
          size="xs"
          type="button"
          variant="ghost"
        >
          {clearLabel}
        </Button>
      </div>
      <div className="divide-y" ref={listRef}>
        {groups.map((group, index) => (
          <Collapsible
            key={group.id}
            onOpenChange={(expanded) => setGroupOpen(group.id, expanded)}
            open={open.has(group.id)}
          >
            <CollapsibleTrigger asChild>
              <Button
                className="w-full justify-start rounded-none px-3"
                data-filter-group={group.id}
                id={`${id}-${group.id}`}
                onKeyDown={(event) => {
                  // Only group headers use arrow navigation; nested searchable
                  // controls retain their own keyboard behavior.
                  let next = index;
                  if (event.key === "ArrowDown")
                    next = Math.min(groups.length - 1, index + 1);
                  else if (event.key === "ArrowUp")
                    next = Math.max(0, index - 1);
                  else if (event.key === "Home") next = 0;
                  else if (event.key === "End") next = groups.length - 1;
                  else if (
                    event.key === "ArrowRight" ||
                    event.key === "ArrowLeft"
                  ) {
                    event.preventDefault();
                    setGroupOpen(group.id, event.key === "ArrowRight");
                    return;
                  } else return;
                  event.preventDefault();
                  listRef.current
                    ?.querySelectorAll<HTMLButtonElement>("[data-filter-group]")
                    [next]?.focus({ preventScroll: true });
                }}
                type="button"
                variant="ghost"
              >
                <ChevronDown
                  aria-hidden="true"
                  className={cn(
                    "text-muted-foreground transition-transform motion-reduce:transition-none",
                    !open.has(group.id) && "-rotate-90",
                  )}
                />
                <span>{group.label}</span>
                {group.count > 0 ? (
                  <span className="ml-auto text-micro font-normal text-muted-foreground tabular-nums">
                    {selectedCountLabel(group.count)}
                  </span>
                ) : null}
              </Button>
            </CollapsibleTrigger>
            <CollapsibleContent
              aria-labelledby={`${id}-${group.id}`}
              className="px-3 pb-3"
              role="group"
            >
              {group.content}
            </CollapsibleContent>
          </Collapsible>
        ))}
      </div>
    </div>
  );
}

export interface FilterChip {
  id: string;
  label: string;
  removeLabel: string;
  onRemove: () => void;
}

function RemovableFilterChip({
  chip,
  buttonRef,
  onRemove,
}: {
  chip: FilterChip;
  buttonRef: (button: HTMLButtonElement | null) => void;
  onRemove: () => void;
}) {
  return (
    <Button
      aria-label={chip.removeLabel}
      className="min-w-0 max-w-64 shrink px-2"
      onClick={onRemove}
      ref={buttonRef}
      size="xs"
      title={chip.label}
      type="button"
      variant="secondary"
    >
      <span className="truncate">{chip.label}</span>
      <X aria-hidden="true" />
    </Button>
  );
}

/** Keeps the toolbar to one chip row, even with many active filter categories. */
export function FilterChips({
  chips,
  clearLabel,
  fallbackFocusRef,
  label,
  onClear,
  overflowLabel,
}: {
  chips: readonly FilterChip[];
  clearLabel: string;
  /** A persistent toolbar control to focus after the final chip is removed. */
  fallbackFocusRef: RefObject<HTMLElement | null>;
  label: string;
  onClear: () => void;
  overflowLabel: (count: number) => string;
}) {
  const buttons = useRef(new Map<string, HTMLButtonElement>());
  const currentChips = useRef(chips);
  const pendingFocus = useRef<{
    removedId: string | null;
    index: number;
    waitForClose: boolean;
  } | null>(null);

  function restoreFocus() {
    const pending = pendingFocus.current;
    if (!pending) return;
    const nearest =
      currentChips.current[
        Math.min(pending.index, currentChips.current.length - 1)
      ];
    const target = nearest ? buttons.current.get(nearest.id) : undefined;
    (target ?? fallbackFocusRef.current)?.focus({ preventScroll: true });
    pendingFocus.current = null;
  }

  useLayoutEffect(() => {
    currentChips.current = chips;
    const pending = pendingFocus.current;
    if (!pending || pending.waitForClose) return;
    const removed =
      pending.removedId === null
        ? chips.length === 0
        : !chips.some((chip) => chip.id === pending.removedId);
    if (removed) restoreFocus();
  });

  if (chips.length === 0) return null;
  const visible = chips.slice(0, 2);
  const overflow = chips.slice(2);
  const renderChip = (chip: FilterChip) => (
    <RemovableFilterChip
      buttonRef={(button) => {
        if (button) buttons.current.set(chip.id, button);
        else buttons.current.delete(chip.id);
      }}
      chip={chip}
      key={chip.id}
      onRemove={() => {
        // Preserve keyboard focus only when the removed control owned it.
        if (document.activeElement === buttons.current.get(chip.id)) {
          const index = chips.findIndex(
            (candidate) => candidate.id === chip.id,
          );
          pendingFocus.current = {
            removedId: chip.id,
            index,
            waitForClose: overflow.length === 1 && index >= 2,
          };
        }
        chip.onRemove();
      }}
    />
  );
  return (
    <div
      aria-label={label}
      className="mt-2 flex min-w-0 items-center gap-1.5"
      data-slot="filter-chips"
      role="group"
    >
      <div className="flex min-w-0 items-center gap-1.5 overflow-hidden">
        {visible.map(renderChip)}
      </div>
      {overflow.length > 0 ? (
        <Popover>
          <PopoverTrigger asChild>
            <Button size="xs" type="button" variant="outline">
              {overflowLabel(overflow.length)}
            </Button>
          </PopoverTrigger>
          <PopoverContent
            align="start"
            aria-label={label}
            className="w-80 p-2"
            onCloseAutoFocus={(event) => {
              // Removing the last overflow chip also removes the trigger.
              // Let Radix finish closing, then focus a remaining chip instead.
              if (!pendingFocus.current?.waitForClose) return;
              event.preventDefault();
              restoreFocus();
            }}
          >
            <div className="flex flex-wrap gap-1.5">
              {overflow.map(renderChip)}
            </div>
          </PopoverContent>
        </Popover>
      ) : null}
      <Button
        className="ml-auto"
        onClick={(event) => {
          if (document.activeElement === event.currentTarget) {
            pendingFocus.current = {
              removedId: null,
              index: 0,
              waitForClose: false,
            };
          }
          onClear();
        }}
        size="xs"
        type="button"
        variant="ghost"
      >
        {clearLabel}
      </Button>
    </div>
  );
}
