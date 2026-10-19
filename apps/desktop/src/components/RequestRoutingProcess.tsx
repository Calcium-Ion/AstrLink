import { useEffect, useState } from "react";

import { getModelRoutingGroupVersion } from "../bridge";
import type { ClassifierDryRunField } from "../classifier-settings-model";
import type { ConversionEngineCapability } from "../core-model";
import { i18n, useT } from "../i18n";
import { isClassifierField } from "../model-routing-editor-model";
import { modelRoutingPublicModelID } from "../model-routing-group-model";
import type { ModelRoutingGroupVersion } from "../model-routing-version-model";
import {
  modelRoutingCodeLabel,
  modelRoutingFieldSkipLabel,
  sortedFieldObservations,
} from "../request-model-routing-model";
import type {
  ModelRoutingFieldObservation,
  RequestRecord,
} from "../request-record-model";
import { routingFieldName } from "../model-routing-phrase-text";
import {
  classifierValueTexts,
  memberModel,
  recordDimensions,
  recordRuleVerdicts,
  routingFactValueText,
  ruleNumber,
} from "../routing-process-model";
import {
  enableServiceEntry,
  serviceEntryTarget,
  serviceHasEntry,
} from "../service-entry-model";
import type { Service } from "../service-model";
import { protocolLabel } from "../service-presets";
import { ConversationIndicator } from "./ConversationIndicator";
import { FormMessage } from "./FormMessage";
import { HelpPopover } from "./HelpPopover";
import { ArrowUpRight, Ban, CircleCheck } from "./icons";
import { RoutingAnswerList, RoutingRuleList } from "./RoutingProcess";
import { Button } from "./ui/button";

/**
 * 一条请求的智能路由过程：先说结论（选了哪个模型、为什么），再列分类器的回答和
 * 每条规则的判定。画法和试运行一样；规则句子按请求当时的配置版本显示。
 */

/** 显示名称所需的 API 提供商信息；带入口协议时还能判断能否开启转换。 */
export interface RoutingProcessService {
  id: string;
  name: string;
  enabled?: boolean;
  capabilities?: Service["capabilities"];
}

const versions = new Map<string, Promise<ModelRoutingGroupVersion>>();

/** 一版配置读一次；记录很多时同一版只请求一次。 */
export function useModelRoutingGroupVersion(
  groupID: string,
  version: number | undefined,
): ModelRoutingGroupVersion | null {
  const [loaded, setLoaded] = useState<ModelRoutingGroupVersion | null>(null);
  useEffect(() => {
    setLoaded(null);
    if (!version) return;
    const key = `${groupID}@${version}`;
    let pending = versions.get(key);
    if (!pending) {
      pending = getModelRoutingGroupVersion(groupID, version);
      versions.set(key, pending);
      // A failed read is retried the next time the record opens.
      pending.catch(() => versions.delete(key));
    }
    let active = true;
    pending.then(
      (value) => active && setLoaded(value),
      () => undefined,
    );
    return () => {
      active = false;
    };
  }, [groupID, version]);
  return loaded;
}

function recordUnknownReason(field: ClassifierDryRunField): string {
  if (isClassifierField(field.field))
    return i18n.t(
      field.skip_reason === "dimension_not_produced"
        ? "dryRunTab.reasons.notProduced"
        : "dryRunTab.reasons.classifier",
    );
  return i18n.t("routingProcess.notInRequest");
}

/**
 * 升级之前的记录没有逐条判定，只有当时那条规则读到的字段：取到了没有、为什么没取到。
 * 保留这些依据，旧记录也能看出为什么落到了其他情况。
 */
function legacyFieldLine(field: ModelRoutingFieldObservation): string {
  let reason: string;
  if (!field.known) reason = modelRoutingFieldSkipLabel(field) ?? "";
  else if (field.field === "literal_keyword")
    reason =
      (field.match_count ?? 0) > 0
        ? i18n.t("routingProcess.keywordsFound", { count: field.match_count })
        : i18n.t("routingProcess.keywordsMissing");
  else if (field.value !== undefined)
    reason = routingFactValueText(field.field, field.value);
  else reason = i18n.t("routingProcess.fieldKnown");
  const line = i18n.t("dryRunTab.reasonLine", {
    field: routingFieldName(field.field),
    reason,
  });
  return field.error_code
    ? `${line}${i18n.t("dryRunTab.reasonSeparator")}${modelRoutingCodeLabel(field.error_code)}`
    : line;
}

