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

// Every visible column shares spare width. Bound the contents separately so
// neither the name column nor a progress bar stretches to fill the whole row.
const tracks: Record<ServiceListColumn, string> = {
  models: "minmax(6rem,0.6fr)",
  usage: "minmax(10rem,1.2fr)",
  billing: "minmax(5.5rem,0.7fr)",
  performance: "minmax(7rem,0.7fr)",
  status: "minmax(3.25rem,0.45fr)",
};

// An ordinary desktop window has about 820px left after the sidebar and padding.
// Keep the optional model counts with identity until there is room for a column.
const columns =
  "@[820px]/service-list:grid-cols-(--service-list-columns) @[1080px]/service-list:grid-cols-(--service-list-expanded-columns)";

function columnsStyle(hidden: readonly ServiceListColumn[]): CSSProperties {
  const visible = SERVICE_LIST_COLUMNS.filter((id) => !hidden.includes(id));
  const template = (ids: readonly ServiceListColumn[]) =>
    [
      "2.75rem",
      "minmax(0,1.4fr)",
      ...ids.map((id) => tracks[id]),
      "minmax(5.75rem,0.65fr)",
    ].join(" ");
  return {
    "--service-list-columns": template(visible.filter((id) => id !== "models")),
    "--service-list-expanded-columns": template(visible),
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
        "sticky top-0 z-10 hidden shrink-0 items-center gap-3 border-y bg-muted px-2 py-2 text-micro font-medium text-muted-foreground transition-opacity group-has-[[data-sorting=true]]/service-list:opacity-0 motion-reduce:transition-none @[820px]/service-list:grid @[1040px]/service-list:gap-4 @[1040px]/service-list:px-3",
        columns,
      )}
      style={columnsStyle(hidden)}
    >
      <span />
      <span>{labels.service}</span>
      {SERVICE_LIST_COLUMNS.filter((id) => !hidden.includes(id)).map((id) => (
        <span
          className={cn(
            id === "models" &&
              "mx-auto hidden w-full max-w-28 @[1080px]/service-list:block",
            id === "usage" && "mx-auto w-full max-w-68 px-2",
            id === "billing" && "mx-auto w-full max-w-24",
            id === "performance" && "mx-auto w-full max-w-32",
            id === "status" && "text-center",
          )}
          key={id}
        >
          {labels[id]}
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
  const showPerformance = !hidden.includes("performance");
  return (
    <DataRow
      asChild
      className={cn(
        "grid grid-cols-[2.75rem_minmax(0,1fr)] items-center gap-x-2 gap-y-2 px-2 py-2.5 transition-colors hover:bg-muted/30 @[480px]/service-list:grid-cols-[2.75rem_minmax(0,1fr)_auto] @[820px]/service-list:min-h-20 @[820px]/service-list:gap-x-3 @[820px]/service-list:gap-y-3 @[820px]/service-list:py-3 @[1040px]/service-list:gap-x-4 @[1040px]/service-list:px-3",
        columns,
      )}
      style={columnsStyle(hidden)}
    >
      <article aria-label={name} data-testid="service-card">
        <div className="col-start-1 row-start-1 self-start pt-1 @[820px]/service-list:self-center @[820px]/service-list:pt-0">
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
        {/* Small windows get a quota column and a compact billing/performance
            summary. The same controls become table cells on wide screens. */}
        <div
          className={cn(
            "col-span-full row-start-2 grid min-w-0 items-start gap-x-5 gap-y-2 @[480px]/service-list:col-span-2 @[480px]/service-list:col-start-2 @[820px]/service-list:contents",
            showUsage &&
              usage &&
              (showBilling || showPerformance) &&
              "@[320px]/service-list:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]",
            !(showUsage && usage) &&
              !(showBilling && billing) &&
              !(showPerformance && performance) &&
              "hidden",
          )}
        >
          {showUsage ? (
            // Keep empty cells in the table so later columns stay aligned.
            <div
              className={cn(
                "min-w-0 @[820px]/service-list:row-start-1 @[820px]/service-list:mx-auto @[820px]/service-list:w-full @[820px]/service-list:max-w-68 @[820px]/service-list:px-2",
                !usage && "hidden @[820px]/service-list:block",
              )}
            >
              {usage}
            </div>
          ) : null}
          <div
            className={cn(
              "grid min-w-0 gap-1 @[820px]/service-list:contents",
              !showBilling && !showPerformance && "hidden",
            )}
          >
            {showBilling ? (
              <div
                className={cn(
                  "min-w-0 @[820px]/service-list:row-start-1 @[820px]/service-list:mx-auto @[820px]/service-list:w-full @[820px]/service-list:max-w-24",
                  !billing && "hidden @[820px]/service-list:block",
                )}
              >
                {billing}
              </div>
            ) : null}
            {showPerformance ? (
              <div className="min-w-0 @[820px]/service-list:row-start-1 @[820px]/service-list:mx-auto @[820px]/service-list:w-full @[820px]/service-list:max-w-32">
                {performance}
              </div>
            ) : null}
          </div>
        </div>
        <div className="col-span-full row-start-3 flex min-w-0 items-center justify-between gap-3 @[480px]/service-list:col-span-1 @[480px]/service-list:col-start-3 @[480px]/service-list:row-start-1 @[820px]/service-list:contents">
          {!hidden.includes("status") ? (
            <div className="flex shrink-0 items-center gap-2 @[820px]/service-list:row-start-1 @[820px]/service-list:justify-center">
              {status}
            </div>
          ) : null}
          <div className="ml-auto flex shrink-0 items-center justify-end gap-1 @max-[480px]/service-list:[&>button]:size-9 @[820px]/service-list:row-start-1 @[820px]/service-list:ml-0 @[820px]/service-list:justify-center">
            {actions}
          </div>
        </div>
      </article>
    </DataRow>
  );
}
