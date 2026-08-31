import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Ellipsis, Plus, RefreshCw } from "lucide-react";

import { ChoiceCard } from "@/components/ChoiceCard";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { EmptyState } from "@/components/EmptyState";
import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { FormMessage } from "@/components/FormMessage";
import { ServiceKindIcon } from "@/components/ServiceKindIcon";
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
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
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
  httpServicePreset,
  httpServicePresetIDs,
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
  serviceKindLabel,
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
} from "./service-model";
import {
  SubscriptionUsageMeter,
  type SubscriptionUsageStatus,
} from "./SubscriptionUsageMeter";
import {
  type AuthorizationFlow,
  type AuthorizationSession,
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

export type ServiceCatalogStatus =
  | "blocked"
  | "loading"
  | "ready"
  | "error";

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
  kind: ServiceKind;
  name: string;
  enabled: boolean;
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

type EditorTab = "connection" | "models" | "protocols";

function serviceTypeOptionLabel(kind: ServiceKind): string {
  return kind === "codex_subscription"
    ? i18n.t("services.codexKind")
    : serviceKindLabel(kind);
}

function mergeDiscoveredServiceModels(
  current: { models: readonly string[] },
  discovered: readonly string[],
): string[] | null {
  const models = [...new Set([...current.models, ...discovered])].sort();
  if (models.length > 2_000) return null;
  return models;
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
  if (kind === "codex_subscription") {
    return {
      kind,
      name: i18n.t("services.codexName"),
      enabled: true,
      baseURL: "",
      authScheme: "none",
      headerName: "",
      secret: "",
      removeCredential: false,
      models: [],
      capabilities: [],
      authorizationFlow: null,
    };
  }
  const preset = httpServicePreset(
    kind as HTTPServicePresetID,
    protocols,
  );
  return {
    kind,
    name: preset.defaultName,
    enabled: true,
    baseURL: preset.baseURL,
    authScheme: preset.authScheme,
    headerName: preset.headerName,
    secret: "",
    removeCredential: false,
    models: [],
    capabilities: preset.capabilities.map((capability) => ({ ...capability })),
    authorizationFlow: null,
  };
}

function draftFromRecord(record: ServiceRecord): Draft {
  const { service } = record;
  if (service.kind === "codex_subscription") {
    return {
      ...draftForKind("codex_subscription", []),
      name: service.name,
      enabled: service.enabled,
      models: [...service.models],
    };
  }
  if (!service.http) throw new Error(i18n.t("services.missingHttp"));
  return {
    kind: service.kind,
    name: service.name,
    enabled: service.enabled,
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
  if (draft.name.trim().length === 0 || [...draft.name.trim()].length > 128) {
    return i18n.t("services.nameInvalid");
  }
  if (draft.models.length > 2_000) {
    return i18n.t("services.tooManyModels");
  }
  if (
    draft.models.some((model) => [...model].length < 1 || [...model].length > 256) ||
    new Set(draft.models).size !== draft.models.length
  ) {
    return i18n.t("services.modelIdsInvalid");
  }
  if (draft.kind === "codex_subscription") {
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

function serviceDot(service: Service): "positive" | "pending" | "negative" | "neutral" {
  if (!service.enabled) return "neutral";
  const status = service.subscription?.status;
  if (!status || status === "connected") return "positive";
  if (status === "authorizing" || status === "disconnected") return "pending";
  return "negative";
}

function serviceStatusTextClass(tone: ReturnType<typeof serviceDot>): string {
  if (tone === "positive") return "text-success-foreground";
  if (tone === "pending") return "text-warning-foreground";
  if (tone === "negative") return "text-danger-foreground";
  return "text-muted-foreground";
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
                {hasQuery ? t("services.clearMatches") : t("services.selectNone")}
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
              <Label className="flex items-center gap-2 rounded-lg border px-2 py-1.5" key={model}>
                <Checkbox
                  checked={selectedSet.has(model)}
                  onCheckedChange={(checked) => {
                    const selected = checked === true
                      ? [...new Set([...preview.selected, model])].sort()
                      : preview.selected.filter((item) => item !== model);
                    onSelectedChange(selected);
                  }}
                />
                <ModelBrandIcon model={model} />
                <code className="min-w-0 truncate font-mono text-xs">{encodeModelEditorValue(model)}</code>
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
  const t = useT();
  const descriptors = useMemo(
    () => protocolDescriptors(protocols),
    [protocols],
  );
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
  const [modelEditor, setModelEditor] = useState("");
  const [probingModels, setProbingModels] = useState(false);
  const [modelPreview, setModelPreview] = useState<ModelPreview | null>(null);
  const [modelPreviewQuery, setModelPreviewQuery] = useState("");
  const [editorTab, setEditorTab] = useState<EditorTab>("connection");
  const [usageByService, setUsageByService] = useState<
    Record<
      string,
      { status: SubscriptionUsageStatus; usage?: SubscriptionUsage; error?: string }
    >
  >({});
  const [usageEpoch, setUsageEpoch] = useState(0);
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
        .filter((service) => service.subscription?.status === "connected")
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
        { status: SubscriptionUsageStatus; usage?: SubscriptionUsage; error?: string }
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
          console.error("AstrLink failed to load subscription usage", id, cause);
          if (usageGeneration.current !== generation) return;
          setUsageByService((current) => ({
            ...current,
            [id]: { status: "error", usage: current[id]?.usage, error: message },
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
          notify.error(
            t("services.loggedInTooMany", { name: service.name }),
          );
          return;
        }
        const unchanged = merged.join("\0") === record.service.models.join("\0");
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
    if (
      !isReady ||
      active === null ||
      active.session.status !== "pending"
    ) {
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
      kind === "codex_subscription" && current === "protocols"
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
    if (draft.kind === "codex_subscription" && !editing) {
      setError(t("services.saveBeforeFetch"));
      return;
    }
    const discoveryProtocols: ModelDiscoveryProtocol[] =
      draft.kind === "codex_subscription"
        ? ["openai.models"]
        : (["openai.models", "google.models"] as const).filter((protocol) =>
            draft.capabilities.some((capability) => capability.protocol === protocol),
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
          if (draft.kind === "codex_subscription") {
            return probeServiceModels(editing!.service.id, protocol);
          }
          return probeDraftServiceModels({
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
        selected: models,
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
          name: draft.name.trim(),
          enabled: draft.enabled,
          models: draft.models,
        };
        if (draft.kind !== "codex_subscription") {
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
        record = await updateService(
          editing.service.id,
          editing.etag,
          patch,
        );
        notify.success(t("services.updated"));
      } else {
        let input: ServiceCreateInput;
        if (draft.kind === "codex_subscription") {
          input = {
            name: draft.name.trim(),
            kind: "codex_subscription",
            enabled: draft.enabled,
            models: draft.models,
          };
        } else {
          input = {
            name: draft.name.trim(),
            kind: draft.kind as HTTPServiceKind,
            enabled: draft.enabled,
            models: draft.models,
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
        if (record.service.kind === "codex_subscription") {
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
            presentAuthorization(
              record.service,
              flow,
              authorization.session,
            );
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

  const authorize = async (
    service: Service,
    flow: AuthorizationFlow,
  ) => {
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
      if (session.flow === "device_code") {
        setAuthorizationDialog({
          service,
          requestedFlow: "device_code",
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
    const url = authorizationDialog?.session.device_code?.verification_url;
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
        console.error("AstrLink failed to reset subscription usage", service.id, cause);
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
    const busy = catalogStatus === "loading";
    return (
      <section className="flex min-h-0 w-full flex-1 flex-col" aria-labelledby="service-heading">
        <PageHeader
          actions={
            <>
              <Button
                disabled={!isReady || busy}
                onClick={() => onViewChange({ kind: "create" })}
                type="button"
              >
                <Plus aria-hidden="true" />
                {t("services.add")}
              </Button>
              <Button
                variant="outline"
                disabled={!isReady || busy}
                onClick={() => {
                  setUsageEpoch((value) => value + 1);
                  void onRefresh();
                }}
                type="button"
              >
                <RefreshCw
                  aria-hidden="true"
                  className={cn("motion-reduce:animate-none", busy && "animate-spin")}
                />
                {busy ? t("common.refreshing") : t("services.refreshList")}
              </Button>
            </>
          }
          description={t("services.description")}
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

        <div className="flex min-h-0 min-w-0 flex-1 flex-col" aria-label={t("services.listLabel")}>
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
          ) : (
            <div className="grid min-h-0 content-start grid-cols-2 gap-2.5 overflow-y-auto max-[720px]:grid-cols-1">
              {services.map((service) => {
                const subscription = service.subscription;
                const acting = actionID === service.id;
                const plan = planTypeLabel(
                  usageByService[service.id]?.usage?.plan_type,
                );
                const tone = serviceDot(service);
                return (
                  <article
                    className="flex min-w-0 flex-col overflow-hidden rounded-md border bg-card"
                    data-testid="service-card"
                    key={service.id}
                  >
                    <div className="flex min-w-0 items-center gap-2 px-3.5 pt-3">
                      <ServiceKindIcon kind={service.kind} />
                      <StatusDot tone={tone} />
                      <strong className="truncate text-sm font-medium">{service.name}</strong>
                      {plan ? (
                        <Badge data-testid="subscription-plan" variant="secondary">
                          {plan}
                        </Badge>
                      ) : null}
                      <div className="ml-auto flex shrink-0 items-center gap-2">
                        <span
                          className={cn(
                            "text-xs font-medium",
                            serviceStatusTextClass(tone),
                          )}
                        >
                          {serviceStatusLabel(service)}
                        </span>
                        <Switch
                          aria-label={t("services.enableNamed", { name: service.name })}
                          checked={service.enabled}
                          disabled={!isReady || acting}
                          onCheckedChange={() => void toggleEnabled(service)}
                          size="sm"
                        />
                      </div>
                    </div>
                    <div className="px-3.5 pt-1 pb-3">
                      <code className="block truncate font-mono text-xs text-text-secondary">
                        {service.http?.base_url ??
                          (subscription?.account_hint
                            ? t("services.openaiAccount", {
                                hint: subscription.account_hint,
                              })
                            : t("services.openaiCodexOauth"))}
                      </code>
                    </div>
                    <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 border-t px-3.5 py-2">
                      <span className="text-xs text-muted-foreground tabular-nums">
                        {t("services.capabilityCount", {
                          count: service.capabilities.length,
                        })}
                        {" · "}
                        {t("services.modelCount", {
                          count: service.models.length,
                        })}
                      </span>
                    </div>
                    {subscription?.status === "connected" ? (
                      <div className="border-t px-3.5 pb-2.5">
                        <SubscriptionUsageMeter
                          error={usageByService[service.id]?.error}
                          now={new Date()}
                          onReset={() =>
                            setConfirmAction({
                              kind: "reset-usage",
                              service,
                              availableCount:
                                usageByService[service.id]?.usage
                                  ?.rate_limit_reset_credits?.available_count ??
                                0,
                            })
                          }
                          resetting={actionID === service.id}
                          status={
                            usageByService[service.id]?.status ?? "loading"
                          }
                          usage={usageByService[service.id]?.usage}
                        />
                      </div>
                    ) : null}
                    <div className="mt-auto flex items-center justify-end gap-1.5 border-t bg-muted px-3.5 py-2">
                      <Button
                        disabled={acting}
                        onClick={() =>
                          onViewChange({
                            kind: "edit",
                            serviceId: service.id,
                          })
                        }
                        size="sm"
                        type="button"
                        variant="outline"
                      >
                        {t("services.editAction")}
                      </Button>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            aria-label={t("services.moreNamed", {
                              name: service.name,
                            })}
                            disabled={acting}
                            size="icon-sm"
                            type="button"
                            variant="outline"
                          >
                            <Ellipsis />
                          </Button>
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
                                  setLoginChoiceFlow(null);
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
                          {subscription ? <DropdownMenuSeparator /> : null}
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
                    </div>
                  </article>
                );
              })}
            </div>
          )}
        </div>
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
              <DialogDescription>{t("services.chooseOauthHint")}</DialogDescription>
            </DialogHeader>
              <RadioGroup
                aria-label={t("services.loginMethod")}
                className="grid grid-cols-2 gap-2 max-[520px]:grid-cols-1"
                onValueChange={(value) => setLoginChoiceFlow(value as AuthorizationFlow)}
                value={loginChoiceFlow ?? ""}
              >
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
          <Dialog open onOpenChange={(open) => !open && setAuthorizationDialog(null)}>
            <DialogContent className="max-w-[460px] sm:max-w-[460px]">
              <DialogHeader>
                <DialogTitle>{t("services.deviceCodeTitle")}</DialogTitle>
                <DialogDescription>
                  {t("services.deviceCodeDescription")}
                </DialogDescription>
              </DialogHeader>
              {authorizationDialog.requestedFlow === "browser" ? (
                <FormMessage tone="warning">
                  {t("services.portsBusyAuto")}
                </FormMessage>
              ) : null}
              {authorizationDialog.session.status === "pending" &&
              authorizationDialog.session.device_code ? (
                <>
                  <p className="text-sm leading-6 text-muted-foreground">
                    {t("services.enterDeviceCode")}
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
                          authorizationDialog.session.device_code?.user_code ?? "",
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
                    {t("services.deviceDisabledHint")}
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
                      ? authorizationDialog.session.error?.message ??
                        t("services.deviceFailed")
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
  const selectedPreset =
    draft.kind === "codex_subscription"
      ? null
      : httpServicePreset(
          draft.kind as HTTPServicePresetID,
          protocols,
        );
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
      onDiscoverModels={() => void discoverModels()}
      onModelEditorChange={setModelEditor}
      onRemoveModels={removeDraftModels}
    />
  );
  const protocolEditor = (
    <section
      aria-labelledby="service-capabilities-heading"
      className="grid gap-2.5 rounded-md border bg-card p-3"
    >
      <div className="flex items-start justify-between gap-3">
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
        <Badge className="mt-px shrink-0 tabular-nums" variant="secondary">
          {t("services.enabledItems", { count: draft.capabilities.length })}
        </Badge>
      </div>

      <div className="grid gap-1.5">
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
            <div
              className="grid min-w-0 grid-cols-[minmax(0,1fr)_minmax(0,232px)] items-center gap-2 rounded-md border bg-card px-2.5 py-2 max-[640px]:grid-cols-1"
              data-testid="service-capability-row"
              key={descriptor.id}
            >
              <Label className="flex min-w-0 flex-row items-center gap-2 text-xs text-text-secondary">
                <Checkbox
                  checked={Boolean(capability)}
                  onCheckedChange={(checked) =>
                    toggleCapability(descriptor, checked === true)
                  }
                />
                <span className="shrink-0">
                  {protocolLabel(descriptor.id)}
                </span>
                <code className="min-w-0 truncate font-mono text-micro text-muted-foreground">
                  {protocolEntryPath(descriptor.id)}
                </code>
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
                  value={
                    capability.convert_to ?? localConversionPassthrough
                  }
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
                <span className="text-micro text-muted-foreground max-[640px]:hidden">
                  {capability ? t("services.passthrough") : ""}
                </span>
              )}
            </div>
          );
        })}
      </div>
    </section>
  );
  const connectionFields = (
    <div className="grid grid-cols-2 gap-3 max-[680px]:grid-cols-1 [&>label]:flex [&>label]:min-w-0 [&>label]:flex-col [&>label]:items-stretch [&>label]:gap-1.5 [&>label>span]:text-xs [&>label>span]:font-medium [&>label>span]:text-text-secondary [&>label>small]:text-xs [&>label>small]:font-normal [&>label>small]:text-muted-foreground">
      <Label className="col-span-full">
        <span>{t("services.serviceType")}</span>
        <Select
          disabled={view.kind === "edit"}
          value={draft.kind}
          onValueChange={(value) => selectKind(value as ServiceKind)}
        >
          <SelectTrigger aria-label={t("services.serviceType")} className="w-full">
            <SelectValue>{serviceTypeOptionLabel(draft.kind)}</SelectValue>
          </SelectTrigger>
          <SelectContent>
          <SelectGroup>
            <SelectLabel>{t("services.groupSubscription")}</SelectLabel>
            <SelectItem value="codex_subscription">
              {serviceTypeOptionLabel("codex_subscription")}
            </SelectItem>
          </SelectGroup>
          <SelectGroup>
            <SelectLabel>{t("services.groupGateway")}</SelectLabel>
            <SelectItem value="newapi">
              {serviceTypeOptionLabel("newapi")}
            </SelectItem>
          </SelectGroup>
          <SelectGroup>
            <SelectLabel>{t("services.groupAdvanced")}</SelectLabel>
            {httpServicePresetIDs
              .filter((kind) => kind !== "newapi")
              .map((kind) => (
                <SelectItem key={kind} value={kind}>
                  {serviceTypeOptionLabel(kind)}
                </SelectItem>
              ))}
          </SelectGroup>
          </SelectContent>
        </Select>
        <small>
          {draft.kind === "codex_subscription"
            ? t("services.codexHint")
            : selectedPreset?.description}
        </small>
      </Label>
      <Label>
        <span>{t("services.serviceName")}</span>
        <Input
          id="service-name"
          maxLength={128}
          placeholder={
            draft.kind === "codex_subscription"
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
      </Label>
      <Label className="col-span-full flex-row! items-center!">
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
      {draft.kind === "codex_subscription" &&
      view.kind === "create" ? (
        <fieldset className="col-span-full min-w-0 rounded-md border bg-muted p-3">
          <legend className="px-1 text-xs font-medium text-text-secondary">{t("services.loginMethod")}</legend>
          <RadioGroup
            aria-label={t("services.newLoginMethod")}
            className="grid grid-cols-2 gap-2 max-[520px]:grid-cols-1"
            onValueChange={(value) =>
              setDraft((current) => ({
                ...current,
                authorizationFlow: value as AuthorizationFlow,
              }))
            }
            value={draft.authorizationFlow ?? ""}
          >
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
          </RadioGroup>
          {draft.authorizationFlow === null ? (
            <small className="mt-2 block text-warning-foreground">
              {t("services.chooseLoginContinue")}
            </small>
          ) : null}
        </fieldset>
      ) : null}
      {draft.kind !== "codex_subscription" ? (
        <>
          <Label>
            <span>{t("services.apiAddress")}</span>
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
          </Label>
          <Label>
            <span>{t("services.authScheme")}</span>
            <Select
              value={draft.authScheme}
              onValueChange={(value) =>
                setDraft((current) => ({
                  ...current,
                  authScheme: value as ServiceAuthScheme,
                }))
              }
            >
              <SelectTrigger aria-label={t("services.authScheme")} className="w-full">
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
          </Label>
          {draft.authScheme === "custom_header" ? (
            <Label>
              <span>{t("services.headerName")}</span>
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
            </Label>
          ) : null}
          {draft.authScheme !== "none" ? (
            <Label>
              <span>
                {editingKind
                  ? canKeepCredential
                    ? t("services.apiKeyKeep")
                    : t("services.apiKeyRequired")
                  : "API Key"}
              </span>
              <Input
                autoComplete="new-password"
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
            </Label>
          ) : null}
          {editing?.service.http?.credential_ref ? (
            <Label className="col-span-full flex-row! items-center!">
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
      ) : null}
      {draft.kind === "codex_subscription" ? (
        <div className="col-span-full flex items-start gap-2 rounded-md border border-success/20 bg-success-wash px-3 py-2.5 text-text-secondary">
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
      ) : null}
    </div>
  );
  const visibleEditorTab: EditorTab =
    draft.kind === "codex_subscription" && editorTab === "protocols"
      ? "models"
      : editorTab;

  return (
    <section className="flex min-h-0 w-full flex-1 flex-col" aria-labelledby="service-editor-heading">
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
          <fieldset className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden border-0 p-0 disabled:pointer-events-none disabled:opacity-70" disabled={!isReady || saving}>
            <Tabs
              className="min-h-0 flex-1 gap-3"
              onValueChange={(value) => setEditorTab(value as EditorTab)}
              value={visibleEditorTab}
            >
              <TabsList aria-label={t("services.tabsAria")} className="h-8 shrink-0">
                <TabsTrigger
                  data-testid="service-editor-tab-connection"
                  onClick={() => setEditorTab("connection")}
                  type="button"
                  value="connection"
                >
                  {t("services.tabConnection")}
                </TabsTrigger>
                <TabsTrigger
                  data-testid="service-editor-tab-models"
                  onClick={() => setEditorTab("models")}
                  type="button"
                  value="models"
                >
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
                {draft.kind === "codex_subscription" ? null : (
                  <TabsTrigger
                    data-testid="service-editor-tab-protocols"
                    onClick={() => setEditorTab("protocols")}
                    type="button"
                    value="protocols"
                  >
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
              </TabsList>
              <TabsContent
                className="min-h-0 flex-1 overflow-y-auto pr-4"
                data-tab-scroller=""
                data-testid="service-editor-tab-panel"
                value="connection"
              >
                {connectionFields}
              </TabsContent>
              <TabsContent
                className="min-h-0 flex-1 overflow-y-auto pr-4"
                data-tab-scroller=""
                data-testid="service-editor-tab-panel"
                value="models"
              >
                {modelsEditor}
              </TabsContent>
              {draft.kind === "codex_subscription" ? null : (
                <TabsContent
                  className="min-h-0 flex-1 overflow-y-auto pr-4"
                  data-tab-scroller=""
                  data-testid="service-editor-tab-panel"
                  value="protocols"
                >
                  {protocolEditor}
                </TabsContent>
              )}
            </Tabs>
          </fieldset>
          <div className="z-4 mt-3 shrink-0 border-t px-0 pt-2.5 pb-1">
            <Button
              className="w-full"
              data-testid="service-submit"
              disabled={
                !isReady ||
                saving ||
                (view.kind === "create" &&
                  draft.kind === "codex_subscription" &&
                  draft.authorizationFlow === null)
              }
              type="submit"
            >
              {saving
                ? t("common.saving")
                : view.kind === "edit"
                  ? t("services.saveChanges")
                  : draft.kind === "codex_subscription"
                    ? t("services.addAndLogin")
                    : t("services.saveService")}
            </Button>
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
