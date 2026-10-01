import type { CSSProperties, ReactNode } from "react";

import { DataRow } from "@/components/DataRow";
import { cn } from "@/lib/utils";

/** Optional table columns in display order; identity and actions always show. */
export const SERVICE_LIST_COLUMNS = [
  "models",
  "usage",
  "billing",
  "performance",
  "status",
] as const;
export type ServiceListColumn = (typeof SERVICE_LIST_COLUMNS)[number];

export type ServiceListLabels = Record<
  "service" | ServiceListColumn | "actions",
  string
>;

// Content columns share spare width. Bound the contents separately so neither
// the name column nor a progress bar stretches to fill the whole row. The
// minimums are what each cell needs to stay legible in the narrowest table.
// The switch and action buttons keep exact tracks: spare width there would
// open a wider gap between the controls than between the buttons themselves.
const tracks: Record<ServiceListColumn | "intelligence", string> = {
  models: "minmax(6rem,0.6fr)",
  usage: "minmax(7.5rem,1.2fr)",
  billing: "minmax(4.75rem,0.7fr)",
  performance: "minmax(5.25rem,0.7fr)",
  intelligence: "5.5rem",
  status: "2.25rem",
};

// The minimum 760px window still leaves about 660px after the collapsed
// sidebar and padding, so every desktop window gets the same table; only
// narrower hosts stack the cells. Keep the optional model counts with identity
// until there is room for a column.
const columns =
  "@[640px]/service-list:grid-cols-(--service-list-columns) @[1080px]/service-list:grid-cols-(--service-list-expanded-columns)";

function visibleColumns(
  hidden: readonly ServiceListColumn[],
  intelligence: boolean,
): (ServiceListColumn | "intelligence")[] {
  const visible: (ServiceListColumn | "intelligence")[] =
    SERVICE_LIST_COLUMNS.filter((id) => !hidden.includes(id));
  if (intelligence)
    visible.splice(
      visible.indexOf("status") < 0
        ? visible.length
        : visible.indexOf("status"),
      0,
      "intelligence",
    );
  return visible;
}

function columnsStyle(
  hidden: readonly ServiceListColumn[],
  intelligence = false,
): CSSProperties {
  const visible = visibleColumns(hidden, intelligence);
  const template = (ids: readonly (ServiceListColumn | "intelligence")[]) =>
    [
      "1.75rem",
      "minmax(0,1.4fr)",
      ...ids.map((id) => tracks[id]),
      intelligence ? "7.75rem" : "5.75rem",
    ].join(" ");
  return {
    "--service-list-columns": template(visible.filter((id) => id !== "models")),
    "--service-list-expanded-columns": template(visible),
  } as CSSProperties;
}

