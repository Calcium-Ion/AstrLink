import { useEffect, useId, useState } from "react";
import { useT } from "../i18n";
import type { ServicePerformance, UsageStatus } from "../usage-range";
import type { PerformanceTarget } from "../use-performance-details";
import { UsagePerformanceDetails } from "./UsagePerformanceDetails";
import { Activity } from "./icons";
import { DataField } from "./DataRow";
import { Button } from "./ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";
import { cn } from "@/lib/utils";

export function UsagePerformanceMeter({
  performance,
  status,
  periodLabel,
  scopeDescription,
  target,
  ready,
  testId = "usage-performance",
  layout = "stacked",
}: {
  performance?: ServicePerformance;
  status: UsageStatus;
  periodLabel: string;
  scopeDescription: string;
  target: PerformanceTarget;
  ready: boolean;
  testId?: string;
  layout?: "stacked" | "fields" | "service";
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (!ready) setOpen(false);
  }, [ready]);
  const titleId = useId();
  const cache = performance?.cache_hit_rate;
  const speed = performance?.output_tokens_per_second;
  const placeholder = status === "loading" ? "…" : "—";
  const details = (
    <Popover open={open && ready} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          aria-label={t("services.performanceDetailsFor", {
            name: target.name,
          })}
          className={layout !== "fields" ? "justify-end" : undefined}
          disabled={!ready}
          size="icon-xs"
          variant="ghost"
          type="button"
        >
          <Activity aria-hidden="true" className="text-muted-foreground" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-96" aria-labelledby={titleId}>
        <UsagePerformanceDetails
          target={target}
          open={open && ready}
          scopeDescription={scopeDescription}
          titleId={titleId}
        />
      </PopoverContent>
    </Popover>
  );
  if (layout === "fields") {
    return (
      <div
        className={cn(
          "col-span-2 row-span-2 grid grid-cols-2 grid-rows-subgrid gap-x-4 gap-y-1 tabular-nums",
          status === "error" && "row-span-3",
        )}
        data-testid={testId}
      >
        <DataField
          className="row-span-2 grid grid-rows-subgrid"
          label={t("services.cacheUtilization")}
          value={
            <span className="font-semibold">
              {cache == null ? placeholder : `${(cache * 100).toFixed(1)}%`}
            </span>
          }
        />
        <DataField
          className="row-span-2 grid grid-rows-subgrid"
          label="TPS"
          value={
            <span className="inline-flex items-center gap-1 font-semibold">
              {speed == null ? placeholder : speed.toFixed(1)}
              {details}
            </span>
          }
        />
        {status === "error" ? (
          <p className="col-span-2 text-micro text-muted-foreground">
            {t("services.performanceError")}
          </p>
        ) : null}
      </div>
    );
  }
  return (
    <div
      className={cn(
        "grid gap-1 text-xs tabular-nums",
        layout === "service" &&
          "flex flex-wrap items-center gap-x-3 @[820px]/service-list:grid @[820px]/service-list:gap-x-1",
      )}
      data-testid={testId}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-muted-foreground">
          {t("services.cacheUtilization")}
        </span>
        <span>
          {cache == null ? placeholder : `${(cache * 100).toFixed(1)}%`}
        </span>
      </div>
      <div className="flex items-center justify-between gap-2">
        <span className="text-muted-foreground">TPS</span>
        <span>{speed == null ? placeholder : speed.toFixed(1)}</span>
      </div>
      <div
        className={cn(
          "flex items-center justify-between gap-2 text-micro text-muted-foreground",
          layout === "service" && "ml-auto @[820px]/service-list:ml-0",
        )}
      >
        <span
          className={cn(
            layout === "service" &&
              status !== "error" &&
              "sr-only @[820px]/service-list:not-sr-only",
          )}
        >
          {status === "error" ? t("services.performanceError") : periodLabel}
        </span>
        {details}
      </div>
    </div>
  );
}
