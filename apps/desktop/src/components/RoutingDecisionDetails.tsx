import { useT } from "../i18n";
import type { RequestRoutingDecision } from "../request-record-model";
import { ConversationIndicator } from "./ConversationIndicator";

/**
 * Why routing used a record's API provider, and the higher-priority ones it
 * excluded before any attempt. Providers tried and rejected are the recovery
 * chain's and the route events' to show.
 */
export function RoutingDecisionDetails({
  value,
  serviceNames,
}: {
  value?: RequestRoutingDecision;
  serviceNames: Readonly<Record<string, string>>;
}) {
  const t = useT();
  if (!value || (!value.selected && value.skipped.length === 0)) return null;
  return (
    <dl className="grid gap-2 text-xs" data-testid="routing-decision">
      {value.selected ? (
        <div>
          <dt className="text-muted-foreground">
            {t("routingDecision.selected")}
          </dt>
          <dd data-selection={value.selected}>
            {value.selected === "session_binding" ? (
              <ConversationIndicator kind="stickiness" />
            ) : (
              t(`routingDecision.selections.${value.selected}`)
            )}
          </dd>
        </div>
      ) : null}
      {value.skipped.length > 0 ? (
        <div>
          <dt className="text-muted-foreground">
            {t("routingDecision.skipped")}
          </dt>
          <dd className="mt-0.5">
            <ol className="grid gap-0.5" data-testid="routing-skipped">
              {value.skipped.map((skip) => (
                <li
                  className="break-all"
                  data-reason={skip.reason}
                  key={skip.service_id}
                >
                  {serviceNames[skip.service_id] ?? skip.service_id}
                  <span className="text-muted-foreground">
                    {" · "}
                    {t(`routingDecision.skips.${skip.reason}`)}
                  </span>
                </li>
              ))}
            </ol>
          </dd>
        </div>
      ) : null}
    </dl>
  );
}
