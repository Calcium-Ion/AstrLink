import type { ReactNode } from "react";

import { DataRow } from "@/components/DataRow";
import { cn } from "@/lib/utils";

// Share spare width across the content columns and reserve all three actions.
// Query the scroller itself, including the space taken by its scrollbar.
const columns =
  "@[860px]/service-list:grid-cols-[3.25rem_minmax(0,1.4fr)_minmax(6.5rem,0.55fr)_minmax(10rem,1fr)_3.75rem_6.25rem]";

export function ServiceListHeader({ labels }: { labels: readonly string[] }) {
  return (
    <div
      aria-hidden="true"
      className={cn(
        "sticky top-0 z-10 hidden shrink-0 items-center gap-4 border-y bg-muted px-3 py-2 text-micro font-medium text-muted-foreground transition-opacity group-has-[[data-sorting=true]]/service-list:opacity-0 motion-reduce:transition-none @[860px]/service-list:grid",
        columns,
      )}
    >
      <span />
      {labels.map((label) => (
        <span key={label}>{label}</span>
      ))}
    </div>
  );
}

export function ServiceListRow({
  name,
  order,
  identity,
  inventory,
  usage,
  status,
  actions,
  sorting = false,
  sortIcon,
  sortStatus,
}: {
  name: string;
  order?: ReactNode;
  identity: ReactNode;
  inventory: ReactNode;
  usage?: ReactNode;
  status: ReactNode;
  actions: ReactNode;
  sorting?: boolean;
  sortIcon?: ReactNode;
  sortStatus?: ReactNode;
}) {
  if (sorting) {
    return (
      <DataRow
        asChild
        className="grid h-13 grid-cols-[3.25rem_minmax(0,1fr)_auto] gap-4 px-3 py-0"
      >
        <article
          aria-label={name}
          data-testid="service-card"
          data-sorting="true"
        >
          <div>{order}</div>
          <div className="flex min-w-0 items-center gap-2.5">
            {sortIcon}
            <span className="truncate text-sm font-semibold">{name}</span>
          </div>
          <div className="flex shrink-0 items-center gap-1.5 text-micro text-muted-foreground">
            {sortStatus}
          </div>
        </article>
      </DataRow>
    );
  }
  return (
    <DataRow
      asChild
      className={cn(
        "grid grid-cols-[3rem_minmax(0,1fr)] items-center gap-x-3 gap-y-2 px-2 py-3 transition-colors hover:bg-muted/30 @[640px]/service-list:grid-cols-[3.25rem_minmax(0,1fr)_minmax(10rem,0.85fr)_6.25rem] @[640px]/service-list:px-3 @[860px]/service-list:min-h-20 @[860px]/service-list:gap-x-4",
        columns,
      )}
    >
      <article aria-label={name} data-testid="service-card">
        <div className="col-start-1 row-start-1 row-span-2 self-start pt-1 @[640px]/service-list:self-center @[640px]/service-list:pt-0 @[860px]/service-list:row-span-1">
          {order}
        </div>
        <div className="col-start-2 row-start-1 min-w-0">{identity}</div>
        <div className="col-start-2 row-start-2 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 @[860px]/service-list:col-start-3 @[860px]/service-list:row-start-1 @[860px]/service-list:grid @[860px]/service-list:gap-1">
          {inventory}
        </div>
        <div
          className={cn(
            "col-span-2 col-start-1 row-start-3 min-w-0 @[640px]/service-list:col-span-1 @[640px]/service-list:col-start-3 @[640px]/service-list:row-span-2 @[640px]/service-list:row-start-1 @[860px]/service-list:col-start-4 @[860px]/service-list:row-span-1",
            !usage && "hidden @[860px]/service-list:block",
          )}
        >
          {usage}
        </div>
        <div className="col-start-1 row-start-4 flex items-center gap-2.5 @[640px]/service-list:col-start-4 @[640px]/service-list:row-start-2 @[640px]/service-list:justify-end @[860px]/service-list:col-start-5 @[860px]/service-list:row-start-1 @[860px]/service-list:justify-between">
          {status}
        </div>
        <div className="col-start-2 row-start-4 flex shrink-0 items-center justify-end gap-1 @max-[640px]/service-list:[&>button]:size-9 @[640px]/service-list:col-start-4 @[640px]/service-list:row-start-1 @[860px]/service-list:col-start-6">
          {actions}
        </div>
      </article>
    </DataRow>
  );
}
