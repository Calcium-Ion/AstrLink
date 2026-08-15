import { useEffect, useMemo, useRef, useState } from "react";

import { ChoiceCard } from "@/components/ChoiceCard";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { EmptyState } from "@/components/EmptyState";
import { FormMessage } from "@/components/FormMessage";
import { StatusDot } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
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
import { cn } from "@/lib/utils";

import {
  beginServiceAuthorization,
  cancelServiceAuthorization,
  createService,
  deleteService,
  getService,
  getServiceAuthorization,
  logoutService,
  openAuthorizationURL,
  probeDraftServiceModels,
  probeServiceModels,
  updateService,
} from "./bridge";
import { copyButtonLabel, useCopyFeedback } from "./copy-feedback";
import {
  httpServicePreset,
  httpServicePresetIDs,
  protocolDescriptors,
  protocolLabel,
  type HTTPServicePresetID,
  type ProtocolDescriptor,
} from "./service-presets";
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
  type AuthorizationFlow,
  type AuthorizationSession,
} from "./subscription-model";

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
  | null;

type AuthorizationDialog = {
  service: Service;
  requestedFlow: AuthorizationFlow;
  session: AuthorizationSession;
};

type ModelPreview = {
  models: string[];
  selected: string[];
  warnings: string[];
};

const authLabels: Record<ServiceAuthScheme, string> = {
  none: "无需认证",
  bearer: "API Key（Bearer）",
  anthropic_api_key: "Anthropic API Key",
  google_api_key: "Google API Key",
  custom_header: "自定义 Header",
};

