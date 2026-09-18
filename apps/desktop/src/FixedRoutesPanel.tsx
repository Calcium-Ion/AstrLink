import { RecoveryPathPicker } from "./components/RecoveryPathPicker";
import { createRecoveryPath } from "./bridge";
import { newPathNodeID } from "./RecoveryPathEditor";
import { useRoutingDefaults } from "./use-routing-defaults";
import { FailoverEditor } from "./components/FailoverEditor";
import {
  parseFailurePolicy,
  parseFailoverPolicy,
  type FailurePolicy,
  type FailoverPolicy,
} from "./failure-policy-model";
import { type FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { ChevronDown } from "@/components/icons";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { DataRow } from "@/components/DataRow";
import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { EmptyState } from "@/components/EmptyState";
import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { Panel } from "@/components/Panel";
import { SectionKicker } from "@/components/SectionKicker";
import { StatusDot } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

import { createRoute, deleteRoute, getRoute, updateRoute } from "./bridge";
import { i18n } from "./i18n";
import { notify } from "./notify";
import { PageHeader } from "./PageHeader";
import { type RoutableService } from "./service-model";
import { protocolLabel, type ProtocolDescriptor } from "./service-presets";
import type {
  Route,
  RouteCreateInput,
  RouteRecord,
  RouteTarget,
  RoutePlanType,
} from "./route-model";
import { isAutoRoute } from "./route-model";
import { availableProtocolIDs } from "./routing-protocols";
import { sortRoutes, useRouteCatalog } from "./use-route-catalog";

interface RouteDraftTarget {
  serviceId: string;
  planType: RoutePlanType;
  upstreamProtocol?: string;
  priority: string;
  upstreamModel: string;
}

interface RouteDraft {
  recoveryPathId?: string;
  failurePolicy?: FailurePolicy;
  failover?: FailoverPolicy;
  name: string;
  enabled: boolean;
  priority: string;
  protocol: string;
  publicModel: string;
  targets: RouteDraftTarget[];
}

type EditorState =
  | { kind: "create"; record: null }
  | { kind: "edit"; record: RouteRecord };

interface FixedRoutesPanelProps {
  coreSessionKey: string | null;
  services: RoutableService[];
  isReady: boolean;
  onDirtyChange: (dirty: boolean) => void;
  onManageServices: () => void;
  protocols: ProtocolDescriptor[];
}

function modeLabel(_mode: RoutePlanType): string {
  if (_mode === "relaykit") return i18n.t("failure.convertedTarget");
  return i18n.t("routes.passthrough");
}

function messageOf(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

function modesFor(
  service: RoutableService | undefined,
  protocol: string,
): RoutePlanType[] {
  if (!service) return [];
  const modes = service.capabilities
    .filter((capability) => capability.protocol === protocol)
    .map((capability) => capability.mode);
  return [...new Set(modes)];
}

function capabilityFor(
  service: RoutableService | undefined,
  protocol: string,
  mode: "native" | "delegated",
) {
  return service?.capabilities.find(
    (capability) =>
      capability.protocol === protocol && capability.mode === mode,
  );
}

function compatibleServices(
  services: RoutableService[],
  protocol: string,
): RoutableService[] {
  return services.filter(
    (service) =>
      service.models.length > 0 &&
      service.capabilities.some(
        (capability) => capability.protocol === protocol,
      ),
  );
}

function nextTarget(
  services: RoutableService[],
  protocol: string,
  excluded = new Set<string>(),
): RouteDraftTarget | null {
  const service = compatibleServices(services, protocol).find(
    (candidate) => !excluded.has(candidate.id),
  );
  if (!service) return null;
  const planType = modesFor(service, protocol)[0];
  if (!planType) return null;
  return {
    serviceId: service.id,
    planType,
    priority: String(excluded.size * 10),
    upstreamModel: "",
  };
}

function createDraft(
  services: RoutableService[],
  protocolIDs: string[],
): RouteDraft {
  const protocol = protocolIDs.includes("openai.responses")
    ? "openai.responses"
    : (protocolIDs[0] ?? "openai.responses");
  const target = nextTarget(services, protocol);
  return {
    name: i18n.t("routes.defaultRoute"),
    enabled: true,
    priority: "100",
    protocol,
    publicModel: "",
    targets: target ? [target] : [],
  };
}

function draftFromRecord(record: RouteRecord): RouteDraft {
  const route = record.route;
  if (
    route.selection?.mode === "auto" ||
    (!route.targets && !route.recovery_path_id)
  ) {
    throw new Error(i18n.t("routes.autoTraining"));
  }
  return {
    name: route.name,
    failurePolicy: route.failure_policy,
    failover: route.failover,
    recoveryPathId: route.recovery_path_id,
    enabled: route.enabled,
    priority: String(route.priority),
    protocol: route.match.protocol,
    publicModel: route.match.model ?? "",
    targets: (route.targets ?? []).map((target) => ({
      serviceId: target.service_id,
      planType: target.plan_type,
      upstreamProtocol: target.upstream_protocol,
      priority: String(target.priority),
      upstreamModel: target.upstream_model ?? "",
    })),
  };
}

function draftSignature(draft: RouteDraft): string {
  return JSON.stringify(draft);
}

function parsePriority(value: string): number | null {
  if (!/^(?:0|[1-9][0-9]{0,6})$/.test(value)) return null;
  const parsed = Number(value);
  return parsed <= 1_000_000 ? parsed : null;
}

function validateDraft(
  draft: RouteDraft,
  services: RoutableService[],
): string | null {
  try {
    if (draft.failurePolicy) parseFailurePolicy(draft.failurePolicy);
    if (draft.failover) parseFailoverPolicy(draft.failover);
  } catch {
    return i18n.t("failure.invalid");
  }
  const name = draft.name.trim();
  if (name.length === 0 || [...name].length > 128) {
    return i18n.t("routes.nameInvalid");
  }
  if (draft.publicModel === "astrlink/auto") {
    return i18n.t("routes.autoReserved");
  }
  if ([...draft.publicModel].length > 256) {
    return i18n.t("routes.publicTooLong");
  }
  if (parsePriority(draft.priority) === null) {
    return i18n.t("routes.priorityRange");
  }
  if (draft.recoveryPathId) return null;
  if (draft.targets.length === 0) {
    return i18n.t("routes.needTarget");
  }
  const seenServices = new Set<string>();
  for (const [index, target] of draft.targets.entries()) {
    const service = services.find(
      (candidate) => candidate.id === target.serviceId,
    );
    if (!service) return i18n.t("routes.missingService", { index: index + 1 });
    if (
      seenServices.has(
        `${service.id}:${target.planType}:${target.upstreamProtocol ?? draft.protocol}:${target.upstreamModel || draft.publicModel}`,
      )
    ) {
      return i18n.t("routes.duplicateService");
    }
    seenServices.add(
      `${service.id}:${target.planType}:${target.upstreamProtocol ?? draft.protocol}:${target.upstreamModel || draft.publicModel}`,
    );
    const capability = capabilityFor(
      service,
      target.planType === "relaykit"
        ? (target.upstreamProtocol ?? draft.protocol)
        : draft.protocol,
      target.planType === "relaykit" ? "native" : target.planType,
    );
    if (!capability) {
      return i18n.t("routes.unsupported", { index: index + 1 });
    }
    if (parsePriority(target.priority) === null) {
      return i18n.t("routes.targetPriority", { index: index + 1 });
    }
    if ([...target.upstreamModel].length > 256) {
      return i18n.t("routes.upstreamTooLong", { index: index + 1 });
    }
    if (!draft.publicModel && target.upstreamModel) {
      return i18n.t("routes.rewriteExactOnly");
    }
    const effectiveModel = target.upstreamModel || draft.publicModel;
    if (effectiveModel && !service.models.includes(effectiveModel)) {
      return i18n.t("routes.upstreamNotListed", { index: index + 1 });
    }
  }
  return null;
}

function createInputFromDraft(draft: RouteDraft): RouteCreateInput {
  const targets: RouteTarget[] = draft.targets.map((target) => ({
    service_id: target.serviceId,
    plan_type: target.planType,
    upstream_protocol:
      target.planType === "relaykit"
        ? (target.upstreamProtocol ?? draft.protocol)
        : draft.protocol,
    priority: Number(target.priority),
    ...(target.upstreamModel
      ? { upstream_model: target.upstreamModel.trim() }
      : {}),
  }));
  return {
    name: draft.name.trim(),
    enabled: draft.enabled,
    priority: Number(draft.priority),
    match: {
      protocol: draft.protocol,
      ...(draft.publicModel.trim() ? { model: draft.publicModel.trim() } : {}),
    },
    selection: { mode: "priority" },
    ...(draft.recoveryPathId
      ? { recovery_path_id: draft.recoveryPathId }
      : { targets }),
    ...(draft.failurePolicy ? { failure_policy: draft.failurePolicy } : {}),
    ...(draft.failover ? { failover: draft.failover } : {}),
  };
}

export function FixedRoutesPanel({
  coreSessionKey,
  services,
  isReady,
  onDirtyChange,
  onManageServices,
  protocols,
}: FixedRoutesPanelProps) {
  const routingDefaults = useRoutingDefaults(isReady);
  const inheritedFailover = {
    enabled: true,
    strategy: routingDefaults.strategy,
    max_attempts: routingDefaults.max_attempts,
  };
  const t = i18n.t.bind(i18n);
  const protocolIDs = useMemo(
    () => availableProtocolIDs(services, protocols),
    [services, protocols],
  );
  const { catalog, setCatalog, refresh } = useRouteCatalog(
    isReady,
    coreSessionKey,
  );
  const [editor, setEditor] = useState<EditorState | null>(null);
  const [draft, setDraft] = useState<RouteDraft>(() =>
    createDraft(services, protocolIDs),
  );
  const draftDisplayName =
    draft.name.trim() || draft.publicModel.trim() || t("routes.unnamedRoute");
  const [baseline, setBaseline] = useState<string | null>(null);
  const [loadingRecord, setLoadingRecord] = useState(false);
  const [saving, setSaving] = useState(false);
  const [mutatingID, setMutatingID] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [cancelPending, setCancelPending] = useState(false);
  const [deletePending, setDeletePending] = useState<RouteRecord | null>(null);
  const editorDirty =
    editor !== null && baseline !== null && draftSignature(draft) !== baseline;
  const dirty = editorDirty;
  const priorityRoutes = catalog.items.filter((route) => !isAutoRoute(route));

  useEffect(() => {
    onDirtyChange(dirty);
  }, [dirty, onDirtyChange]);

  const dirtyCallback = useRef(onDirtyChange);
  dirtyCallback.current = onDirtyChange;
  useEffect(
    () => () => {
      dirtyCallback.current(false);
    },
    [],
  );

  const closeEditor = () => {
    setEditor(null);
    setBaseline(null);
    setError(null);
    setCancelPending(false);
    onDirtyChange(false);
  };

  const beginCreate = () => {
    const next = createDraft(services, protocolIDs);
    setDraft(next);
    setBaseline(draftSignature(next));
    setEditor({ kind: "create", record: null });
    setError(null);
  };

  const beginEdit = async (route: Route) => {
    setLoadingRecord(true);
    setError(null);
    try {
      const record = await getRoute(route.id);
      const next = draftFromRecord(record);
      setDraft(next);
      setBaseline(draftSignature(next));
      setEditor({ kind: "edit", record });
    } catch (loadError) {
      setError(messageOf(loadError, i18n.t("routes.detailFailed")));
    } finally {
      setLoadingRecord(false);
    }
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    const issue = validateDraft(draft, services);
    if (issue) {
      setError(issue);
      return;
    }
    if (!editor) return;
    const input = createInputFromDraft(draft);
    setSaving(true);
    setError(null);
    try {
      const record =
        editor.kind === "create"
          ? await createRoute(input)
          : await updateRoute(editor.record.route.id, editor.record.etag, {
              name: input.name,
              enabled: input.enabled,
              priority: input.priority,
              match: input.match,
              selection: input.selection,
              targets: input.targets ?? null,
              recovery_path_id: input.recovery_path_id ?? null,
              categories: null,
              failure_policy: input.failure_policy ?? null,
              failover: input.failover ?? null,
            });
      setCatalog((current) => ({
        status: "ready",
        items: sortRoutes([
          ...current.items.filter((route) => route.id !== record.route.id),
          record.route,
        ]),
        error: null,
        stale: false,
      }));
      notify.success(
        editor.kind === "create"
          ? i18n.t("routes.created")
          : i18n.t("routes.saved"),
      );
      closeEditor();
    } catch (saveError) {
      setError(messageOf(saveError, i18n.t("routes.saveFailed")));
    } finally {
      setSaving(false);
    }
  };

  const toggleRoute = async (route: Route) => {
    setMutatingID(route.id);
    setError(null);
    try {
      const record = await getRoute(route.id);
      const updated = await updateRoute(route.id, record.etag, {
        enabled: !record.route.enabled,
      });
      setCatalog((current) => ({
        ...current,
        status: "ready",
        items: sortRoutes(
          current.items.map((item) =>
            item.id === updated.route.id ? updated.route : item,
          ),
        ),
        error: null,
        stale: false,
      }));
      notify.success(
        updated.route.enabled
          ? i18n.t("routes.enabledToast")
          : i18n.t("routes.disabledToast"),
      );
    } catch (toggleError) {
      setError(messageOf(toggleError, i18n.t("routes.statusFailed")));
    } finally {
      setMutatingID(null);
    }
  };

  const askDelete = async (route: Route) => {
    setMutatingID(route.id);
    setError(null);
    try {
      setDeletePending(await getRoute(route.id));
    } catch (loadError) {
      setError(messageOf(loadError, i18n.t("routes.deleteReadFailed")));
    } finally {
      setMutatingID(null);
    }
  };

  const confirmDelete = async () => {
    if (!deletePending) return;
    const id = deletePending.route.id;
    setMutatingID(id);
    setError(null);
    try {
      await deleteRoute(id, deletePending.etag);
      setCatalog((current) => ({
        status: "ready",
        items: current.items.filter((route) => route.id !== id),
        error: null,
        stale: false,
      }));
      setDeletePending(null);
      notify.success(i18n.t("routes.deleted"));
    } catch (deleteError) {
      setError(messageOf(deleteError, i18n.t("routes.deleteFailed")));
    } finally {
      setMutatingID(null);
    }
  };

  const changeProtocol = (protocol: string) => {
    setDraft((current) => {
      const compatible = compatibleServices(services, protocol);
      const targets = current.targets
        .filter((target) =>
          compatible.some((service) => service.id === target.serviceId),
        )
        .map((target) => {
          const service = services.find(
            (candidate) => candidate.id === target.serviceId,
          );
          const modes = modesFor(service, protocol);
          return {
            ...target,
            planType: modes.includes(target.planType)
              ? target.planType
              : (modes[0] ?? "native"),
          };
        });
      if (targets.length === 0) {
        const target = nextTarget(services, protocol);
        if (target) targets.push(target);
      }
      return { ...current, protocol, targets };
    });
  };

  const updateTarget = (
    index: number,
    update: (target: RouteDraftTarget) => RouteDraftTarget,
  ) => {
    setDraft((current) => ({
      ...current,
      targets: current.targets.map((target, targetIndex) =>
        targetIndex === index ? update(target) : target,
      ),
    }));
  };

  const addTarget = () => {
    setDraft((current) => {
      const target =
        nextTarget(
          services,
          current.protocol,
          new Set(current.targets.map((item) => item.serviceId)),
        ) ?? nextTarget(services, current.protocol);
      return target
        ? {
            ...current,
            targets: [
              ...current.targets,
              { ...target, priority: String(current.targets.length * 10) },
            ],
          }
        : current;
    });
  };

  const servicesForProtocol = services.filter(
    (service) =>
      compatibleServices([service], draft.protocol).length ||
      draft.targets.some((target) => target.serviceId === service.id),
  );
  const canAddTarget =
    servicesForProtocol.length > 0 && draft.targets.length < 200;

  return (
    <section
      className="flex min-h-0 flex-1 flex-col overflow-hidden"
      data-testid="fixed-routes-panel"
    >
      {editor ? (
        <PageHeader
          headingLevel={2}
          back={{
            label: t("routes.back"),
            onClick: () => (dirty ? setCancelPending(true) : closeEditor()),
          }}
          title={
            editor.kind === "create"
              ? t("routes.newFixed")
              : t("routes.editFixed")
          }
          variant="compact"
        />
      ) : null}

      {error || catalog.error ? (
        <FormMessage className="mb-2.5" tone="error">
          {error ?? catalog.error}
          {catalog.error ? (
            <Button
              type="button"
              variant="ghost"
              onClick={() => void refresh()}
            >
              {t("common.retry")}
            </Button>
          ) : null}
        </FormMessage>
      ) : null}

      <div className="min-h-0 flex-1 overflow-y-auto" data-tab-scroller>
        {editor ? (
          <form
            aria-busy={saving}
            className="w-full min-w-0 aria-busy:pointer-events-none aria-busy:opacity-70"
            onSubmit={save}
          >
            <div className="grid grid-cols-2 gap-3 max-[720px]:grid-cols-1">
              <Field label={t("routes.name")}>
                <Input
                  autoFocus
                  maxLength={128}
                  onChange={(event) =>
                    setDraft((current) => ({
                      ...current,
                      name: event.target.value,
                    }))
                  }
                  value={draft.name}
                />
              </Field>
              <Field
                hint={t("routes.priorityHint")}
                label={t("routes.priority")}
              >
                <Input
                  inputMode="numeric"
                  max="1000000"
                  min="0"
                  onChange={(event) =>
                    setDraft((current) => ({
                      ...current,
                      priority: event.target.value,
                    }))
                  }
                  type="number"
                  value={draft.priority}
                />
              </Field>
              <Field label={t("routes.entryProtocol")}>
                <Select onValueChange={changeProtocol} value={draft.protocol}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {protocolIDs.map((protocol) => (
                      <SelectItem key={protocol} value={protocol}>
                        {protocolLabel(protocol)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field
                hint={t("routes.publicModelHint")}
                label={t("routes.publicModel")}
              >
                <Input
                  maxLength={256}
                  onChange={(event) => {
                    const publicModel = event.target.value;
                    setDraft((current) => ({
                      ...current,
                      publicModel,
                      targets: publicModel
                        ? current.targets
                        : current.targets.map((target) => ({
                            ...target,
                            upstreamModel: "",
                          })),
                    }));
                  }}
                  placeholder={t("routes.publicPlaceholder")}
                  value={draft.publicModel}
                />
              </Field>
            </div>

            <Label className="mt-3 flex items-start gap-2 rounded-md border bg-muted px-3 py-2.5">
              <Checkbox
                checked={draft.enabled}
                onCheckedChange={(checked) =>
                  setDraft((current) => ({
                    ...current,
                    enabled: checked === true,
                  }))
                }
              />
              <span className="flex flex-col gap-0.5">
                <strong className="text-sm font-medium text-foreground">
                  {t("routes.enableOnSave")}
                </strong>
                <small className="text-xs font-normal text-muted-foreground">
                  {t("routes.enableOnSaveHint")}
                </small>
              </span>
            </Label>

            <div className="mt-4">
              <RecoveryPathPicker
                value={draft.recoveryPathId}
                protocol={draft.protocol}
                services={services}
                ready={isReady}
                hasOverride={!!draft.failover || !!draft.failurePolicy}
                onChange={(recoveryPathId) =>
                  setDraft((current) => ({ ...current, recoveryPathId }))
                }
                onSaveLegacy={() => {
                  const input = createInputFromDraft(draft);
                  void createRecoveryPath({
                    name: draftDisplayName,
                    protocol: draft.protocol,
                    mode: "automatic",
                    targets: (input.targets ?? [])
                      .sort((a, b) => a.priority - b.priority)
                      .map((target) => ({
                        id: newPathNodeID(),
                        service_id: target.service_id,
                        upstream_model: target.upstream_model,
                        upstream_protocol: target.upstream_protocol,
                        plan_type: target.plan_type,
                      })),
                    ...(draft.failurePolicy
                      ? { failure_policy: draft.failurePolicy }
                      : {}),
                    ...(draft.failover
                      ? {
                          strategy: draft.failover.strategy,
                          max_attempts: draft.failover.max_attempts,
                        }
                      : {}),
                  })
                    .then((record) => {
                      setDraft((current) => ({
                        ...current,
                        recoveryPathId: record.path.id,
                      }));
                      setError(t("paths.unbound"));
                    })
                    .catch((error) => setError(String(error)));
                }}
              />
            </div>
            <div className="mt-4">
              <FailoverEditor
                onReloadDefaults={routingDefaults.reload}
                defaultsLoaded={routingDefaults.loaded}
                onResetOrder={
                  draft.failover
                    ? () =>
                        setDraft((current) => ({
                          ...current,
                          failover: undefined,
                        }))
                    : undefined
                }
                value={draft.failover ?? inheritedFailover}
                inheritedFailurePolicy={routingDefaults.default_failure_policy}
                override={draft.failurePolicy}
                title={t("routes.failureTitle", { name: draftDisplayName })}
                scopeHint={t("routes.failureScope", {
                  name: draftDisplayName,
                  protocol: protocolLabel(draft.protocol),
                })}
                overrideLabel={t("routes.failureOverride", {
                  name: draftDisplayName,
                })}
                overrideHint={t("routes.failureOverrideHint", {
                  name: draftDisplayName,
                })}
                onChange={(failover) =>
                  setDraft((current) => ({ ...current, failover }))
                }
                onOverrideChange={(failurePolicy) =>
                  setDraft((current) => ({ ...current, failurePolicy }))
                }
                targets={[...draft.targets]
                  .sort((a, b) => Number(a.priority) - Number(b.priority))
                  .map((target) => {
                    const service = services.find(
                      (service) => service.id === target.serviceId,
                    );
                    return {
                      name: `${service?.name ?? target.serviceId}${target.upstreamModel ? ` / ${target.upstreamModel}` : ""}`,
                      policy: service?.failure_policy,
                    };
                  })}
              />
            </div>
            {!draft.recoveryPathId ? (
              <section className="mt-4 border-t pt-3.5">
                <header className="flex items-center justify-between gap-2.5">
                  <div className="flex flex-col gap-0.5">
                    <SectionKicker>{t("routes.targets")}</SectionKicker>
                    <strong className="text-sm font-medium">
                      {t("routes.tryByPriority")}
                    </strong>
                  </div>
                  <Button
                    variant="outline"
                    disabled={!canAddTarget}
                    onClick={addTarget}
                    type="button"
                  >
                    {t("routes.addFallback")}
                  </Button>
                </header>

                {draft.targets.length === 0 ? (
                  <div className="mt-2.5 flex items-center justify-between gap-3 rounded-md border border-dashed bg-muted p-3 text-text-secondary">
                    <p className="text-xs">
                      {t("routes.noProtocolCapability", {
                        protocol: protocolLabel(draft.protocol),
                      })}
                    </p>
                    <Button
                      variant="outline"
                      onClick={onManageServices}
                      type="button"
                    >
                      {t("routes.manageServices")}
                    </Button>
                  </div>
                ) : (
                  <ol className="mt-2.5 grid list-none gap-2 p-0">
                    {draft.targets.map((target, index) => {
                      const service = services.find(
                        (candidate) => candidate.id === target.serviceId,
                      );
                      const modes = [
                        ...modesFor(service, draft.protocol),
                        ...(target.planType === "relaykit"
                          ? ["relaykit" as const]
                          : []),
                      ];
                      return (
                        <li
                          className="grid min-w-0 grid-cols-[24px_minmax(0,1fr)_max-content] items-center gap-2 rounded-md border bg-muted p-2.5 max-[760px]:grid-cols-[24px_minmax(0,1fr)]"
                          key={`${target.serviceId}:${index}`}
                        >
                          <span className="grid size-6 place-items-center rounded-sm border bg-card text-micro font-medium tabular-nums">
                            {String(index + 1).padStart(2, "0")}
                          </span>
                          <div
                            className={cn(
                              "grid min-w-0 gap-2 max-[960px]:grid-cols-2 max-[600px]:grid-cols-1",
                              modes.length > 1
                                ? "grid-cols-[minmax(150px,1.2fr)_minmax(100px,.7fr)_minmax(90px,.55fr)_minmax(150px,1fr)]"
                                : "grid-cols-[minmax(150px,1.2fr)_minmax(90px,.55fr)_minmax(150px,1fr)]",
                            )}
                          >
                            <Field label={t("routes.apiService")}>
                              <Select
                                onValueChange={(serviceId) => {
                                  const nextService = services.find(
                                    (candidate) => candidate.id === serviceId,
                                  );
                                  const nextModes = modesFor(
                                    nextService,
                                    draft.protocol,
                                  );
                                  updateTarget(index, (current) => ({
                                    ...current,
                                    serviceId,
                                    upstreamProtocol: draft.protocol,
                                    planType: nextModes.includes(
                                      current.planType,
                                    )
                                      ? current.planType
                                      : (nextModes[0] ?? "native"),
                                  }));
                                }}
                                value={target.serviceId}
                              >
                                <SelectTrigger className="w-full">
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  {servicesForProtocol.map((candidate) => (
                                    <SelectItem
                                      key={candidate.id}
                                      value={candidate.id}
                                    >
                                      {candidate.name}
                                      {candidate.enabled
                                        ? ""
                                        : t("routes.disabledSuffix")}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            </Field>
                            {modes.length > 1 ? (
                              <Field label={t("routes.planType")}>
                                <Select
                                  onValueChange={(value) =>
                                    updateTarget(index, (current) => ({
                                      ...current,
                                      planType:
                                        value as RouteDraftTarget["planType"],
                                    }))
                                  }
                                  value={target.planType}
                                >
                                  <SelectTrigger className="w-full">
                                    <SelectValue />
                                  </SelectTrigger>
                                  <SelectContent>
                                    {modes.map((mode) => (
                                      <SelectItem key={mode} value={mode}>
                                        {modeLabel(mode)}
                                      </SelectItem>
                                    ))}
                                  </SelectContent>
                                </Select>
                              </Field>
                            ) : null}
                            <Field label={t("routes.targetPriorityField")}>
                              <Input
                                inputMode="numeric"
                                max="1000000"
                                min="0"
                                onChange={(event) =>
                                  updateTarget(index, (current) => ({
                                    ...current,
                                    priority: event.target.value,
                                  }))
                                }
                                type="number"
                                value={target.priority}
                              />
                            </Field>
                            <div className="grid min-w-0 grid-cols-[minmax(0,1fr)_32px] gap-x-1.5 gap-y-1.5 text-xs font-medium text-text-secondary">
                              <Label
                                className="contents"
                                htmlFor={`route-upstream-model-${index}`}
                              >
                                <span className="col-span-full">
                                  {t("routes.upstreamModel")}
                                </span>
                                <Input
                                  className="min-w-0 flex-1"
                                  disabled={!draft.publicModel}
                                  id={`route-upstream-model-${index}`}
                                  maxLength={256}
                                  onChange={(event) =>
                                    updateTarget(index, (current) => ({
                                      ...current,
                                      upstreamModel: event.target.value,
                                    }))
                                  }
                                  placeholder={
                                    draft.publicModel
                                      ? t("routes.upstreamPlaceholder")
                                      : t("routes.needPublicFirst")
                                  }
                                  value={target.upstreamModel}
                                />
                              </Label>
                              <DropdownMenu>
                                <DropdownMenuTrigger asChild>
                                  <Button
                                    aria-label={t("routes.chooseUpstream", {
                                      index: index + 1,
                                    })}
                                    disabled={
                                      !draft.publicModel ||
                                      (service?.models.length ?? 0) === 0
                                    }
                                    size="icon"
                                    type="button"
                                    variant="outline"
                                  >
                                    <ChevronDown aria-hidden="true" />
                                  </Button>
                                </DropdownMenuTrigger>
                                <DropdownMenuContent
                                  align="end"
                                  className="max-w-[min(420px,calc(100vw-2rem))] min-w-[220px]"
                                >
                                  {service?.models.map((model) => (
                                    <DropdownMenuItem
                                      key={model}
                                      onSelect={() =>
                                        updateTarget(index, (current) => ({
                                          ...current,
                                          upstreamModel: model,
                                        }))
                                      }
                                    >
                                      <ModelBrandIcon model={model} />
                                      <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">
                                        {model}
                                      </span>
                                    </DropdownMenuItem>
                                  ))}
                                </DropdownMenuContent>
                              </DropdownMenu>
                            </div>
                          </div>
                          <Button
                            aria-label={t("routes.removeTarget", {
                              index: index + 1,
                            })}
                            className="text-danger-foreground max-[760px]:col-start-2 max-[760px]:justify-self-end"
                            disabled={draft.targets.length === 1}
                            onClick={() =>
                              setDraft((current) => ({
                                ...current,
                                targets: current.targets.filter(
                                  (_, targetIndex) => targetIndex !== index,
                                ),
                              }))
                            }
                            type="button"
                            variant="ghost"
                          >
                            {t("routes.remove")}
                          </Button>
                        </li>
                      );
                    })}
                  </ol>
                )}
              </section>
            ) : null}

            <div className="mt-4 flex justify-end gap-2 border-t pt-3">
              <Button
                variant="outline"
                disabled={saving}
                onClick={() => (dirty ? setCancelPending(true) : closeEditor())}
                type="button"
              >
                {t("common.cancel")}
              </Button>
              <Button
                disabled={
                  saving ||
                  (!draft.recoveryPathId && draft.targets.length === 0)
                }
                type="submit"
              >
                {saving
                  ? t("common.saving")
                  : editor.kind === "create"
                    ? t("routes.createFixed")
                    : t("routes.saveChanges")}
              </Button>
            </div>
          </form>
        ) : (
          <div className="flex min-w-0 flex-1 flex-col gap-5 pb-6">
            <section
              aria-labelledby="manual-routes-title"
              className="flex min-w-0 flex-col gap-3"
            >
              <div className="flex min-w-0 items-start justify-between gap-4 max-[720px]:flex-col">
                <div className="min-w-0">
                  <h3
                    className="mt-1 text-base font-semibold tracking-tight"
                    id="manual-routes-title"
                  >
                    {t("routes.fixedAndAliases")}
                  </h3>
                  <p className="mt-1 max-w-[560px] text-xs text-text-secondary">
                    {t("routes.fixedHint")}
                  </p>
                </div>
                <div className="flex shrink-0 flex-wrap justify-end gap-2">
                  <Button
                    variant="outline"
                    disabled={!isReady || catalog.status === "loading"}
                    onClick={() => void refresh()}
                    type="button"
                  >
                    {t("common.refresh")}
                  </Button>
                  <Button
                    disabled={!isReady || loadingRecord}
                    onClick={beginCreate}
                    type="button"
                  >
                    {t("routes.newFixed")}
                  </Button>
                </div>
              </div>

              <div className="flex min-w-0 flex-col">
                {!isReady && catalog.items.length === 0 ? (
                  <EmptyState
                    description={t("routes.waitingHint")}
                    title={t("routes.waiting")}
                  />
                ) : catalog.status === "loading" &&
                  priorityRoutes.length === 0 ? (
                  <EmptyState title={t("routes.loading")} />
                ) : priorityRoutes.length === 0 ? (
                  <EmptyState
                    action={
                      <Button
                        disabled={!isReady}
                        onClick={beginCreate}
                        type="button"
                      >
                        {t("routes.createFirst")}
                      </Button>
                    }
                    description={t("routes.emptyHint")}
                    title={t("routes.empty")}
                  />
                ) : (
                  <Panel asChild>
                    <ol className="list-none p-0">
                      {priorityRoutes.map((route) => {
                        const targets = route.targets ?? [];
                        const aliasTarget = targets.find(
                          (target) => target.upstream_model,
                        );
                        return (
                          <DataRow
                            asChild
                            className="items-start px-3.5 py-3 max-[760px]:flex-wrap"
                            key={route.id}
                          >
                            <li>
                              <div className="flex w-11 shrink-0 flex-col items-center gap-0.5">
                                <span className="text-micro tracking-[0.06em] text-muted-foreground uppercase">
                                  {t("routes.priorityShort")}
                                </span>
                                <strong className="text-base tabular-nums">
                                  {route.priority}
                                </strong>
                              </div>
                              <div className="min-w-0 flex-1">
                                <header className="flex min-w-0 items-center justify-between gap-3">
                                  <div className="flex min-w-0 items-center gap-1.5">
                                    <StatusDot
                                      tone={
                                        route.enabled ? "positive" : "neutral"
                                      }
                                    />
                                    <strong className="truncate text-sm font-medium">
                                      {route.name}
                                    </strong>
                                    <span
                                      className={cn(
                                        "shrink-0 text-micro text-success-foreground",
                                        !route.enabled &&
                                          "text-muted-foreground",
                                      )}
                                    >
                                      {route.enabled
                                        ? t("common.enabled")
                                        : t("common.disabled")}
                                    </span>
                                  </div>
                                  <code className="inline-flex max-w-[40%] items-center gap-1 overflow-hidden rounded-sm border px-1.5 py-px font-mono text-micro text-foreground">
                                    <ModelBrandIcon model={route.match.model} />
                                    <span className="min-w-0 truncate">
                                      {route.match.model ??
                                        t("routes.allModels")}
                                    </span>
                                  </code>
                                </header>
                                <p className="mt-1 truncate text-xs text-muted-foreground">
                                  {protocolLabel(route.match.protocol)} ·{" "}
                                  {t("routes.targetCount", {
                                    count: targets.length,
                                  })}
                                  {aliasTarget
                                    ? t("routes.aliasTo", {
                                        model: aliasTarget.upstream_model,
                                      })
                                    : ""}
                                </p>
                                <div className="mt-1.5 flex min-w-0 flex-wrap gap-1">
                                  {targets.map((target, index) => {
                                    const service = services.find(
                                      (candidate) =>
                                        candidate.id === target.service_id,
                                    );
                                    return (
                                      <span
                                        className="inline-flex min-w-0 items-center gap-1 truncate rounded-sm border bg-muted px-1.5 py-px text-micro text-text-secondary"
                                        key={`${target.service_id}:${target.priority}:${index}`}
                                      >
                                        {index + 1}.{" "}
                                        {service?.name ?? target.service_id}
                                        <small className="text-muted-foreground">
                                          {target.plan_type === "native" ||
                                          target.plan_type === "delegated"
                                            ? modeLabel(target.plan_type)
                                            : target.plan_type}
                                        </small>
                                      </span>
                                    );
                                  })}
                                </div>
                              </div>
                              <div className="flex shrink-0 items-center gap-1 max-[760px]:w-full max-[760px]:justify-end">
                                <Button
                                  size="sm"
                                  variant="outline"
                                  disabled={
                                    mutatingID === route.id || loadingRecord
                                  }
                                  onClick={() => void beginEdit(route)}
                                  type="button"
                                >
                                  {t("routes.edit")}
                                </Button>
                                <Button
                                  size="sm"
                                  variant="outline"
                                  disabled={mutatingID === route.id}
                                  onClick={() => void toggleRoute(route)}
                                  type="button"
                                >
                                  {route.enabled
                                    ? t("routes.disable")
                                    : t("routes.enable")}
                                </Button>
                                <Button
                                  className="text-danger-foreground hover:bg-danger-wash hover:text-danger-foreground"
                                  disabled={mutatingID === route.id}
                                  onClick={() => void askDelete(route)}
                                  type="button"
                                  size="sm"
                                  variant="ghost"
                                >
                                  {t("common.delete")}
                                </Button>
                              </div>
                            </li>
                          </DataRow>
                        );
                      })}
                    </ol>
                  </Panel>
                )}
                {catalog.stale ? (
                  <p className="mt-2 text-xs text-warning-foreground">
                    {t("routes.stale")}
                  </p>
                ) : null}
              </div>
            </section>
          </div>
        )}
      </div>

      <ConfirmDialog
        cancelLabel={t("common.continueEditing")}
        confirmLabel={t("routes.discard")}
        description={<p>{t("routes.unsavedBody")}</p>}
        onCancel={() => setCancelPending(false)}
        onConfirm={closeEditor}
        open={cancelPending}
        title={t("routes.discardTitle")}
      />

      <ConfirmDialog
        confirmLabel={
          mutatingID === deletePending?.route.id
            ? t("routes.deleting")
            : t("routes.confirmDelete")
        }
        description={
          <p>
            {t("routes.deleteBody", { name: deletePending?.route.name ?? "" })}
          </p>
        }
        destructive
        disabled={mutatingID === deletePending?.route.id}
        onCancel={() => setDeletePending(null)}
        onConfirm={() => void confirmDelete()}
        open={deletePending !== null}
        title={t("routes.deleteTitle")}
      />
    </section>
  );
}
