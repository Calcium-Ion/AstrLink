import type { ComponentProps } from "react";

import { StatusDot, type StatusTone } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

const toneClasses: Record<StatusTone, string> = {
  blocked: "bg-blocked-wash text-blocked-foreground",
  negative: "bg-danger-wash text-danger-foreground",
  neutral: "bg-muted text-muted-foreground",
  pending: "bg-warning-wash text-warning-foreground",
  positive: "bg-success-wash text-success-foreground",
};

/** Quiet semantic status with both a colour cue and a readable label. */
export function StatusBadge({
  children,
  className,
  tone = "neutral",
  ...props
}: Omit<ComponentProps<typeof Badge>, "variant"> & { tone?: StatusTone }) {
  return (
    <Badge
      className={cn("gap-1.5 px-2 py-0.5", toneClasses[tone], className)}
      data-tone={tone}
      variant="secondary"
      {...props}
    >
      <StatusDot tone={tone} />
      <span className="text-micro">{children}</span>
    </Badge>
  );
}
