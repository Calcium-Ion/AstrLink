import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

export function DevelopmentBadge({ className }: { className?: string }) {
  return (
    <Badge
      variant="secondary"
      className={cn(
        "border-warning-foreground/20 bg-warning-wash font-semibold tracking-wide text-warning-foreground",
        className,
      )}
    >
      DEV
    </Badge>
  );
}
