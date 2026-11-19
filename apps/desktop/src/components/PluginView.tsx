import { StatusBadge } from "@/components/StatusBadge";
import type { StatusTone } from "@/components/StatusDot";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import type { PluginViewBlock } from "@/plugin-model";

const badgeTones: Record<"ok" | "warn" | "error", StatusTone> = {
  ok: "positive",
  warn: "pending",
  error: "negative",
};

/**
 * A view a plugin returned: label-value rows, tables, badges and text. Core
 * cleaned it to plain text; every string renders as text, never as markup.
 */
export function PluginView({
  blocks,
  className,
  kvClassName,
  truncated = false,
  truncatedLabel,
}: {
  blocks: PluginViewBlock[];
  className?: string;
  /**
   * Classes for the label-value rows, such as the label column of the rows
   * around the view, so both line up.
   */
  kvClassName?: string;
  truncated?: boolean;
  /** Shown under the view when Core cut it to its limits. */
  truncatedLabel: string;
}) {
  return (
    <div
      className={cn("grid min-w-0 gap-3", className)}
      data-testid="plugin-view"
    >
      {blocks.map((block, index) => (
        <PluginViewBlockItem
          block={block}
          key={index}
          kvClassName={kvClassName}
        />
      ))}
      {truncated ? (
        <p className="text-micro text-muted-foreground">{truncatedLabel}</p>
      ) : null}
    </div>
  );
}

function PluginViewBlockItem({
  block,
  kvClassName,
}: {
  block: PluginViewBlock;
  kvClassName?: string;
}) {
  switch (block.type) {
    case "kv":
      return (
        <dl
          className={cn(
            "grid min-w-0 grid-cols-[minmax(5rem,auto)_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-xs",
            kvClassName,
          )}
        >
          {block.items.map((item, index) => (
            <div className="contents" key={index}>
              <dt className="text-muted-foreground">{item.label}</dt>
              <dd
                className={cn(
                  "min-w-0 break-words whitespace-pre-wrap",
                  item.mono && "font-mono",
                )}
              >
                {item.value}
              </dd>
            </div>
          ))}
        </dl>
      );
    case "table":
      return (
        <div className="min-w-0 overflow-x-auto rounded-md border">
          <Table className="text-xs">
            <TableHeader>
              <TableRow>
                {block.columns.map((column, index) => (
                  <TableHead className="h-8" key={index}>
                    {column}
                  </TableHead>
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {block.rows.map((row, rowIndex) => (
                <TableRow key={rowIndex}>
                  {row.map((cell, cellIndex) => (
                    <TableCell
                      className={cn(
                        "py-1.5",
                        typeof cell === "number" && "text-right tabular-nums",
                      )}
                      key={cellIndex}
                    >
                      {cell}
                    </TableCell>
                  ))}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      );
    case "badges":
      return (
        <div className="flex flex-wrap gap-1.5">
          {block.items.map((badge, index) => (
            <StatusBadge
              key={index}
              tone={badge.tone ? badgeTones[badge.tone] : "neutral"}
            >
              {badge.text}
            </StatusBadge>
          ))}
        </div>
      );
    case "text":
      return (
        <p className="text-xs leading-5 break-words whitespace-pre-wrap">
          {block.text}
        </p>
      );
  }
}
