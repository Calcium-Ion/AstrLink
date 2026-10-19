import {
  Fragment,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { BadgeAlert, Check, ChevronDown, X } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Popover,
  PopoverAnchor,
  PopoverContent,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";

const listOffset = 4;
const listCollisionPadding = 12;
const multiComboboxInputMinWidth = "6rem";

/**
 * The side with room for `count` rows, up to the list's 16rem cap. Below
 * is preferred; the list only opens upward when it would be cut short there.
 */
function listSide(anchor: HTMLElement, count: number): "top" | "bottom" {
  const rem =
    Number.parseFloat(getComputedStyle(document.documentElement).fontSize) ||
    16;
  // Rows are 2rem tall, plus the list's padding and border.
  const wanted = Math.min(16, Math.max(count, 1) * 2 + 0.75) * rem;
  const { top, bottom } = anchor.getBoundingClientRect();
  const margin = listOffset + listCollisionPadding;
  const below = window.innerHeight - bottom - margin;
  const above = top - margin;
  return below < wanted && above > below ? "top" : "bottom";
}

/**
 * One row of the list. `value` is what the caller persists and `label` is what
 * the reader sees; they differ when two values share one display name, so rows
 * are keyed and matched by value rather than by their text.
 *
 * `group` puts the row under a heading; consecutive rows with the same group
 * share one heading, so callers pass options already in group order.
 * `description` is muted text on the right of the row. Typing matches the
 * label, the description and the group.
 */
export type ComboboxOption = {
  value: string;
  label: string;
  group?: string;
  description?: string;
};

function optionMatches(option: ComboboxOption, text: string): boolean {
  const query = text.trim().toLowerCase();
  return [option.label, option.description, option.group].some(
    (part) => part?.toLowerCase().includes(query) ?? false,
  );
}

/** A heading row before the first option of each group. */
function GroupHeading({ children }: { children: ReactNode }) {
  return (
    <div
      role="presentation"
      className="flex items-center gap-1.5 px-2 pt-2 pb-1 text-micro font-medium text-muted-foreground first:pt-1"
    >
      {children}
    </div>
  );
}

/** An option's body: its label, then the muted description on the right. */
function OptionText({
  label,
  description,
}: {
  label: ReactNode;
  description?: string;
}) {
  if (!description)
    return <span className="min-w-0 flex-1 break-all">{label}</span>;
  return (
    <>
      <span className="min-w-0 flex-1 break-all">{label}</span>
      <span className="max-w-[45%] shrink-0 truncate text-xs text-muted-foreground">
        {description}
      </span>
    </>
  );
}

/** Editable selection: opening shows every option; only typing filters the list. */
export function Combobox({
  id,
  value,
  displayValue,
  options,
  onValueChange,
  onValueCommit,
  disabled,
  placeholder,
  maxLength,
  emptyMessage,
  clearLabel,
  className,
  leadingIcon,
  hint,
  warning,
  renderOption,
  renderGroup,
  allowCustomValue = true,
  "aria-label": ariaLabel,
}: {
  id?: string;
  value: string;
  /**
   * What the field shows for `value`; defaults to the value itself. Callers
   * whose values have display labels put the label here while `value` stays the
   * thing they persist.
   */
  displayValue?: string;
  options: readonly ComboboxOption[];
  onValueChange: (value: string) => void;
  /** Selection, Enter, or blur completes a value edit for automatic saving. */
  onValueCommit?: (value: string) => void;
  disabled?: boolean;
  placeholder?: string;
  maxLength?: number;
  emptyMessage: string;
  /** Shows a clear button while the field has a value. */
  clearLabel?: string;
  className?: string;
  leadingIcon?: ReactNode;
  /**
   * Muted text on the right while the list is closed, such as the API
   * provider of the chosen model. It gives way to the value when space runs out.
   */
  hint?: string;
  /**
   * Marks a value that is valid but will fail in some situations, such as a
   * model that does not accept every request format its routing group serves.
   * The text names those situations; it shows on hover and to screen readers.
   */
  warning?: string;
  renderOption?: (label: string, option?: ComboboxOption) => ReactNode;
  /** Content of a group heading; defaults to the group name. */
  renderGroup?: (group: string) => ReactNode;
  allowCustomValue?: boolean;
  "aria-label": string;
}) {
  const generatedId = useId();
  const listId = `${generatedId}-options`;
  const anchorRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [side, setSide] = useState<"top" | "bottom">("bottom");
  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(-1);
  const matches = (text: string) =>
    options.filter((option) => optionMatches(option, text));
  const filtered = matches(query);
  const expanded = open && !disabled;
  const clearable = !!clearLabel && !!value && !disabled;
  const showHint = !!hint && !expanded;
  const warned = !!warning && !expanded;

  /** The row the current value owns, if the list still offers it. */
  const selectedIndex = options.findIndex((option) => option.value === value);

  // The side is chosen once per opening, so filtering never moves the list.
  function openList(text: string) {
    if (!expanded && anchorRef.current)
      setSide(listSide(anchorRef.current, matches(text).length));
    setOpen(true);
  }

  function showOptions() {
    if (disabled) return;
    setQuery("");
    setActiveIndex(selectedIndex);
    openList("");
  }

  function choose(option: ComboboxOption) {
    onValueChange(option.value);
    onValueCommit?.(option.value);
    setOpen(false);
    inputRef.current?.focus({ preventScroll: true });
  }

  useEffect(() => {
    if (disabled) setOpen(false);
  }, [disabled]);

  useEffect(() => {
    if (!expanded || activeIndex < 0) return;
    const list = listRef.current;
    const option = list?.querySelector<HTMLElement>(
      `[data-option-index="${activeIndex}"]`,
    );
    if (!list || !option) return;
    // Scroll only the popup, preserving the dialog and workspace positions.
    const bounds = list.getBoundingClientRect();
    const row = option.getBoundingClientRect();
    if (row.top < bounds.top) list.scrollTop -= bounds.top - row.top;
    else if (row.bottom > bounds.bottom)
      list.scrollTop += row.bottom - bounds.bottom;
  }, [expanded, activeIndex]);

  return (
    <Popover open={expanded} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <div
          ref={anchorRef}
          className={cn("relative min-w-0", className)}
          data-slot="combobox"
        >
          {leadingIcon ? (
            <span
              aria-hidden="true"
              className="pointer-events-none absolute left-2.5 top-1/2 z-10 inline-flex -translate-y-1/2 text-muted-foreground"
            >
              {leadingIcon}
            </span>
          ) : null}
          <Input
            ref={inputRef}
            id={id ?? generatedId}
            role="combobox"
            aria-label={ariaLabel}
            aria-autocomplete="list"
            aria-expanded={expanded}
            aria-controls={expanded ? listId : undefined}
            aria-activedescendant={
              expanded && filtered[activeIndex] !== undefined
                ? `${listId}-${activeIndex}`
                : undefined
            }
            autoComplete="off"
            className={cn(
              clearable || warned ? "pr-14" : "pr-9",
              leadingIcon && "pl-8",
              showHint &&
                (warned
                  ? "pr-[calc(35%+3.75rem)] text-ellipsis"
                  : "pr-[calc(35%+2.25rem)] text-ellipsis"),
              warned && "border-warning/55",
            )}
            aria-description={warned ? warning : undefined}
            value={
              !allowCustomValue && expanded ? query : (displayValue ?? value)
            }
            disabled={disabled}
            placeholder={placeholder}
            maxLength={maxLength}
            onClick={() => {
              if (!expanded) showOptions();
            }}
            onChange={(event) => {
              if (allowCustomValue) onValueChange(event.target.value);
              setQuery(event.target.value);
              setActiveIndex(-1);
              // An IME repair can land as focus leaves; only typing opens the list.
              if (
                (event.nativeEvent as InputEvent).inputType !==
                "insertReplacementText"
              )
                openList(event.target.value);
            }}
            onBlur={(event) =>
              onValueCommit?.(
                allowCustomValue ? event.currentTarget.value : value,
              )
            }
            onKeyDown={(event) => {
              if (event.key === "ArrowDown" || event.key === "ArrowUp") {
                event.preventDefault();
                if (!expanded) {
                  showOptions();
                  if (selectedIndex < 0)
                    setActiveIndex(
                      event.key === "ArrowDown" ? 0 : options.length - 1,
                    );
                } else {
                  setActiveIndex((index) =>
                    event.key === "ArrowDown"
                      ? Math.min(index + 1, filtered.length - 1)
                      : index < 0
                        ? filtered.length - 1
                        : Math.max(0, index - 1),
                  );
                }
              } else if (event.key === "Enter") {
                if (expanded) event.preventDefault();
                if (expanded && filtered[activeIndex] !== undefined)
                  choose(filtered[activeIndex]);
                else {
                  setOpen(false);
                  onValueCommit?.(
                    allowCustomValue ? event.currentTarget.value : value,
                  );
                }
              } else if (event.key === "Tab") {
                setOpen(false);
              }
            }}
          />
          {showHint ? (
            <span
              aria-hidden="true"
              className={cn(
                "pointer-events-none absolute top-1/2 max-w-[35%] -translate-y-1/2 truncate text-xs text-muted-foreground",
                clearable || warned ? "right-14" : "right-9",
                clearable && warned && "right-20",
              )}
            >
              {hint}
            </span>
          ) : null}
          {warned ? (
            <span
              className={cn(
                "absolute top-1/2 inline-flex -translate-y-1/2 cursor-help text-warning-foreground",
                clearable ? "right-14" : "right-8",
              )}
              title={warning}
              data-combobox-warning
            >
              <BadgeAlert aria-hidden="true" className="size-3.5" />
            </span>
          ) : null}
          {clearable ? (
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              className="absolute top-1/2 right-7.5 -translate-y-1/2 text-muted-foreground"
              aria-label={clearLabel}
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => {
                onValueChange("");
                setQuery("");
                setActiveIndex(-1);
                inputRef.current?.focus({ preventScroll: true });
              }}
            >
              <X aria-hidden="true" />
            </Button>
          ) : null}
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            tabIndex={-1}
            className="absolute top-1/2 right-0.5 -translate-y-1/2 text-muted-foreground"
            aria-label={ariaLabel}
            aria-haspopup="listbox"
            aria-expanded={expanded}
            aria-controls={expanded ? listId : undefined}
            disabled={disabled}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => {
              inputRef.current?.focus({ preventScroll: true });
              if (expanded) setOpen(false);
              else showOptions();
            }}
          >
            <ChevronDown aria-hidden="true" />
          </Button>
        </div>
      </PopoverAnchor>
      <PopoverContent
        ref={listRef}
        id={listId}
        role="listbox"
        aria-label={ariaLabel}
        align="start"
        side={side}
        sideOffset={listOffset}
        collisionPadding={listCollisionPadding}
        // The side is picked on opening; flipping would fight the height cap.
        avoidCollisions={false}
        className="z-110 max-h-[min(16rem,var(--radix-popover-content-available-height))] w-(--radix-popover-trigger-width) overscroll-contain p-1"
        onOpenAutoFocus={(event) => event.preventDefault()}
        onCloseAutoFocus={(event) => event.preventDefault()}
        onInteractOutside={(event) => {
          if (anchorRef.current?.contains(event.target as Node))
            event.preventDefault();
        }}
        onEscapeKeyDown={(event) => {
          event.preventDefault();
          setOpen(false);
        }}
      >
        {filtered.length ? (
          filtered.map((option, index) => (
            <Fragment key={option.value}>
              {option.group !== undefined &&
              option.group !== filtered[index - 1]?.group ? (
                <GroupHeading>
                  {renderGroup ? renderGroup(option.group) : option.group}
                </GroupHeading>
              ) : null}
              <div
                id={`${listId}-${index}`}
                role="option"
                aria-label={
                  option.description
                    ? `${option.label} ${option.description}`
                    : option.label
                }
                aria-selected={value === option.value}
                data-option-index={index}
                data-option-value={option.value}
                title={option.label}
                className={cn(
                  "flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 text-sm select-none",
                  index === activeIndex && "bg-accent text-accent-foreground",
                )}
                onPointerMove={() => setActiveIndex(index)}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => choose(option)}
              >
                <OptionText
                  label={
                    renderOption
                      ? renderOption(option.label, option)
                      : option.label
                  }
                  description={option.description}
                />
                {value === option.value ? (
                  <Check aria-hidden="true" className="size-3.5 shrink-0" />
                ) : option.description ? (
                  // Keeps descriptions aligned with the checked row's.
                  <span aria-hidden="true" className="size-3.5 shrink-0" />
                ) : null}
              </div>
            </Fragment>
          ))
        ) : (
          <div
            role="status"
            className="px-2 py-1.5 text-xs text-muted-foreground"
          >
            {emptyMessage}
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}

/**
 * 可创建的多选组合框：已选值以 chip 形式嵌在输入框内，输入框本身用于新增。
 *
 * 与 `Combobox` 的差别不只是「多选」——`Combobox` 是「一个值 + 一个可编辑
 * 输入框」，两者共用输入框；这里输入框始终是**新建槽位**，已选值退化成
 * chip，因此不会出现「输入框既显示当前值又要接收新值」的歧义。
 *
 * `allowCustomValue` 决定自由输入：开放词汇字段允许把输入直接创建为新值，
 * 固定枚举字段只允许从候选里选（点候选项即选中，输入只用于过滤）。
 */
export function MultiCombobox({
  values,
  options,
  onChange,
  disabled,
  placeholder,
  max,
  allowCustomValue = true,
  valueLabel,
  maxLength,
  emptyMessage,
  createLabel,
  removeLabel,
  className,
  "aria-label": ariaLabel,
}: {
  values: readonly string[];
  options: readonly ComboboxOption[];
  onChange: (values: string[]) => void;
  disabled?: boolean;
  placeholder?: string;
  max: number;
  allowCustomValue?: boolean;
  /** chip 与候选行的显示文本；默认显示 value 本身。 */
  valueLabel?: (value: string) => string;
  maxLength?: number;
  emptyMessage: string;
  /** 「新建 X」行的文本；缺省时不显示新建行。 */
  createLabel?: (query: string) => string;
  removeLabel: (value: string) => string;
  className?: string;
  "aria-label": string;
}) {
  const generatedId = useId();
  const listId = `${generatedId}-options`;
  const anchorRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [side, setSide] = useState<"top" | "bottom">("bottom");
  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(-1);

  const label = (value: string) => valueLabel?.(value) ?? value;
  const selected = new Set(values);
  const atLimit = values.length >= max;
  const matches = (text: string) =>
    options.filter((option) =>
      option.label.toLowerCase().includes(text.trim().toLowerCase()),
    );
  const filtered = matches(query).filter(
    (option) => !selected.has(option.value),
  );
  const trimmed = query.trim();
  // 自由输入时，多出一行「新建」；它与候选行共用同一套索引空间。
  const canCreate =
    allowCustomValue &&
    trimmed !== "" &&
    !selected.has(trimmed) &&
    !options.some((option) => option.value === trimmed);
  const creatable = canCreate && createLabel !== undefined;
  const rowCount = filtered.length + (creatable ? 1 : 0);
  const expanded = open && !disabled && !atLimit;

  function openList(text: string) {
    if (!expanded && anchorRef.current)
      setSide(listSide(anchorRef.current, Math.max(matches(text).length, 1)));
    setOpen(true);
  }

  function showOptions() {
    if (disabled || atLimit) return;
    setActiveIndex(-1);
    openList("");
  }

  function commit(next: readonly string[]) {
    onChange([...next]);
    setQuery("");
    setActiveIndex(-1);
    inputRef.current?.focus({ preventScroll: true });
  }

  function add(value: string) {
    if (atLimit || selected.has(value)) return;
    commit([...values, value]);
  }

  function remove(value: string) {
    commit(values.filter((item) => item !== value));
  }

  /** 执行当前高亮行的动作；高亮行超出候选范围时即为「新建」。 */
  function activate(index: number) {
    const option = filtered[index];
    if (option) {
      add(option.value);
      return;
    }
    if (creatable && index === filtered.length) add(trimmed);
  }

  useEffect(() => {
    if (disabled || atLimit) setOpen(false);
  }, [disabled, atLimit]);

  useEffect(() => {
    if (!expanded || activeIndex < 0) return;
    const list = listRef.current;
    const row = list?.querySelector<HTMLElement>(
      `[data-option-index="${activeIndex}"]`,
    );
    if (!list || !row) return;
    // 只滚动弹层本身，保持对话框与工作区的位置。
    const bounds = list.getBoundingClientRect();
    const cell = row.getBoundingClientRect();
    if (cell.top < bounds.top) list.scrollTop -= bounds.top - cell.top;
    else if (cell.bottom > bounds.bottom)
      list.scrollTop += cell.bottom - bounds.bottom;
  }, [expanded, activeIndex]);

  return (
    <Popover open={expanded} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <div
          ref={anchorRef}
          data-slot="multi-combobox"
          className={cn(
            "flex min-w-0 flex-wrap items-center gap-1 rounded-md border bg-background px-1.5 py-1 text-sm focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/30",
            disabled && "pointer-events-none opacity-50",
            className,
          )}
          onMouseDown={(event) => {
            // 点空白处聚焦输入框，但不抢 chip 删除按钮的点击。
            if (event.target === event.currentTarget) {
              event.preventDefault();
              inputRef.current?.focus({ preventScroll: true });
              if (!expanded) showOptions();
            }
          }}
        >
          {values.map((value) => (
            <span
              key={value}
              data-multi-combobox-chip
              className="inline-flex min-w-0 max-w-full items-center gap-1 rounded-sm bg-secondary px-1.5 py-px text-micro font-medium text-secondary-foreground"
            >
              <span className="min-w-0 break-all">{label(value)}</span>
              <button
                type="button"
                aria-label={removeLabel(label(value))}
                data-multi-combobox-remove
                className="shrink-0 text-muted-foreground hover:text-destructive"
                disabled={disabled}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => remove(value)}
              >
                <X aria-hidden="true" className="size-3" />
              </button>
            </span>
          ))}
          <input
            ref={inputRef}
            id={generatedId}
            role="combobox"
            aria-label={ariaLabel}
            aria-autocomplete="list"
            aria-expanded={expanded}
            aria-controls={expanded ? listId : undefined}
            aria-activedescendant={
              expanded && activeIndex >= 0 && activeIndex < rowCount
                ? `${listId}-${activeIndex}`
                : undefined
            }
            autoComplete="off"
            className="min-w-0 flex-1 bg-transparent px-0.5 py-0.5 outline-none placeholder:text-muted-foreground disabled:cursor-not-allowed"
            style={{ minWidth: multiComboboxInputMinWidth }}
            value={query}
            disabled={disabled || atLimit}
            placeholder={atLimit ? undefined : placeholder}
            maxLength={maxLength}
            onClick={() => {
              if (!expanded) showOptions();
            }}
            onChange={(event) => {
              setQuery(event.target.value);
              setActiveIndex(-1);
              // IME 修复串可能落在失焦时；只有真实输入才展开列表。
              if (
                (event.nativeEvent as InputEvent).inputType !==
                "insertReplacementText"
              )
                openList(event.target.value);
            }}
            onKeyDown={(event) => {
              if (event.key === "Backspace" && query === "") {
                // 输入框为空时，退格删除最后一个 chip。
                const last = values[values.length - 1];
                if (last !== undefined) {
                  event.preventDefault();
                  remove(last);
                }
                return;
              }
              if (event.key === "ArrowDown" || event.key === "ArrowUp") {
                event.preventDefault();
                if (!expanded) {
                  showOptions();
                  if (rowCount > 0)
                    setActiveIndex(
                      event.key === "ArrowDown" ? 0 : rowCount - 1,
                    );
                } else {
                  setActiveIndex((index) =>
                    event.key === "ArrowDown"
                      ? Math.min(index + 1, rowCount - 1)
                      : index < 0
                        ? rowCount - 1
                        : Math.max(0, index - 1),
                  );
                }
                return;
              }
              if (event.key === "Enter") {
                event.preventDefault();
                if (!expanded) return;
                if (activeIndex >= 0) activate(activeIndex);
                else if (creatable) add(trimmed);
                else if (filtered.length > 0) add(filtered[0].value);
                return;
              }
              if (event.key === "Escape" || event.key === "Tab") {
                setOpen(false);
                setQuery("");
              }
            }}
          />
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            tabIndex={-1}
            className="ml-auto shrink-0 text-muted-foreground"
            aria-label={ariaLabel}
            aria-haspopup="listbox"
            aria-expanded={expanded}
            aria-controls={expanded ? listId : undefined}
            disabled={disabled || atLimit}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => {
              inputRef.current?.focus({ preventScroll: true });
              if (expanded) setOpen(false);
              else showOptions();
            }}
          >
            <ChevronDown aria-hidden="true" />
          </Button>
        </div>
      </PopoverAnchor>
      <PopoverContent
        ref={listRef}
        id={listId}
        role="listbox"
        aria-label={ariaLabel}
        align="start"
        side={side}
        sideOffset={listOffset}
        collisionPadding={listCollisionPadding}
        avoidCollisions={false}
        className="z-110 max-h-[min(16rem,var(--radix-popover-content-available-height))] w-(--radix-popover-trigger-width) overscroll-contain p-1"
        onOpenAutoFocus={(event) => event.preventDefault()}
        onCloseAutoFocus={(event) => event.preventDefault()}
        onInteractOutside={(event) => {
          if (anchorRef.current?.contains(event.target as Node))
            event.preventDefault();
        }}
        onEscapeKeyDown={(event) => {
          event.preventDefault();
          setOpen(false);
          setQuery("");
        }}
      >
        {filtered.map((option, index) => (
          <div
            key={option.value}
            id={`${listId}-${index}`}
            role="option"
            aria-label={option.label}
            aria-selected={false}
            data-option-index={index}
            data-option-value={option.value}
            title={option.label}
            className={cn(
              "flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 text-sm select-none",
              index === activeIndex && "bg-accent text-accent-foreground",
            )}
            onPointerMove={() => setActiveIndex(index)}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => add(option.value)}
          >
            <span className="min-w-0 flex-1 break-all">{option.label}</span>
          </div>
        ))}
        {creatable ? (
          <div
            id={`${listId}-${filtered.length}`}
            role="option"
            aria-selected={false}
            data-option-index={filtered.length}
            data-multi-combobox-create
            className={cn(
              "cursor-default rounded-sm px-2 py-1.5 text-sm select-none",
              activeIndex === filtered.length &&
                "bg-accent text-accent-foreground",
            )}
            onPointerMove={() => setActiveIndex(filtered.length)}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => add(trimmed)}
          >
            {createLabel(trimmed)}
          </div>
        ) : null}
        {rowCount === 0 ? (
          <div
            role="status"
            className="px-2 py-1.5 text-xs text-muted-foreground"
          >
            {emptyMessage}
          </div>
        ) : null}
      </PopoverContent>
    </Popover>
  );
}
