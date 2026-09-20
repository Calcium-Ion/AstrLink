import { Search, X } from "@/components/icons";
import type { ReactNode } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { HelpPopover } from "@/components/HelpPopover";
import { cn } from "@/lib/utils";

/** Keep list identity, search, help, and actions together in one compact toolbar. */
export function ListToolbar({
  title,
  count,
  query,
  onQueryChange,
  searchLabel,
  placeholder,
  clearLabel,
  filters,
  actions,
  help,
}: {
  title: string;
  count: string | number;
  query: string;
  onQueryChange: (value: string) => void;
  searchLabel: string;
  placeholder: string;
  clearLabel: string;
  filters?: ReactNode;
  actions?: ReactNode;
  help?: { label: string; content: ReactNode };
}) {
  return (
    <div className={cn(
      "min-w-0 shrink-0 items-center",
      actions
        ? "grid grid-cols-[minmax(0,1fr)_auto] gap-2 @[480px]:grid-cols-[auto_minmax(0,1fr)_auto]"
        : "flex flex-wrap justify-between gap-3",
    )}>
      {filters ?? (
        <div className="col-start-1 row-start-1 flex min-w-0 items-center gap-2">
          <h2 className="text-sm font-semibold">{title}</h2>
          <Badge className="tabular-nums" variant="secondary">
            {count}
          </Badge>
          {help ? (
            <HelpPopover label={help.label}>{help.content}</HelpPopover>
          ) : null}
        </div>
      )}
      <div className={cn(
        "relative min-w-0",
        actions
          ? "col-span-2 col-start-1 row-start-2 @[480px]:col-span-1 @[480px]:col-start-2 @[480px]:row-start-1"
          : "flex-1 basis-48 @[560px]:max-w-72",
      )}>
        <Search
          aria-hidden="true"
          className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
        />
        <Input
          aria-label={searchLabel}
          className="h-8 pr-8 pl-8 [&::-webkit-search-cancel-button]:appearance-none"
          onChange={(event) => onQueryChange(event.currentTarget.value)}
          placeholder={placeholder}
          type="search"
          value={query}
        />
        {query ? (
          <Button
            aria-label={clearLabel}
            className="absolute top-1/2 right-1 -translate-y-1/2 text-muted-foreground"
            onClick={() => onQueryChange("")}
            size="icon-xs"
            type="button"
            variant="ghost"
          >
            <X aria-hidden="true" />
          </Button>
        ) : null}
      </div>
      {actions ? (
        <div className="col-start-2 row-start-1 flex shrink-0 items-center gap-2 @[480px]:col-start-3">
          {actions}
        </div>
      ) : null}
    </div>
  );
}
