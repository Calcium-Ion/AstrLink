import { useRef } from "react";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { DataRow } from "@/components/DataRow";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { useResetCredits } from "./use-reset-credits";
import { i18n, useT } from "./i18n";
import { formatResetCreditExpiry } from "./subscription-usage-model";

export function SubscriptionResetDialog({
  serviceId,
  serviceName,
  onCancel,
  onConfirm,
}: {
  serviceId: string;
  serviceName: string;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const t = useT();
  const { state, now, retry } = useResetCredits(serviceId);
  const confirmed = useRef(false);
  const details = state.status === "ready" ? state.details : null;
  const credits = (details?.credits ?? [])
    .filter(
      (credit) => !credit.expires_at || Date.parse(credit.expires_at) > now,
    )
    .sort(
      (a, b) =>
        (a.expires_at ? Date.parse(a.expires_at) : Infinity) -
        (b.expires_at ? Date.parse(b.expires_at) : Infinity),
    );
  const count = Math.max(
    0,
    (details?.available_count ?? 0) -
      ((details?.credits.length ?? 0) - credits.length),
  );
  const dateFormat = new Intl.DateTimeFormat(i18n.language, {
    dateStyle: "medium",
    timeStyle: "short",
  });
  return (
    <ConfirmDialog
      open
      title={t("services.confirmReset")}
      confirmLabel={t("services.reset")}
      confirmDisabled={state.status !== "ready" || count === 0}
      onCancel={onCancel}
      onConfirm={() => {
        if (confirmed.current) return;
        const expired =
          details?.credits.filter(
            (credit) =>
              credit.expires_at && Date.parse(credit.expires_at) <= Date.now(),
          ).length ?? 0;
        if (
          state.status === "ready" &&
          details &&
          details.available_count > expired
        ) {
          confirmed.current = true;
          onConfirm();
        } else retry();
      }}
      description={
        <div className="space-y-3 text-left">
          {state.status === "loading" ? (
            <p role="status">{t("services.resetDetailsLoading")}</p>
          ) : state.status === "error" ? (
            <div className="space-y-2">
              <p role="alert">{t("services.resetDetailsError")}</p>
              <Button variant="outline" size="sm" onClick={retry}>
                {t("common.retry")}
              </Button>
            </div>
          ) : count === 0 ? (
            <p role="status">{t("services.resetDetailsEmpty")}</p>
          ) : (
            <>
              <p>{t("services.resetBody", { name: serviceName, count })}</p>
              <ol
                aria-label={t("services.resetDetailsList")}
                className="max-h-[min(18rem,45dvh)] overflow-y-auto rounded-md border"
              >
                {credits.slice(0, count).map((credit, index) => (
                  <DataRow
                    key={index}
                    asChild
                    className="flex-wrap justify-between gap-y-1"
                  >
                    <li>
                      <div className="flex items-center gap-2 text-foreground">
                        <span>
                          {t("services.resetCreditNumber", {
                            number: index + 1,
                          })}
                        </span>
                        {index === 0 && credit.expires_at ? (
                          <Badge variant="secondary">
                            {t("services.resetEarliestExpiry")}
                          </Badge>
                        ) : null}
                      </div>
                      <div className="min-w-0 text-right">
                        {credit.expires_at ? (
                          <>
                            <time
                              dateTime={credit.expires_at}
                              className="block text-foreground tabular-nums"
                            >
                              {dateFormat.format(new Date(credit.expires_at))}
                            </time>
                            <span>
                              {t("services.resetExpiresIn", {
                                time: formatResetCreditExpiry(
                                  Date.parse(credit.expires_at),
                                  now,
                                ),
                              })}
                            </span>
                          </>
                        ) : (
                          <span>{t("services.resetExpiryUnknown")}</span>
                        )}
                      </div>
                    </li>
                  </DataRow>
                ))}
              </ol>
              {count > credits.length ? (
                <p>
                  {t("services.resetDetailsMissing", {
                    count: count - credits.length,
                  })}
                </p>
              ) : null}
              <p className="text-muted-foreground">
                {t("services.resetExpiryTimezone", {
                  zone: dateFormat.resolvedOptions().timeZone,
                })}
              </p>
            </>
          )}
        </div>
      }
    />
  );
}
