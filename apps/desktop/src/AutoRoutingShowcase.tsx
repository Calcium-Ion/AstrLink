import { useEffect, useMemo, useState } from "react";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { Panel } from "@/components/Panel";
import { SectionKicker } from "@/components/SectionKicker";
import { Button } from "@/components/ui/button";
import { Input, InputDatalist } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";

import { createRoute, getRoute, updateRoute } from "./bridge";
import { i18n } from "./i18n";
import { notify } from "./notify";
import {
  isAutoRoute,
  type Route,
  type RouteCategory,
  type RouteCreateInput,
  type RouteTarget,
} from "./route-model";
import { type RoutableService } from "./service-model";
import { protocolLabel } from "./service-presets";

const AUTO_MODEL_ID = "astrlink/auto";
const AUTO_TAXONOMY_ID = "astrlink-text-v1";

const routingSteps = [
  {
    id: "understand",
    labelKey: "auto.understand",
    detailKey: "auto.understandHint",
  },
  {
    id: "category",
    labelKey: "auto.match",
    detailKey: "auto.matchHint",
  },
  {
    id: "priority",
    labelKey: "auto.try",
    detailKey: "auto.tryHint",
  },
] as const;

export const autoCategories = [
  {
    id: "general",
    labelKey: "auto.general",
    descriptionKey: "auto.generalHint",
  },
  {
    id: "research",
    labelKey: "auto.research",
    descriptionKey: "auto.researchHint",
  },
  {
    id: "coding",
    labelKey: "auto.coding",
    descriptionKey: "auto.codingHint",
  },
  {
    id: "architect",
    labelKey: "auto.planning",
    descriptionKey: "auto.planningHint",
  },
] as const;

interface CategoryDraft {
  id: string;
  serviceId: string;
  planType: "native" | "delegated";
  upstreamModel: string;
  extraTargets: RouteTarget[];
}

interface AutoDraft {
  protocol: string;
  enabled: boolean;
  name: string;
  categories: CategoryDraft[];
}

export interface AutoRoutingShowcaseProps {
  services?: RoutableService[];
  protocolIDs?: string[];
  routes?: Route[];
  isReady?: boolean;
  onRouteSaved?: (route: Route) => void;
  onDirtyChange?: (dirty: boolean) => void;
}

function modesFor(
  service: RoutableService | undefined,
  protocol: string,
): Array<"native" | "delegated"> {
  if (!service) return [];
  return [
    ...new Set(
      service.capabilities
        .filter((capability) => capability.protocol === protocol)
        .map((capability) => capability.mode),
    ),
  ];
}

function compatibleServices(
  services: RoutableService[],
  protocol: string,
): RoutableService[] {
  return services.filter(
    (service) =>
      service.models.length > 0 &&
      service.capabilities.some((capability) => capability.protocol === protocol),
  );
}

function defaultService(
  services: RoutableService[],
  protocol: string,
): RoutableService | undefined {
  return compatibleServices(services, protocol)[0];
}

function emptyCategory(
  id: string,
  services: RoutableService[],
  protocol: string,
): CategoryDraft {
  const service = defaultService(services, protocol);
  const planType = modesFor(service, protocol)[0] ?? "native";
  return {
    id,
    serviceId: service?.id ?? "",
    planType,
    upstreamModel: "",
    extraTargets: [],
  };
}

function draftFromRoute(
  route: Route | undefined,
  protocol: string,
  services: RoutableService[],
): AutoDraft {
  return {
    protocol,
    enabled: route?.enabled ?? true,
    name: route?.name ?? i18n.t("auto.banner", { protocol: protocolLabel(protocol) }),
    categories: autoCategories.map((category) => {
      const found = route?.categories?.find(
        (item) => item.category_id === category.id,
      );
      const [primary, ...extra] = found?.targets ?? [];
      if (!primary) {
        return emptyCategory(category.id, services, protocol);
      }
      return {
        id: category.id,
        serviceId: primary.service_id,
        planType: primary.plan_type === "delegated" ? "delegated" : "native",
        upstreamModel: primary.upstream_model ?? "",
        extraTargets: extra,
      };
    }),
  };
}

function draftSignature(draft: AutoDraft): string {
  return JSON.stringify(draft);
}

