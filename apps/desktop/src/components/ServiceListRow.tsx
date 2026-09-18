import type { ReactNode } from "react";

import { DataRow } from "@/components/DataRow";
import { cn } from "@/lib/utils";

const columns =
  "@[860px]:grid-cols-[3.25rem_minmax(0,1fr)_6rem_12rem_7.5rem_4rem]";

export function ServiceListHeader({ labels }: { labels: readonly string[] }) {
  return (
    <div
      aria-hidden="true"
      className={cn(
        "sticky top-0 z-10 hidden shrink-0 items-center gap-5 border-y bg-muted px-3 py-2 text-micro font-medium text-muted-foreground transition-opacity group-has-[[data-sorting=true]]/service-list:opacity-0 motion-reduce:transition-none @[860px]:grid",
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
      <DataRow asChild className="grid h-13 grid-cols-[3.25rem_minmax(0,1fr)_auto] gap-4 px-3 py-0">
        <article aria-label={name} data-testid="service-card" data-sorting="true">
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
        "grid grid-cols-[3.25rem_minmax(0,1fr)_auto] items-center gap-x-4 gap-y-3 px-3 py-3 transition-colors hover:bg-muted/30 @[860px]:min-h-20 @[860px]:gap-5",
        columns,
      )}
    >
      <article aria-label={name} data-testid="service-card">
        <div className="col-start-1 row-start-1 row-span-3 self-start pt-1 @[860px]:row-span-1 @[860px]:self-center @[860px]:pt-0">
          {order}
        </div>
        <div className="min-w-0">{identity}</div>
        <div className="col-start-2 row-start-2 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 @[860px]:col-auto @[860px]:row-auto @[860px]:grid @[860px]:gap-1">
          {inventory}
        </div>
        <div
          className={cn(
            "col-span-2 col-start-2 min-w-0 @[860px]:col-start-auto @[860px]:col-span-1",
            !usage && "hidden @[860px]:block",
          )}
        >
          {usage}
        </div>
        <div className="col-start-3 row-start-2 flex items-center justify-end gap-2.5 @[860px]:col-auto @[860px]:row-auto @[860px]:justify-between">
          {status}
        </div>
        <div className="col-start-3 row-start-1 flex shrink-0 items-center justify-end gap-1 @[860px]:col-auto @[860px]:row-auto">
          {actions}
        </div>
      </article>
    </DataRow>
  );
}
