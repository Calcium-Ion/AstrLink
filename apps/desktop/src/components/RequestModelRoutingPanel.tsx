import { useState } from "react";

import { DataField } from "@/components/DataRow";
import { Route } from "@/components/icons";
import { EmptyState } from "@/components/EmptyState";
import { Panel, PanelHeader } from "@/components/Panel";
import { ServiceKindIcon } from "@/components/ServiceKindIcon";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { ConversionEngineCapability } from "@/core-model";
import { i18n, useT } from "@/i18n";
import {
  modelRoutingCodeLabel,
  modelRoutingExecutionStateLabel,
  modelRoutingPrivacyDecisionLabel,
} from "@/request-model-routing-model";
import type {
  ModelRoutingDecision,
  RequestRecord,
} from "@/request-record-model";
import type { Service } from "@/service-model";
import { cn } from "@/lib/utils";
import {
  RequestRoutingProcess,
  useModelRoutingGroupVersion,
} from "./RequestRoutingProcess";
import { RoutingVersionDialog } from "./RoutingVersionDialog";

type RoutingService = Pick<
  Service,
  "id" | "name" | "kind" | "enabled" | "capabilities"
>;

/**
 * 请求详情「模型路由」页签。左边是和「路由」页签同一个路由过程；右边只留排查用得上的
 * 数据：配置版本、模型和 API 提供商、耗时和隐私检查，都显示名称而不是 ID。
 * 面板不展示 prompt、媒体 URL、命中关键词、classifier 原文或任何 credential ——
 * 那些内容不在 decision payload 里。
 */
export function RequestModelRoutingPanel({
  record,
  services = [],
  conversionEngine,
  onOpenProviderRouting,
  onEditGroup,
  onOpenRequest,
  onOpenService,
  onServicesChanged,
}: {
  record: RequestRecord;
  services?: readonly RoutingService[];
  conversionEngine?: ConversionEngineCapability | null;
  /** 跳到调用轨迹，API 提供商的排序、跳过与故障转移在那里。 */
  onOpenProviderRouting?: () => void;
  onEditGroup?: (groupID: string) => void;
  onOpenRequest?: (requestID: string) => void;
  onOpenService?: (serviceID: string) => void;
  onServicesChanged?: () => void;
}) {
  const t = useT();
  const decision = record.model_routing;
  if (!decision) {
    return (
      <div className="gutter-scroller min-h-0 flex-1 overflow-y-auto py-4">
        <EmptyState title={t("modelRouting.empty")} />
      </div>
    );
  }
  return (
    <div
      className="gutter-scroller grid min-h-0 flex-1 auto-rows-max content-start items-start gap-3 overflow-y-auto py-4 @[720px]:grid-cols-[minmax(0,1.25fr)_minmax(0,1fr)]"
      data-testid="model-routing-panel"
    >
      <Panel className="p-3">
        <RequestRoutingProcess
          record={record}
          services={services}
          conversionEngine={conversionEngine}
          onEditGroup={onEditGroup}
          onOpenRequest={onOpenRequest}
          onOpenService={onOpenService}
          onServicesChanged={onServicesChanged}
        />
      </Panel>
      <div className="grid min-w-0 gap-3">
        <ConfigurationPanel
          decision={decision}
          services={services}
          onOpenProviderRouting={onOpenProviderRouting}
        />
        <PrivacyPanel decision={decision} services={services} />
      </div>
    </div>
  );
}

function serviceName(services: readonly RoutingService[], id: string): string {
  return (
    services.find((service) => service.id === id)?.name ??
    i18n.t("binding.removedService")
  );
}

