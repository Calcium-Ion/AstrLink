import type { CSSProperties, ReactNode } from "react";

import { DataRow } from "@/components/DataRow";
import { cn } from "@/lib/utils";

/** Optional table columns in display order; identity and actions always show. */
export const SERVICE_LIST_COLUMNS = [
  "models",
  "usage",
  "billing",
  "status",
] as const;
export type ServiceListColumn = (typeof SERVICE_LIST_COLUMNS)[number];

export type ServiceListLabels = Record<
  "service" | ServiceListColumn | "actions",
  string
>;

const tracks: Record<ServiceListColumn, string> = {
  models: "minmax(6rem,0.5fr)",
  usage: "minmax(9.5rem,0.95fr)",
  billing: "minmax(6.5rem,0.6fr)",
  status: "3.75rem",
};

// Share spare width across the visible content columns and reserve all three
// actions. Query the scroller itself, including the space taken by its scrollbar.
const columns = "@[860px]/service-list:grid-cols-(--service-list-columns)";

function columnsStyle(hidden: readonly ServiceListColumn[]): CSSProperties {
  return {
    "--service-list-columns": [
      "3.25rem",
      "minmax(0,1.25fr)",
      ...SERVICE_LIST_COLUMNS.filter((id) => !hidden.includes(id)).map(
        (id) => tracks[id],
      ),
      "6.25rem",
    ].join(" "),
  } as CSSProperties;
}

export function ServiceListHeader({
  labels,
  hidden = [],
}: {
  labels: ServiceListLabels;
  hidden?: readonly ServiceListColumn[];
}) {
  return (
    <div
      aria-hidden="true"
      className={cn(
        "sticky top-0 z-10 hidden shrink-0 items-center gap-4 border-y bg-muted px-3 py-2 text-micro font-medium text-muted-foreground transition-opacity group-has-[[data-sorting=true]]/service-list:opacity-0 motion-reduce:transition-none @[860px]/service-list:grid",
        columns,
      )}
      style={columnsStyle(hidden)}
    >
      <span />
      <span>{labels.service}</span>
      {SERVICE_LIST_COLUMNS.filter((id) => !hidden.includes(id)).map((id) => (
        <span key={id}>{labels[id]}</span>
      ))}
      <span>{labels.actions}</span>
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
  hidden = [],
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
  hidden?: readonly ServiceListColumn[];
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
  const showUsage = !hidden.includes("usage");
  const showBilling = !hidden.includes("billing");
  // Without usage or billing, the compact layout gives their column to identity.
  const widenIdentity =
    !showUsage &&
    !showBilling &&
    "@[640px]/service-list:col-end-4 @[860px]/service-list:col-end-auto";
  return (
    <DataRow
      asChild
      className={cn(
        "grid grid-cols-[3rem_minmax(0,1fr)] items-center gap-x-3 gap-y-2 px-2 py-3 transition-colors hover:bg-muted/30 @[640px]/service-list:grid-cols-[3.25rem_minmax(0,1fr)_minmax(10rem,0.85fr)_6.25rem] @[640px]/service-list:px-3 @[860px]/service-list:min-h-20 @[860px]/service-list:gap-x-4",
        columns,
      )}
      style={columnsStyle(hidden)}
    >
      <article aria-label={name} data-testid="service-card">
        {/* The table layout pins order and identity, then auto-places visible cells in row one. */}
        <div className="col-start-1 row-start-1 row-span-2 self-start pt-1 @[640px]/service-list:self-center @[640px]/service-list:pt-0 @[860px]/service-list:row-span-1 @[860px]/service-list:row-start-1">
          {order}
        </div>
        <div className={cn("col-start-2 row-start-1 min-w-0", widenIdentity)}>
          {identity}
        </div>
        {!hidden.includes("models") ? (
          <div
            className={cn(
              "col-start-2 row-start-2 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 @[860px]/service-list:col-start-auto @[860px]/service-list:row-start-1 @[860px]/service-list:grid @[860px]/service-list:gap-1",
              widenIdentity,
            )}
          >
            {inventory}
          </div>
        ) : null}
        {/* Usage and billing stack in one cell until the table layout gives each its own column. */}
        <div
          className={cn(
            "col-span-2 col-start-1 row-start-3 grid min-w-0 gap-1 @[640px]/service-list:col-span-1 @[640px]/service-list:col-start-3 @[640px]/service-list:row-span-2 @[640px]/service-list:row-start-1 @[860px]/service-list:contents",
            !(showUsage && usage) && !(showBilling && billing) && "hidden",
          )}
        >
          {showUsage ? (
            // Keep empty cells in the table so later columns stay aligned.
            <div
              className={cn(
                "min-w-0 @[860px]/service-list:row-start-1 @[860px]/service-list:pr-6",
                !usage && "hidden @[860px]/service-list:block",
              )}
            >
              {usage}
            </div>
          ) : null}
          {showBilling ? (
            <div
              className={cn(
                "min-w-0 @[860px]/service-list:row-start-1",
                !billing && "hidden @[860px]/service-list:block",
              )}
            >
              {billing}
            </div>
          ) : null}
        </div>
        {!hidden.includes("status") ? (
          <div className="col-start-1 row-start-4 flex items-center gap-2.5 @[640px]/service-list:col-start-4 @[640px]/service-list:row-start-2 @[640px]/service-list:justify-end @[860px]/service-list:col-start-auto @[860px]/service-list:row-start-1 @[860px]/service-list:justify-between">
            {status}
          </div>
        ) : null}
        <div className="col-start-2 row-start-4 flex shrink-0 items-center justify-end gap-1 @max-[640px]/service-list:[&>button]:size-9 @[640px]/service-list:col-start-4 @[640px]/service-list:row-start-1 @[860px]/service-list:col-start-auto">
          {actions}
        </div>
      </article>
    </DataRow>
  );
}
