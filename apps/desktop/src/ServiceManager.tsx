import { ServiceProxyFields } from "./components/ServiceProxyFields";
import {
  proxyDraft,
  proxyInput,
  validProxyDraft,
  type ProxyDraft,
} from "./service-proxy-model";
import { ServiceTestDialog } from "./ServiceTestDialog";
import { PricingWorkspace, ServiceBillingMeter } from "./PricingWorkspace";
import { useServiceOrder } from "./use-service-order";
import { ServiceOrderHelp } from "./ServiceOrderHelp";
import { OrderedList } from "./components/OrderedList";
import { useRoutingDefaults } from "./use-routing-defaults";
import { FailurePolicyEditor } from "./components/FailurePolicyEditor";
import { parseFailurePolicy, type FailurePolicy } from "./failure-policy-model";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Boxes,
  Flask,
  Connect as Cable,
  Menu as Ellipsis,
  Key as KeyRound,
  SquarePen as Pencil,
  Plus,
  RefreshCw,
  SlidersHorizontal,
} from "@/components/icons";

import { ChoiceCard } from "@/components/ChoiceCard";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { EmptyState } from "@/components/EmptyState";
import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { FormMessage } from "@/components/FormMessage";
import { Field } from "@/components/Field";
import { ListToolbar } from "@/components/ListToolbar";
import { IconButton } from "@/components/IconButton";
import { SegmentedControl } from "@/components/SegmentedControl";
import { CapabilityIndicator } from "@/components/CapabilityIndicator";
import { CapabilityToggle } from "@/components/CapabilityToggle";
import { ServiceListHeader, ServiceListRow } from "@/components/ServiceListRow";
import { Panel, PanelHeader } from "@/components/Panel";
import { DataRow } from "@/components/DataRow";
import { ServiceKindIcon } from "@/components/ServiceKindIcon";
import { ServiceKindLabel } from "@/components/ServiceKindLabel";
import { StatusDot } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Switch } from "@/components/ui/switch";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup } from "@/components/ui/radio-group";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";

import {
  beginServiceAuthorization,
  cancelServiceAuthorization,
  completeServiceAuthorization,
  createService,
  deleteService,
  getService,
  getServiceAuthorization,
  getServiceUsage,
  logoutService,
  resetServiceUsage,
  openAuthorizationURL,
  probeDraftServiceModels,
  probeServiceModels,
  updateService,
} from "./bridge";
import { copyButtonLabel, useCopyFeedback } from "./copy-feedback";
import { i18n, useT } from "./i18n";
import type { ConversionEngineCapability } from "./core-model";
import {
  conversionQualityLabels,
  codingPlanPresetIDs,
  httpServicePreset,
  httpServicePresetIDs,
  payAsYouGoPresetIDs,
  localConversionPassthrough,
  localConversionTargets,
  protocolDescriptors,
  protocolEntryPath,
  protocolLabel,
  supportsLocalConversion,
  type HTTPServicePresetID,
  type ProtocolDescriptor,
} from "./service-presets";
import { notify } from "./notify";
import { PageHeader } from "./PageHeader";
import { decodeModelEditorValue, encodeModelEditorValue } from "./model-editor";
import { filterModels } from "./model-groups";
import { ServiceModelsEditor } from "./ServiceModelsEditor";
import {
  responsesWebSocketEnabled,
  supportsResponsesWebSocket,
  serviceKindLabel,
  hasPlanUsage,
  isSubscriptionKind,
  serviceStatusLabel,
  type HTTPServiceKind,
  type ModelDiscoveryProtocol,
  type Service,
  type ServiceAuthScheme,
  type ServiceCapability,
  type ServiceCreateInput,
  type ServiceKind,
  type ServicePatchInput,
  type ServiceRecord,
  type SubscriptionServiceKind,
} from "./service-model";
import {
  SubscriptionUsageMeter,
  type SubscriptionUsageStatus,
} from "./SubscriptionUsageMeter";
import {
  type AuthorizationFlow,
  type AuthorizationSession,
  type SubscriptionProvider,
} from "./subscription-model";
import {
  formatSubscriptionUsageError,
  planTypeLabel,
  resetOutcomeMessage,
  type SubscriptionUsage,
} from "./subscription-usage-model";

export type ServiceManagerView =
  | { kind: "list" }
  | { kind: "create" }
  | { kind: "edit"; serviceId: string };

export type ServiceCatalogStatus = "blocked" | "loading" | "ready" | "error";

export interface ServiceManagerProps {
  isReady: boolean;
  protocols: ProtocolDescriptor[];
  conversionEngine?: ConversionEngineCapability | null;
  view: ServiceManagerView;
  services: Service[];
  catalogStatus: ServiceCatalogStatus;
  catalogError: string | null;
  onRefresh: () => void | Promise<void>;
  onViewChange: (view: ServiceManagerView) => void;
  onServiceSaved: (service: Service) => void;
  onServiceRemoved: (id: string) => void;
  onDirtyChange: (dirty: boolean) => void;
}

type Draft = {
  proxy: ProxyDraft;
  failurePolicy?: FailurePolicy;
  kind: ServiceKind;
  name: string;
  enabled: boolean;
  responsesWebSocket: boolean;
  baseURL: string;
  authScheme: ServiceAuthScheme;
  headerName: string;
  secret: string;
  removeCredential: boolean;
  models: string[];
  capabilities: ServiceCapability[];
  authorizationFlow: AuthorizationFlow | null;
};

type ConfirmAction =
  | { kind: "delete"; service: Service }
  | { kind: "logout"; service: Service }
  | { kind: "reset-usage"; service: Service; availableCount: number }
  | null;

type AuthorizationDialog = {
  service: Service;
  requestedFlow: AuthorizationFlow;
  session: AuthorizationSession;
};

type EditorTab = "connection" | "models" | "protocols" | "failure";
type ServiceFilter = "all" | "enabled" | "disabled";

function serviceTypeOptionLabel(kind: ServiceKind): string {
  if (kind === "codex_subscription") return i18n.t("services.codexKind");
  if (kind === "grok_subscription") return i18n.t("services.grokKind");
  return serviceKindLabel(kind);
}

/** The only login transport a single-flow provider offers; null when the user must pick. */
function defaultAuthorizationFlow(kind: ServiceKind): AuthorizationFlow | null {
  if (kind === "claude_subscription") return "authorization_code";
  if (kind === "grok_subscription") return "device_code";
  return null;
}

function subscriptionDefaultName(kind: SubscriptionServiceKind): string {
  if (kind === "claude_subscription") return "Claude Code";
  if (kind === "grok_subscription") return i18n.t("services.grokName");
  return i18n.t("services.codexName");
}

function subscriptionKindHint(kind: SubscriptionServiceKind): string {
  if (kind === "claude_subscription") return i18n.t("services.claudeOauthHint");
  if (kind === "grok_subscription") return i18n.t("services.grokHint");
  return i18n.t("services.codexHint");
}

function subscriptionOauthLabel(kind: ServiceKind): string {
  if (kind === "claude_subscription") return "Claude Code OAuth";
  if (kind === "grok_subscription") return i18n.t("services.xaiGrokOauth");
  return i18n.t("services.openaiCodexOauth");
}

function subscriptionAccountLabel(kind: ServiceKind, hint: string): string {
  if (kind === "claude_subscription")
    return i18n.t("services.claudeAccount", { hint });
  if (kind === "grok_subscription")
    return i18n.t("services.xaiAccount", { hint });
  return i18n.t("services.openaiAccount", { hint });
}

function deviceCodeDescription(
  provider: SubscriptionProvider | undefined,
): string {
  return provider === "xai_grok"
    ? i18n.t("services.grokDeviceCodeDescription")
    : i18n.t("services.deviceCodeDescription");
}

function mergeDiscoveredServiceModels(
  current: { models: readonly string[] },
  discovered: readonly string[],
): string[] | null {
  const models = [...new Set([...current.models, ...discovered])].sort();
  if (models.length > 2_000) return null;
  return models;
}

// Applying the preview replaces the whole allowlist, so a service that already
// has models must open with only those checked — never the fresh discoveries.
function initialModelPreviewSelection(
  current: readonly string[],
  preview: readonly string[],
): string[] {
  if (current.length === 0) return [...preview];
  const allowed = new Set(preview);
  return current.filter((model) => allowed.has(model)).sort();
}

type ModelPreview = {
  models: string[];
  selected: string[];
  warnings: string[];
};

const authLabels: Record<ServiceAuthScheme, string> = {
  get none() {
    return i18n.t("services.authNone");
  },
  get bearer() {
    return i18n.t("services.authBearer");
  },
  get anthropic_api_key() {
    return i18n.t("services.authAnthropic");
  },
  get google_api_key() {
    return i18n.t("services.authGoogle");
  },
  get custom_header() {
    return i18n.t("services.authCustomHeader");
  },
};

function draftForKind(
  kind: ServiceKind,
  protocols: readonly ProtocolDescriptor[],
): Draft {
  if (isSubscriptionKind(kind)) {
    return {
      kind,
      name: subscriptionDefaultName(kind),
      enabled: true,
      responsesWebSocket: kind === "codex_subscription",
      baseURL: "",
      authScheme: "none",
      headerName: "",
      secret: "",
      removeCredential: false,
      proxy: proxyDraft(),
      models: [],
      capabilities: [],
      authorizationFlow: defaultAuthorizationFlow(kind),
    };
  }
  const preset = httpServicePreset(kind as HTTPServicePresetID, protocols);
  return {
    kind,
    name: preset.defaultName,
    enabled: true,
    responsesWebSocket: false,
    baseURL: preset.baseURL,
    authScheme: preset.authScheme,
    headerName: preset.headerName,
    secret: "",
    removeCredential: false,
    proxy: proxyDraft(),
    models: [...(preset.models ?? [])],
    capabilities: preset.capabilities.map((capability) => ({ ...capability })),
    authorizationFlow: null,
  };
}

function draftFromRecord(record: ServiceRecord): Draft {
  const { service } = record;
  if (isSubscriptionKind(service.kind)) {
    return {
      ...draftForKind(service.kind, []),
      name: service.name,
      proxy: proxyDraft(service.proxy),
      failurePolicy: service.failure_policy,
      enabled: service.enabled,
      responsesWebSocket: responsesWebSocketEnabled(service),
      models: [...service.models],
    };
  }
  if (!service.http) throw new Error(i18n.t("services.missingHttp"));
  return {
    kind: service.kind,
    name: service.name,
    proxy: proxyDraft(service.proxy),
    failurePolicy: service.failure_policy,
    enabled: service.enabled,
    responsesWebSocket: responsesWebSocketEnabled(service),
    baseURL: service.http.base_url,
    authScheme: service.http.auth.scheme,
    headerName: service.http.auth.header_name ?? "",
    secret: "",
    removeCredential: false,
    models: [...service.models],
    authorizationFlow: null,
    capabilities: service.capabilities.map((capability) =>
      wireCapability(capability),
    ),
  };
}