function autoRouteForProtocol(routes: Route[], protocol: string): Route | undefined {
  return routes.find(
    (route) => isAutoRoute(route) && route.match.protocol === protocol,
  );
}

function filledCategories(draft: AutoDraft): CategoryDraft[] {
  return draft.categories.filter(
    (category) => category.serviceId && category.upstreamModel.trim(),
  );
}

function validateAutoDraft(
  draft: AutoDraft,
  services: RoutableService[],
): string | null {
  const filled = filledCategories(draft);
  if (filled.length < 2) {
    return i18n.t("auto.needTwo");
  }
  const models = new Set(filled.map((category) => category.upstreamModel.trim()));
  if (models.size < 2) {
    return i18n.t("auto.needDistinct");
  }
  for (const category of filled) {
    const meta = autoCategories.find((item) => item.id === category.id);
    const label = meta ? i18n.t(meta.labelKey) : category.id;
    const service = services.find((item) => item.id === category.serviceId);
    if (!service) {
      return i18n.t("auto.missingService", { category: label });
    }
    const model = category.upstreamModel.trim();
    if ([...model].length > 256) {
      return i18n.t("auto.modelTooLong", { category: label });
    }
    if (!service.models.includes(model)) {
      return i18n.t("auto.modelNotListed", { category: label });
    }
    if (!modesFor(service, draft.protocol).includes(category.planType)) {
      return i18n.t("auto.protocolUnsupported", { category: label });
    }
  }
  return null;
}

function categoriesFromDraft(draft: AutoDraft): RouteCategory[] {
  return filledCategories(draft).map((category) => {
    const primary: RouteTarget = {
      service_id: category.serviceId,
      plan_type: category.planType,
      upstream_protocol: draft.protocol,
      priority: 0,
      upstream_model: category.upstreamModel.trim(),
    };
    return {
      category_id: category.id,
      targets: [primary, ...category.extraTargets],
    };
  });
}

function createInputFromAutoDraft(draft: AutoDraft): RouteCreateInput {
  return {
    name: draft.name.trim() || i18n.t("auto.banner", { protocol: protocolLabel(draft.protocol) }),
    enabled: draft.enabled,
    priority: 0,
    match: { protocol: draft.protocol, model: AUTO_MODEL_ID },
    selection: { mode: "auto", taxonomy_id: AUTO_TAXONOMY_ID },
    categories: categoriesFromDraft(draft),
  };
}

