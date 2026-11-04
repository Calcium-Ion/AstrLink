import type { ComponentProps, ReactNode } from "react";

import { cn } from "@/lib/utils";

export type QuotaBarTone = "positive" | "warning" | "critical" | "neutral";

const fillClasses: Record<QuotaBarTone, string> = {
  positive: "bg-success",
  warning: "bg-warning",
  critical: "bg-destructive",
  neutral: "bg-muted-foreground/40",
};

const dotClasses: Record<QuotaBarTone, string> = {
  positive: "bg-success",
  warning: "bg-warning",
  critical: "bg-destructive",
  neutral: "bg-muted-foreground/40",
};

/** Percent clamped to the bar, with a sliver left visible above zero. */
function width(percent: number) {
  const value = Math.min(Math.max(percent, 0), 100);
  return `${value > 0 ? Math.max(value, 2) : 0}%`;
}

/**
 * One quota bar, 10px tall in every shape: a flat fill, a short window drawn
 * inside a long one, or a striped placeholder when there is no fraction to
 * draw. Fills change width without transforms.
 */
export function QuotaBar({
  label,
  ...shape
}: { label?: string } & (
  | { kind: "flat"; percent: number; tone: QuotaBarTone }
  | {
      kind: "nested";
      outer: { percent: number; tone: QuotaBarTone } | null;
      inner: { percent: number; tone: QuotaBarTone } | null;
    }
  | { kind: "empty" }
)) {
  const common = {
    "aria-label": label,
    "aria-hidden": label ? undefined : true,
    "data-slot": "quota-bar",
    "data-bar": shape.kind,
  } as const;
  if (shape.kind === "empty")
    return (
      <div
        {...common}
        className="h-2.5 rounded-full bg-[repeating-linear-gradient(135deg,var(--muted)_0_6px,var(--background)_6px_12px)] ring-1 ring-border/60 ring-inset"
        role={label ? "img" : undefined}
      />
    );
  if (shape.kind === "flat")
    return (
      <div
        {...common}
        className="h-2.5 overflow-hidden rounded-full bg-muted"
        data-tone={shape.tone}
        role={label ? "img" : undefined}
      >
        <div
          className={cn(
            "h-full rounded-full transition-[width] duration-300",
            fillClasses[shape.tone],
          )}
          style={{ width: width(shape.percent) }}
        />
      </div>
    );
  return (
    <div
      {...common}
      className="relative h-2.5 overflow-hidden rounded-full bg-muted"
      data-tone={shape.inner?.tone ?? shape.outer?.tone}
      role={label ? "img" : undefined}
    >
      {shape.outer ? (
        <div
          className={cn(
            "absolute inset-y-0 left-0 rounded-full opacity-35 transition-[width] duration-300",
            fillClasses[shape.outer.tone],
          )}
          data-part="outer"
          style={{ width: width(shape.outer.percent) }}
        />
      ) : null}
      {shape.inner ? (
        <div
          className={cn(
            "absolute top-0.5 left-0 h-1.5 rounded-full transition-[width] duration-300",
            fillClasses[shape.inner.tone],
          )}
          data-part="inner"
          style={{ width: width(shape.inner.percent) }}
        />
      ) : null}
    </div>
  );
}

/**
 * The three-line quota area every card shares: status, bar, caption. Without
 * a bar the caption takes the bar's room as a second line, so a long message
 * shows in full and the card keeps its height.
 */
export function QuotaSummary({
  tone,
  status,
  statusEnd,
  bar,
  captionStart,
  captionEnd,
  busy = false,
}: {
  tone: QuotaBarTone;
  status: ReactNode;
  /** A short note at the end of the status line; the status gives way. */
  statusEnd?: ReactNode;
  bar: ReactNode;
  captionStart?: ReactNode;
  captionEnd?: ReactNode;
  /** The quota is still being read. */
  busy?: boolean;
}) {
  return (
    <div
      aria-busy={busy || undefined}
      className="grid min-w-0 gap-1.5"
      data-slot="quota-summary"
    >
      <div
        className="flex min-w-0 items-center gap-1.5 text-[13px] leading-5 font-semibold"
        data-slot="quota-status"
        data-tone={tone}
      >
        <span
          aria-hidden="true"
          className={cn("size-2 shrink-0 rounded-full", dotClasses[tone])}
        />
        <span
          className="min-w-0 truncate"
          title={typeof status === "string" ? status : undefined}
        >
          {status}
        </span>
        {statusEnd ? (
          <span
            className="ml-auto shrink-0 pl-1 text-xs font-medium whitespace-nowrap text-muted-foreground tabular-nums"
            data-slot="quota-status-end"
          >
            {statusEnd}
          </span>
        ) : null}
      </div>
      <div className="grid gap-1">
        {bar}
        <div
          className={cn(
            "flex min-w-0 justify-between gap-2 text-micro text-muted-foreground",
            // The caption keeps its line when empty. Without a bar it keeps
            // the bar's room too: two 15px lines match the bar, its gap and
            // one 16px line, so footers stay in line across the row.
            bar
              ? "min-h-4 items-center leading-4"
              : "min-h-7.5 items-start leading-3.75",
          )}
          data-slot="quota-caption"
        >
          <span
            className={cn(
              "min-w-0",
              bar ? "truncate" : "line-clamp-2 break-words",
            )}
          >
            {captionStart}
          </span>
          {captionEnd ? (
            <span className="shrink-0 whitespace-nowrap">{captionEnd}</span>
          ) : null}
        </div>
      </div>
    </div>
  );
}

