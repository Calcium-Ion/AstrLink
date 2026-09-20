import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "@/components/ui/dialog";
import { Panel } from "@/components/Panel";
import { DataRow } from "@/components/DataRow";
import { EmptyState } from "@/components/EmptyState";
import { useT } from "./i18n";
import type { Service } from "./service-model";
import { getServiceBilling } from "./pricing-bridge";
import {
  billingAmount,
  currentBillingPeriod,
  OFFICIAL_PROVIDERS,
  type BillingAmounts,
  type BillingPeriod,
  type ServiceBilling,
} from "./pricing-model";

function periodLabel(p: BillingPeriod, t: ReturnType<typeof useT>) {
  const stamp = (value: string) =>
    new Date(value).toLocaleString(undefined, {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  const kind =
    p.kind === "month"
      ? t("pricing.month")
      : p.kind === "secondary"
        ? t("pricing.secondary")
        : t("pricing.primary");
  return `${kind} · ${stamp(p.start)} – ${stamp(p.end)}`;
}

export function BillingNote({
  amounts,
  partial = false,
}: {
  amounts: BillingAmounts;
  partial?: boolean;
}) {
  const t = useT();
  const notes = [
    amounts.unpriced > 0
      ? t("pricing.unpricedCount", { count: amounts.unpriced })
      : "",
    amounts.pending > 0
      ? t("pricing.pendingCount", { count: amounts.pending })
      : "",
    partial ? t("pricing.partial") : "",
  ].filter(Boolean);
  return notes.length ? (
    <span className="text-xs text-muted-foreground">{notes.join(" · ")}</span>
  ) : null;
}

function useBillingReport(
  serviceId: string | undefined,
  ready: boolean,
  revision = "",
) {
  const [report, setReport] = useState<ServiceBilling | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  useEffect(() => {
    let cancelled = false;
    let running = false;
    setReport(null);
    setError(false);
    setLoading(!!serviceId && ready);
    if (!serviceId || !ready) return;
    const refresh = async () => {
      if (running || cancelled) return;
      running = true;
      try {
        const value = await getServiceBilling(serviceId);
        if (!cancelled) {
          setReport(value);
          setError(false);
        }
      } catch {
        if (!cancelled) {
          setReport(null);
          setError(true);
        }
      } finally {
        running = false;
        if (!cancelled) setLoading(false);
      }
    };
    void refresh();
    const timer = window.setInterval(() => {
      if (!document.hidden) void refresh();
    }, 60_000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [serviceId, ready, revision]);
  return { report, loading, error };
}

export function ServiceBillingMeter({
  serviceId,
  ready,
  epoch,
  observedAt,
  onOpen,
}: {
  serviceId: string;
  ready: boolean;
  epoch: number;
  observedAt?: string;
  onOpen: () => void;
}) {
  const t = useT();
  const { report } = useBillingReport(
    serviceId,
    ready,
    `${epoch}/${observedAt ?? ""}`,
  );
  const period = currentBillingPeriod(report?.periods ?? []);
  return (
    <Button
      size="xs"
      variant="ghost"
      className="h-auto max-w-full justify-start whitespace-normal px-0 text-left text-xs tabular-nums"
      disabled={!ready}
      onClick={onOpen}
      title={t("pricing.description")}
    >
      {t(
        period?.kind === "month"
          ? "pricing.monthAmount"
          : "pricing.cycleAmount",
      )}{" "}
      {billingAmount(period?.summary)}
      {period && period.summary.unpriced > 0
        ? ` · ${t("pricing.incomplete")}`
        : ""}
    </Button>
  );
}

export function PricingWorkspace({
  services,
  initialServiceId,
  onClose,
}: {
  services: Service[];
  initialServiceId?: string;
  onClose: () => void;
}) {
  const t = useT();
  const service =
    services.find((s) => s.id === initialServiceId) ?? services[0];
  const serviceId = service?.id;
  const [selected, setSelected] = useState("");
  const { report, loading, error } = useBillingReport(serviceId, true);
  const period =
    report?.periods.find((p) => p.id === selected) ??
    currentBillingPeriod(report?.periods ?? []) ??
    report?.periods[0];
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="flex h-[calc(100dvh-4rem)] max-h-[calc(100dvh-4rem)] min-h-0 flex-col gap-3 overflow-hidden p-4 sm:max-w-2xl">
        <DialogTitle className="truncate pr-8 text-base">
          {service?.name} · {t("pricing.title")}
        </DialogTitle>
        <DialogDescription className="sr-only">
          {t("pricing.description")}
        </DialogDescription>
        <Panel
          className="flex min-h-0 flex-1 flex-col"
          data-testid="pricing-primary-region"
        >
          {period ? (
            <div className="shrink-0 border-b p-3">
              <Select value={period.id} onValueChange={setSelected}>
                <SelectTrigger
                  aria-label={t("pricing.period")}
                  className="w-full min-w-0"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {report?.periods.map((p) => (
                    <SelectItem key={p.id} value={p.id}>
                      {periodLabel(p, t)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          ) : null}
          <div
            className="min-h-0 flex-1 overflow-y-auto"
            data-testid="pricing-period-scroller"
          >
            {loading ? (
              <EmptyState title={t("common.loading")} />
            ) : error ? (
              <EmptyState title={t("pricing.unavailable")} />
            ) : !period ? (
              <EmptyState title={t("pricing.empty")} />
            ) : (
              <>
                <div className="grid gap-1 border-b p-4">
                  <div className="flex flex-wrap items-baseline justify-between gap-2">
                    <span
                      className="text-xs text-muted-foreground"
                      title={t("pricing.description")}
                    >
                      {t("pricing.officialAmount")}
                    </span>
                    <strong className="text-2xl tabular-nums">
                      {billingAmount(period.summary)}
                    </strong>
                  </div>
                  <BillingNote
                    amounts={period.summary}
                    partial={period.coverage === "partial"}
                  />
                </div>
                {period.summary.by_model.length === 0 ? (
                  <EmptyState title={t("pricing.empty")} />
                ) : (
                  period.summary.by_model.map((m) => (
                    <DataRow
                      key={`${m.provider}/${m.model}`}
                      className="flex items-center justify-between gap-3"
                    >
                      <div className="grid min-w-0 gap-0.5">
                        <strong
                          className="block truncate text-sm"
                          title={m.model}
                        >
                          {m.model}
                        </strong>
                        {OFFICIAL_PROVIDERS[m.provider] ? (
                          <span className="text-xs text-muted-foreground">
                            {OFFICIAL_PROVIDERS[m.provider]}
                          </span>
                        ) : null}
                        <BillingNote amounts={m} />
                      </div>
                      <strong className="shrink-0 text-sm tabular-nums">
                        {billingAmount(m)}
                      </strong>
                    </DataRow>
                  ))
                )}
              </>
            )}
          </div>
        </Panel>
      </DialogContent>
    </Dialog>
  );
}