export function ServiceListHeader({
  labels,
  intelligence = false,
  hidden = [],
}: {
  labels: ServiceListLabels;
  intelligence?: boolean;
  hidden?: readonly ServiceListColumn[];
}) {
  return (
    <div
      aria-hidden="true"
      className={cn(
        "sticky top-0 z-10 hidden shrink-0 items-center gap-2 border-y bg-muted px-2 py-2 text-micro font-medium text-muted-foreground transition-opacity group-has-[[data-sorting=true]]/service-list:opacity-0 motion-reduce:transition-none @[640px]/service-list:grid @[640px]/service-list:gap-3 @[1040px]/service-list:px-3",
        columns,
      )}
      style={columnsStyle(hidden, !!intelligence)}
    >
      <span />
      <span>{labels.service}</span>
      {visibleColumns(hidden, intelligence).map((id) => (
        <span
          className={cn(
            id === "models" &&
              "mx-auto hidden w-full max-w-28 @[1080px]/service-list:block",
            id === "usage" && "mx-auto w-full max-w-68 pr-3 pl-1.5",
            id === "billing" && "mx-auto w-full max-w-24",
            id === "performance" && "mx-auto w-full max-w-32",
            id === "status" && "text-center",
          )}
          key={id}
        >
          {id === "intelligence" ? "智力结果" : labels[id]}
        </span>
      ))}
      <span className="text-center">{labels.actions}</span>
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
  performance,
  intelligence,
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
  performance?: ReactNode;
  intelligence?: ReactNode;
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
        className="grid h-13 grid-cols-[1.75rem_minmax(0,1fr)_auto] gap-4 px-3 py-0"
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
  const showPerformance = !hidden.includes("performance");
  return (
    <DataRow
      asChild
      className={cn(
        "grid grid-cols-[1.75rem_minmax(0,1fr)] items-center gap-x-2 gap-y-2 px-2 py-2.5 transition-colors hover:bg-muted/30 @[480px]/service-list:grid-cols-[1.75rem_minmax(0,1fr)_auto] @[640px]/service-list:min-h-20 @[640px]/service-list:gap-y-3 @[640px]/service-list:gap-x-3 @[640px]/service-list:py-3 @[1040px]/service-list:px-3",
        columns,
      )}
      style={columnsStyle(hidden, !!intelligence)}
    >
      <article aria-label={name} data-testid="service-card">
        <div className="col-start-1 row-start-1 self-start pt-1 @[640px]/service-list:self-center @[640px]/service-list:pt-0">
          {order}
        </div>
        <div className="col-start-2 row-start-1 grid min-w-0 gap-2 @[1080px]/service-list:contents">
          <div className="min-w-0 @[1080px]/service-list:col-start-2 @[1080px]/service-list:row-start-1">
            {identity}
          </div>
          {!hidden.includes("models") ? (
            <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 @[1080px]/service-list:row-start-1 @[1080px]/service-list:mx-auto @[1080px]/service-list:grid @[1080px]/service-list:w-full @[1080px]/service-list:max-w-28 @[1080px]/service-list:gap-1">
              {inventory}
            </div>
          ) : null}
        </div>
        {/* Hosts narrower than the table get a quota column and a compact
            billing/performance summary. The same controls become table cells
            in every desktop window. */}
        <div
          className={cn(
            "col-span-full row-start-2 grid min-w-0 items-start gap-x-5 gap-y-2 @[480px]/service-list:col-span-2 @[480px]/service-list:col-start-2 @[640px]/service-list:contents",
            showUsage &&
              usage &&
              (showBilling || showPerformance) &&
              "@[320px]/service-list:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]",
            !(showUsage && usage) &&
              !(showBilling && billing) &&
              !(showPerformance && performance) &&
              !intelligence &&
              "hidden",
          )}
        >
          {showUsage ? (
            // Keep empty cells in the table so later columns stay aligned. The
            // quota bar fills its cell, so its trailing edge gets the wider
            // inset; the leading edge already follows the service column's
            // spare width.
            <div
              className={cn(
                "min-w-0 @[640px]/service-list:row-start-1 @[640px]/service-list:mx-auto @[640px]/service-list:w-full @[640px]/service-list:max-w-68 @[640px]/service-list:pr-3 @[640px]/service-list:pl-1.5",
                !usage && "hidden @[640px]/service-list:block",
              )}
            >
              {usage}
            </div>
          ) : null}
          <div
            className={cn(
              "grid min-w-0 gap-1 @[640px]/service-list:contents",
              !showBilling && !showPerformance && "hidden",
            )}
          >
            {showBilling ? (
              <div
                className={cn(
                  "min-w-0 @[640px]/service-list:row-start-1 @[640px]/service-list:mx-auto @[640px]/service-list:w-full @[640px]/service-list:max-w-24",
                  !billing && "hidden @[640px]/service-list:block",
                )}
              >
                {billing}
              </div>
            ) : null}
            {showPerformance ? (
              <div className="min-w-0 @[640px]/service-list:row-start-1 @[640px]/service-list:mx-auto @[640px]/service-list:w-full @[640px]/service-list:max-w-32 @[640px]/service-list:pr-1.5">
                {performance}
              </div>
            ) : null}
          </div>
          {intelligence ? (
            <div className="flex min-w-0 items-center gap-2 @[640px]/service-list:row-start-1">
              <span className="text-xs text-muted-foreground @[640px]/service-list:hidden">
                智力结果
              </span>
              {intelligence}
            </div>
          ) : null}
        </div>
        <div className="col-span-full row-start-3 flex min-w-0 items-center justify-between gap-3 @[480px]/service-list:col-span-1 @[480px]/service-list:col-start-3 @[480px]/service-list:row-start-1 @[640px]/service-list:contents">
          {!hidden.includes("status") ? (
            <div className="flex shrink-0 items-center gap-2 @[640px]/service-list:row-start-1 @[640px]/service-list:justify-center">
              {status}
            </div>
          ) : null}
          <div className="ml-auto flex shrink-0 items-center justify-end gap-1 @max-[480px]/service-list:[&>button]:size-9 @[640px]/service-list:row-start-1 @[640px]/service-list:ml-0 @[640px]/service-list:justify-center">
            {actions}
          </div>
        </div>
      </article>
    </DataRow>
  );
}
