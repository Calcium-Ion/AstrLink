import { useEffect, useState } from "react";
import { getBillingSummary } from "./pricing-bridge";
import { billingAmount, type BillingSummary } from "./pricing-model";
import { useT } from "./i18n";
import { BillingNote } from "./PricingWorkspace";

export function BillingOverview({
  from,
  to,
  ready,
  revision,
}: {
  from?: string;
  to?: string;
  ready: boolean;
  revision: unknown;
}) {
  const t = useT();
  const [summary, setSummary] = useState<BillingSummary | null>(null);
  useEffect(() => {
    let cancelled = false;
    setSummary(null);
    if (ready && from && to)
      void getBillingSummary(from, to)
        .then((s) => {
          if (!cancelled) setSummary(s);
        })
        .catch(() => {
          if (!cancelled) setSummary(null);
        });
    return () => {
      cancelled = true;
    };
  }, [from, to, ready, revision]);
  return (
    <div
      className="flex flex-wrap items-baseline justify-between gap-2 border-t bg-muted/30 px-4 py-2"
      data-testid="billing-overview"
    >
      <span className="text-xs text-muted-foreground">
        {t("pricing.officialAmount")}
      </span>
      <div className="flex flex-wrap items-baseline gap-3">
        <strong className="text-sm tabular-nums">
          {billingAmount(summary ?? undefined)}
        </strong>
        {summary ? <BillingNote amounts={summary} /> : null}
      </div>
    </div>
  );
}
