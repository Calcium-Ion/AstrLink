import { cn } from "@/lib/utils";

/** Compact context around one match, preserving the matched text and Unicode characters. */
export function TextExcerpt({
  text,
  start,
  end,
  tone = "primary",
  contextLength = 28,
}: {
  text: string;
  start: number;
  end: number;
  tone?: "primary" | "warning";
  contextLength?: number;
}) {
  const before = Array.from(text.slice(0, start));
  const after = Array.from(text.slice(end));
  return (
    <p className="whitespace-pre-wrap break-words text-sm leading-relaxed [overflow-wrap:anywhere]">
      <span className="text-muted-foreground">
        {before.length > contextLength ? "…" : ""}
        {before.slice(-contextLength).join("").replace(/\s+/g, " ")}
      </span>
      <mark
        className={cn(
          "rounded-sm px-0.5 font-medium",
          tone === "warning"
            ? "bg-warning-wash text-warning-foreground"
            : "bg-accent text-primary",
        )}
      >
        {text.slice(start, end)}
      </mark>
      <span className="text-muted-foreground">
        {after.slice(0, contextLength).join("").replace(/\s+/g, " ")}
        {after.length > contextLength ? "…" : ""}
      </span>
    </p>
  );
}
