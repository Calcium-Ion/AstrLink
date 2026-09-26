import { useEffect, useRef, useState } from "react";

import {
  ArrowRight,
  Check,
  Copy,
  Key,
  RefreshCw,
  Server,
} from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { CopyableValue } from "@/components/CopyableValue";
import { FormMessage } from "@/components/FormMessage";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { Panel, PanelBody } from "@/components/Panel";
import { SegmentedControl } from "@/components/SegmentedControl";
import { SetupSteps } from "@/components/SetupSteps";
import { InferencePortNotice } from "@/components/InferencePortNotice";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { revealAccessToken } from "./bridge";
import { CCSwitchImportDialog } from "./CCSwitchImportDialog";
import { CCSwitchIcon } from "@/components/CCSwitchIcon";
import type { AppSnapshot } from "./core-model";
import type { AccessTokenCatalog } from "./AccessTokenManager";
import type { ServiceCatalog } from "./Overview";
import { PageHeader } from "./PageHeader";
import { useT } from "./i18n";
import { notify } from "./notify";
import type { useOnboarding } from "./use-onboarding";
import type { UsageState } from "./usage-range";

export function GettingStarted({
  onboarding,
  catalog,
  tokenCatalog,
  snapshot,
  usage,
  isReady,
  isRestarting,
  onAddService,
  onManageServices,
  onManageTokens,
  onOpenRecords,
  onRefresh,
  onRestart,
}: {
  onboarding: ReturnType<typeof useOnboarding>;
  catalog: ServiceCatalog;
  tokenCatalog: AccessTokenCatalog;
  snapshot: AppSnapshot | null;
  usage: UsageState;
  isReady: boolean;
  isRestarting: boolean;
  onAddService: () => void;
  onManageServices: () => void;
  onManageTokens: () => void;
  onOpenRecords: () => void;
  onRefresh: () => void;
  onRestart: () => void;
}) {
  const t = useT();
  const [selectedStep, setSelectedStep] = useState(onboarding.step);
  const [protocol, setProtocol] = useState<"openai" | "anthropic">("openai");
  const [tokenId, setTokenId] = useState("");
  const [copying, setCopying] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const mounted = useRef(true);
  const copyPending = useRef(false);
  const token =
    tokenCatalog.items.find((item) => item.id === tokenId) ??
    tokenCatalog.items[0];
  const inferenceURL =
    snapshot?.ready?.inference_url?.replace(/\/+$/, "") ?? "";
  const baseURL = inferenceURL
    ? `${inferenceURL}${protocol === "openai" ? "/v1" : ""}`
    : "";
  const canConnect =
    onboarding.catalogsReady &&
    onboarding.serviceReady &&
    onboarding.tokenReady &&
    Boolean(baseURL);
  const complete =
    onboarding.catalogsReady &&
    onboarding.serviceReady &&
    onboarding.tokenReady &&
    onboarding.requestReady;
  const steps = [
    {
      title: t("onboarding.serviceTitle"),
      description: t("onboarding.serviceShort"),
      complete: onboarding.serviceReady,
    },
    {
      title: t("onboarding.tokenTitle"),
      description: t("onboarding.tokenShort"),
      complete: onboarding.tokenReady,
    },
    {
      title: t("onboarding.clientTitle"),
      description: t("onboarding.clientShort"),
      complete: onboarding.requestReady,
    },
  ];
  const completedCount = steps.filter((step) => step.complete).length;
  const models = [
    ...new Set(
      catalog.items
        .filter(
          (service) =>
            service.enabled &&
            (!service.subscription ||
              service.subscription.status === "connected") &&
            service.capabilities.some((capability) =>
              protocol === "openai"
                ? capability.protocol.startsWith("openai.")
                : capability.protocol === "anthropic.messages",
            ),
        )
        .flatMap((service) => service.models),
    ),
  ];
  const error = catalog.error ?? tokenCatalog.error ?? usage.error;

  useEffect(() => setSelectedStep(onboarding.step), [onboarding.step]);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    if (!canConnect) setImportOpen(false);
  }, [canConnect]);

  const copyToken = async () => {
    if (!token || !canConnect || copyPending.current) return;
    copyPending.current = true;
    setCopying(true);
    try {
      const result = await revealAccessToken(token.id);
      if (!mounted.current) return;
      await navigator.clipboard.writeText(result.access_token);
      notify.success(t("onboarding.tokenCopied"));
    } catch (error) {
      if (mounted.current)
        notify.error(
          error instanceof Error ? error.message : t("tokens.copyFailed"),
        );
    } finally {
      copyPending.current = false;
      if (mounted.current) setCopying(false);
    }
  };

  return (
    <section
      className="flex min-h-0 flex-1 flex-col gap-3 overflow-hidden"
      data-slot="onboarding-workspace"
    >
      <PageHeader
        className="mb-0"
        title={t("onboarding.title")}
        actions={
          <Button variant="ghost" size="sm" onClick={onboarding.dismiss}>
            {t("onboarding.skip")}
          </Button>
        }
      />
      <InferencePortNotice snapshot={snapshot} />
      <Panel className="flex min-h-0 flex-1 flex-col">
        <PanelBody className="p-0" data-slot="onboarding-content">
          <div className="grid min-h-full @min-[720px]/workspace-surface:grid-cols-[240px_minmax(0,1fr)]">
            <div className="flex flex-col gap-3 border-b bg-muted/30 p-4 @min-[720px]/workspace-surface:gap-4 @min-[720px]/workspace-surface:border-r @min-[720px]/workspace-surface:border-b-0 @min-[720px]/workspace-surface:p-5">
              <div>
                <p className="text-lg font-semibold tracking-tight">
                  {t("onboarding.welcome")}
                </p>
                <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
                  {t("onboarding.description")}
                </p>
              </div>
              <Badge variant="secondary" className="w-fit" role="status">
                {t("onboarding.progress", { count: completedCount })}
              </Badge>
              <SetupSteps
                label={t("onboarding.steps")}
                steps={steps}
                current={selectedStep}
                onSelect={setSelectedStep}
                completeLabel={t("onboarding.completed")}
              />
              <p className="mt-auto hidden text-xs leading-relaxed text-muted-foreground @min-[720px]/workspace-surface:block">
                {t("onboarding.resumeHint")}
              </p>
            </div>
            <div className="flex min-w-0 flex-col gap-4 p-4 @min-[720px]/workspace-surface:gap-5 @min-[720px]/workspace-surface:p-5 @min-[860px]/workspace-surface:p-8">
              {!isReady ? (
                <FormMessage tone="notice">
                  <p>{t("onboarding.gatewayUnavailable")}</p>
                  {snapshot?.last_error ? (
                    <p className="mt-2 break-words">{snapshot.last_error}</p>
                  ) : null}
                  <Button
                    className="mt-3"
                    variant="outline"
                    size="sm"
                    disabled={
                      isRestarting ||
                      snapshot?.phase === "stopping" ||
                      snapshot?.phase === "unavailable"
                    }
                    onClick={onRestart}
                  >
                    {t(
                      isRestarting
                        ? "overview.restarting"
                        : "overview.restartGateway",
                    )}
                  </Button>
                </FormMessage>
              ) : error ? (
                <FormMessage tone="error">
                  <p>{error}</p>
                  <Button
                    className="mt-2"
                    variant="outline"
                    size="sm"
                    onClick={onRefresh}
                  >
                    {t("common.retry")}
                  </Button>
                </FormMessage>
              ) : !onboarding.catalogsReady ? (
                <p role="status" className="text-sm text-muted-foreground">
                  {t("overview.welcomeLoading")}
                </p>
              ) : null}
              <div>
                <p className="mb-2 text-xs font-medium text-muted-foreground">
                  {t("onboarding.step", { count: selectedStep + 1 })}
                </p>
                <h2 className="text-xl font-semibold tracking-tight">
                  {steps[selectedStep].title}
                </h2>
                <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
                  {t(
                    [
                      "onboarding.serviceBody",
                      "onboarding.tokenBody",
                      "onboarding.clientBody",
                    ][selectedStep],
                  )}
                </p>
              </div>
              {selectedStep === 0 ? (
                <>
                  <div className="flex flex-wrap gap-2">
                    <Button
                      disabled={!onboarding.catalogsReady}
                      onClick={
                        catalog.items.length ? onManageServices : onAddService
                      }
                    >
                      <Server aria-hidden="true" />
                      {t(
                        catalog.items.length
                          ? "onboarding.manageServices"
                          : "overview.addService",
                      )}
                    </Button>
                    {onboarding.serviceReady ? (
                      <Button
                        variant="outline"
                        onClick={() => setSelectedStep(1)}
                      >
                        {t("onboarding.next")}
                        <ArrowRight aria-hidden="true" />
                      </Button>
                    ) : null}
                  </div>
                  {catalog.items.length && !onboarding.serviceReady ? (
                    <FormMessage tone="notice">
                      {t("onboarding.serviceIncomplete")}
                    </FormMessage>
                  ) : null}
                  <div className="grid gap-3 text-sm leading-relaxed">
                    <p>{t("onboarding.serviceOptions")}</p>
                    <HelpDisclosure title={t("onboarding.serviceHelp")}>
                      <p>{t("onboarding.serviceHelpBody")}</p>
                    </HelpDisclosure>
                  </div>
                </>
              ) : selectedStep === 1 ? (
                <>
                  {!onboarding.serviceReady ? (
                    <FormMessage tone="notice">
                      {t("onboarding.finishService")}
                    </FormMessage>
                  ) : null}
                  <div className="flex flex-wrap gap-2">
                    <Button
                      disabled={
                        !onboarding.catalogsReady || !onboarding.serviceReady
                      }
                      onClick={onManageTokens}
                    >
                      <Key aria-hidden="true" />
                      {t(
                        onboarding.tokenReady
                          ? "onboarding.manageTokens"
                          : "onboarding.createToken",
                      )}
                    </Button>
                    {onboarding.tokenReady && onboarding.serviceReady ? (
                      <Button
                        variant="outline"
                        onClick={() => setSelectedStep(2)}
                      >
                        {t("onboarding.next")}
                        <ArrowRight aria-hidden="true" />
                      </Button>
                    ) : null}
                  </div>
                  <p className="text-sm leading-relaxed">
                    {t("onboarding.tokenHelp")}
                  </p>
                </>
              ) : !onboarding.catalogsReady ? null : !canConnect ? (
                <FormMessage tone="notice">
                  {t("onboarding.finishSetup")}
                </FormMessage>
              ) : (
                <>
                  <div className="grid gap-3">
                    {tokenCatalog.items.length > 1 ? (
                      <Select value={token?.id} onValueChange={setTokenId}>
                        <SelectTrigger aria-label={t("onboarding.selectToken")}>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {tokenCatalog.items.map((item) => (
                            <SelectItem key={item.id} value={item.id}>
                              {item.name}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    ) : null}
                    <Button
                      variant="outline"
                      className="w-fit"
                      onClick={() => setImportOpen(true)}
                    >
                      <CCSwitchIcon />
                      {t("onboarding.importClient")}
                    </Button>
                    <HelpDisclosure
                      title={t("onboarding.manualSetup")}
                      open={!complete}
                    >
                      <SegmentedControl
                        label={t("onboarding.protocol")}
                        value={protocol}
                        onValueChange={setProtocol}
                        options={[
                          { value: "openai", label: t("onboarding.openai") },
                          {
                            value: "anthropic",
                            label: t("onboarding.anthropic"),
                          },
                        ]}
                      />
                      <CopyableValue
                        label="Base URL"
                        value={baseURL}
                        placeholder={t("overview.waitingReady")}
                        copyLabel={t("overview.copyApiAddress")}
                      />
                      <div className="flex flex-wrap items-center gap-2">
                        <span>
                          {t("onboarding.apiKey", { name: token?.name })}
                        </span>
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={copying}
                          onClick={() => void copyToken()}
                        >
                          <Copy aria-hidden="true" />
                          {t(
                            copying ? "common.copying" : "onboarding.copyToken",
                          )}
                        </Button>
                      </div>
                      <p>{t("onboarding.modelHelp")}</p>
                      {models[0] ? (
                        <CopyableValue
                          label={t("onboarding.model")}
                          value={models[0]}
                          placeholder=""
                          copyLabel={t("onboarding.copyModel")}
                        />
                      ) : (
                        <p>{t("onboarding.noModels")}</p>
                      )}
                    </HelpDisclosure>
                  </div>
                  <div className="mt-auto grid gap-3 border-t pt-4">
                    <p className="text-sm leading-relaxed" role="status">
                      {t(
                        complete
                          ? "onboarding.success"
                          : usage.summary?.totals.failed_requests
                            ? "onboarding.failedRequest"
                            : "onboarding.sendRequest",
                      )}
                    </p>
                    <div className="flex flex-wrap gap-2">
                      {complete ? (
                        <Button onClick={onboarding.complete}>
                          <Check aria-hidden="true" />
                          {t("onboarding.finish")}
                        </Button>
                      ) : (
                        <Button
                          disabled={usage.status === "loading"}
                          onClick={onRefresh}
                        >
                          <RefreshCw aria-hidden="true" />
                          {t(
                            usage.status === "loading"
                              ? "common.refreshing"
                              : "onboarding.checkRequest",
                          )}
                        </Button>
                      )}
                      <Button variant="ghost" onClick={onOpenRecords}>
                        {t("onboarding.viewRecords")}
                        <ArrowRight aria-hidden="true" />
                      </Button>
                    </div>
                  </div>
                </>
              )}
            </div>
          </div>
        </PanelBody>
      </Panel>
      {importOpen && token && canConnect ? (
        <CCSwitchImportDialog
          token={token}
          inferenceURL={inferenceURL}
          onClose={() => setImportOpen(false)}
        />
      ) : null}
    </section>
  );
}
