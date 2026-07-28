import { useEffect, useMemo, useRef, useState } from "react";

import {
  beginServiceAuthorization,
  cancelServiceAuthorization,
  createService,
  deleteService,
  getService,
  getServiceAuthorization,
  logoutService,
  openAuthorizationURL,
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
import {
  serviceKindLabel,
  serviceStatusLabel,
  type HTTPServiceKind,
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
    authorizationFlow: null,
    capabilities: service.capabilities.map((capability) => ({
      ...capability,
      ...(capability.models
        ? { models: [...capability.models] }
        : {}),
    })),
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
  const copyFeedback = useCopyFeedback();
  const loadGeneration = useRef(0);

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
    if (view.kind === "list") {
      setEditing(null);
      setBaseline(null);
      setLoadingRecord(false);
      onDirtyChange(false);
      return;
    }
    if (view.kind === "create") {
      const next = draftForKind("codex_subscription", protocols);
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
  }, [onDirtyChange, protocols, view]);

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
        };
        if (draft.kind !== "codex_subscription") {
          patch.http = {
            base_url: draft.baseURL.trim(),
            auth: authForDraft(draft),
            ...(draft.secret.trim()
              ? { credential: { secret: draft.secret } }
              : draft.removeCredential ||
                  (draft.authScheme === "none" &&
                    Boolean(editing.service.http?.credential_ref))
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
          };
        } else {
          input = {
            name: draft.name.trim(),
            kind: draft.kind as HTTPServiceKind,
            enabled: draft.enabled,
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
      <section className="service-manager" aria-labelledby="service-heading">
        <PageHeader
          actions={
            <>
              <button
                className="btn-primary"
                disabled={!isReady || busy}
                onClick={() => onViewChange({ kind: "create" })}
                type="button"
              >
                添加服务
              </button>
              <button
                className="btn-secondary"
                disabled={!isReady || busy}
                onClick={() => void onRefresh()}
                type="button"
              >
                {busy ? "刷新中…" : "刷新列表"}
              </button>
            </>
          }
          description="订阅与外部网关都是 API 服务；每次添加都会创建独立服务，可配置多个 Codex 账户。"
          eyebrow="API 服务"
          title="管理 API 服务"
          titleId="service-heading"
        />
        {!isReady || catalogStatus === "blocked" ? (
          <div className="service-manager__unavailable">
            Core 就绪后才能管理 API 服务。
          </div>
        ) : null}
        {catalogStatus === "error" && catalogError ? (
          <div className="form-message form-message--error" role="alert">
            {catalogError}
          </div>
        ) : null}
        {error ? (
          <div className="form-message form-message--error" role="alert">
            {error}
          </div>
        ) : null}
        {notice ? (
          <div className="form-message form-message--notice">{notice}</div>
        ) : null}

        <div className="service-manager__grid service-manager__grid--single">
          <div className="service-list" aria-label="已配置服务">
            <div className="panel-title">
              <strong>全部服务</strong>
              <span>{catalogStatus === "blocked" ? "—" : `${services.length} 个`}</span>
            </div>
            <div className="service-list__scroll">
              {catalogStatus === "loading" && services.length === 0 ? (
                <p className="service-list__empty">正在加载 API 服务…</p>
              ) : services.length === 0 ? (
                <p className="service-list__empty">
                  尚未添加服务。可先添加 Codex 订阅，或接入 new-api 外部网关。
                </p>
              ) : (
                services.map((service) => {
                  const subscription = service.subscription;
                  const acting = actionID === service.id;
                  return (
                    <article className="service-card" key={service.id}>
                      <div className="service-card__top">
                        <span className="service-card__state">
                          <span
                            aria-hidden="true"
                            className={`dot dot--${serviceDot(service)}`}
                          />
                          {serviceStatusLabel(service)}
                        </span>
                        <strong>{service.name}</strong>
                        <span className="service-card__kind">
                          {serviceKindLabel(service.kind)}
                        </span>
                      </div>
                      <code>
                        {service.http?.base_url ??
                          (subscription?.account_hint
                            ? `OpenAI 账户 ${subscription.account_hint}`
                            : "OpenAI Codex OAuth")}
                      </code>
                      <p>
                        支持 {service.capabilities.length} 项 API 能力
                        {subscription?.authorization_boundary
                          ? ` · ${subscription.authorization_boundary}`
                          : ""}
                      </p>
                      <div className="service-card__footer">
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
                        <div>
                          {subscription ? (
                            subscription.status === "authorizing" ? (
                              <>
                                <button
                                  disabled={acting}
                                  onClick={() => void showAuthorization(service)}
                                  type="button"
                                >
                                  {acting ? "处理中…" : "查看登录"}
                                </button>
                                <button
                                  disabled={acting}
                                  onClick={() => void cancelAuthorization(service)}
                                  type="button"
                                >
                                  取消登录
                                </button>
                              </>
                            ) : (
                              <button
                                disabled={acting}
                                onClick={() => {
                                  setLoginChoice(service);
                                  setLoginChoiceFlow(null);
                                  setError(null);
                                }}
                                type="button"
                              >
                                {acting
                                  ? "处理中…"
                                  : subscription.status === "connected"
                                    ? "重新登录"
                                    : "登录"}
                              </button>
                            )
                          ) : null}
                          {subscription?.status === "connected" ? (
                            <button
                              disabled={acting}
                              onClick={() =>
                                setConfirmAction({ kind: "logout", service })
                              }
                              type="button"
                            >
                              退出
                            </button>
                          ) : null}
                          <button
                            disabled={acting}
                            onClick={() =>
                              onViewChange({
                                kind: "edit",
                                serviceId: service.id,
                              })
                            }
                            type="button"
                          >
                            编辑
                          </button>
                          <button
                            className="danger-link"
                            disabled={acting}
                            onClick={() =>
                              setConfirmAction({ kind: "delete", service })
                            }
                            type="button"
                          >
                            {acting ? "处理中…" : "删除"}
                          </button>
                        </div>
                      </div>
                    </article>
                  );
                })
              )}
            </div>
          </div>
        </div>
        {confirmAction ? (
          <div className="token-dialog-backdrop" role="presentation">
            <section
              aria-labelledby="service-confirm-title"
              aria-modal="true"
              className="token-dialog"
              role="dialog"
            >
              <h3 id="service-confirm-title">
                {confirmAction.kind === "delete"
                  ? "删除这个服务？"
                  : "退出这个 Codex 账户？"}
              </h3>
              <p>
                {confirmAction.kind === "delete"
                  ? `“${confirmAction.service.name}”及其本机凭据会被删除；被路由引用时 Core 会拒绝删除。`
                  : `“${confirmAction.service.name}”的 OAuth 凭据将从系统钥匙串删除，服务会保留。`}
              </p>
              <div className="token-dialog__actions">
                <button
                  className="btn-secondary"
                  onClick={() => setConfirmAction(null)}
                  type="button"
                >
                  取消
                </button>
                <button
                  className="btn-primary"
                  onClick={() => void confirmDestructiveAction()}
                  type="button"
                >
                  确认
                </button>
              </div>
            </section>
          </div>
        ) : null}
        {loginChoice ? (
          <div className="token-dialog-backdrop" role="presentation">
            <section
              aria-labelledby="codex-login-method-title"
              aria-modal="true"
              className="token-dialog authorization-method-dialog"
              role="dialog"
            >
              <h3 id="codex-login-method-title">
                登录“{loginChoice.name}”
              </h3>
              <p>请选择本次使用的 OpenAI 登录方式。</p>
              <div className="authorization-flow-grid">
                <label
                  className={
                    loginChoiceFlow === "browser" ? "is-selected" : undefined
                  }
                >
                  <input
                    checked={loginChoiceFlow === "browser"}
                    name="existing-codex-login-flow"
                    onChange={() => setLoginChoiceFlow("browser")}
                    type="radio"
                  />
                  <span>
                    <strong>浏览器 OAuth</strong>
                    <small>依次使用本机回调端口 1455 和 1457。</small>
                  </span>
                </label>
                <label
                  className={
                    loginChoiceFlow === "device_code"
                      ? "is-selected"
                      : undefined
                  }
                >
                  <input
                    checked={loginChoiceFlow === "device_code"}
                    name="existing-codex-login-flow"
                    onChange={() => setLoginChoiceFlow("device_code")}
                    type="radio"
                  />
                  <span>
                    <strong>Device Code</strong>
                    <small>打开登录页并输入一次性验证码。</small>
                  </span>
                </label>
              </div>
              <div className="token-dialog__actions">
                <button
                  className="btn-secondary"
                  onClick={() => {
                    setLoginChoice(null);
                    setLoginChoiceFlow(null);
                  }}
                  type="button"
                >
                  取消
                </button>
                <button
                  className="btn-primary"
                  disabled={loginChoiceFlow === null}
                  onClick={() => {
                    if (loginChoiceFlow) {
                      void authorize(loginChoice, loginChoiceFlow);
                    }
                  }}
                  type="button"
                >
                  开始登录
                </button>
              </div>
            </section>
          </div>
        ) : null}
        {authorizationDialog ? (
          <div className="token-dialog-backdrop" role="presentation">
            <section
              aria-labelledby="device-code-title"
              aria-modal="true"
              className="token-dialog device-code-dialog"
              role="dialog"
            >
              <h3 id="device-code-title">使用 Device Code 登录</h3>
              {authorizationDialog.requestedFlow === "browser" ? (
                <p className="device-code-dialog__fallback" role="status">
                  本机回调端口 1455 和 1457 均不可用，已自动切换。
                </p>
              ) : null}
              {authorizationDialog.session.status === "pending" &&
              authorizationDialog.session.device_code ? (
                <>
                  <p>
                    请在 OpenAI 登录页面输入下方一次性验证码。验证码将在
                    15 分钟内失效。
                  </p>
                  <div className="device-code-dialog__code">
                    <code>
                      {authorizationDialog.session.device_code.user_code}
                    </code>
                    <button
                      className="btn-secondary"
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
                    </button>
                  </div>
                  <small className="device-code-dialog__note">
                    如果账户或工作区禁用了 Device Code，请改用浏览器 OAuth，
                    或由管理员启用该登录方式。
                  </small>
                  <div className="token-dialog__actions">
                    <button
                      className="btn-secondary"
                      disabled={actionID === authorizationDialog.service.id}
                      onClick={() =>
                        void cancelAuthorization(authorizationDialog.service)
                      }
                      type="button"
                    >
                      取消登录
                    </button>
                    <button
                      className="btn-primary"
                      onClick={() => void reopenAuthorizationPage()}
                      type="button"
                    >
                      重新打开登录页面
                    </button>
                  </div>
                </>
              ) : (
                <>
                  <p role="status">
                    {authorizationDialog.session.status === "failed"
                      ? authorizationDialog.session.error?.message ??
                        "Device Code 登录失败。"
                      : authorizationDialog.session.status === "expired"
                        ? "Device Code 已过期，请重新开始登录。"
                        : authorizationDialog.session.status === "cancelled"
                          ? "Device Code 登录已取消。"
                          : "登录已完成。"}
                  </p>
                  <div className="token-dialog__actions">
                    <button
                      className="btn-primary"
                      onClick={() => setAuthorizationDialog(null)}
                      type="button"
                    >
                      关闭
                    </button>
                  </div>
                </>
              )}
            </section>
          </div>
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
    <section className="service-manager" aria-labelledby="service-editor-heading">
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
        <div className="service-manager__unavailable">
          Core 就绪后才能管理 API 服务。
        </div>
      ) : null}
      {error ? (
        <div className="form-message form-message--error" role="alert">
          {error}
        </div>
      ) : null}
      {loadingRecord ? (
        <div className="service-manager__unavailable" aria-busy="true">
          正在载入服务配置…
        </div>
      ) : view.kind === "edit" && !editing ? (
        <div className="service-manager__unavailable">
          无法载入这个 API 服务。请返回列表后重试。
        </div>
      ) : (
        <form
          aria-busy={saving}
          className="service-form"
          noValidate
          onSubmit={(event) => void submit(event)}
        >
          <fieldset className="service-form__fields" disabled={!isReady || saving}>
            <div className="simple-form">
              <label>
                <span>服务类型</span>
                <select
                  disabled={view.kind === "edit"}
                  value={draft.kind}
                  onChange={(event) =>
                    selectKind(event.target.value as ServiceKind)
                  }
                >
                  <optgroup label="订阅服务（P0）">
                    <option value="codex_subscription">
                      Codex 订阅（OpenAI OAuth）
                    </option>
                  </optgroup>
                  <optgroup label="外部网关（P1）">
                    <option value="newapi">new-api</option>
                  </optgroup>
                  <optgroup label="高级 API 服务">
                    {httpServicePresetIDs
                      .filter((kind) => kind !== "newapi")
                      .map((kind) => (
                        <option key={kind} value={kind}>
                          {serviceKindLabel(kind)}
                        </option>
                      ))}
                  </optgroup>
                </select>
                <small>
                  {draft.kind === "codex_subscription"
                    ? "使用 Codex 官方公开 OAuth 客户端接入真实订阅账户。"
                    : selectedPreset?.description}
                </small>
              </label>
              <label>
                <span>服务名称</span>
                <input
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
              </label>
              <label className="advanced-check">
                <input
                  checked={draft.enabled}
                  onChange={(event) =>
                    setDraft((current) => ({
                      ...current,
                      enabled: event.target.checked,
                    }))
                  }
                  type="checkbox"
                />
                <span>启用这个服务</span>
              </label>
              {draft.kind === "codex_subscription" &&
              view.kind === "create" ? (
                <fieldset className="authorization-flow-picker">
                  <legend>登录方式</legend>
                  <div className="authorization-flow-grid">
                    <label
                      className={
                        draft.authorizationFlow === "browser"
                          ? "is-selected"
                          : undefined
                      }
                    >
                      <input
                        checked={draft.authorizationFlow === "browser"}
                        name="new-codex-login-flow"
                        onChange={() =>
                          setDraft((current) => ({
                            ...current,
                            authorizationFlow: "browser",
                          }))
                        }
                        type="radio"
                      />
                      <span>
                        <strong>浏览器 OAuth</strong>
                        <small>
                          打开系统浏览器，依次尝试本机回调端口 1455 和
                          1457。
                        </small>
                      </span>
                    </label>
                    <label
                      className={
                        draft.authorizationFlow === "device_code"
                          ? "is-selected"
                          : undefined
                      }
                    >
                      <input
                        checked={draft.authorizationFlow === "device_code"}
                        name="new-codex-login-flow"
                        onChange={() =>
                          setDraft((current) => ({
                            ...current,
                            authorizationFlow: "device_code",
                          }))
                        }
                        type="radio"
                      />
                      <span>
                        <strong>Device Code</strong>
                        <small>
                          打开 OpenAI 登录页并输入一次性验证码；部分工作区需要管理员启用。
                        </small>
                      </span>
                    </label>
                  </div>
                  {draft.authorizationFlow === null ? (
                    <small className="authorization-flow-picker__hint">
                      请选择一种登录方式后继续。
                    </small>
                  ) : null}
                </fieldset>
              ) : null}
              {draft.kind !== "codex_subscription" ? (
                <>
                  <label>
                    <span>API 地址</span>
                    <input
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
                  </label>
                  <label>
                    <span>认证方式</span>
                    <select
                      value={draft.authScheme}
                      onChange={(event) =>
                        setDraft((current) => ({
                          ...current,
                          authScheme:
                            event.target.value as ServiceAuthScheme,
                        }))
                      }
                    >
                      {Object.entries(authLabels).map(([scheme, label]) => (
                        <option key={scheme} value={scheme}>
                          {label}
                        </option>
                      ))}
                    </select>
                  </label>
                  {draft.authScheme === "custom_header" ? (
                    <label>
                      <span>认证 Header 名称</span>
                      <input
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
                    </label>
                  ) : null}
                  {draft.authScheme !== "none" ? (
                    <label>
                      <span>
                        {editingKind
                          ? canKeepCredential
                            ? "API Key（留空保留）"
                            : "API Key（需填写）"
                          : "API Key"}
                      </span>
                      <input
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
                    </label>
                  ) : null}
                  {editing?.service.http?.credential_ref ? (
                    <label className="advanced-check">
                      <input
                        checked={draft.removeCredential}
                        onChange={(event) =>
                          setDraft((current) => ({
                            ...current,
                            removeCredential: event.target.checked,
                          }))
                        }
                        type="checkbox"
                      />
                      <span>保存时删除已存 API Key</span>
                    </label>
                  ) : null}
                </>
              ) : null}
            </div>

            {draft.kind === "codex_subscription" ? (
              <div className="preset-summary">
                <span aria-hidden="true">✓</span>
                <div>
                  <strong>
                    {view.kind === "create"
                      ? "保存后使用所选方式登录"
                      : "订阅登录在服务列表中管理"}
                  </strong>
                  <p>
                    每次添加都会创建独立服务，可同时管理多个 Codex 订阅账户。
                  </p>
                </div>
              </div>
            ) : (
              <details className="advanced-settings">
                <summary>
                  <span>API 能力</span>
                  <small>{draft.capabilities.length} 项已启用</small>
                </summary>
                <div className="advanced-settings__body">
                  <div className="service-capability-grid">
                    {descriptors.map((descriptor) => {
                      const capability = draft.capabilities.find(
                        (item) => item.protocol === descriptor.id,
                      );
                      return (
                        <div className="service-capability-option" key={descriptor.id}>
                          <label className="advanced-check">
                            <input
                              checked={Boolean(capability)}
                              onChange={(event) =>
                                toggleCapability(
                                  descriptor,
                                  event.target.checked,
                                )
                              }
                              type="checkbox"
                            />
                            <span>{protocolLabel(descriptor.id)}</span>
                          </label>
                          {capability ? (
                            <select
                              aria-label={`${protocolLabel(descriptor.id)} 处理模式`}
                              value={capability.mode}
                              onChange={(event) =>
                                setDraft((current) => ({
                                  ...current,
                                  capabilities: current.capabilities.map(
                                    (item) =>
                                      item.protocol === descriptor.id
                                        ? {
                                            ...item,
                                            mode: event.target.value as
                                              | "native"
                                              | "delegated",
                                          }
                                        : item,
                                  ),
                                }))
                              }
                            >
                              <option value="native">原生协议</option>
                              <option value="delegated">由网关路由</option>
                            </select>
                          ) : null}
                        </div>
                      );
                    })}
                  </div>
                </div>
              </details>
            )}
          </fieldset>
          <div className="service-form__actions">
            <button
              className="btn-primary service-form__submit"
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
            </button>
          </div>
        </form>
      )}
    </section>
  );
}
