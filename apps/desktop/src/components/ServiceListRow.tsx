import type { ReactNode } from "react";

import { DataRow } from "@/components/DataRow";
import { cn } from "@/lib/utils";

// Share spare width across the content columns and reserve the visible actions.
// Query the scroller itself, including the space taken by its scrollbar.
const columns = (intelligence: boolean) =>
  intelligence
    ? "@[860px]/service-list:grid-cols-[3rem_minmax(7rem,1.2fr)_4.5rem_minmax(7rem,0.9fr)_5rem_5.5rem_3.25rem_10rem]"
    : "@[860px]/service-list:grid-cols-[3rem_minmax(8rem,1.4fr)_minmax(4.5rem,0.5fr)_minmax(7rem,0.9fr)_minmax(5rem,0.6fr)_3.25rem_8.75rem]";

export function ServiceListHeader({
  labels,
  intelligence = false,
}: {
  labels: readonly string[];
  intelligence?: boolean;
}) {
  return (
    <div
      aria-hidden="true"
      className={cn(
        "sticky top-0 z-10 hidden shrink-0 items-center gap-2 border-y bg-muted px-3 py-2 text-micro font-medium text-muted-foreground transition-opacity group-has-[[data-sorting=true]]/service-list:opacity-0 motion-reduce:transition-none @[860px]/service-list:grid",
        columns(!!intelligence),
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
  billing,
  status,
  actions,
  intelligence,
  sorting = false,
  sortIcon,
  sortStatus,
}: {
  name: string;
  order?: ReactNode;
  identity: ReactNode;
  inventory: ReactNode;
  usage?: ReactNode;
  billing?: ReactNode;
  status: ReactNode;
  actions: ReactNode;
  intelligence?: ReactNode;
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
        "grid grid-cols-[3rem_minmax(0,1fr)] items-center gap-x-3 gap-y-2 px-2 py-3 transition-colors hover:bg-muted/30 @[640px]/service-list:grid-cols-[3.25rem_minmax(0,1fr)_minmax(10rem,0.85fr)_10rem] @[640px]/service-list:px-3 @[860px]/service-list:min-h-20 @[860px]/service-list:gap-x-2",
        columns(!!intelligence),
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
        {/* Usage and billing stack in one cell until the table layout gives each its own column. */}
        <div
          className={cn(
            "col-span-2 col-start-1 row-start-3 grid min-w-0 gap-1 @[640px]/service-list:col-span-1 @[640px]/service-list:col-start-3 @[640px]/service-list:row-span-2 @[640px]/service-list:row-start-1 @[860px]/service-list:contents",
            !usage && !billing && "hidden",
          )}
        >
          {usage ? (
            <div className="min-w-0 @[860px]/service-list:col-start-4 @[860px]/service-list:row-start-1 @[860px]/service-list:pr-6">
              {usage}
            </div>
          ) : null}
          {billing ? (
            <div className="min-w-0 @[860px]/service-list:col-start-5 @[860px]/service-list:row-start-1">
              {billing}
            </div>
          ) : null}
        </div>
        <div
          className={cn(
            "col-start-1 row-start-4 flex items-center gap-2.5 @[640px]/service-list:col-start-4 @[640px]/service-list:row-start-2 @[640px]/service-list:justify-end @[860px]/service-list:row-start-1 @[860px]/service-list:justify-between",
            intelligence
              ? "@[860px]/service-list:col-start-7"
              : "@[860px]/service-list:col-start-6",
          )}
        >
          {status}
        </div>
        <div
          className={cn(
            "col-start-2 row-start-4 flex shrink-0 items-center justify-end gap-1 @max-[640px]/service-list:[&>button]:size-9 @[640px]/service-list:col-start-4 @[640px]/service-list:row-start-1",
            intelligence
              ? "@[860px]/service-list:col-start-8"
              : "@[860px]/service-list:col-start-7",
          )}
        >
          {actions}
        </div>
        {intelligence ? (
          <div className="col-start-2 row-start-5 flex min-w-0 items-center gap-2 @[640px]/service-list:col-start-3 @[640px]/service-list:row-start-3 @[860px]/service-list:col-start-6 @[860px]/service-list:row-start-1">
            <span className="text-xs text-muted-foreground @[860px]/service-list:hidden">
              智力结果
            </span>
            {intelligence}
          </div>
        ) : null}
      </article>
    </DataRow>
  );
}
