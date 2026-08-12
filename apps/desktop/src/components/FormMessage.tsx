import type { HTMLAttributes } from "react";

import { cn } from "@/lib/utils";

type FormMessageTone = "error" | "notice" | "success" | "warning";

const toneClasses: Record<FormMessageTone, string> = {
  error: "border-danger-wash bg-danger-wash text-danger-foreground",
  notice: "border-accent bg-accent text-accent-foreground",
  success: "border-success-wash bg-success-wash text-success-foreground",
  warning: "border-warning-wash bg-warning-wash text-warning-foreground",
};

export function FormMessage({
  className,
  tone = "notice",
  ...props
}: HTMLAttributes<HTMLParagraphElement> & { tone?: FormMessageTone }) {
  return (
    <p
      className={cn(
        "rounded-lg border px-3 py-2 text-xs leading-5",
        toneClasses[tone],
        className,
      )}
      role={tone === "error" ? "alert" : "status"}
      {...props}
    />
  );
}