/** A provider logo in a bordered tile, with an account count when stacked. */
export function PackageLogo({
  children,
  count,
  countLabel,
}: {
  children: ReactNode;
  count?: number;
  countLabel?: string;
}) {
  return (
    <span className="relative shrink-0" data-slot="package-logo">
      <span className="grid size-8.5 place-items-center rounded-[10px] border bg-background">
        {children}
      </span>
      {count && count > 1 ? (
        <span
          aria-label={countLabel}
          className="absolute -right-1.5 -bottom-1.5 min-w-4 rounded-md border-2 border-card bg-foreground px-1 text-center text-[10px] leading-3 font-bold text-background tabular-nums"
          data-slot="package-count"
        >
          {count}
        </span>
      ) : null}
    </span>
  );
}

/**
 * A provider card: header, quota area and footer at one fixed height so the
 * grid rows line up; `compact` cards size to their content. A stacked card
 * draws two sheets behind it with shadows in token colors; a disabled card
 * dims everything but its footer. Actions float over the header's end while
 * the card is hovered or focused, so they take no room from the name. Pass
 * `aria-label` so the card is announced by its name.
 */
export function PackageCardShell({
  className,
  stacked = false,
  disabled = false,
  dragging = false,
  compact = false,
  header,
  actions,
  children,
  footer,
  ...props
}: Omit<ComponentProps<"article">, "children"> & {
  stacked?: boolean;
  disabled?: boolean;
  dragging?: boolean;
  compact?: boolean;
  header: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  footer: ReactNode;
}) {
  return (
    <article
      className={cn(
        "group/package-card relative flex min-w-0 flex-col rounded-xl border bg-card text-card-foreground transition-opacity",
        compact ? "gap-1.5 px-3 py-2.5" : "h-37.5 gap-2 px-3 py-3",
        stacked &&
          "mb-2 shadow-[0_4px_0_-1px_var(--card),0_4px_0_0_var(--border),0_8px_0_-2px_var(--card),0_8px_0_-1px_var(--border)]",
        disabled && "bg-muted/30",
        // The slot a dragged card left: its copy follows the pointer.
        dragging &&
          "border-dashed border-primary/50 bg-primary/5 shadow-none *:invisible",
        className,
      )}
      data-disabled={disabled || undefined}
      data-dragging={dragging || undefined}
      data-slot="package-card"
      data-stacked={stacked || undefined}
      {...props}
    >
      <div
        className={cn(
          "flex min-w-0 items-center gap-2.5",
          disabled && "opacity-50",
        )}
      >
        {header}
      </div>
      {actions ? (
        <div
          className={cn(
            "absolute z-10 flex items-center gap-0.5 rounded-md bg-card p-0.5 opacity-0 shadow-xs transition-opacity group-focus-within/package-card:opacity-100 group-hover/package-card:opacity-100 has-[[data-state=open]]:opacity-100 [@media(hover:none)]:opacity-100",
            compact ? "top-1.5 right-1.5" : "top-2 right-2",
          )}
          data-slot="package-card-actions"
        >
          {actions}
        </div>
      ) : null}
      <div className={cn("min-w-0", disabled && "opacity-50")}>{children}</div>
      <div className="mt-auto flex min-w-0 items-center gap-1.5 text-xs">
        {footer}
      </div>
    </article>
  );
}

/** The dashed slot that ends a section or a tray and starts adding one. */
export function PackageAddSlot({
  className,
  title,
  hint,
  ...props
}: Omit<ComponentProps<"button">, "title"> & {
  title: ReactNode;
  hint?: ReactNode;
}) {
  return (
    <button
      className={cn(
        "grid min-h-37.5 min-w-0 place-items-center rounded-xl border-[1.5px] border-dashed border-border px-4 text-center text-sm text-primary transition-colors hover:border-primary/50 hover:bg-primary/5 focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none disabled:pointer-events-none disabled:opacity-50",
        className,
      )}
      data-slot="package-add-slot"
      type="button"
      {...props}
    >
      <span className="grid gap-1">
        <span className="font-medium">{title}</span>
        {hint ? (
          <span className="text-xs text-muted-foreground">{hint}</span>
        ) : null}
      </span>
    </button>
  );
}
