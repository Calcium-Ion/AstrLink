import type { ReactNode } from "react";

import { FileText } from "@/components/icons";
import { cn } from "@/lib/utils";

export interface FileDiffLine {
  kind: "context" | "add" | "remove" | "gap";
  text: string;
}

const lineClasses: Record<FileDiffLine["kind"], string> = {
  context: "text-muted-foreground",
  add: "bg-success-wash text-success-foreground",
  remove: "bg-danger-wash text-danger-foreground",
  gap: "text-muted-foreground",
};

const linePrefix: Record<FileDiffLine["kind"], string> = {
  context: "  ",
  add: "+ ",
  remove: "- ",
  gap: "  ",
};

/** One file's changed lines, headed by its path and a summary badge. */
export function FileDiff({
  path,
  badge,
  lines,
  className,
}: {
  path: string;
  badge?: ReactNode;
  lines: readonly FileDiffLine[];
  className?: string;
}) {
  return (
    <section
      className={cn("overflow-hidden rounded-md border text-xs", className)}
      data-slot="file-diff"
    >
      <header className="flex min-w-0 items-center gap-2 border-b bg-muted px-2.5 py-1.5">
        <FileText
          aria-hidden="true"
          className="size-3.5 shrink-0 text-muted-foreground"
        />
        <code className="min-w-0 truncate font-mono" title={path}>
          {path}
        </code>
        <span className="flex-1" />
        {badge}
      </header>
      {lines.length > 0 ? (
        <pre className="overflow-x-auto py-1.5 font-mono text-xs leading-relaxed whitespace-pre">
          {lines.map((line, index) => (
            <span
              key={index}
              className={cn("block px-2.5", lineClasses[line.kind])}
              data-kind={line.kind}
            >
              {line.kind === "gap"
                ? `  ${line.text || "…"}`
                : `${linePrefix[line.kind]}${line.text}`}
            </span>
          ))}
        </pre>
      ) : null}
    </section>
  );
}
