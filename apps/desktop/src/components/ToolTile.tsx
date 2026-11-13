import { cn } from "@/lib/utils";

/**
 * A small tile for one tool or skill: its name, how much it was used, and
 * what it does. A filled dot marks one in use; the rest read quieter.
 */
export function ToolTile({
  name,
  note,
  description,
  active,
}: {
  name: string;
  /** How much it was used, such as a count, or that it never was. */
  note: string;
  description?: string;
  active: boolean;
}) {
  return (
    <div
      className="min-w-0 rounded-md border px-2.75 pt-2 pb-2.25"
      data-active={active || undefined}
      data-slot="tool-tile"
    >
      <div className="flex min-w-0 items-center gap-1.75">
        <span
          aria-hidden="true"
          className={cn(
            "size-1.5 shrink-0 rounded-full",
            active ? "bg-tide" : "border-[1.25px] border-input",
          )}
        />
        <code
          className={cn(
            "min-w-0 truncate font-mono text-xs",
            active ? "font-medium" : "text-text-secondary",
          )}
          title={name}
        >
          {name}
        </code>
        <span
          className={cn(
            "ml-auto shrink-0 pl-1.5 text-xs whitespace-nowrap",
            active ? "text-text-secondary" : "text-muted-foreground",
          )}
        >
          {note}
        </span>
      </div>
      {description ? (
        <p
          className="mt-0.5 ml-3.25 truncate text-xs text-muted-foreground"
          title={description}
        >
          {description}
        </p>
      ) : null}
    </div>
  );
}
