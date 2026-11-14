import type { ButtonHTMLAttributes, ReactNode } from "react";
import { ChevronRight } from "@/components/icons";
import { cn } from "@/lib/utils";

type DisclosureTone = "neutral" | "warning";

/** The chevron-and-label look of a disclosure's summary or toggle button. */
function disclosureToggleClassName(tone: DisclosureTone): string {
  return cn(
    "flex w-fit cursor-pointer list-none items-center gap-1.5 rounded-sm transition-colors hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring [&::-webkit-details-marker]:hidden",
    tone === "warning" ? "text-warning-foreground" : "text-muted-foreground",
  );
}

const chevronClassName = "size-3.5 shrink-0 transition-transform";

export function HelpDisclosure({
  title,
  children,
  open,
  tone = "neutral",
}: {
  title: ReactNode;
  children: ReactNode;
  open?: boolean;
  tone?: DisclosureTone;
}) {
  return (
    <details className="group/details min-w-0 text-xs" open={open}>
      <summary className={disclosureToggleClassName(tone)}>
        <ChevronRight
          aria-hidden="true"
          className={cn(chevronClassName, "group-open/details:rotate-90")}
        />
        {title}
      </summary>
      <div className="mt-3 grid min-w-0 gap-3 text-xs leading-relaxed text-muted-foreground">
        {children}
      </div>
    </details>
  );
}

/**
 * A HelpDisclosure summary as a button, for content that cannot sit inside a
 * <details>, such as table rows. The caller renders the content when open.
 */
export function DisclosureToggle({
  children,
  className,
  open,
  tone = "neutral",
  ...props
}: Omit<ButtonHTMLAttributes<HTMLButtonElement>, "aria-expanded" | "type"> & {
  open: boolean;
  tone?: DisclosureTone;
}) {
  return (
    <button
      {...props}
      aria-expanded={open}
      className={cn(disclosureToggleClassName(tone), "text-xs", className)}
      type="button"
    >
      <ChevronRight
        aria-hidden="true"
        className={cn(chevronClassName, open && "rotate-90")}
      />
      {children}
    </button>
  );
}
