import { ArrowRight } from "@/components/icons";
import { useT } from "../i18n";
import {
  statusLabel,
  type RequestModelRedirect,
  type RequestRecord,
  type RequestRecovery,
} from "../request-record-model";

export function RecoveryDetails({
  value,
  modelRedirect,
}: {
  value?: RequestRecovery;
  /** Shown under the upstream model so a renamed model is not a mystery. */
  modelRedirect?: RequestModelRedirect;
}) {
  const t = useT();
  if (!value) return null;
  return (
    <dl className="grid gap-2 text-xs">
      {value.path_name ? (
        <div>
          <dt className="text-muted-foreground">{t("paths.title")}</dt>
          <dd>{value.path_name}</dd>
          <dd className="break-all text-muted-foreground">
            {value.path_version} · {value.step_id}
          </dd>
        </div>
      ) : null}
      {value.upstream_model ? (
        <div>
          <dt className="text-muted-foreground">
            {t("failure.upstreamModel")}
          </dt>
          <dd className="break-all">{value.upstream_model}</dd>
          {modelRedirect ? (
            <dd
              className="break-all text-muted-foreground"
              data-testid="recovery-model-redirect"
            >
              {t("records.modelRedirectNote", {
                from: modelRedirect.from,
                to: modelRedirect.to,
              })}
            </dd>
          ) : null}
        </div>
      ) : null}
      {value.action ? (
        <div>
          <dt className="text-muted-foreground">{t("failure.order")}</dt>
          <dd>
            {t(`failure.${value.action}`)}
            {value.delay_ms > 0
              ? ` · ${t("failure.wait", { count: value.delay_ms })}`
              : ""}
          </dd>
        </div>
      ) : null}
      {value.reason ? (
        <div>
          <dt className="text-muted-foreground">{t("failure.reason")}</dt>
          <dd>
            {value.reason.startsWith("http_")
              ? t("failure.httpCode", { code: value.reason.slice(5) })
              : value.reason === "upstream_timeout"
                ? t("failure.responseTimeout")
                : value.reason === "upstream_unavailable"
                  ? t("failure.networkError")
                  : value.reason === "thinking_signature_repair"
                    ? t("failure.thinkingSignatureRepair")
                    : value.reason === "openai_reasoning_repair"
                      ? t("failure.openaiReasoningRepair")
                      : value.reason === "openai_function_output_repair"
                        ? t("failure.openaiFunctionOutputRepair")
                        : value.reason}
          </dd>
        </div>
      ) : null}
      {value.stop_reason ? (
        <div>
          <dt className="text-muted-foreground">{t("failure.stopped")}</dt>
          <dd>
            {t(`failure.stops.${value.stop_reason}`, {
              defaultValue: value.stop_reason,
            })}
          </dd>
        </div>
      ) : null}
    </dl>
  );
}

export function RecoveryChain({
  records,
  serviceNames,
}: {
  records: RequestRecord[];
  serviceNames: Record<string, string>;
}) {
  const t = useT();
  const attempts = [...records]
    .filter((record) => record.attempt_index > 0)
    .sort((a, b) => a.attempt_index - b.attempt_index);
  if (attempts.length < 2) return null;
  return (
    <ol
      className="mb-3 flex flex-wrap gap-2 text-xs"
      aria-label={t("failure.details")}
    >
      {attempts.map((record, index) => {
        const action =
          index > 0 ? attempts[index - 1].recovery?.action : undefined;
        const service = record.service_id
          ? (serviceNames[record.service_id] ?? record.service_id)
          : t("records.selectingService");
        return (
          <li key={record.id} className="flex min-w-0 items-center gap-2">
            {index > 0 ? <ArrowRight className="size-3.5" /> : null}
            <span>
              {action ? `${t(`failure.chain.${action}`)} ` : ""}
              {service}
              {record.recovery?.upstream_model
                ? ` / ${record.recovery.upstream_model}`
                : ""}{" "}
              · {statusLabel(record.status)}
            </span>
          </li>
        );
      })}
    </ol>
  );
}
