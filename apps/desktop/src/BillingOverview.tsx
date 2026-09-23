import { billingAmount, type BillingSummary } from "./pricing-model";
import { useT } from "./i18n";
import { BillingNote } from "./PricingWorkspace";

export function BillingOverview({
  loading = false,
  summary,
}: {
  loading?: boolean;
  summary: BillingSummary | null;
}) {
  const t = useT();
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
          {loading ? t("common.loading") : billingAmount(summary ?? undefined)}
        </strong>
        {summary ? <BillingNote amounts={summary} /> : null}
      </div>
    </div>
  );
}
