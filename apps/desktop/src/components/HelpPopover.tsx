import type { ReactNode } from "react";
import { CircleHelp } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";

/** Supporting explanations stay available without consuming working height. */
export function HelpPopover({ label, children }: { label: string; children: ReactNode }) {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button aria-label={label} size="icon-xs" type="button" variant="ghost">
          <CircleHelp aria-hidden="true" className="text-muted-foreground" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="text-xs leading-relaxed">
        {children}
      </PopoverContent>
    </Popover>
  );
}