/** 配置版本、模型、API 提供商、状态和耗时；调用轨迹的入口放在标题右边。 */
function ConfigurationPanel({
  decision,
  services,
  onOpenProviderRouting,
}: {
  decision: ModelRoutingDecision;
  services: readonly RoutingService[];
  onOpenProviderRouting?: () => void;
}) {
  const t = useT();
  const [versionOpen, setVersionOpen] = useState(false);
  const version = useModelRoutingGroupVersion(
    decision.group_id,
    decision.config_version,
  );
  const state = decision.execution.status;
  const service = services.find(
    (item) => item.id === decision.member_service_id,
  );
  return (
    <Panel>
      <PanelHeader
        size="sm"
        actions={
          onOpenProviderRouting ? (
            <Button
              type="button"
              variant="ghost"
              size="xs"
              onClick={onOpenProviderRouting}
            >
              <Route aria-hidden="true" />
              {t("modelRouting.providerLink")}
            </Button>
          ) : undefined
        }
      >
        <h2 className="text-sm font-semibold">
          {t("modelRouting.config.title")}
        </h2>
      </PanelHeader>
      <dl className="grid min-w-0 grid-cols-2 gap-3 p-3">
        <DataField
          label={t("modelRouting.config.version")}
          value={
            decision.config_version ? (
              <Button
                type="button"
                variant="link"
                size="sm"
                className="h-auto justify-start px-0 py-0 text-xs underline underline-offset-3"
                onClick={() => setVersionOpen(true)}
                data-routing-config-version={decision.config_version}
              >
                {[
                  t("routingProcess.versionLabel", {
                    version: decision.config_version,
                  }),
                  version
                    ? new Date(version.created_at).toLocaleString(
                        i18n.language,
                        {
                          month: "short",
                          day: "numeric",
                          hour: "2-digit",
                          minute: "2-digit",
                        },
                      )
                    : null,
                ]
                  .filter(Boolean)
                  .join(" · ")}
              </Button>
            ) : (
              t("routingProcess.versionLegacy")
            )
          }
        />
        <DataField
          label={t("modelRouting.execution.stateTitle")}
          value={
            <span
              className={cn(
                state === "failed" && "text-danger-foreground",
                state === "succeeded" && "text-foreground",
              )}
            >
              {modelRoutingExecutionStateLabel(state)}
            </span>
          }
        />
        <DataField
          label={t("modelRouting.config.model")}
          value={decision.member_model}
        />
        <DataField
          label={t("modelRouting.execution.attemptsTitle")}
          value={t("modelRouting.config.attempts", {
            count: decision.execution.attempt_count,
          })}
        />
        <DataField
          label={t("modelRouting.config.provider")}
          value={
            <span
              className="flex min-w-0 items-center gap-1.5"
              title={serviceName(services, decision.member_service_id)}
            >
              {service ? (
                <ServiceKindIcon kind={service.kind} size={14} />
              ) : null}
              <span className="min-w-0 truncate">
                {serviceName(services, decision.member_service_id)}
              </span>
            </span>
          }
        />
        <DataField
          label={t("modelRouting.execution.durationTitle")}
          value={t("modelRouting.config.ms", { ms: decision.duration_ms })}
        />
        {decision.classifier_duration_ms === undefined ? null : (
          <DataField
            label={t("modelRouting.execution.classifierTitle")}
            value={t("modelRouting.config.ms", {
              ms: decision.classifier_duration_ms,
            })}
          />
        )}
        {decision.fallback_reason ? (
          <DataField
            label={t("modelRouting.fallback")}
            value={modelRoutingCodeLabel(decision.fallback_reason)}
          />
        ) : null}
        {decision.error_code ? (
          <DataField
            label={t("modelRouting.error")}
            value={modelRoutingCodeLabel(decision.error_code)}
          />
        ) : null}
      </dl>
      {decision.config_version ? (
        <RoutingVersionDialog
          groupID={decision.group_id}
          version={decision.config_version}
          open={versionOpen}
          onOpenChange={setVersionOpen}
          services={services}
        />
      ) : null}
    </Panel>
  );
}

/** 每次具体上游尝试前的隐私检查；任何一次被拒绝都在发送前停止。 */
function PrivacyPanel({
  decision,
  services,
}: {
  decision: ModelRoutingDecision;
  services: readonly RoutingService[];
}) {
  const t = useT();
  return (
    <Panel>
      <PanelHeader size="sm">
        <h2 className="text-sm font-semibold">
          {t("modelRouting.privacy.title")}
        </h2>
      </PanelHeader>
      {decision.privacy_attempts.length === 0 ? (
        <p className="p-3 text-xs text-muted-foreground">
          {t("modelRouting.privacy.noAttempts")}
        </p>
      ) : (
        <Table containerClassName="min-h-0 overflow-x-auto">
          <TableHeader>
            <TableRow>
              <TableHead>{t("modelRouting.privacy.attemptColumn")}</TableHead>
              <TableHead>{t("modelRouting.config.provider")}</TableHead>
              <TableHead>{t("modelRouting.privacy.decisionColumn")}</TableHead>
              <TableHead>{t("modelRouting.privacy.findingsColumn")}</TableHead>
              <TableHead>{t("modelRouting.privacy.durationColumn")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {/* A provider rejected before it was tried leaves the next
                attempt's number to the following check, so numbers can repeat. */}
            {decision.privacy_attempts.map((attempt, position) => (
              <TableRow key={`${attempt.attempt_index}-${position}`}>
                <TableCell className="text-xs tabular-nums">
                  {t("modelRouting.privacy.attempt", {
                    index: attempt.attempt_index,
                  })}
                </TableCell>
                <TableCell className="text-xs">
                  {serviceName(services, attempt.service_id)}
                </TableCell>
                <TableCell>
                  <Badge
                    variant={
                      attempt.decision === "reject"
                        ? "destructive"
                        : attempt.decision === "allow"
                          ? "secondary"
                          : "outline"
                    }
                  >
                    {modelRoutingPrivacyDecisionLabel(attempt.decision)}
                  </Badge>
                </TableCell>
                <TableCell className="text-xs tabular-nums">
                  {t("modelRouting.privacy.findings", {
                    count: attempt.finding_count,
                  })}
                </TableCell>
                <TableCell className="text-xs tabular-nums text-muted-foreground">
                  {attempt.duration_ms} ms
                  {attempt.error_code
                    ? ` · ${modelRoutingCodeLabel(attempt.error_code)}`
                    : ""}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Panel>
  );
}
