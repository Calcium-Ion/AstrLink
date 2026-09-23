import { useEffect, useState } from "react";
import { ValueTransition } from "./components/ValueTransition";
import { useWorkspaceSnapshot } from "./workspace-snapshots";
import { getBillingSummary } from "./pricing-bridge";
import { billingAmount, type BillingSummary } from "./pricing-model";
import { useT } from "./i18n";
import { BillingNote } from "./PricingWorkspace";

export type BillingStatus = "idle" | "loading" | "ready" | "error";

/** Loads the range total; a retained summary stays visible while it refreshes. */
export function useBillingSummary({
  from,
  to,
  ready,
  revision,
}: {
  from?: string;
  to?: string;
  ready: boolean;
  revision: unknown;
}): { status: BillingStatus; summary: BillingSummary | null } {
  const [summary, setSummary] = useWorkspaceSnapshot<BillingSummary | null>(
    `billing-summary:${from ?? ""}:${to ?? ""}`,
    null,
  );
  const [status, setStatus] = useState<BillingStatus>("idle");
  useEffect(() => {
    let cancelled = false;
    if (!ready || !from || !to) {
      setSummary(null);
      setStatus("idle");
    } else {
      setStatus("loading");
      void getBillingSummary(from, to)
        .then((next) => {
          if (cancelled) return;
          setSummary(next);
          setStatus("ready");
        })
        .catch(() => {
          if (cancelled) return;
          setSummary(null);
          setStatus("error");
        });
    }
    return () => {
      cancelled = true;
    };
  }, [from, to, ready, revision, setSummary]);
  return { status: summary ? "ready" : status, summary };
}

export function BillingOverview({
  loading = false,
  summary,
}: {
  loading?: boolean;
  summary: BillingSummary | null;
}) {
  const t = useT();
  const amount = loading
    ? t("common.loading")
    : billingAmount(summary ?? undefined);
  return (
    <div
      className="flex flex-wrap items-baseline justify-between gap-2 border-t bg-muted/30 px-4 py-2"
      data-testid="billing-overview"
    >
      <span className="text-xs text-muted-foreground">
        {t("pricing.officialAmount")}
      </span>
      <ValueTransition
        valueKey={`${amount}/${summary?.unpriced ?? 0}/${summary?.pending ?? 0}`}
        className="flex flex-wrap items-baseline gap-3"
      >
        <strong className="text-sm tabular-nums">{amount}</strong>
        {summary ? <BillingNote amounts={summary} /> : null}
      </ValueTransition>
    </div>
  );
}
