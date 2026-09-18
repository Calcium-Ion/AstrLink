import { Search, X } from "@/components/icons";
import type { ReactNode } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

/** Shared title, result count, and search for management lists. */
export function ListToolbar({
  title,
  count,
  query,
  onQueryChange,
  searchLabel,
  placeholder,
  clearLabel,
  filters,
}: {
  title: string;
  count: string | number;
  query: string;
  onQueryChange: (value: string) => void;
  searchLabel: string;
  placeholder: string;
  clearLabel: string;
  filters?: ReactNode;
}) {
  return (
    <div className="flex min-w-0 shrink-0 flex-wrap items-center justify-between gap-3">
      {filters ?? (
        <div className="flex min-w-0 items-center gap-2">
          <h2 className="text-sm font-semibold">{title}</h2>
          <Badge className="tabular-nums" variant="secondary">
            {count}
          </Badge>
        </div>
      )}
      <div className="relative min-w-0 flex-1 basis-48 @[560px]:max-w-72">
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
    </div>
  );
}