function draftForKind(
  kind: ServiceKind,
  protocols: readonly ProtocolDescriptor[],
): Draft {
  if (kind === "codex_subscription") {
    return {
      kind,
      name: "Codex 订阅",
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
  if (!service.http) throw new Error("HTTP 服务缺少连接配置。");
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
    capabilities: service.capabilities.map((capability) => ({ ...capability })),
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
    return "服务名称需包含 1 至 128 个字符。";
  }
  if (draft.models.length > 2_000) return "每个服务最多配置 2,000 个模型。";
  if (
    draft.models.some(
      (model) => [...model].length < 1 || [...model].length > 256,
    ) || new Set(draft.models).size !== draft.models.length
  ) {
    return "模型 ID 必须唯一，且每项包含 1 至 256 个字符。";
  }
  if (draft.kind === "codex_subscription") {
    if (!editing && draft.authorizationFlow === null) {
      return "请选择 Codex 登录方式。";
    }
    return null;
  }
  let parsed: URL;
  try {
    parsed = new URL(draft.baseURL.trim());
  } catch {
    return "请输入有效的 API 地址。";
  }
  if (
    (parsed.protocol !== "http:" && parsed.protocol !== "https:") ||
    parsed.username !== "" ||
    parsed.password !== "" ||
    parsed.search !== "" ||
    parsed.hash !== ""
  ) {
    return "API 地址必须是安全的 http(s) 根地址，且不能包含凭据、查询或片段。";
  }
  if (draft.authScheme === "custom_header" && draft.headerName.trim() === "") {
    return "自定义认证需要填写 Header 名称。";
  }
  const hasStoredCredential = Boolean(editing?.service.http?.credential_ref);
  if (
    draft.authScheme !== "none" &&
    draft.secret.trim() === "" &&
    !hasStoredCredential
  ) {
    return "请填写 API Key。";
  }
  if (draft.capabilities.length === 0) {
    return "HTTP 服务至少需要一项 API 能力。";
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
          <DialogTitle>选择服务支持的模型</DialogTitle>
          <DialogDescription>确认后，服务模型清单将替换为下面勾选的项目。</DialogDescription>
        </DialogHeader>
        {preview.warnings.length > 0 ? (
          <FormMessage tone="warning">
            部分协议获取失败：{preview.warnings.join("；")}
          </FormMessage>
        ) : null}
        {preview.models.length > 0 ? (
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <Input
              className="h-8 min-w-0 flex-[1_1_160px]"
              aria-label="搜索上游模型"
              placeholder="搜索模型…"
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
                {hasQuery ? `全选匹配（${filtered.length}）` : "全选"}
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
                {hasQuery ? "取消匹配" : "全不选"}
              </Button>
            </div>
          </div>
        ) : null}
        <div className="my-2 grid max-h-[min(52vh,460px)] gap-1 overflow-auto">
          {preview.models.length === 0 ? (
            <p>上游没有返回模型；确认后将应用空清单。</p>
          ) : filtered.length === 0 ? (
            <p>没有匹配“{query.trim()}”的模型。</p>
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
                <code className="min-w-0 truncate font-mono text-xs">{encodeModelEditorValue(model)}</code>
              </Label>
            ))
          )}
        </div>
        <small className="mb-2 block text-xs text-muted-foreground">
          已选 {preview.selected.length}
          {hasQuery ? ` · 显示 ${filtered.length} / ${preview.models.length}` : ""}
        </small>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} type="button">
            取消
          </Button>
          <Button onClick={onApply} type="button">
            应用所选模型（{preview.selected.length}）
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function ServiceManager({
  isReady,
  protocols,
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
  const [notice, setNotice] = useState<string | null>(null);
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
  const copyFeedback = useCopyFeedback();
  const loadGeneration = useRef(0);
  const protocolsRef = useRef(protocols);
  protocolsRef.current = protocols;
  const viewKind = view.kind;
  const editingServiceID = view.kind === "edit" ? view.serviceId : null;

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
        setError(errorMessage(cause, "无法读取服务配置。"));
      })
      .finally(() => {
        if (loadGeneration.current === generation) setLoadingRecord(false);
      });
  }, [editingServiceID, viewKind]);

  useEffect(() => {
    const authorizing = services.filter(
      (service) => service.subscription?.status === "authorizing",
    );
    if (!isReady || authorizing.length === 0) return;
    let cancelled = false;
    const check = async () => {
      let completed = false;
      await Promise.all(
        authorizing.map(async (service) => {
          try {
            const session = await getServiceAuthorization(service.id);
            if (session.status !== "pending") completed = true;
          } catch {
            completed = true;
          }
        }),
      );
      if (!cancelled && completed) await onRefresh();
    };
    const timer = window.setInterval(() => void check(), 1_500);
    void check();
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [isReady, onRefresh, services]);

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
          setNotice(`“${active.service.name}”已登录。`);
          await onRefresh();
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
          setError(errorMessage(cause, "无法读取 Codex 登录状态。"));
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
    isReady,
    onRefresh,
  ]);

  const selectKind = (kind: ServiceKind) => {
    const next = draftForKind(kind, protocols);
    setDraft(next);
    setError(null);
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
            mode: current.kind === "newapi" ? "delegated" : "native",
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
      setError("请输入至少一个模型 ID；批量添加时每行一个。");
      return;
    }
    if (additions.some((model) => [...model].length > 256)) {
      setError("模型 ID 不能超过 256 个字符。");
      return;
    }
    const models = [...new Set([...draft.models, ...additions])].sort();
    if (models.length > 2_000) {
      setError("每个服务最多配置 2,000 个模型。");
      return;
    }
    setDraft((current) => ({ ...current, models }));
    setModelEditor("");
    setError(null);
  };

  const discoverModels = async () => {
    if (draft.kind === "codex_subscription" && !editing) {
      setError("请先保存并完成 Codex 登录，再从上游获取模型。");
      return;
    }
    const discoveryProtocols: ModelDiscoveryProtocol[] =
      draft.kind === "codex_subscription"
        ? ["openai.models"]
        : (["openai.models", "google.models"] as const).filter((protocol) =>
            draft.capabilities.some((capability) => capability.protocol === protocol),
          );
    if (discoveryProtocols.length === 0) {
      setError("请先在 API 能力中启用 OpenAI Models 或 Gemini Models。");
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
            `${protocolLabel(discoveryProtocols[index] ?? "models")}：${errorMessage(attempt.reason, "获取失败")}`,
          );
        }
      });
      if (warnings.length === attempts.length) {
        setError(`无法从上游获取模型。${warnings.join("；")}`);
        return;
      }
      const models = [...new Set([...draft.models, ...discovered])].sort();
      if (models.length > 2_000) {
        setError("上游模型与当前清单合并后超过 2,000 项，草稿未作更改。");
        return;
      }
      setModelPreviewQuery("");
      setModelPreview({ models, selected: [...models], warnings });
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
      setNotice(
        requestedFlow === "browser"
          ? "回调端口 1455 和 1457 均不可用，已切换为 Device Code 登录。"
          : "Device Code 登录已开始，请在浏览器中输入一次性验证码。",
      );
      return;
    }
    setAuthorizationDialog(null);
    setNotice(`已为“${service.name}”打开浏览器登录。`);
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
    setNotice(null);
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
          patch.capabilities = draft.capabilities;
        }
        record = await updateService(
          editing.service.id,
          editing.etag,
          patch,
        );
        setNotice("服务配置已更新。");
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
            capabilities: draft.capabilities,
          };
        }
        record = await createService(input);
        if (record.service.kind === "codex_subscription") {
          try {
            const flow = draft.authorizationFlow;
            if (flow === null) {
              throw new Error("请选择 Codex 登录方式。");
            }
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
            setNotice("Codex 服务已添加，可稍后从服务列表重新登录。");
            setError(errorMessage(cause, "服务已添加，但无法开始 OAuth 登录。"));
          }
        } else {
          setNotice("服务已添加，API Key 只保存在本机凭据库中。");
        }
      }
      onServiceSaved(record.service);
      setEditing(null);
      setBaseline(null);
      onDirtyChange(false);
      onViewChange({ kind: "list" });
      await onRefresh();
    } catch (cause) {
      setError(errorMessage(cause, "保存服务失败。"));
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
      const result = await beginServiceAuthorization(service.id, flow);
      presentAuthorization(service, flow, result.session);
      await onRefresh();
    } catch (cause) {
      setError(errorMessage(cause, "无法开始 Codex 登录。"));
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
      setNotice(`已取消“${service.name}”的登录。`);
      await onRefresh();
    } catch (cause) {
      setError(errorMessage(cause, "无法取消 Codex 登录。"));
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
        setNotice(`“${service.name}”正在等待浏览器登录回调。`);
      }
    } catch (cause) {
      setError(errorMessage(cause, "无法读取 Codex 登录状态。"));
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
      setError(errorMessage(cause, "无法打开 Device Code 登录页面。"));
    }
  };

  const confirmDestructiveAction = async () => {
    if (!confirmAction) return;
    const { service } = confirmAction;
    setConfirmAction(null);
    setActionID(service.id);
    setError(null);
    try {
      if (confirmAction.kind === "logout") {
        const record = await logoutService(service.id);
        onServiceSaved(record.service);
        setNotice(`已退出“${service.name}”。`);
      } else {
        const record = await getService(service.id);
        await deleteService(service.id, record.etag);
        onServiceRemoved(service.id);
        setNotice(`已删除“${service.name}”。`);
      }
      await onRefresh();
    } catch (cause) {
      setError(
        errorMessage(
          cause,
          confirmAction.kind === "logout"
            ? "退出登录失败。"
            : "删除服务失败。",
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
                添加服务
              </Button>
              <Button
                variant="outline"
                disabled={!isReady || busy}
                onClick={() => void onRefresh()}
                type="button"
              >
                {busy ? "刷新中…" : "刷新列表"}
              </Button>
            </>
          }
          description="订阅与外部网关都是 API 服务；每次添加都会创建独立服务，可配置多个 Codex 账户。"
          eyebrow="API 服务"
          title="管理 API 服务"
          titleId="service-heading"
        />
        {!isReady || catalogStatus === "blocked" ? (
          <FormMessage className="mb-3" tone="notice">
            Core 就绪后才能管理 API 服务。
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
        {notice ? (
          <FormMessage className="mb-3" tone="success">{notice}</FormMessage>
        ) : null}

        <div className="flex min-h-0 min-w-0 flex-1 flex-col" aria-label="已配置服务">
          <div className="mb-2 flex shrink-0 items-baseline justify-between gap-2">
            <strong className="text-sm font-semibold tracking-tight">
              全部服务
            </strong>
            <span className="text-xs text-muted-foreground">
              {catalogStatus === "blocked" ? "—" : `${services.length} 个`}
            </span>
          </div>
          {/* Cards rather than flat rows: a service carries four lines of
              heterogeneous detail, which needs its own bounding box to read. */}
          <div className="grid min-h-0 content-start gap-2 overflow-y-auto">
              {catalogStatus === "loading" && services.length === 0 ? (
                <EmptyState title="正在加载 API 服务…" />
              ) : services.length === 0 ? (
                <EmptyState
                  description="可先添加 Codex 订阅，或接入 new-api 外部网关。"
                  title="尚未添加服务"
                />
              ) : (
                services.map((service) => {
                  const subscription = service.subscription;
                  const acting = actionID === service.id;
                  return (
                    <article
                      className="min-w-0 rounded-md border bg-card"
                      data-testid="service-card"
                      key={service.id}
                    >
                      <div className="min-w-0 px-3.5 py-3">
                      <div className="flex min-w-0 items-center gap-2">
                        <StatusDot tone={serviceDot(service)} />
                        <strong className="truncate text-sm font-medium">{service.name}</strong>
                        <span className="shrink-0 text-xs text-muted-foreground">
                          {serviceStatusLabel(service)}
                        </span>
                        <Badge className="ml-auto" variant="secondary">
                          {serviceKindLabel(service.kind)}
                        </Badge>
                      </div>
                      <code className="mt-1.5 block truncate font-mono text-xs text-text-secondary">
                        {service.http?.base_url ??
                          (subscription?.account_hint
                            ? `OpenAI 账户 ${subscription.account_hint}`
                            : "OpenAI Codex OAuth")}
                      </code>
                      <p className="mt-1 truncate text-xs text-muted-foreground">
                        支持 {service.capabilities.length} 项 API 能力 · {service.models.length} 个模型
                        {subscription?.authorization_boundary
                          ? ` · ${subscription.authorization_boundary}`
                          : ""}
                      </p>
                      </div>
                      <div className="flex items-center justify-between gap-3 border-t px-3.5 py-2 text-xs text-muted-foreground max-[700px]:items-start max-[700px]:flex-col max-[700px]:gap-2">
                        <span>
                          {service.http
                            ? service.http.credential_ref
                              ? "API Key 已安全保存"
                              : service.http.auth.scheme === "none"
                                ? "无需 API Key"
                                : "尚未保存 API Key"
                            : subscription?.status === "connected"
                              ? "OAuth 凭据已存入系统钥匙串"
                              : "等待 OAuth 登录"}
                        </span>
                        <div className="flex shrink-0 flex-wrap items-center gap-1">
                          {subscription ? (
                            subscription.status === "authorizing" ? (
                              <>
                                <Button
                                  disabled={acting}
                                  onClick={() => void showAuthorization(service)}
                                  size="sm"
                                  type="button"
                                  variant="outline"
                                >
                                  {acting ? "处理中…" : "查看登录"}
                                </Button>
                                <Button
                                  disabled={acting}
                                  onClick={() => void cancelAuthorization(service)}
                                  size="sm"
                                  type="button"
                                  variant="ghost"
                                >
                                  取消登录
                                </Button>
                              </>
                            ) : (
                              <Button
                                disabled={acting}
                                onClick={() => {
                                  setLoginChoice(service);
                                  setLoginChoiceFlow(null);
                                  setError(null);
                                }}
                                size="sm"
                                type="button"
                                variant="outline"
                              >
                                {acting
                                  ? "处理中…"
                                  : subscription.status === "connected"
                                    ? "重新登录"
                                    : "登录"}
                              </Button>
                            )
                          ) : null}
                          {subscription?.status === "connected" ? (
                            <Button
                              disabled={acting}
                              onClick={() =>
                                setConfirmAction({ kind: "logout", service })
                              }
                              size="sm"
                              type="button"
                              variant="ghost"
                            >
                              退出
                            </Button>
                          ) : null}
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
                            编辑
                          </Button>
                          <Button
                            className="text-danger-foreground hover:bg-danger-wash hover:text-danger-foreground"
                            disabled={acting}
                            onClick={() =>
                              setConfirmAction({ kind: "delete", service })
                            }
                            size="sm"
                            type="button"
                            variant="ghost"
                          >
                            {acting ? "处理中…" : "删除"}
                          </Button>
                        </div>
                      </div>
                    </article>
                  );
                })
              )}
            </div>
        </div>
        <ConfirmDialog
          confirmLabel="确认"
          description={
            <p>
              {confirmAction?.kind === "delete"
                ? `“${confirmAction.service.name}”及其本机凭据会被删除；被路由引用时 Core 会拒绝删除。`
                : `“${confirmAction?.service.name ?? ""}”的 OAuth 凭据将从系统钥匙串删除，服务会保留。`}
            </p>
          }
          destructive
          onCancel={() => setConfirmAction(null)}
          onConfirm={() => void confirmDestructiveAction()}
          open={confirmAction !== null}
          title={
            confirmAction?.kind === "delete"
              ? "删除这个服务？"
              : "退出这个 Codex 账户？"
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
              <DialogTitle>登录“{loginChoice?.name ?? ""}”</DialogTitle>
              <DialogDescription>请选择本次使用的 OpenAI 登录方式。</DialogDescription>
            </DialogHeader>
              <RadioGroup
                aria-label="登录方式"
                className="grid grid-cols-2 gap-2 max-[520px]:grid-cols-1"
                onValueChange={(value) => setLoginChoiceFlow(value as AuthorizationFlow)}
                value={loginChoiceFlow ?? ""}
              >
                <ChoiceCard
                  description="依次使用本机回调端口 1455 和 1457。"
                  label="浏览器 OAuth"
                  selected={loginChoiceFlow === "browser"}
                  value="browser"
                />
                <ChoiceCard
                  description="打开登录页并输入一次性验证码。"
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
                  取消
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
                  开始登录
                </Button>
              </DialogFooter>
          </DialogContent>
        </Dialog>
        {authorizationDialog ? (
          <Dialog open onOpenChange={(open) => !open && setAuthorizationDialog(null)}>
            <DialogContent className="max-w-[460px] sm:max-w-[460px]">
              <DialogHeader>
                <DialogTitle>使用 Device Code 登录</DialogTitle>
                <DialogDescription>
                  在 OpenAI 登录页面完成本次 Codex 账户授权。
                </DialogDescription>
              </DialogHeader>
              {authorizationDialog.requestedFlow === "browser" ? (
                <FormMessage tone="warning">
                  本机回调端口 1455 和 1457 均不可用，已自动切换。
                </FormMessage>
              ) : null}
              {authorizationDialog.session.status === "pending" &&
              authorizationDialog.session.device_code ? (
                <>
                  <p className="text-sm leading-6 text-muted-foreground">
                    请在 OpenAI 登录页面输入下方一次性验证码。验证码将在
                    15 分钟内失效。
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
                        "复制验证码",
                      )}
                    </Button>
                  </div>
                  <small className="mt-2 block text-xs text-muted-foreground">
                    如果账户或工作区禁用了 Device Code，请改用浏览器 OAuth，
                    或由管理员启用该登录方式。
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
                      取消登录
                    </Button>
                    <Button
                      onClick={() => void reopenAuthorizationPage()}
                      type="button"
                    >
                      重新打开登录页面
                    </Button>
                  </DialogFooter>
                </>
              ) : (
                <>
                  <p className="text-sm text-muted-foreground" role="status">
                    {authorizationDialog.session.status === "failed"
                      ? authorizationDialog.session.error?.message ??
                        "Device Code 登录失败。"
                      : authorizationDialog.session.status === "expired"
                        ? "Device Code 已过期，请重新开始登录。"
                        : authorizationDialog.session.status === "cancelled"
                          ? "Device Code 登录已取消。"
                          : "登录已完成。"}
                  </p>
                  <DialogFooter>
                    <Button
                      onClick={() => setAuthorizationDialog(null)}
                      type="button"
                    >
                      关闭
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

  return (
    <section className="flex min-h-0 w-full flex-1 flex-col" aria-labelledby="service-editor-heading">
      <PageHeader
        back={{
          label: "返回服务列表",
          onClick: () => onViewChange({ kind: "list" }),
        }}
        description={
          view.kind === "create"
            ? "Codex 订阅与外部网关都从这里添加，并分别获得独立 service_id。"
            : "服务类型不可变；可修改名称、启用状态与对应连接配置。"
        }
        eyebrow="API 服务"
        title={view.kind === "edit" ? "编辑服务" : "添加服务"}
        titleId="service-editor-heading"
      />
      {!isReady ? (
        <FormMessage className="mb-3" tone="notice">
          Core 就绪后才能管理 API 服务。
        </FormMessage>
      ) : null}
      {error ? (
        <FormMessage className="mb-3" tone="error">
          {error}
        </FormMessage>
      ) : null}
      {loadingRecord ? (
        <FormMessage className="mb-3" aria-busy="true" tone="notice">
          正在载入服务配置…
        </FormMessage>
      ) : view.kind === "edit" && !editing ? (
        <FormMessage className="mb-3" tone="error">
          无法载入这个 API 服务。请返回列表后重试。
        </FormMessage>
      ) : (
        <form
          aria-busy={saving}
          className="mx-auto w-full max-w-[760px] min-w-0 rounded-lg border bg-card p-4"
          data-testid="service-form"
          noValidate
          onSubmit={(event) => void submit(event)}
        >
          <fieldset className="min-w-0 border-0 p-0 disabled:pointer-events-none disabled:opacity-70" disabled={!isReady || saving}>
            <div className="grid grid-cols-2 gap-3 max-[680px]:grid-cols-1 [&>label]:flex [&>label]:min-w-0 [&>label]:flex-col [&>label]:items-stretch [&>label]:gap-1.5 [&>label>span]:text-xs [&>label>span]:font-medium [&>label>span]:text-text-secondary [&>label>small]:text-xs [&>label>small]:font-normal [&>label>small]:text-muted-foreground">
              <Label className="col-span-full">
                <span>服务类型</span>
                <Select
                  disabled={view.kind === "edit"}
                  value={draft.kind}
                  onValueChange={(value) => selectKind(value as ServiceKind)}
                >
                  <SelectTrigger aria-label="服务类型" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                  <SelectGroup>
                    <SelectLabel>订阅服务（P0）</SelectLabel>
                    <SelectItem value="codex_subscription">
                      Codex 订阅（OpenAI OAuth）
                    </SelectItem>
                  </SelectGroup>
                  <SelectGroup>
                    <SelectLabel>外部网关（P1）</SelectLabel>
                    <SelectItem value="newapi">new-api</SelectItem>
                  </SelectGroup>
                  <SelectGroup>
                    <SelectLabel>高级 API 服务</SelectLabel>
                    {httpServicePresetIDs
                      .filter((kind) => kind !== "newapi")
                      .map((kind) => (
                        <SelectItem key={kind} value={kind}>
                          {serviceKindLabel(kind)}
                        </SelectItem>
                      ))}
                  </SelectGroup>
                  </SelectContent>
                </Select>
                <small>
                  {draft.kind === "codex_subscription"
                    ? "使用 Codex 官方公开 OAuth 客户端接入真实订阅账户。"
                    : selectedPreset?.description}
                </small>
              </Label>
              <Label>
                <span>服务名称</span>
                <Input
                  id="service-name"
                  maxLength={128}
                  placeholder={
                    draft.kind === "codex_subscription"
                      ? "例如：个人 Codex"
                      : "例如：团队 new-api"
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
                <span>启用这个服务</span>
              </Label>
              {draft.kind === "codex_subscription" &&
              view.kind === "create" ? (
                <fieldset className="col-span-full min-w-0 rounded-md border bg-muted p-3">
                  <legend className="px-1 text-xs font-medium text-text-secondary">登录方式</legend>
                  <RadioGroup
                    aria-label="新服务登录方式"
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
                      description="打开系统浏览器，依次尝试本机回调端口 1455 和 1457。"
                      label="浏览器 OAuth"
                      selected={draft.authorizationFlow === "browser"}
                      value="browser"
                    />
                    <ChoiceCard
                      description="打开 OpenAI 登录页并输入一次性验证码；部分工作区需要管理员启用。"
                      label="Device Code"
                      selected={draft.authorizationFlow === "device_code"}
                      value="device_code"
                    />
                  </RadioGroup>
                  {draft.authorizationFlow === null ? (
                    <small className="mt-2 block text-warning-foreground">
                      请选择一种登录方式后继续。
                    </small>
                  ) : null}
                </fieldset>
              ) : null}
              {draft.kind !== "codex_subscription" ? (
                <>
                  <Label>
                    <span>API 地址</span>
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
                    <span>认证方式</span>
                    <Select
                      value={draft.authScheme}
                      onValueChange={(value) =>
                        setDraft((current) => ({
                          ...current,
                          authScheme: value as ServiceAuthScheme,
                        }))
                      }
                    >
                      <SelectTrigger aria-label="认证方式" className="w-full">
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
                      <span>认证 Header 名称</span>
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
                            ? "API Key（留空保留）"
                            : "API Key（需填写）"
                          : "API Key"}
                      </span>
                      <Input
                        autoComplete="new-password"
                        maxLength={16_384}
                        placeholder={
                          canKeepCredential
                            ? "已安全保存，无需重复输入"
                            : "粘贴 API Key"
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
                      <span>保存时删除已存 API Key</span>
                    </Label>
                  ) : null}
                </>
              ) : null}
            </div>

            <ServiceModelsEditor
              key={editingServiceID ?? "create"}
              modelEditor={modelEditor}
              models={draft.models}
              probingModels={probingModels}
              onAddModels={addModels}
              onDiscoverModels={() => void discoverModels()}
              onModelEditorChange={setModelEditor}
              onRemoveModels={removeDraftModels}
              onReplaceModels={(models) =>
                setDraft((current) => ({ ...current, models }))
              }
            />

            {draft.kind === "codex_subscription" ? (
              <div className="mt-3 flex items-start gap-2 rounded-md border border-success/20 bg-success-wash px-3 py-2.5 text-text-secondary">
                <StatusDot className="mt-1.5" tone="positive" />
                <div>
                  <strong className="text-sm font-medium text-success-foreground">
                    {view.kind === "create"
                      ? "保存后使用所选方式登录"
                      : "订阅登录在服务列表中管理"}
                  </strong>
                  <p className="mt-0.5 text-xs">
                    每次添加都会创建独立服务，可同时管理多个 Codex 订阅账户。
                  </p>
                </div>
              </div>
            ) : (
              <details className="group mt-3 rounded-md border bg-card">
                <summary className="flex cursor-pointer list-none items-center gap-1.5 px-3 py-2.5 text-sm font-medium text-text-secondary before:text-base before:leading-none before:text-muted-foreground before:content-['›'] group-open:before:rotate-90 [&::-webkit-details-marker]:hidden">
                  <span>API 能力</span>
                  <small className="ml-auto text-xs font-normal text-muted-foreground">{draft.capabilities.length} 项已启用</small>
                </summary>
                <div className="border-t p-3">
                  <div className="grid grid-cols-2 gap-2 max-[600px]:grid-cols-1">
                    {descriptors.map((descriptor) => {
                      const capability = draft.capabilities.find(
                        (item) => item.protocol === descriptor.id,
                      );
                      return (
                        <div className="grid min-w-0 gap-1.5 rounded-md border bg-card p-2.5" key={descriptor.id}>
                          <Label className="flex flex-row items-center gap-2 text-xs text-text-secondary">
                            <Checkbox
                              checked={Boolean(capability)}
                              onCheckedChange={(checked) =>
                                toggleCapability(
                                  descriptor,
                                  checked === true,
                                )
                              }
                            />
                            <span>{protocolLabel(descriptor.id)}</span>
                          </Label>
                          {capability ? (
                            <Select
                              value={capability.mode}
                              onValueChange={(value) =>
                                setDraft((current) => ({
                                  ...current,
                                  capabilities: current.capabilities.map(
                                    (item) =>
                                      item.protocol === descriptor.id
                                        ? {
                                            ...item,
                                            mode: value as
                                              | "native"
                                              | "delegated",
                                          }
                                        : item,
                                  ),
                                }))
                              }
                            >
                              <SelectTrigger
                                aria-label={`${protocolLabel(descriptor.id)} 处理模式`}
                                className="w-full"
                              >
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                <SelectItem value="native">原生协议</SelectItem>
                                <SelectItem value="delegated">由网关路由</SelectItem>
                              </SelectContent>
                            </Select>
                          ) : null}
                        </div>
                      );
                    })}
                  </div>
                </div>
              </details>
            )}
          </fieldset>
          <div className="sticky bottom-0 z-4 mt-[15px] border-t bg-card px-0 pt-2.5 pb-1">
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
                ? "保存中…"
                : view.kind === "edit"
                  ? "保存修改"
                  : draft.kind === "codex_subscription"
                    ? "添加并登录"
                    : "保存服务"}
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
            setDraft((current) => ({
              ...current,
              models: [...modelPreview.selected].sort(),
            }));
            setModelPreview(null);
            setModelPreviewQuery("");
          }}
        />
      ) : null}
    </section>
  );
}
