import type { HTMLAttributes } from "react";

import { cn } from "@/lib/utils";

type FormMessageTone = "error" | "notice" | "success" | "warning";

const toneClasses: Record<FormMessageTone, string> = {
  error: "border-destructive/25 bg-danger-wash text-danger-foreground",
  notice: "border-border bg-muted text-text-secondary",
  success: "border-success/25 bg-success-wash text-success-foreground",
  warning: "border-warning/30 bg-warning-wash text-warning-foreground",
};

export function FormMessage({
  className,
  tone = "notice",
  ...props
}: HTMLAttributes<HTMLParagraphElement> & { tone?: FormMessageTone }) {
  return (
    <p
      className={cn(
        "shrink-0 rounded-md border px-3 py-2 text-xs",
        toneClasses[tone],
        className,
      )}
      role={tone === "error" ? "alert" : "status"}
      {...props}
    />
  );
}