function clock(value: string): string {
  return new Date(value).toLocaleTimeString(i18n.language, {
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function RequestRoutingProcess({
  record,
  services,
  conversionEngine,
  onEditGroup,
  onOpenRequest,
  onOpenService,
  onServicesChanged,
}: {
  record: RequestRecord;
  services: readonly RoutingProcessService[];
  conversionEngine?: ConversionEngineCapability | null;
  onEditGroup?: (groupID: string) => void;
  /** 打开另一条请求（沿用绑定时，指向当时做判定的那条）。 */
  onOpenRequest?: (requestID: string) => void;
  onOpenService?: (serviceID: string) => void;
  onServicesChanged?: () => void;
}) {
  const t = useT();
  const decision = record.model_routing;
  const configuration = useModelRoutingGroupVersion(
    decision?.group_id ?? "",
    decision?.config_version,
  );
  const [enabling, setEnabling] = useState(false);
  const [enableError, setEnableError] = useState<string | null>(null);
  if (!decision) return null;
  const group = configuration?.group ?? null;
  const service = services.find(
    (item) => item.id === decision.member_service_id,
  );
  const provider = service?.name ?? decision.member_service_id;
  const model = decision.member_model;
  const rules = recordRuleVerdicts(decision);
  const dimensions = recordDimensions(decision);
  const errorCode = record.error?.code;
  const unsupported = errorCode === "model_routing_member_unsupported";
  const unavailable = errorCode === "model_routing_member_unavailable";
  const reset = decision.binding_reset
    ? t("routingProcess.bindingReset", { model: decision.binding_reset.model })
    : "";

  let tone: "success" | "notice" | "error" = "success";
  let summary: string;
  if (unsupported || unavailable) {
    tone = "error";
    summary = t(
      unsupported ? "routingProcess.unsupported" : "routingProcess.unavailable",
      { model, provider, format: protocolLabel(record.input_protocol) },
    );
  } else if (decision.selection_source === "group_binding") {
    tone = "notice";
    summary = decision.binding_expires_at
      ? t("routingProcess.bindingReused", {
          model,
          provider,
          time: clock(decision.binding_expires_at),
        })
      : t("routingProcess.bindingReusedNoTime", { model, provider });
  } else if (decision.selection_source === "response_affinity") {
    tone = "notice";
    summary = t("routingProcess.affinityReused", { model, provider });
  } else if (decision.selection_source === "websocket_connection") {
    tone = "notice";
    summary = t("routingProcess.websocketReused", { model, provider });
  } else if (decision.used_default) {
    summary = t("routingProcess.chosenByDefault", { model, provider });
  } else {
    const index =
      ruleNumber(group, decision.rule_id) ??
      (rules.findIndex((rule) => rule.selected) + 1 || null);
    summary =
      index === null
        ? t("routingProcess.chosenByRuleId", {
            model,
            provider,
            rule: decision.rule_id ?? "",
          })
        : t("routingProcess.chosenByRule", { model, provider, index });
  }
  if (reset && tone !== "error") summary = `${summary}${reset}`;

  // A member that cannot take this format can often be fixed in one step.
  // Offered only while the entry is still missing: a record outlives the fix.
  const convertible =
    unsupported &&
    service?.enabled &&
    service.capabilities &&
    onServicesChanged &&
    !serviceHasEntry(
      { enabled: true, capabilities: service.capabilities },
      record.input_protocol,
    )
      ? Boolean(
          serviceEntryTarget(
            { enabled: true, capabilities: service.capabilities },
            record.input_protocol,
            conversionEngine,
          ),
        )
      : false;
  const enable = async () => {
    if (!service) return;
    setEnabling(true);
    setEnableError(null);
    try {
      await enableServiceEntry(
        service.id,
        record.input_protocol,
        conversionEngine,
      );
    } catch {
      setEnableError(t("routingFormats.enableFailed"));
    } finally {
      setEnabling(false);
      onServicesChanged?.();
    }
  };

  const judged = rules.length > 0;
  const legacy =
    !judged &&
    (decision.selection_source === "rule" ||
      decision.selection_source === "default_member");
  const fallbackModel = group
    ? memberModel(group, group.default_member_id)
    : decision.used_default
      ? model
      : "";
  return (
    <section className="grid gap-2.5 text-xs" data-request-routing-process>
      <div className="flex min-w-0 items-center justify-between gap-2">
        <h3 className="flex min-w-0 items-center gap-1 text-xs font-medium text-text-secondary">
          <span className="truncate">
            {t("routingProcess.title")} ·{" "}
            <span className="font-mono">
              {modelRoutingPublicModelID(decision.group_id)}
            </span>
          </span>
          {legacy ? (
            <HelpPopover label={t("routingProcess.legacyHelpLabel")}>
              {t("routingProcess.legacyHelp")}
            </HelpPopover>
          ) : null}
        </h3>
        {onEditGroup ? (
          <Button
            type="button"
            variant="link"
            size="sm"
            className="h-auto shrink-0 gap-1 px-0 py-0 text-xs text-primary"
            onClick={() => onEditGroup(decision.group_id)}
          >
            {t("dryRunTab.editRules")}
            <ArrowUpRight aria-hidden="true" className="size-3" />
          </Button>
        ) : null}
      </div>
      <FormMessage
        tone={tone}
        asChild
        data-request-routing-summary={
          tone === "error" ? "failed" : decision.selection_source
        }
      >
        <div className="grid gap-2">
          <span className="flex items-start gap-2">
            {tone === "error" ? (
              <Ban aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
            ) : tone === "notice" ? (
              <ConversationIndicator kind="stickiness" />
            ) : (
              <CircleCheck
                aria-hidden="true"
                className="mt-0.5 size-3.5 shrink-0"
              />
            )}
            <span className="min-w-0 flex-1">{summary}</span>
            {decision.selection_source === "group_binding" &&
            tone !== "error" ? (
              <HelpPopover label={t("routingProcess.bindingHelpLabel")}>
                {t("routingProcess.bindingHelp")}
              </HelpPopover>
            ) : null}
          </span>
          {unsupported && (convertible || onOpenService) ? (
            <span className="flex flex-wrap items-center gap-2">
              {convertible ? (
                <Button
                  type="button"
                  size="sm"
                  disabled={enabling}
                  aria-busy={enabling}
                  onClick={() => void enable()}
                  data-request-routing-enable
                >
                  {t("routingFormats.enableOne", { service: provider })}
                </Button>
              ) : null}
              {onOpenService ? (
                <Button
                  type="button"
                  variant="link"
                  size="sm"
                  className="h-auto gap-1 px-0 py-0 text-xs"
                  onClick={() => onOpenService(decision.member_service_id)}
                >
                  {t("routingFormats.openService")}
                  <ArrowUpRight aria-hidden="true" className="size-3" />
                </Button>
              ) : null}
            </span>
          ) : null}
          {enableError ? <span role="alert">{enableError}</span> : null}
        </div>
      </FormMessage>
      {decision.selection_source === "group_binding" &&
      decision.binding_request_id &&
      onOpenRequest ? (
        <Button
          type="button"
          variant="link"
          size="sm"
          className="h-auto justify-self-start gap-1 px-0 py-0 text-xs"
          onClick={() => onOpenRequest(decision.binding_request_id ?? "")}
        >
          {t("routingProcess.viewOrigin")}
          <ArrowUpRight aria-hidden="true" className="size-3" />
        </Button>
      ) : null}
      {decision.classifier ? (
        <div className="grid gap-2" data-request-routing-answers>
          <span className="text-text-secondary">
            {t("dryRunTab.classifierTitle")}
            {decision.classifier_duration_ms !== undefined ? (
              <span className="text-muted-foreground">
                {" · "}
                {t("routingProcess.classifierDuration", {
                  ms: decision.classifier_duration_ms,
                })}
              </span>
            ) : null}
          </span>
          <RoutingAnswerList
            taskType={decision.classifier.task_type ?? ""}
            confidence={decision.classifier.confidence ?? null}
            lowConfidence={
              decision.fallback_reason === "classifier_low_confidence"
            }
            dimensions={dimensions}
          />
        </div>
      ) : null}
      {judged ? (
        <div className="grid gap-2" data-request-routing-rules>
          <span className="text-text-secondary">
            {t("dryRunTab.rulesTitle")}
          </span>
          <RoutingRuleList
            group={group}
            rules={rules}
            usedDefault={decision.used_default}
            fallbackModel={fallbackModel}
            classifierValues={classifierValueTexts(
              decision.classifier?.task_type ?? "",
              decision.classifier?.confidence ?? null,
              dimensions,
            )}
            unknownReason={recordUnknownReason}
            factLines
          />
        </div>
      ) : null}
      {legacy && decision.fields.length > 0 ? (
        <div className="grid gap-1.5" data-request-routing-fields>
          <span className="text-text-secondary">
            {t("routingProcess.fieldsTitle")}
          </span>
          <ul className="m-0 grid list-none gap-1 p-0">
            {sortedFieldObservations(decision).map((field) => (
              <li
                key={field.field}
                className="text-micro text-muted-foreground"
                data-request-routing-field={field.field}
              >
                {legacyFieldLine(field)}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </section>
  );
}
