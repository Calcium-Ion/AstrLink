import { LoaderCircle } from "@/components/icons";

import { cn } from "@/lib/utils";

export function LoadingState({
  className,
  label,
}: {
  className?: string;
  label: string;
}) {
  return (
    <div
      aria-busy="true"
      className={cn(
        "inline-flex items-center justify-center gap-2 text-sm text-text-secondary",
        className,
      )}
      data-slot="loading-state"
      role="status"
    >
      <LoaderCircle animateOnHover={false}
        aria-hidden="true"
        className="size-3.5 animate-spin motion-reduce:animate-none"
      />
      {label}
    </div>
  );
}