function messageOf(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

const noServices: RoutableService[] = [];

export function AutoRoutingShowcase({
  services,
  protocolIDs = [],
  routes = [],
  isReady = false,
  onRouteSaved,
  onDirtyChange,
}: AutoRoutingShowcaseProps = {}) {
  const t = i18n.t.bind(i18n);
  const live = services !== undefined;
  const catalog = services ?? noServices;
  const initialProtocol = protocolIDs[0] ?? "openai.responses";
  const [protocol, setProtocol] = useState(initialProtocol);
  const [draft, setDraft] = useState<AutoDraft>(() =>
    draftFromRoute(autoRouteForProtocol(routes, initialProtocol), initialProtocol, catalog),
  );
  const [baseline, setBaseline] = useState(() =>
    draftSignature(
      draftFromRoute(autoRouteForProtocol(routes, initialProtocol), initialProtocol, catalog),
    ),
  );
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pendingProtocol, setPendingProtocol] = useState<string | null>(null);

  const dirty = draftSignature(draft) !== baseline;
  const persisted = autoRouteForProtocol(routes, protocol);
  const persistedKey = routes
    .filter(isAutoRoute)
    .map((route) => `${route.id}:${route.enabled}:${JSON.stringify(route.categories)}`)
    .join("|");
  const protocolKey = protocolIDs.join("|");

  useEffect(() => {
    onDirtyChange?.(dirty);
    return () => onDirtyChange?.(false);
  }, [dirty, onDirtyChange]);

  useEffect(() => {
    if (!live || dirty) return;
    const nextProtocol = protocolIDs.includes(protocol)
      ? protocol
      : (protocolIDs[0] ?? protocol);
    const next = draftFromRoute(
      autoRouteForProtocol(routes, nextProtocol),
      nextProtocol,
      catalog,
    );
    setProtocol(nextProtocol);
    setDraft(next);
    setBaseline(draftSignature(next));
    setError(null);
  }, [live, dirty, persistedKey, protocolKey, protocol, protocolIDs, routes, catalog]);

  const applyProtocol = (
    nextProtocol: string,
    nextRoutes: Route[],
    nextServices: RoutableService[],
  ) => {
    const next = draftFromRoute(
      autoRouteForProtocol(nextRoutes, nextProtocol),
      nextProtocol,
      nextServices,
    );
    setProtocol(nextProtocol);
    setDraft(next);
    setBaseline(draftSignature(next));
    setError(null);
  };

  const requestProtocolChange = (nextProtocol: string) => {
    if (nextProtocol === protocol) return;
    if (dirty) {
      setPendingProtocol(nextProtocol);
      return;
    }
    applyProtocol(nextProtocol, routes, catalog);
  };

  const updateCategory = (id: string, update: (current: CategoryDraft) => CategoryDraft) => {
    setDraft((current) => ({
      ...current,
      categories: current.categories.map((category) =>
        category.id === id ? update(category) : category,
      ),
    }));
  };

  const save = async () => {
    const issue = validateAutoDraft(draft, catalog);
    if (issue) {
      setError(issue);
      return;
    }
    const input = createInputFromAutoDraft(draft);
    setSaving(true);
    setError(null);
    try {
      const record = persisted
        ? await updateRoute(
            persisted.id,
            (await getRoute(persisted.id)).etag,
            {
              name: input.name,
              enabled: input.enabled,
              priority: input.priority,
              match: input.match,
              selection: input.selection,
              targets: null,
              categories: input.categories,
            },
          )
        : await createRoute(input);
      setBaseline(draftSignature(draft));
      onRouteSaved?.(record.route);
      notify.success(persisted ? i18n.t("auto.saved") : i18n.t("auto.enabled"));
    } catch (saveError) {
      setError(messageOf(saveError, i18n.t("auto.saveFailed")));
    } finally {
      setSaving(false);
    }
  };

  const servicesForProtocol = useMemo(
    () => compatibleServices(catalog, protocol),
    [catalog, protocol],
  );

  return (
    <div data-testid="auto-routing-showcase">
      <Panel className="p-4">
        <SectionKicker>{t("auto.publicModel")}</SectionKicker>
        <code className="mt-1.5 block truncate font-mono text-xl font-semibold tracking-tight text-primary">
          astrlink/auto
        </code>
        <p className="mt-2 max-w-[68ch] text-xs text-text-secondary">
          {t("auto.clientHint")}
        </p>
      </Panel>

      <ol aria-label={t("auto.flow")} className="mt-3 grid list-none grid-cols-3 gap-2 p-0 max-[720px]:grid-cols-1">
        {routingSteps.map((step, index) => (
          <li className="flex min-w-0 items-center gap-2.5 rounded-md border bg-card p-3" key={step.id}>
            <span aria-hidden="true" className="grid size-6 shrink-0 place-items-center rounded-sm border bg-muted text-micro font-medium tabular-nums">
              {String(index + 1).padStart(2, "0")}
            </span>
            <div className="flex min-w-0 flex-col gap-0.5">
              <strong className="text-sm font-medium">{t(step.labelKey)}</strong>
              <small className="truncate text-xs text-muted-foreground">{t(step.detailKey)}</small>
            </div>
          </li>
        ))}
      </ol>

      <section aria-labelledby="routing-categories-title" className="mt-5">
        <div className="mb-2 flex items-end justify-between gap-3 max-[720px]:flex-col max-[720px]:items-start">
          <div>
            <SectionKicker>{t("auto.taxonomy")}</SectionKicker>
            <h3
              className="mt-1 text-base font-semibold tracking-tight"
              id="routing-categories-title"
            >
              {t("auto.configurePools")}
            </h3>
          </div>
          {live ? (
            <div className="flex flex-wrap items-center gap-2">
              {protocolIDs.length > 1 ? (
                <Field className="min-w-[220px]" label={t("auto.entryProtocol")}>
                  <Select onValueChange={requestProtocolChange} value={protocol}>
                    <SelectTrigger className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {protocolIDs.map((id) => (
                        <SelectItem key={id} value={id}>
                          {protocolLabel(id)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
              ) : null}
              <Label className="flex items-center gap-2 text-xs font-normal text-text-secondary">
                <Switch
                  checked={draft.enabled}
                  disabled={!isReady}
                  onCheckedChange={(enabled) =>
                    setDraft((current) => ({ ...current, enabled }))
                  }
                />
                {t("auto.enableRouting")}
              </Label>
            </div>
          ) : (
            <small className="text-xs text-muted-foreground">taxonomy astrlink-text-v1</small>
          )}
        </div>

        {live && error ? (
          <FormMessage className="mb-2.5" tone="error">
            {error}
          </FormMessage>
        ) : null}

        {live && servicesForProtocol.length === 0 ? (
          <Panel className="p-3 text-xs text-text-secondary">
            {t("auto.needService")}
          </Panel>
        ) : (
          <div className="grid grid-cols-2 gap-2 max-[720px]:grid-cols-1">
            {autoCategories.map((category) => {
              const current = draft.categories.find((item) => item.id === category.id);
              const service = catalog.find((item) => item.id === current?.serviceId);
              const categoryLabel = t(category.labelKey);
              return (
                <Panel className="min-w-0 p-3" data-testid="routing-category" key={category.id}>
                  <code className="block truncate font-mono text-micro text-accent-foreground">
                    {category.id}
                  </code>
                  <h4 className="mt-1 text-sm font-medium">{categoryLabel}</h4>
                  <p className="mt-1 text-xs text-text-secondary">
                    {t(category.descriptionKey)}
                  </p>
                  {live && current ? (
                    <div className="mt-3 grid gap-2">
                      <Field label={t("auto.serviceFor", { category: categoryLabel })}>
                        <Select
                          onValueChange={(serviceId) => {
                            const nextService = catalog.find((item) => item.id === serviceId);
                            const nextModes = modesFor(nextService, protocol);
                            updateCategory(category.id, (item) => ({
                              ...item,
                              serviceId,
                              planType: nextModes.includes(item.planType)
                                ? item.planType
                                : (nextModes[0] ?? "native"),
                            }));
                          }}
                          value={current.serviceId}
                        >
                          <SelectTrigger className="w-full">
                            <SelectValue placeholder={t("auto.chooseService")} />
                          </SelectTrigger>
                          <SelectContent>
                            {servicesForProtocol.map((candidate) => (
                              <SelectItem key={candidate.id} value={candidate.id}>
                                {candidate.name}
                                {candidate.enabled ? "" : t("auto.disabledSuffix")}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </Field>
                      <Field label={t("auto.modelFor", { category: categoryLabel })}>
                        <Input
                          list={`auto-models-${category.id}`}
                          maxLength={256}
                          onChange={(event) =>
                            updateCategory(category.id, (item) => ({
                              ...item,
                              upstreamModel: event.target.value,
                            }))
                          }
                          placeholder={t("auto.chooseModel")}
                          value={current.upstreamModel}
                        />
                        <InputDatalist
                          id={`auto-models-${category.id}`}
                          options={service?.models ?? []}
                        />
                      </Field>
                      {current.upstreamModel ? (
                        <p className="flex min-w-0 items-center gap-1 truncate text-micro text-muted-foreground">
                          <ModelBrandIcon model={current.upstreamModel} />
                          <span className="truncate">{current.upstreamModel}</span>
                        </p>
                      ) : null}
                    </div>
                  ) : null}
                </Panel>
              );
            })}
          </div>
        )}

        {live ? (
          <div className="mt-3 flex justify-end">
            <Button
              disabled={!isReady || saving || servicesForProtocol.length === 0}
              onClick={() => void save()}
              type="button"
            >
              {saving ? t("common.saving") : persisted ? t("auto.save") : t("auto.enable")}
            </Button>
          </div>
        ) : null}
      </section>

      <ConfirmDialog
        confirmLabel={t("auto.switchProtocol")}
        description={t("auto.switchBody")}
        onCancel={() => setPendingProtocol(null)}
        onConfirm={() => {
          if (pendingProtocol) {
            applyProtocol(pendingProtocol, routes, catalog);
          }
          setPendingProtocol(null);
        }}
        open={pendingProtocol !== null}
        title={t("auto.discardTitle")}
      />
    </div>
  );
}