function wireCapability(capability: ServiceCapability): ServiceCapability {
  return {
    protocol: capability.protocol,
    mode: "native",
    streaming: capability.streaming,
    ...(capability.convert_to ? { convert_to: capability.convert_to } : {}),
  };
}

function draftSignature(draft: Draft): string {
  return JSON.stringify(draft);
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

function authForDraft(draft: Draft) {
  return draft.authScheme === "custom_header"
    ? { scheme: draft.authScheme, header_name: draft.headerName.trim() }
    : { scheme: draft.authScheme };
}

function validateDraft(
  draft: Draft,
  editing: ServiceRecord | null,
): string | null {
  if (!validProxyDraft(draft.proxy)) return i18n.t("serviceProxy.invalid");
  if (draft.name.trim().length === 0 || [...draft.name.trim()].length > 128) {
    return i18n.t("services.nameInvalid");
  }
  if (draft.models.length > 2_000) {
    return i18n.t("services.tooManyModels");
  }
  if (
    draft.models.some(
      (model) => [...model].length < 1 || [...model].length > 256,
    ) ||
    new Set(draft.models).size !== draft.models.length
  ) {
    return i18n.t("services.modelIdsInvalid");
  }
  if (isSubscriptionKind(draft.kind)) {
    if (!editing && draft.authorizationFlow === null) {
      return i18n.t("services.chooseLogin");
    }
    return null;
  }
  let parsed: URL;
  try {
    parsed = new URL(draft.baseURL.trim());
  } catch {
    return i18n.t("services.invalidUrl");
  }
  if (
    (parsed.protocol !== "http:" && parsed.protocol !== "https:") ||
    parsed.username !== "" ||
    parsed.password !== "" ||
    parsed.search !== "" ||
    parsed.hash !== ""
  ) {
    return i18n.t("services.urlRules");
  }
  if (draft.authScheme === "custom_header" && draft.headerName.trim() === "") {
    return i18n.t("services.headerRequired");
  }
  const hasStoredCredential = Boolean(editing?.service.http?.credential_ref);
  if (
    draft.authScheme !== "none" &&
    draft.secret.trim() === "" &&
    !hasStoredCredential
  ) {
    return i18n.t("services.keyRequired");
  }
  if (draft.capabilities.length === 0) {
    return i18n.t("services.capabilityRequired");
  }
  return null;
}

function serviceDot(
  service: Service,
): "positive" | "pending" | "negative" | "neutral" {
  if (!service.enabled) return "neutral";
  const status = service.subscription?.status;
  if (!status || status === "connected") return "positive";
  if (status === "authorizing" || status === "disconnected") return "pending";
  return "negative";
}

function ModelPreviewDialog({
  preview,
  query,
  onQueryChange,
  onSelectedChange,
  onApply,
  onClose,
}: {
  preview: ModelPreview;
  query: string;
  onQueryChange: (value: string) => void;
  onSelectedChange: (selected: string[]) => void;
  onApply: () => void;
  onClose: () => void;
}) {
  const t = useT();
  const filtered = useMemo(
    () => filterModels(preview.models, query),
    [preview.models, query],
  );
  const selectedSet = useMemo(
    () => new Set(preview.selected),
    [preview.selected],
  );
  const filteredSelectedCount = filtered.reduce(
    (count, model) => count + (selectedSet.has(model) ? 1 : 0),
    0,
  );
  const hasQuery = query.trim().length > 0;

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="w-[min(620px,calc(100vw-40px))] max-w-none sm:max-w-none">
        <DialogHeader>
          <DialogTitle>{t("services.selectModelsTitle")}</DialogTitle>
          <DialogDescription>
            {t("services.selectModelsHint")}
          </DialogDescription>
        </DialogHeader>
        {preview.warnings.length > 0 ? (
          <FormMessage tone="warning">
            {t("services.partialFetchFailed", {
              warnings: preview.warnings.join("；"),
            })}
          </FormMessage>
        ) : null}
        {preview.models.length > 0 ? (
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <Input
              className="h-8 min-w-0 flex-[1_1_160px]"
              aria-label={t("services.searchUpstream")}
              placeholder={t("services.searchModels")}
              type="search"
              value={query}
              onChange={(event) => onQueryChange(event.target.value)}
            />
            <div className="ml-auto flex flex-wrap items-center gap-2">
              <Button
                variant="outline"
                disabled={filtered.length === 0}
                onClick={() =>
                  onSelectedChange(
                    [...new Set([...preview.selected, ...filtered])].sort(),
                  )
                }
                type="button"
              >
                {hasQuery
                  ? t("services.selectAllMatches", { count: filtered.length })
                  : t("services.selectAll")}
              </Button>
              <Button
                variant="outline"
                disabled={filteredSelectedCount === 0}
                onClick={() => {
                  if (!hasQuery) {
                    onSelectedChange([]);
                    return;
                  }
                  const drop = new Set(filtered);
                  onSelectedChange(
                    preview.selected.filter((model) => !drop.has(model)),
                  );
                }}
                type="button"
              >
                {hasQuery
                  ? t("services.clearMatches")
                  : t("services.selectNone")}
              </Button>
            </div>
          </div>
        ) : null}
        <div className="my-2 grid max-h-[min(52vh,460px)] gap-1 overflow-auto">
          {preview.models.length === 0 ? (
            <p>{t("services.emptyUpstream")}</p>
          ) : filtered.length === 0 ? (
            <p>{t("models.noMatch", { query: query.trim() })}</p>
          ) : (
            filtered.map((model) => (
              <Label
                className="flex items-center gap-2 rounded-lg border px-2 py-1.5"
                key={model}
              >
                <Checkbox
                  checked={selectedSet.has(model)}
                  onCheckedChange={(checked) => {
                    const selected =
                      checked === true
                        ? [...new Set([...preview.selected, model])].sort()
                        : preview.selected.filter((item) => item !== model);
                    onSelectedChange(selected);
                  }}
                />
                <ModelBrandIcon model={model} />
                <code className="min-w-0 truncate font-mono text-xs">
                  {encodeModelEditorValue(model)}
                </code>
              </Label>
            ))
          )}
        </div>
        <small className="mb-2 block text-xs text-muted-foreground">
          {t("services.selectedCount", {
            selected: preview.selected.length,
            total: preview.models.length,
          })}
          {hasQuery
            ? t("services.showingFiltered", {
                shown: filtered.length,
                total: preview.models.length,
              })
            : ""}
        </small>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} type="button">
            {t("common.cancel")}
          </Button>
          <Button onClick={onApply} type="button">
            {t("services.applySelected", { count: preview.selected.length })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function ServiceManager({
  isReady,
  protocols,
  conversionEngine = null,
  view,
  services,
  catalogStatus,
  catalogError,
  onRefresh,
  onViewChange,
  onServiceSaved,
  onServiceRemoved,
  onDirtyChange,
}: ServiceManagerProps) {
  const routingDefaults = useRoutingDefaults(isReady);
  const serviceOrder = useServiceOrder(
    services,
    isReady && view.kind === "list" && catalogStatus === "ready",
    onRefresh,
  );
  const t = useT();
  const descriptors = useMemo(
    () => protocolDescriptors(protocols),
    [protocols],
  );
  const [query, setQuery] = useState("");
  const [modelQuery, setModelQuery] = useState("");
  const [serviceFilter, setServiceFilter] = useState<ServiceFilter>("all");
  const [draft, setDraft] = useState<Draft>(() =>
    draftForKind("codex_subscription", protocols),
  );
  const [editing, setEditing] = useState<ServiceRecord | null>(null);
  const [baseline, setBaseline] = useState<string | null>(null);
  const [loadingRecord, setLoadingRecord] = useState(false);
  const [saving, setSaving] = useState(false);
  const [actionID, setActionID] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirmAction, setConfirmAction] = useState<ConfirmAction>(null);
  const [loginChoice, setLoginChoice] = useState<Service | null>(null);
  const [loginChoiceFlow, setLoginChoiceFlow] =
    useState<AuthorizationFlow | null>(null);
  const [authorizationDialog, setAuthorizationDialog] =
    useState<AuthorizationDialog | null>(null);
  const [authorizationCode, setAuthorizationCode] = useState("");
  useEffect(
    () => setAuthorizationCode(""),
    [authorizationDialog?.session.id, authorizationDialog?.session.status],
  );
  const [modelEditor, setModelEditor] = useState("");
  const [probingModels, setProbingModels] = useState(false);
  const [modelPreview, setModelPreview] = useState<ModelPreview | null>(null);
  const [modelPreviewQuery, setModelPreviewQuery] = useState("");
  const [editorTab, setEditorTab] = useState<EditorTab>("connection");
  const [usageByService, setUsageByService] = useState<
    Record<
      string,
      {
        status: SubscriptionUsageStatus;
        usage?: SubscriptionUsage;
        error?: string;
      }
    >
  >({});
  const [usageEpoch, setUsageEpoch] = useState(0);
  const [testingService, setTestingService] = useState<Service | null>(null);
  const [billingService, setBillingService] = useState<string | null>(null);
  const copyFeedback = useCopyFeedback();
  const loadGeneration = useRef(0);
  const usageGeneration = useRef(0);
  const importedAfterLogin = useRef(new Set<string>());
  const protocolsRef = useRef(protocols);
  protocolsRef.current = protocols;
  const viewKind = view.kind;
  const editingServiceID = view.kind === "edit" ? view.serviceId : null;
  const connectedUsageIDs = useMemo(
    () =>
      services
        .filter((service) => hasPlanUsage(service))
        .map((service) => service.id)
        .sort()
        .join("\0"),
    [services],
  );

  useEffect(() => {
    if (view.kind !== "list" || !isReady) return;
    const ids = connectedUsageIDs === "" ? [] : connectedUsageIDs.split("\0");
    const generation = usageGeneration.current + 1;
    usageGeneration.current = generation;
    setUsageByService((current) => {
      const next: Record<
        string,
        {
          status: SubscriptionUsageStatus;
          usage?: SubscriptionUsage;
          error?: string;
        }
      > = {};
      for (const id of ids) {
        next[id] = { status: "loading", usage: current[id]?.usage };
      }
      return next;
    });
    if (ids.length === 0) return;
    void Promise.all(
      ids.map(async (id) => {
        try {
          const usage = await getServiceUsage(id);
          if (usageGeneration.current !== generation) return;
          setUsageByService((current) => ({
            ...current,
            [id]: { status: "ready", usage },
          }));
        } catch (cause) {
          const message = formatSubscriptionUsageError(cause);
          console.error(
            "AstrLink failed to load subscription usage",
            id,
            cause,
          );
          if (usageGeneration.current !== generation) return;
          setUsageByService((current) => ({
            ...current,
            [id]: {
              status: "error",
              usage: current[id]?.usage,
              error: message,
            },
          }));
        }
      }),
    );
  }, [connectedUsageIDs, isReady, usageEpoch, view.kind]);

  const dirty =
    view.kind !== "list" &&
    baseline !== null &&
    draftSignature(draft) !== baseline;

  useEffect(() => {
    onDirtyChange(dirty);
  }, [dirty, onDirtyChange]);

  useEffect(() => {
    const generation = loadGeneration.current + 1;
    loadGeneration.current = generation;
    setError(null);
    setModelEditor("");
    setModelPreview(null);
    setModelPreviewQuery("");
    setEditorTab("connection");
    if (view.kind === "list") {
      setEditing(null);
      setBaseline(null);
      setLoadingRecord(false);
      return;
    }
    if (view.kind === "create") {
      const next = draftForKind("codex_subscription", protocolsRef.current);
      setDraft(next);
      setEditing(null);
      setBaseline(draftSignature(next));
      setLoadingRecord(false);
      return;
    }
    setLoadingRecord(true);
    void getService(view.serviceId)
      .then((record) => {
        if (loadGeneration.current !== generation) return;
        const next = draftFromRecord(record);
        setEditing(record);
        setDraft(next);
        setBaseline(draftSignature(next));
      })
      .catch((cause) => {
        if (loadGeneration.current !== generation) return;
        setEditing(null);
        setError(errorMessage(cause, t("services.readFailed")));
      })
      .finally(() => {
        if (loadGeneration.current === generation) setLoadingRecord(false);
      });
  }, [editingServiceID, t, viewKind]);

  const importCodexModelsAfterLogin = useCallback(
    async (service: Service) => {
      if (importedAfterLogin.current.has(service.id)) return;
      importedAfterLogin.current.add(service.id);
      try {
        const [record, probe] = await Promise.all([
          getService(service.id),
          probeServiceModels(service.id, "openai.models"),
        ]);
        const merged = mergeDiscoveredServiceModels(
          { models: record.service.models },
          probe.model_ids,
        );
        if (!merged) {
          notify.error(t("services.loggedInTooMany", { name: service.name }));
          return;
        }
        const unchanged =
          merged.join("\0") === record.service.models.join("\0");
        if (!unchanged) {
          await updateService(service.id, record.etag, {
            models: merged,
          });
        }
        notify.success(
          probe.model_ids.length === 0
            ? t("services.loggedInNone", { name: service.name })
            : t("services.loggedInFetched", {
                name: service.name,
                failurePolicy: service.failure_policy,
                count: merged.length,
              }),
        );
        await onRefresh();
      } catch (cause) {
        importedAfterLogin.current.delete(service.id);
        notify.warning(
          errorMessage(
            cause,
            t("services.loggedInFetchFailed", { name: service.name }),
          ),
        );
      }
    },
    [onRefresh, t],
  );

  useEffect(() => {
    const authorizing = services.filter(
      (service) => service.subscription?.status === "authorizing",
    );
    if (!isReady || authorizing.length === 0) return;
    let cancelled = false;
    const check = async () => {
      let completed = false;
      const connected: Service[] = [];
      await Promise.all(
        authorizing.map(async (service) => {
          try {
            const session = await getServiceAuthorization(service.id);
            if (session.status === "completed") {
              completed = true;
              connected.push(service);
            } else if (session.status !== "pending") {
              completed = true;
            }
          } catch {
            completed = true;
          }
        }),
      );
      if (cancelled) return;
      if (completed) await onRefresh();
      for (const service of connected) {
        if (cancelled) return;
        await importCodexModelsAfterLogin(service);
      }
    };
    const timer = window.setInterval(() => void check(), 1_500);
    void check();
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [importCodexModelsAfterLogin, isReady, onRefresh, services]);

  useEffect(() => {
    const active = authorizationDialog;
    if (!isReady || active === null || active.session.status !== "pending") {
      return;
    }
    let stopped = false;
    const check = async () => {
      try {
        const session = await getServiceAuthorization(active.service.id);
        if (stopped) return;
        if (session.status === "completed") {
          setAuthorizationDialog(null);
          await onRefresh();
          await importCodexModelsAfterLogin(active.service);
          return;
        }
        setAuthorizationDialog((current) =>
          current?.service.id === active.service.id
            ? { ...current, session }
            : current,
        );
        if (session.status !== "pending") await onRefresh();
      } catch (cause) {
        if (!stopped) {
          setError(errorMessage(cause, t("services.authStatusFailed")));
        }
      }
    };
    const timer = window.setInterval(() => void check(), 1_500);
    void check();
    return () => {
      stopped = true;
      window.clearInterval(timer);
    };
  }, [
    authorizationDialog?.service.id,
    authorizationDialog?.session.status,
    importCodexModelsAfterLogin,
    isReady,
    onRefresh,
    t,
  ]);

  const selectKind = (kind: ServiceKind) => {
    const next = draftForKind(kind, protocols);
    setDraft(next);
    setEditorTab((current) =>
      isSubscriptionKind(kind) && current === "protocols"
        ? "connection"
        : current,
    );
    setError(null);
  };

  const setConvertTo = (protocol: string, value: string) => {
    setDraft((current) => ({
      ...current,
      capabilities: current.capabilities.map((item) =>
        item.protocol === protocol
          ? {
              protocol: item.protocol,
              mode: "native",
              streaming: item.streaming,
              ...(value !== localConversionPassthrough
                ? { convert_to: value }
                : {}),
            }
          : item,
      ),
    }));
  };

  const toggleCapability = (
    descriptor: ProtocolDescriptor,
    checked: boolean,
  ) => {
    setDraft((current) => {
      if (!checked) {
        return {
          ...current,
          capabilities: current.capabilities.filter(
            (capability) => capability.protocol !== descriptor.id,
          ),
        };
      }
      if (
        current.capabilities.some(
          (capability) => capability.protocol === descriptor.id,
        )
      ) {
        return current;
      }
      return {
        ...current,
        capabilities: [
          ...current.capabilities,
          {
            protocol: descriptor.id,
            mode: "native",
            streaming: descriptor.streaming,
          },
        ],
      };
    });
  };

  const addModels = () => {
    const additions = modelEditor
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter(Boolean)
      .map(decodeModelEditorValue);
    if (additions.length === 0) {
      setError(t("services.needModelIds"));
      return;
    }
    if (additions.some((model) => [...model].length > 256)) {
      setError(t("services.modelIdTooLong"));
      return;
    }
    const models = [...new Set([...draft.models, ...additions])].sort();
    if (models.length > 2_000) {
      setError(t("services.tooManyModels"));
      return;
    }
    setDraft((current) => ({
      ...current,
      models,
    }));
    setModelEditor("");
    setError(null);
  };

  const discoverModels = async () => {
    if (isSubscriptionKind(draft.kind) && !editing) {
      setError(t("services.saveBeforeFetch"));
      return;
    }
    const discoveryProtocols: ModelDiscoveryProtocol[] = isSubscriptionKind(
      draft.kind,
    )
      ? ["openai.models"]
      : (["openai.models", "google.models"] as const).filter((protocol) =>
          draft.capabilities.some(
            (capability) => capability.protocol === protocol,
          ),
        );
    if (discoveryProtocols.length === 0) {
      setError(t("services.enableDiscovery"));
      return;
    }
    setProbingModels(true);
    setError(null);
    try {
      const attempts = await Promise.allSettled(
        discoveryProtocols.map((protocol) => {
          if (isSubscriptionKind(draft.kind)) {
            return probeServiceModels(editing!.service.id, protocol);
          }
          return probeDraftServiceModels({
            ...(draft.proxy.mode !== "inherit" || editing?.service.proxy
              ? { proxy: proxyInput(draft.proxy) }
              : {}),
            ...(editing ? { service_id: editing.service.id } : {}),
            kind: draft.kind as HTTPServiceKind,
            http: {
              base_url: draft.baseURL.trim(),
              auth: authForDraft(draft),
              ...(draft.secret.trim()
                ? { credential: { secret: draft.secret } }
                : {}),
            },
            protocol,
          });
        }),
      );
      const discovered: string[] = [];
      const warnings: string[] = [];
      attempts.forEach((attempt, index) => {
        if (attempt.status === "fulfilled") {
          discovered.push(...attempt.value.model_ids);
        } else {
          warnings.push(
            `${protocolLabel(discoveryProtocols[index] ?? "models")}：${errorMessage(attempt.reason, t("services.fetchFailed"))}`,
          );
        }
      });
      if (warnings.length === attempts.length) {
        setError(
          t("services.fetchFailedDetail", { warnings: warnings.join("；") }),
        );
        return;
      }
      const models = [...new Set([...draft.models, ...discovered])].sort();
      if (models.length > 2_000) {
        setError(t("services.mergeTooMany"));
        return;
      }
      setModelPreviewQuery("");
      setModelPreview({
        models,
        selected: initialModelPreviewSelection(draft.models, models),
        warnings,
      });
    } finally {
      setProbingModels(false);
    }
  };

  const removeDraftModels = (removals: string[]) => {
    if (removals.length === 0) return;
    const drop = new Set(removals);
    setDraft((current) => ({
      ...current,
      models: current.models.filter((model) => !drop.has(model)),
    }));
  };

  const presentAuthorization = (
    service: Service,
    requestedFlow: AuthorizationFlow,
    session: AuthorizationSession,
  ) => {
    if (session.flow === "authorization_code") {
      setAuthorizationDialog({ service, requestedFlow, session });
      return;
    }
    if (session.flow === "device_code") {
      setAuthorizationDialog({ service, requestedFlow, session });
      notify.success(
        requestedFlow === "browser"
          ? t("services.portsBusy")
          : t("services.deviceStarted"),
      );
      return;
    }
    setAuthorizationDialog(null);
    notify.success(t("services.browserOpened", { name: service.name }));
  };

  const submit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    try {
      if (draft.failurePolicy) parseFailurePolicy(draft.failurePolicy);
    } catch {
      setError(t("failure.invalid"));
      return;
    }
    const issue = validateDraft(draft, editing);
    if (issue) {
      setError(issue);
      return;
    }
    setSaving(true);
    setError(null);
    try {
      let record: ServiceRecord;
      if (editing) {
        const patch: ServicePatchInput = {
          ...(draft.proxy.mode !== "inherit" || editing?.service.proxy
            ? { proxy: proxyInput(draft.proxy) }
            : {}),
          name: draft.name.trim(),
          enabled: draft.enabled,
          responses_websocket_enabled: draft.responsesWebSocket,
          models: draft.models,
          failure_policy: draft.failurePolicy ?? null,
        };
        if (!isSubscriptionKind(draft.kind)) {
          patch.http = {
            base_url: draft.baseURL.trim(),
            auth: authForDraft(draft),
            ...(draft.secret.trim()
              ? { credential: { secret: draft.secret } }
              : draft.removeCredential
                ? { credential: null }
                : {}),
          };
          patch.capabilities = draft.capabilities.map(wireCapability);
        }
        record = await updateService(editing.service.id, editing.etag, patch);
        notify.success(t("services.updated"));
      } else {
        let input: ServiceCreateInput;
        if (isSubscriptionKind(draft.kind)) {
          input = {
            ...(draft.proxy.mode !== "inherit"
              ? { proxy: proxyInput(draft.proxy) }
              : {}),
            name: draft.name.trim(),
            kind: draft.kind,
            enabled: draft.enabled,
            responses_websocket_enabled: draft.responsesWebSocket,
            models: draft.models,
            ...(draft.failurePolicy
              ? { failure_policy: draft.failurePolicy }
              : {}),
          };
        } else {
          input = {
            ...(draft.proxy.mode !== "inherit"
              ? { proxy: proxyInput(draft.proxy) }
              : {}),
            name: draft.name.trim(),
            kind: draft.kind as HTTPServiceKind,
            enabled: draft.enabled,
            responses_websocket_enabled: draft.responsesWebSocket,
            models: draft.models,
            ...(draft.failurePolicy
              ? { failure_policy: draft.failurePolicy }
              : {}),
            http: {
              base_url: draft.baseURL.trim(),
              auth: authForDraft(draft),
              ...(draft.secret.trim()
                ? { credential: { secret: draft.secret } }
                : {}),
            },
            capabilities: draft.capabilities.map(wireCapability),
          };
        }
        record = await createService(input);
        if (isSubscriptionKind(record.service.kind)) {
          try {
            const flow = draft.authorizationFlow;
            if (flow === null) {
              throw new Error(t("services.chooseLogin"));
            }
            importedAfterLogin.current.delete(record.service.id);
            const authorization = await beginServiceAuthorization(
              record.service.id,
              flow,
            );
            presentAuthorization(record.service, flow, authorization.session);
          } catch (cause) {
            notify.success(t("services.addedLaterLogin"));
            setError(errorMessage(cause, t("services.addedOauthFailed")));
          }
        } else {
          notify.success(t("services.addedKey"));
        }
      }
      onServiceSaved(record.service);
      setEditing(null);
      setBaseline(null);
      onDirtyChange(false);
      onViewChange({ kind: "list" });
      await onRefresh();
    } catch (cause) {
      setError(errorMessage(cause, t("services.saveFailed")));
    } finally {
      setSaving(false);
      setDraft((current) => ({ ...current, secret: "" }));
    }
  };

  const authorize = async (service: Service, flow: AuthorizationFlow) => {
    setLoginChoice(null);
    setLoginChoiceFlow(null);
    setActionID(service.id);
    setError(null);
    try {
      importedAfterLogin.current.delete(service.id);
      const result = await beginServiceAuthorization(service.id, flow);
      presentAuthorization(service, flow, result.session);
      await onRefresh();
    } catch (cause) {
      setError(errorMessage(cause, t("services.beginLoginFailed")));
    } finally {
      setActionID(null);
    }
  };

  const cancelAuthorization = async (service: Service) => {
    setActionID(service.id);
    setError(null);
    try {
      await cancelServiceAuthorization(service.id);
      setAuthorizationDialog((current) =>
        current?.service.id === service.id ? null : current,
      );
      notify.success(t("services.cancelledLogin", { name: service.name }));
      await onRefresh();
    } catch (cause) {
      setError(errorMessage(cause, t("services.cancelLoginFailed")));
    } finally {
      setActionID(null);
    }
  };

  const showAuthorization = async (service: Service) => {
    setActionID(service.id);
    setError(null);
    try {
      const session = await getServiceAuthorization(service.id);
      if (
        session.flow === "device_code" ||
        session.flow === "authorization_code"
      ) {
        setAuthorizationDialog({
          service,
          requestedFlow: session.flow,
          session,
        });
      } else {
        notify.success(t("services.waitingCallback", { name: service.name }));
      }
    } catch (cause) {
      setError(errorMessage(cause, t("services.authStatusFailed")));
    } finally {
      setActionID(null);
    }
  };

  const reopenAuthorizationPage = async () => {
    const url =
      authorizationDialog?.session.device_code?.verification_url ??
      authorizationDialog?.session.authorization_url;
    if (!url) return;
    setError(null);
    try {
      await openAuthorizationURL(url);
    } catch (cause) {
      setError(errorMessage(cause, t("services.openDeviceFailed")));
    }
  };

  const toggleEnabled = async (service: Service) => {
    setActionID(service.id);
    setError(null);
    try {
      const record = await getService(service.id);
      const updated = await updateService(service.id, record.etag, {
        enabled: !record.service.enabled,
      });
      onServiceSaved(updated.service);
      notify.success(
        updated.service.enabled
          ? t("services.enabledToast")
          : t("services.disabledToast"),
      );
      await onRefresh();
    } catch (cause) {
      setError(errorMessage(cause, t("services.statusFailed")));
    } finally {
      setActionID(null);
    }
  };

  const confirmDestructiveAction = async () => {
    if (!confirmAction) return;
    const { service } = confirmAction;
    setConfirmAction(null);
    setActionID(service.id);
    setError(null);
    try {
      if (confirmAction.kind === "reset-usage") {
        const result = await resetServiceUsage(service.id);
        notify.success(resetOutcomeMessage(result.outcome));
        setUsageEpoch((current) => current + 1);
        return;
      }
      if (confirmAction.kind === "logout") {
        const record = await logoutService(service.id);
        onServiceSaved(record.service);
        notify.success(t("services.loggedOut", { name: service.name }));
      } else {
        const record = await getService(service.id);
        await deleteService(service.id, record.etag);
        onServiceRemoved(service.id);
        notify.success(t("services.deleted", { name: service.name }));
      }
      await onRefresh();
    } catch (cause) {
      if (confirmAction.kind === "reset-usage") {
        console.error(
          "AstrLink failed to reset subscription usage",
          service.id,
          cause,
        );
        notify.error(formatSubscriptionUsageError(cause));
        return;
      }
      setError(
        errorMessage(
          cause,
          confirmAction.kind === "logout"
            ? t("services.logoutFailed")
            : t("services.deleteFailed"),
        ),
      );
    } finally {
      setActionID(null);
    }
  };

  if (view.kind === "list") {
    const search = query.trim().toLocaleLowerCase();
    const modelSearch = modelQuery.trim().toLocaleLowerCase();
    const filtered = !!search || !!modelSearch || serviceFilter !== "all";
    const enabledCount = services.filter((service) => service.enabled).length;
    const visibleServices = serviceOrder.ordered.filter(
      (service) =>
        (serviceFilter === "all" ||
          service.enabled === (serviceFilter === "enabled")) &&
        (!modelSearch ||
          service.models.some((model) =>
            model.toLocaleLowerCase().includes(modelSearch),
          )) &&
        [
          service.name,
          serviceKindLabel(service.kind),
          service.http?.base_url,
          service.subscription?.account_hint,
        ]
          .filter(Boolean)
          .join(" ")
          .toLocaleLowerCase()
          .includes(search),
    );
    const busy = catalogStatus === "loading";
    return (
      <section
        className="@container flex min-h-0 w-full min-w-0 flex-1 flex-col overflow-hidden"
        aria-labelledby="service-heading"
      >
        {billingService !== null ? (
          <PricingWorkspace
            services={services}
            initialServiceId={billingService}
            onClose={() => setBillingService(null)}
          />
        ) : null}
        <PageHeader
          variant="compact"
          className="@max-[360px]:gap-2"
          actions={
            <>
              <ServiceOrderHelp ready={isReady && catalogStatus === "ready"}>
                <p>{t("services.orderHint")}</p>
                {filtered ? (
                  <p className="mt-2">{t("services.orderFiltered")}</p>
                ) : null}
                {routingDefaults.loaded &&
                !routingDefaults.allow_unmatched_failover ? (
                  <p className="mt-2">{t("failure.globalOffHint")}</p>
                ) : null}
              </ServiceOrderHelp>
              <IconButton
                label={
                  busy ? t("common.refreshing") : t("services.refreshList")
                }
                disabled={!isReady || busy}
                onClick={() => {
                  setUsageEpoch((value) => value + 1);
                  void onRefresh();
                }}
                size="icon"
                type="button"
              >
                <RefreshCw
                  aria-hidden="true"
                  className={cn(
                    "motion-reduce:animate-none",
                    busy && "animate-spin motion-reduce:animate-none",
                  )}
                />
              </IconButton>
              <Button
                aria-label={t("services.add")}
                disabled={!isReady || busy}
                onClick={() => onViewChange({ kind: "create" })}
                size="sm"
                type="button"
              >
                <Plus aria-hidden="true" />
                <span className="@max-[480px]:hidden">{t("services.add")}</span>
                <span className="hidden @max-[480px]:inline">
                  {t("overview.add")}
                </span>
              </Button>
            </>
          }
          title={t("services.title")}
          titleId="service-heading"
        />
        {!isReady || catalogStatus === "blocked" ? (
          <FormMessage className="mb-3" tone="notice">
            {t("services.gatewayNotReady")}
          </FormMessage>
        ) : null}
        {catalogStatus === "error" && catalogError ? (
          <FormMessage className="mb-3" tone="error">
            {catalogError}
          </FormMessage>
        ) : null}
        {error ? (
          <FormMessage className="mb-3" tone="error">
            {error}
          </FormMessage>
        ) : null}

        <div className="mb-3 shrink-0">
          <ListToolbar
            title={t("services.listLabel")}
            count={
              filtered
                ? `${visibleServices.length} / ${services.length}`
                : services.length
            }
            query={query}
            onQueryChange={setQuery}
            searchLabel={t("services.searchServices")}
            placeholder={t("services.searchServicesPlaceholder")}
            clearLabel={t("common.clearSearch")}
            secondaryFilters={
              <Input
                aria-label={t("services.filterModel")}
                className="h-8 w-full"
                onChange={(event) => setModelQuery(event.currentTarget.value)}
                placeholder={t("services.filterModelPlaceholder")}
                type="search"
                value={modelQuery}
              />
            }
            filters={
              <SegmentedControl<ServiceFilter>
                label={t("services.filterStatus")}
                onValueChange={setServiceFilter}
                options={[
                  {
                    value: "all",
                    label: t("services.filterAll"),
                    count: services.length,
                  },
                  {
                    value: "enabled",
                    label: t("services.filterEnabled"),
                    count: enabledCount,
                  },
                  {
                    value: "disabled",
                    label: t("services.filterDisabled"),
                    count: services.length - enabledCount,
                  },
                ]}
                value={serviceFilter}
              />
            }
          />
        </div>
        <p className="sr-only" aria-live="polite">
          {serviceOrder.saving
            ? t("services.orderSaving")
            : filtered
              ? t("services.orderFiltered")
              : ""}
        </p>
        {serviceOrder.error ? (
          <FormMessage className="mb-3" tone="error">
            {serviceOrder.error}
            <Button type="button" variant="ghost" onClick={serviceOrder.reload}>
              {t("common.retry")}
            </Button>
          </FormMessage>
        ) : null}
        <div
          className="flex min-h-0 min-w-0 flex-1 flex-col"
          aria-label={t("services.listLabel")}
        >
          {catalogStatus === "loading" && services.length === 0 ? (
            <EmptyState title={t("services.loading")} />
          ) : services.length === 0 ? (
            <EmptyState
              action={
                <Button
                  disabled={!isReady || busy}
                  onClick={() => onViewChange({ kind: "create" })}
                  size="sm"
                  type="button"
                >
                  <Plus aria-hidden="true" />
                  {t("services.add")}
                </Button>
              }
              description={t("services.emptyHint")}
              title={t("services.empty")}
            />
          ) : !serviceOrder.hasOrder && !serviceOrder.error ? (
            <EmptyState title={t("services.loading")} />
          ) : !serviceOrder.hasOrder ? null : (
            <>
              <div
                className="@container/service-list group/service-list min-h-0 min-w-0 flex-1 overflow-y-auto [scrollbar-gutter:stable]"
                data-testid="service-list-scroller"
              >
                <ServiceListHeader
                  labels={[
                    t("services.columnService"),
                    t("services.columnModels"),
                    t("services.columnUsage"),
                    t("services.columnStatus"),
                    t("services.columnActions"),
                  ]}
                />
                {visibleServices.length === 0 ? (
                  <EmptyState
                    className="col-span-full"
                    title={t("common.noSearchResults")}
                    description={t("services.noSearchResults")}
                    action={
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          setQuery("");
                          setModelQuery("");
                          setServiceFilter("all");
                        }}
                        type="button"
                      >
                        {t("services.clearFilters")}
                      </Button>
                    }
                  />
                ) : null}
                <OrderedList
                  items={visibleServices}
                  label={t("services.orderLabel")}
                  compact
                  disabled={
                    !isReady ||
                    busy ||
                    serviceOrder.saving ||
                    !serviceOrder.complete
                  }
                  positionOf={(service) =>
                    serviceOrder.ordered.findIndex(
                      (item) => item.id === service.id,
                    ) + 1
                  }
                  onChange={(items) => void serviceOrder.save(items)}
                >
                  {(service, _index, controls, sorting) => {
                    const subscription = service.subscription;
                    const acting = actionID === service.id;
                    const plan = planTypeLabel(
                      usageByService[service.id]?.usage?.plan_type,
                      subscription?.provider,
                    );
                    const tone = serviceDot(service);
                    return (
                      <ServiceListRow
                        key={service.id}
                        name={service.name}
                        order={controls}
                        sorting={sorting}
                        sortIcon={
                          <ServiceKindIcon kind={service.kind} size={20} />
                        }
                        sortStatus={
                          <StatusDot
                            label={serviceStatusLabel(service)}
                            tone={tone}
                          />
                        }
                        identity={
                          <div className="flex min-w-0 items-center gap-3">
                            <span className="flex size-9 shrink-0 items-center justify-center rounded-md border bg-background">
                              <ServiceKindIcon kind={service.kind} size={24} />
                            </span>
                            <div className="grid min-w-0 gap-0.5">
                              <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                                <Button
                                  className="block h-auto min-w-0 max-w-full shrink truncate rounded-sm p-0 text-left text-sm font-semibold"
                                  disabled={acting}
                                  onClick={() =>
                                    onViewChange({
                                      kind: "edit",
                                      serviceId: service.id,
                                    })
                                  }
                                  title={service.name}
                                  type="button"
                                  variant="link"
                                >
                                  {service.name}
                                </Button>
                                {plan ? (
                                  <Badge
                                    data-testid="subscription-plan"
                                    variant="secondary"
                                  >
                                    {plan}
                                  </Badge>
                                ) : null}
                              </div>
                              <span className="text-micro text-muted-foreground">
                                {serviceKindLabel(service.kind)}
                              </span>
                              <span
                                className="block truncate text-xs text-text-secondary"
                                title={
                                  service.http?.base_url ??
                                  subscription?.account_hint
                                }
                              >
                                {service.http?.base_url ??
                                  (subscription?.account_hint
                                    ? subscriptionAccountLabel(
                                        service.kind,
                                        subscription.account_hint,
                                      )
                                    : subscriptionOauthLabel(service.kind))}
                              </span>
                            </div>
                          </div>
                        }
                        inventory={
                          <>
                            <span className="text-xs font-medium tabular-nums">
                              {t("services.modelCount", {
                                count: service.models.length,
                              })}
                            </span>
                            <span className="inline-flex items-center gap-1.5 text-micro text-muted-foreground tabular-nums">
                              {t("services.apiCount", {
                                count: service.capabilities.length,
                              })}
                              {supportsResponsesWebSocket(service) &&
                              responsesWebSocketEnabled(service) ? (
                                <CapabilityIndicator
                                  label={t("services.webSocketOn")}
                                >
                                  <Cable
                                    aria-hidden="true"
                                    animateOnHover={false}
                                    className="size-3"
                                  />
                                </CapabilityIndicator>
                              ) : null}
                            </span>
                          </>
                        }
                        usage={
                          <div className="grid gap-1">
                            {hasPlanUsage(service) ? (
                              <SubscriptionUsageMeter
                                error={usageByService[service.id]?.error}
                                now={new Date()}
                                onReset={() =>
                                  setConfirmAction({
                                    kind: "reset-usage",
                                    service,
                                    availableCount:
                                      usageByService[service.id]?.usage
                                        ?.rate_limit_reset_credits
                                        ?.available_count ?? 0,
                                  })
                                }
                                resetting={actionID === service.id}
                                status={
                                  usageByService[service.id]?.status ??
                                  "loading"
                                }
                                usage={usageByService[service.id]?.usage}
                              />
                            ) : null}
                            <ServiceBillingMeter
                              serviceId={service.id}
                              ready={isReady}
                              epoch={usageEpoch}
                              observedAt={
                                usageByService[service.id]?.usage?.fetched_at
                              }
                              onOpen={() => setBillingService(service.id)}
                            />
                          </div>
                        }
                        status={
                          <>
                            <StatusDot
                              label={serviceStatusLabel(service)}
                              tone={tone}
                            />
                            <Switch
                              aria-label={t("services.enableNamed", {
                                name: service.name,
                              })}
                              checked={service.enabled}
                              disabled={!isReady || acting}
                              onCheckedChange={() =>
                                void toggleEnabled(service)
                              }
                              size="sm"
                            />
                          </>
                        }
                        actions={
                          <>
                            <IconButton
                              label={t("serviceTest.testNamed", {
                                name: service.name,
                              })}
                              disabled={!isReady || acting}
                              onClick={() => setTestingService(service)}
                              type="button"
                            >
                              <Flask aria-hidden="true" />
                            </IconButton>
                            <IconButton
                              label={t("services.editNamed", {
                                name: service.name,
                              })}
                              disabled={acting}
                              onClick={() =>
                                onViewChange({
                                  kind: "edit",
                                  serviceId: service.id,
                                })
                              }
                              type="button"
                            >
                              <Pencil aria-hidden="true" />
                            </IconButton>
                            <DropdownMenu>
                              <DropdownMenuTrigger asChild>
                                <IconButton
                                  label={t("services.moreNamed", {
                                    name: service.name,
                                  })}
                                  disabled={acting}
                                  size="icon-sm"
                                  type="button"
                                >
                                  <Ellipsis aria-hidden="true" />
                                </IconButton>
                              </DropdownMenuTrigger>
                              <DropdownMenuContent align="end">
                                {subscription ? (
                                  subscription.status === "authorizing" ? (
                                    <>
                                      <DropdownMenuItem
                                        disabled={acting}
                                        onSelect={() =>
                                          void showAuthorization(service)
                                        }
                                      >
                                        {acting
                                          ? t("common.processing")
                                          : t("services.viewLogin")}
                                      </DropdownMenuItem>
                                      <DropdownMenuItem
                                        disabled={acting}
                                        onSelect={() =>
                                          void cancelAuthorization(service)
                                        }
                                      >
                                        {t("services.cancelLogin")}
                                      </DropdownMenuItem>
                                    </>
                                  ) : (
                                    <DropdownMenuItem
                                      disabled={acting}
                                      onSelect={() => {
                                        setLoginChoice(service);
                                        setLoginChoiceFlow(
                                          defaultAuthorizationFlow(
                                            service.kind,
                                          ),
                                        );
                                        setError(null);
                                      }}
                                    >
                                      {acting
                                        ? t("common.processing")
                                        : subscription.status === "connected"
                                          ? t("services.resignIn")
                                          : t("services.signIn")}
                                    </DropdownMenuItem>
                                  )
                                ) : null}
                                {subscription?.status === "connected" ? (
                                  <DropdownMenuItem
                                    disabled={acting}
                                    onSelect={() =>
                                      setConfirmAction({
                                        kind: "logout",
                                        service,
                                      })
                                    }
                                  >
                                    {t("services.signOut")}
                                  </DropdownMenuItem>
                                ) : null}
                                {subscription ? (
                                  <DropdownMenuSeparator />
                                ) : null}
                                <DropdownMenuItem
                                  disabled={acting}
                                  onSelect={() =>
                                    setConfirmAction({
                                      kind: "delete",
                                      service,
                                    })
                                  }
                                  variant="destructive"
                                >
                                  {acting
                                    ? t("common.processing")
                                    : t("common.delete")}
                                </DropdownMenuItem>
                              </DropdownMenuContent>
                            </DropdownMenu>
                          </>
                        }
                      />
                    );
                  }}
                </OrderedList>
              </div>
            </>
          )}
        </div>
        {testingService ? (
          <ServiceTestDialog
            key={testingService.id}
            service={testingService}
            onClose={() => setTestingService(null)}
          />
        ) : null}
        <ConfirmDialog
          confirmLabel={
            confirmAction?.kind === "reset-usage"
              ? t("services.reset")
              : t("common.confirm")
          }
          description={
            <p>
              {confirmAction?.kind === "delete"
                ? t("services.deleteBody", {
                    name: confirmAction.service.name,
                  })
                : confirmAction?.kind === "reset-usage"
                  ? t("services.resetBody", {
                      name: confirmAction.service.name,
                      count: confirmAction.availableCount,
                    })
                  : t("services.logoutBody", {
                      name: confirmAction?.service.name ?? "",
                    })}
            </p>
          }
          destructive
          onCancel={() => setConfirmAction(null)}
          onConfirm={() => void confirmDestructiveAction()}
          open={confirmAction !== null}
          title={
            confirmAction?.kind === "delete"
              ? t("services.confirmDelete")
              : confirmAction?.kind === "reset-usage"
                ? t("services.confirmReset")
                : t("services.confirmLogout")
          }
        />
        <Dialog
          open={loginChoice !== null}
          onOpenChange={(open) => {
            if (!open) {
              setLoginChoice(null);
              setLoginChoiceFlow(null);
            }
          }}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>
                {t("services.loginNamed", { name: loginChoice?.name ?? "" })}
              </DialogTitle>
              <DialogDescription>
                {loginChoice?.kind === "grok_subscription"
                  ? t("services.grokDeviceCodeHint")
                  : loginChoice?.kind === "claude_subscription"
                    ? t("services.claudeOauthHint")
                    : t("services.chooseOauthHint")}
              </DialogDescription>
            </DialogHeader>
            <RadioGroup
              aria-label={t("services.loginMethod")}
              className="grid grid-cols-2 gap-2 max-[520px]:grid-cols-1"
              onValueChange={(value) =>
                setLoginChoiceFlow(value as AuthorizationFlow)
              }
              value={loginChoiceFlow ?? ""}
            >
              {loginChoice?.kind === "claude_subscription" ? (
                <ChoiceCard
                  label={t("services.claudeOauth")}
                  description={t("services.claudeOauthHint")}
                  selected={loginChoiceFlow === "authorization_code"}
                  value="authorization_code"
                />
              ) : loginChoice?.kind === "grok_subscription" ? (
                <ChoiceCard
                  label="Device Code"
                  description={t("services.grokDeviceCodeHint")}
                  selected={loginChoiceFlow === "device_code"}
                  value="device_code"
                />
              ) : (
                <>
                  <ChoiceCard
                    description={t("services.browserOauthHint")}
                    label={t("services.browserOauth")}
                    selected={loginChoiceFlow === "browser"}
                    value="browser"
                  />
                  <ChoiceCard
                    description={t("services.deviceCodeHint")}
                    label="Device Code"
                    selected={loginChoiceFlow === "device_code"}
                    value="device_code"
                  />
                </>
              )}
            </RadioGroup>
            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => {
                  setLoginChoice(null);
                  setLoginChoiceFlow(null);
                }}
                type="button"
              >
                {t("common.cancel")}
              </Button>
              <Button
                disabled={loginChoiceFlow === null}
                onClick={() => {
                  if (loginChoice && loginChoiceFlow) {
                    void authorize(loginChoice, loginChoiceFlow);
                  }
                }}
                type="button"
              >
                {t("services.startLogin")}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
        {authorizationDialog ? (
          <Dialog
            open
            onOpenChange={(open) => !open && setAuthorizationDialog(null)}
          >
            <DialogContent className="max-w-[460px] sm:max-w-[460px]">
              <DialogHeader>
                <DialogTitle>
                  {authorizationDialog.session.flow === "authorization_code"
                    ? t("services.claudeOauth")
                    : t("services.deviceCodeTitle")}
                </DialogTitle>
                <DialogDescription>
                  {authorizationDialog.session.flow === "authorization_code"
                    ? t("services.claudeOauthHint")
                    : deviceCodeDescription(
                        authorizationDialog.session.provider,
                      )}
                </DialogDescription>
              </DialogHeader>
              {authorizationDialog.requestedFlow === "browser" ? (
                <FormMessage tone="warning">
                  {t("services.portsBusyAuto")}
                </FormMessage>
              ) : null}
              {authorizationDialog.session.status === "pending" &&
              authorizationDialog.session.flow === "authorization_code" ? (
                <form
                  className="grid gap-4"
                  onSubmit={async (event) => {
                    event.preventDefault();
                    const active = authorizationDialog;
                    setActionID(active.service.id);
                    setError(null);
                    const code = authorizationCode;
                    setAuthorizationCode("");
                    try {
                      const session = await completeServiceAuthorization(
                        active.service.id,
                        active.session.id,
                        code,
                      );
                      setAuthorizationDialog((current) =>
                        current?.session.id === active.session.id
                          ? { ...current, session }
                          : current,
                      );
                      await onRefresh();
                    } catch (cause) {
                      setError(
                        errorMessage(cause, t("services.codeExchangeFailed")),
                      );
                    } finally {
                      setActionID(null);
                    }
                  }}
                >
                  <Field
                    htmlFor="claude-authorization-code"
                    label={t("services.authorizationCode")}
                  >
                    <Input
                      id="claude-authorization-code"
                      aria-label={t("services.authorizationCode")}
                      type="password"
                      autoComplete="off"
                      spellCheck={false}
                      placeholder="code#state"
                      value={authorizationCode}
                      maxLength={8192}
                      onChange={(event) =>
                        setAuthorizationCode(event.target.value)
                      }
                    />
                  </Field>
                  {error ? (
                    <FormMessage tone="error">{error}</FormMessage>
                  ) : null}
                  <DialogFooter>
                    <Button
                      variant="outline"
                      type="button"
                      disabled={actionID === authorizationDialog.service.id}
                      onClick={() =>
                        void cancelAuthorization(authorizationDialog.service)
                      }
                    >
                      {t("services.cancelLogin")}
                    </Button>
                    <Button
                      variant="outline"
                      type="button"
                      onClick={() => void reopenAuthorizationPage()}
                    >
                      {t("services.reopenLogin")}
                    </Button>
                    <Button
                      type="submit"
                      disabled={
                        !authorizationCode.trim() ||
                        actionID === authorizationDialog.service.id
                      }
                    >
                      {t("services.completeLogin")}
                    </Button>
                  </DialogFooter>
                </form>
              ) : authorizationDialog.session.status === "pending" &&
                authorizationDialog.session.device_code ? (
                <>
                  <p className="text-sm leading-6 text-muted-foreground">
                    {authorizationDialog.session.provider === "xai_grok"
                      ? t("services.grokEnterDeviceCode")
                      : t("services.enterDeviceCode")}
                  </p>
                  <div className="flex items-center justify-between gap-3 rounded-md border border-primary/20 bg-accent p-3">
                    <code className="font-mono text-xl font-semibold tracking-[0.08em] text-accent-foreground select-all">
                      {authorizationDialog.session.device_code.user_code}
                    </code>
                    <Button
                      variant="outline"
                      onClick={() =>
                        copyFeedback.copy(
                          "codex-device-code",
                          authorizationDialog.session.device_code?.user_code ??
                            "",
                        )
                      }
                      type="button"
                    >
                      {copyButtonLabel(
                        copyFeedback,
                        "codex-device-code",
                        t("services.copyCode"),
                      )}
                    </Button>
                  </div>
                  <small className="mt-2 block text-xs text-muted-foreground">
                    {authorizationDialog.session.provider === "xai_grok"
                      ? t("services.grokDeviceHint")
                      : t("services.deviceDisabledHint")}
                  </small>
                  <DialogFooter>
                    <Button
                      variant="outline"
                      disabled={actionID === authorizationDialog.service.id}
                      onClick={() =>
                        void cancelAuthorization(authorizationDialog.service)
                      }
                      type="button"
                    >
                      {t("services.cancelLogin")}
                    </Button>
                    <Button
                      onClick={() => void reopenAuthorizationPage()}
                      type="button"
                    >
                      {t("services.reopenLogin")}
                    </Button>
                  </DialogFooter>
                </>
              ) : (
                <>
                  <p className="text-sm text-muted-foreground" role="status">
                    {authorizationDialog.session.status === "failed"
                      ? (authorizationDialog.session.error?.message ??
                        t("services.deviceFailed"))
                      : authorizationDialog.session.status === "expired"
                        ? t("services.deviceExpired")
                        : authorizationDialog.session.status === "cancelled"
                          ? t("services.deviceCancelled")
                          : t("services.loginDone")}
                  </p>
                  <DialogFooter>
                    <Button
                      onClick={() => setAuthorizationDialog(null)}
                      type="button"
                    >
                      {t("common.close")}
                    </Button>
                  </DialogFooter>
                </>
              )}
            </DialogContent>
          </Dialog>
        ) : null}
      </section>
    );
  }

  const editingKind = editing?.service.kind;
  const canKeepCredential = Boolean(editing?.service.http?.credential_ref);
  const selectedPreset = isSubscriptionKind(draft.kind)
    ? null
    : httpServicePreset(draft.kind as HTTPServicePresetID, protocols);
  const modelsEditor = (
    <ServiceModelsEditor
      key={editingServiceID ?? "create"}
      modelEditor={modelEditor}
      models={draft.models}
      probingModels={probingModels}
      onAddModels={addModels}
      onClearModels={() =>
        setDraft((current) => ({
          ...current,
          models: [],
        }))
      }
      onDiscoverModels={
        isSubscriptionKind(draft.kind) ||
        draft.capabilities.some(
          ({ protocol }) =>
            protocol === "openai.models" || protocol === "google.models",
        )
          ? () => void discoverModels()
          : undefined
      }
      onModelEditorChange={setModelEditor}
      onRemoveModels={removeDraftModels}
    />
  );
  const protocolEditor = (
    <Panel asChild>
      <section aria-labelledby="service-capabilities-heading">
        <PanelHeader
          actions={
            <Badge className="mt-px shrink-0 tabular-nums" variant="secondary">
              {t("services.enabledItems", { count: draft.capabilities.length })}
            </Badge>
          }
        >
          <div className="grid min-w-0 gap-0.5">
            <strong
              className="text-sm font-semibold"
              id="service-capabilities-heading"
            >
              {t("services.capabilitiesTitle")}
            </strong>
            <p className="text-xs text-muted-foreground">
              {conversionEngine?.available
                ? t("services.capabilityHintConvert")
                : t("services.capabilityHintPassthrough")}
            </p>
          </div>
        </PanelHeader>

        <div>
          {descriptors.map((descriptor) => {
            const capability = draft.capabilities.find(
              (item) => item.protocol === descriptor.id,
            );
            const convertible = supportsLocalConversion(descriptor.id);
            const targets = convertible
              ? localConversionTargets(descriptor.id, conversionEngine)
              : [];
            const selected = targets.find(
              (target) => target.id === capability?.convert_to,
            );
            return (
              <DataRow
                className="grid grid-cols-1 gap-3 py-3 @[640px]:grid-cols-[minmax(0,1fr)_232px]"
                data-testid="service-capability-row"
                key={descriptor.id}
              >
                <Label className="flex min-w-0 items-center gap-3 text-xs text-text-secondary">
                  <Checkbox
                    checked={Boolean(capability)}
                    onCheckedChange={(checked) =>
                      toggleCapability(descriptor, checked === true)
                    }
                  />
                  <span className="grid min-w-0 gap-1">
                    <span className="font-medium text-foreground">
                      {protocolLabel(descriptor.id)}
                    </span>
                    <code className="min-w-0 truncate font-mono text-micro text-muted-foreground">
                      {protocolEntryPath(descriptor.id)}
                    </code>
                  </span>
                  {selected?.quality ? (
                    <Badge
                      className="shrink-0 px-1.5 py-0 text-micro"
                      variant={
                        selected.quality === "discouraged"
                          ? "destructive"
                          : "secondary"
                      }
                    >
                      {conversionQualityLabels[selected.quality]}
                      {selected.streaming ? "" : t("services.noStreaming")}
                    </Badge>
                  ) : null}
                </Label>
                {capability && convertible ? (
                  <Select
                    value={capability.convert_to ?? localConversionPassthrough}
                    onValueChange={(value) =>
                      setConvertTo(descriptor.id, value)
                    }
                  >
                    <SelectTrigger
                      aria-label={t("services.localConvert", {
                        protocol: protocolLabel(descriptor.id),
                      })}
                      className="h-8 w-full"
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={localConversionPassthrough}>
                        {t("services.passthrough")}
                      </SelectItem>
                      {targets.map((target) => (
                        <SelectItem
                          disabled={!target.enabled}
                          key={target.id}
                          value={target.id}
                        >
                          {t("services.convertTo", {
                            protocol: protocolLabel(target.id),
                          })}
                          {target.enabled
                            ? target.quality
                              ? ` · ${conversionQualityLabels[target.quality]}`
                              : ""
                            : t("services.notEnabled")}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                ) : (
                  <span className="hidden text-xs text-muted-foreground @[640px]:block">
                    {capability ? t("services.passthrough") : ""}
                  </span>
                )}
              </DataRow>
            );
          })}
        </div>
      </section>
    </Panel>
  );
  const connectionFields = (
    <div className="grid min-w-0 items-start gap-4 pb-2 @[760px]:grid-cols-2">
      <Panel>
        <PanelHeader>
          <h2 className="flex items-center gap-2 text-sm font-semibold">
            <SlidersHorizontal
              aria-hidden="true"
              className="size-4 text-primary"
            />
            {t("services.basicInformation")}
          </h2>
          <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
            {t("services.basicInformationHint")}
          </p>
        </PanelHeader>
        <div className="grid gap-4 p-4">
          <Field
            label={t("services.serviceType")}
            hint={
              isSubscriptionKind(draft.kind)
                ? subscriptionKindHint(draft.kind)
                : selectedPreset?.description
            }
          >
            <Select
              disabled={view.kind === "edit"}
              value={draft.kind}
              onValueChange={(value) => selectKind(value as ServiceKind)}
            >
              <SelectTrigger
                aria-label={t("services.serviceType")}
                className="w-full"
              >
                <SelectValue>
                  <ServiceKindLabel kind={draft.kind}>
                    {serviceTypeOptionLabel(draft.kind)}
                  </ServiceKindLabel>
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectLabel>{t("services.groupSubscription")}</SelectLabel>
                  <SelectItem
                    value="codex_subscription"
                    textValue={serviceTypeOptionLabel("codex_subscription")}
                  >
                    <ServiceKindLabel kind="codex_subscription">
                      {serviceTypeOptionLabel("codex_subscription")}
                    </ServiceKindLabel>
                  </SelectItem>
                  <SelectItem
                    value="claude_subscription"
                    textValue={serviceTypeOptionLabel("claude_subscription")}
                  >
                    <ServiceKindLabel kind="claude_subscription">
                      {serviceTypeOptionLabel("claude_subscription")}
                    </ServiceKindLabel>
                  </SelectItem>
                  <SelectItem
                    value="grok_subscription"
                    textValue={serviceTypeOptionLabel("grok_subscription")}
                  >
                    <ServiceKindLabel kind="grok_subscription">
                      {serviceTypeOptionLabel("grok_subscription")}
                    </ServiceKindLabel>
                  </SelectItem>
                  {codingPlanPresetIDs.map((kind) => (
                    <SelectItem
                      key={kind}
                      value={kind}
                      textValue={serviceTypeOptionLabel(kind)}
                    >
                      <ServiceKindLabel kind={kind}>
                        {serviceTypeOptionLabel(kind)}
                      </ServiceKindLabel>
                    </SelectItem>
                  ))}
                </SelectGroup>
                <SelectGroup>
                  <SelectLabel>{t("services.groupGateway")}</SelectLabel>
                  <SelectItem
                    value="newapi"
                    textValue={serviceTypeOptionLabel("newapi")}
                  >
                    <ServiceKindLabel kind="newapi">
                      {serviceTypeOptionLabel("newapi")}
                    </ServiceKindLabel>
                  </SelectItem>
                </SelectGroup>
                <SelectGroup>
                  <SelectLabel>{t("services.groupPayAsYouGo")}</SelectLabel>
                  {payAsYouGoPresetIDs.map((kind) => (
                    <SelectItem
                      key={kind}
                      value={kind}
                      textValue={serviceTypeOptionLabel(kind)}
                    >
                      <ServiceKindLabel kind={kind}>
                        {serviceTypeOptionLabel(kind)}
                      </ServiceKindLabel>
                    </SelectItem>
                  ))}
                </SelectGroup>
                <SelectGroup>
                  <SelectLabel>{t("services.groupAdvanced")}</SelectLabel>
                  {httpServicePresetIDs
                    .filter(
                      (kind) =>
                        kind !== "newapi" &&
                        !codingPlanPresetIDs.includes(kind) &&
                        !payAsYouGoPresetIDs.includes(kind),
                    )
                    .map((kind) => (
                      <SelectItem
                        key={kind}
                        value={kind}
                        textValue={serviceTypeOptionLabel(kind)}
                      >
                        <ServiceKindLabel kind={kind}>
                          {serviceTypeOptionLabel(kind)}
                        </ServiceKindLabel>
                      </SelectItem>
                    ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </Field>
          <Field htmlFor="service-name" label={t("services.serviceName")}>
            <Input
              id="service-name"
              maxLength={128}
              placeholder={
                draft.kind === "grok_subscription"
                  ? t("services.namePlaceholderGrok")
                  : isSubscriptionKind(draft.kind)
                    ? t("services.namePlaceholderCodex")
                    : t("services.namePlaceholderHttp")
              }
              required
              value={draft.name}
              onChange={(event) =>
                setDraft((current) => ({
                  ...current,
                  name: event.target.value,
                }))
              }
            />
          </Field>
          <Label className="flex items-center gap-2 border-t pt-4 text-xs font-medium">
            <Checkbox
              checked={draft.enabled}
              onCheckedChange={(checked) =>
                setDraft((current) => ({
                  ...current,
                  enabled: checked === true,
                }))
              }
            />
            <span>{t("services.enableThis")}</span>
          </Label>
          {supportsResponsesWebSocket(draft) ? (
            <div className="border-t pt-4">
              <CapabilityToggle
                label={t("services.responsesWebSocket")}
                description={t("services.responsesWebSocketHint")}
                checked={draft.responsesWebSocket}
                onCheckedChange={(checked) =>
                  setDraft((current) => ({
                    ...current,
                    responsesWebSocket: checked,
                  }))
                }
              />
            </div>
          ) : null}
        </div>
      </Panel>
      <Panel>
        <PanelHeader>
          <h2 className="flex items-center gap-2 text-sm font-semibold">
            <KeyRound aria-hidden="true" className="size-4 text-primary" />
            {isSubscriptionKind(draft.kind)
              ? t("services.accountAuthorization")
              : t("services.connectionAuthentication")}
          </h2>
          <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
            {isSubscriptionKind(draft.kind)
              ? t("services.independentAccounts")
              : t("services.connectionAuthenticationHint")}
          </p>
        </PanelHeader>
        <div className="grid min-w-0 gap-4 p-4">
          {isSubscriptionKind(draft.kind) ? (
            <>
              {view.kind === "create" ? (
                <fieldset className="min-w-0 border-0 p-0">
                  <legend className="sr-only">
                    {t("services.loginMethod")}
                  </legend>
                  <RadioGroup
                    aria-label={t("services.newLoginMethod")}
                    className="grid gap-2"
                    onValueChange={(value) =>
                      setDraft((current) => ({
                        ...current,
                        authorizationFlow: value as AuthorizationFlow,
                      }))
                    }
                    value={draft.authorizationFlow ?? ""}
                  >
                    {draft.kind === "claude_subscription" ? (
                      <ChoiceCard
                        label={t("services.claudeOauth")}
                        description={t("services.claudeOauthHint")}
                        selected
                        value="authorization_code"
                      />
                    ) : draft.kind === "grok_subscription" ? (
                      <ChoiceCard
                        label="Device Code"
                        description={t("services.grokDeviceCodeHint")}
                        selected
                        value="device_code"
                      />
                    ) : (
                      <>
                        <ChoiceCard
                          description={t("services.browserOauthCreateHint")}
                          label={t("services.browserOauth")}
                          selected={draft.authorizationFlow === "browser"}
                          value="browser"
                        />
                        <ChoiceCard
                          description={t("services.deviceCodeCreateHint")}
                          label="Device Code"
                          selected={draft.authorizationFlow === "device_code"}
                          value="device_code"
                        />
                      </>
                    )}
                  </RadioGroup>
                  {draft.authorizationFlow === null ? (
                    <small className="mt-2 block text-warning-foreground">
                      {t("services.chooseLoginContinue")}
                    </small>
                  ) : null}
                </fieldset>
              ) : null}
              <div className="flex items-start gap-2 rounded-md border border-success/20 bg-success-wash px-3 py-2.5 text-text-secondary">
                <StatusDot className="mt-1.5" tone="positive" />
                <div>
                  <strong className="text-sm font-medium text-success-foreground">
                    {view.kind === "create"
                      ? t("services.saveThenLogin")
                      : t("services.loginInList")}
                  </strong>
                  <p className="mt-0.5 text-xs">
                    {t("services.independentAccounts")}
                  </p>
                </div>
              </div>
            </>
          ) : (
            <>
              <Field label={t("services.apiAddress")}>
                <Input
                  maxLength={2048}
                  placeholder={
                    selectedPreset?.baseURLPlaceholder ??
                    "https://api.example.com"
                  }
                  required
                  type="url"
                  value={draft.baseURL}
                  onChange={(event) =>
                    setDraft((current) => ({
                      ...current,
                      baseURL: event.target.value,
                    }))
                  }
                />
              </Field>
              <Field label={t("services.authScheme")}>
                <Select
                  value={draft.authScheme}
                  onValueChange={(value) =>
                    setDraft((current) => ({
                      ...current,
                      authScheme: value as ServiceAuthScheme,
                    }))
                  }
                >
                  <SelectTrigger
                    aria-label={t("services.authScheme")}
                    className="w-full"
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {Object.entries(authLabels).map(([scheme, label]) => (
                      <SelectItem key={scheme} value={scheme}>
                        {label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              {draft.authScheme === "custom_header" ? (
                <Field label={t("services.headerName")}>
                  <Input
                    maxLength={128}
                    placeholder="X-Api-Key"
                    value={draft.headerName}
                    onChange={(event) =>
                      setDraft((current) => ({
                        ...current,
                        headerName: event.target.value,
                      }))
                    }
                  />
                </Field>
              ) : null}
              {draft.authScheme !== "none" ? (
                <Field
                  label={
                    editingKind
                      ? canKeepCredential
                        ? t("services.apiKeyKeep")
                        : t("services.apiKeyRequired")
                      : "API Key"
                  }
                >
                  <Input
                    autoComplete="new-password"
                    aria-label={
                      editingKind
                        ? canKeepCredential
                          ? t("services.apiKeyKeep")
                          : t("services.apiKeyRequired")
                        : "API Key"
                    }
                    maxLength={16_384}
                    placeholder={
                      canKeepCredential
                        ? t("services.apiKeySaved")
                        : t("services.apiKeyPaste")
                    }
                    type="password"
                    value={draft.secret}
                    onChange={(event) =>
                      setDraft((current) => ({
                        ...current,
                        secret: event.target.value,
                      }))
                    }
                  />
                </Field>
              ) : null}
              {editing?.service.http?.credential_ref ? (
                <Label className="flex items-start gap-2 border-t pt-4 text-xs font-normal text-muted-foreground">
                  <Checkbox
                    checked={draft.removeCredential}
                    onCheckedChange={(checked) =>
                      setDraft((current) => ({
                        ...current,
                        removeCredential: checked === true,
                      }))
                    }
                  />
                  <span>{t("services.removeStoredKey")}</span>
                </Label>
              ) : null}
            </>
          )}
        </div>
      </Panel>
      <ServiceProxyFields
        value={draft.proxy}
        onChange={(proxy) => setDraft((current) => ({ ...current, proxy }))}
        hasCredential={Boolean(editing?.service.proxy?.credential_ref)}
      />
    </div>
  );
  const visibleEditorTab: EditorTab =
    isSubscriptionKind(draft.kind) && editorTab === "protocols"
      ? "models"
      : editorTab;

  return (
    <section
      className="@container flex min-h-0 w-full min-w-0 flex-1 flex-col overflow-hidden"
      aria-labelledby="service-editor-heading"
    >
      <PageHeader
        back={{
          label: t("services.back"),
          onClick: () => onViewChange({ kind: "list" }),
        }}
        title={view.kind === "edit" ? t("services.edit") : t("services.add")}
        titleId="service-editor-heading"
        variant="compact"
      />
      {!isReady ? (
        <FormMessage className="mb-3 shrink-0" tone="notice">
          {t("services.gatewayNotReady")}
        </FormMessage>
      ) : null}
      {error ? (
        <FormMessage className="mb-3 shrink-0" tone="error">
          {error}
        </FormMessage>
      ) : null}
      {loadingRecord ? (
        <FormMessage className="mb-3 shrink-0" aria-busy="true" tone="notice">
          {t("services.loadingRecord")}
        </FormMessage>
      ) : view.kind === "edit" && !editing ? (
        <FormMessage className="mb-3 shrink-0" tone="error">
          {t("services.loadRecordFailed")}
        </FormMessage>
      ) : (
        <form
          aria-busy={saving}
          className="flex min-h-0 w-full min-w-0 flex-1 flex-col overflow-hidden"
          data-testid="service-form"
          noValidate
          onSubmit={(event) => void submit(event)}
        >
          <fieldset
            className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden border-0 p-0 disabled:pointer-events-none disabled:opacity-70"
            disabled={!isReady || saving}
          >
            <Tabs
              className="min-h-0 flex-1 gap-3"
              onValueChange={(value) => setEditorTab(value as EditorTab)}
              value={visibleEditorTab}
            >
              <TabsList
                aria-label={t("services.tabsAria")}
                scrollable
                className="h-9 max-w-full shrink-0"
              >
                <TabsTrigger
                  data-testid="service-editor-tab-connection"
                  onClick={() => setEditorTab("connection")}
                  type="button"
                  value="connection"
                >
                  <Cable aria-hidden="true" />
                  {t("services.tabConnection")}
                </TabsTrigger>
                <TabsTrigger
                  data-testid="service-editor-tab-models"
                  onClick={() => setEditorTab("models")}
                  type="button"
                  value="models"
                >
                  <Boxes aria-hidden="true" />
                  {t("services.tabModels")}
                  <Badge
                    className="px-1.5 py-0 text-micro tabular-nums"
                    variant="secondary"
                  >
                    {t("services.modelCount", {
                      count: draft.models.length,
                    })}
                  </Badge>
                </TabsTrigger>
                {isSubscriptionKind(draft.kind) ? null : (
                  <TabsTrigger
                    data-testid="service-editor-tab-protocols"
                    onClick={() => setEditorTab("protocols")}
                    type="button"
                    value="protocols"
                  >
                    <SlidersHorizontal aria-hidden="true" />
                    {t("services.tabProtocols")}
                    <Badge
                      className="px-1.5 py-0 text-micro tabular-nums"
                      variant="secondary"
                    >
                      {t("services.enabledItems", {
                        count: draft.capabilities.length,
                      })}
                    </Badge>
                  </TabsTrigger>
                )}
                <TabsTrigger
                  data-testid="service-editor-tab-failure"
                  onClick={() => setEditorTab("failure")}
                  type="button"
                  value="failure"
                >
                  {t("failure.title")}
                </TabsTrigger>
              </TabsList>
              <TabsContent
                className="min-h-0 min-w-0 flex-1 overflow-y-auto pr-4 pb-1"
                data-tab-scroller=""
                data-testid="service-editor-tab-panel"
                value="connection"
              >
                {connectionFields}
              </TabsContent>
              <TabsContent
                className="min-h-0 min-w-0 flex-1 overflow-y-auto pr-4 pb-1"
                data-tab-scroller=""
                data-testid="service-editor-tab-panel"
                value="models"
              >
                {modelsEditor}
              </TabsContent>
              {isSubscriptionKind(draft.kind) ? null : (
                <TabsContent
                  className="min-h-0 min-w-0 flex-1 overflow-y-auto pr-4 pb-1"
                  data-tab-scroller=""
                  data-testid="service-editor-tab-panel"
                  value="protocols"
                >
                  {protocolEditor}
                </TabsContent>
              )}
              <TabsContent
                className="min-h-0 min-w-0 flex-1 overflow-y-auto pr-4 pb-1"
                data-tab-scroller=""
                data-testid="service-editor-tab-panel"
                value="failure"
              >
                <Panel className="mb-3 grid gap-3 p-4">
                  <p className="text-sm">{t("failure.serviceGlobalHint")}</p>
                  {!routingDefaults.loaded ? (
                    <p className="text-xs text-muted-foreground">
                      {t("failure.defaultsUnavailable")}
                      <Button
                        type="button"
                        variant="ghost"
                        onClick={routingDefaults.reload}
                      >
                        {t("common.retry")}
                      </Button>
                    </p>
                  ) : null}
                  <Label className="flex items-center gap-2">
                    <Switch
                      disabled={!routingDefaults.loaded}
                      checked={draft.failurePolicy !== undefined}
                      onCheckedChange={(checked) =>
                        setDraft((current) => ({
                          ...current,
                          failurePolicy: checked
                            ? structuredClone(
                                routingDefaults.default_failure_policy,
                              )
                            : undefined,
                        }))
                      }
                    />
                    {t("failure.serviceOverride")}
                  </Label>
                </Panel>
                {draft.failurePolicy ? (
                  <FailurePolicyEditor
                    value={draft.failurePolicy}
                    onChange={(failurePolicy) =>
                      setDraft((current) => ({ ...current, failurePolicy }))
                    }
                  />
                ) : null}
              </TabsContent>
            </Tabs>
          </fieldset>
          <div className="mt-3 flex shrink-0 items-center justify-between gap-3 border-t pt-3 pb-1">
            <span className="text-xs text-muted-foreground">
              {dirty ? t("services.unsavedChanges") : t("services.saveHint")}
            </span>
            <div className="flex shrink-0 items-center gap-2">
              <Button
                type="button"
                variant="outline"
                disabled={saving}
                onClick={() => onViewChange({ kind: "list" })}
              >
                {t("common.cancel")}
              </Button>
              <Button
                className="min-w-28"
                data-testid="service-submit"
                disabled={
                  !isReady ||
                  saving ||
                  (view.kind === "create" &&
                    isSubscriptionKind(draft.kind) &&
                    draft.authorizationFlow === null)
                }
                type="submit"
              >
                {saving
                  ? t("common.saving")
                  : view.kind === "edit"
                    ? t("services.saveChanges")
                    : isSubscriptionKind(draft.kind)
                      ? t("services.addAndLogin")
                      : t("services.saveService")}
              </Button>
            </div>
          </div>
        </form>
      )}
      {modelPreview ? (
        <ModelPreviewDialog
          preview={modelPreview}
          query={modelPreviewQuery}
          onClose={() => {
            setModelPreview(null);
            setModelPreviewQuery("");
          }}
          onQueryChange={setModelPreviewQuery}
          onSelectedChange={(selected) =>
            setModelPreview((current) =>
              current ? { ...current, selected } : current,
            )
          }
          onApply={() => {
            const selected = new Set(modelPreview.selected);
            setDraft((current) => ({
              ...current,
              models: [...selected].sort(),
            }));
            setModelPreview(null);
            setModelPreviewQuery("");
          }}
        />
      ) : null}
    </section>
  );
}
