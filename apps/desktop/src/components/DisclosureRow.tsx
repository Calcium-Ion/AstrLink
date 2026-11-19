import { useState, type ReactNode } from "react";

import { ChevronRight } from "@/components/icons";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";

/**
 * A row of a hairline list whose line opens what waits behind it: the whole
 * line is the toggle, and a chevron at its end turns when it opens.
 */
export function DisclosureRow({
  summary,
  children,
  defaultOpen = false,
}: {
  summary: ReactNode;
  children: ReactNode;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <Collapsible
      className="group/disclosure min-w-0"
      onOpenChange={setOpen}
      open={open}
    >
      <CollapsibleTrigger asChild>
        <button
          className="flex w-full min-w-0 items-start gap-2 px-3 py-2 text-left transition-colors outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring/30 focus-visible:ring-inset"
          type="button"
        >
          <div className="min-w-0 flex-1">{summary}</div>
          <ChevronRight
            aria-hidden="true"
            className="mt-0.5 size-3.5 shrink-0 text-muted-foreground transition-transform group-data-[state=open]/disclosure:rotate-90"
          />
        </button>
      </CollapsibleTrigger>
      <CollapsibleContent>{children}</CollapsibleContent>
    </Collapsible>
  );
}
