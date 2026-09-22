import type { ReactNode } from "react";

import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";

/** A passive capability hint: no status badge, reserved space, or click action. */
export function CapabilityIndicator({ label, children }: { label: string; children: ReactNode }) {
  return (
    <TooltipProvider delayDuration={300}>
      <Tooltip>
        <TooltipTrigger asChild>
          <span role="img" aria-label={label} tabIndex={0}
            className="inline-flex shrink-0 items-center text-muted-foreground/70 outline-none focus-visible:ring-2 focus-visible:ring-ring/30">
            {children}
          </span>
        </TooltipTrigger>
        <TooltipContent sideOffset={6}>{label}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}
