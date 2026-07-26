import {
  type FormEvent,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import {
  createEndpoint,
  deleteEndpoint,
  getEndpoint,
  updateEndpoint,
} from "./bridge";
import {
  activeDraft,
  bindDraftSecret,
  buildCreateInput,
  buildPatch,
  canKeepStoredCredential,
  createDraftSession,
  editDraftSession,
  hasCurrentDraftSecret,
  restoreActivePreset,
  switchDraftProfile,
  updateActiveDraft,
  updateDraftIdentity,
  validateDraftIssue,
  type DraftProfileID,
  type EndpointDraft,
  type EndpointDraftSession,
} from "./endpoint-draft";
import type {
  AuthScheme,
  Endpoint,
  EndpointCapability,
  EndpointRecord,
} from "./endpoint-model";
import {
  decodeModelEditorValue,
  encodeModelEditorValue,
} from "./model-editor";
import {
  endpointKindLabel,
  endpointPreset,
  endpointProfileIDs,
  endpointProfileLabel,
  protocolDescriptors,
  protocolLabel,
  type EndpointProfileID,
  type ProtocolDescriptor,
} from "./endpoint-presets";

const authLabels: Record<AuthScheme, string> = {
  none: "无认证",
  bearer: "API Key（Bearer）",
  anthropic_api_key: "Anthropic API Key",
  google_api_key: "Google API Key",
  custom_header: "自定义 Header",
};

const modeLabels: Record<EndpointCapability["mode"], string> = {
  native: "上游原生处理",
  delegated: "由聚合服务路由",
};

const recommendedProfileIDs: EndpointProfileID[] = [
  "newapi",
  "subscription_openai",
  "subscription_anthropic",
  "subscription_gemini",
];
const recommendedProfileIDSet = new Set<EndpointProfileID>(
  recommendedProfileIDs,
);
const otherProfileIDs = endpointProfileIDs.filter(
  (profileID) => !recommendedProfileIDSet.has(profileID),
);

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : "上游配置操作失败。";
}

function updateCapability(
  draft: EndpointDraft,
  index: number,
  update: (capability: EndpointCapability) => EndpointCapability,
): EndpointDraft {
  return {
    ...draft,
    capabilities: draft.capabilities.map((capability, capabilityIndex) =>
      capabilityIndex === index ? update(capability) : capability,
    ),
  };
}

function profileDescription(
  profileID: DraftProfileID,
  protocols: readonly ProtocolDescriptor[],
): string {
  return profileID === "current"
    ? "继续编辑当前配置草稿，不会猜测或改写协议能力。"
    : endpointPreset(profileID, protocols).description;
}

function sessionSignature(session: EndpointDraftSession): string {
  const drafts = Object.entries(session.drafts)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([profileID, draft]) => {
      if (!draft) return [profileID, null] as const;
      const { advancedOpen: _advancedOpen, ...persistedDraft } = draft;
      return [profileID, persistedDraft] as const;
    });
  return JSON.stringify({
    activeProfile: session.activeProfile,
    drafts,
  });
}

function ModelValueInput({
  id,
  label,
  value,
  onChange,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
}) {
  const [displayValue, setDisplayValue] = useState(() =>
    encodeModelEditorValue(value),
  );

  useEffect(() => {
    setDisplayValue((current) =>
      decodeModelEditorValue(current) === value
        ? current
        : encodeModelEditorValue(value),
    );
  }, [value]);

  return (
    <input
      id={id}
      aria-label={label}
      value={displayValue}
      onChange={(event) => {
        const next = event.target.value;
        setDisplayValue(next);
        onChange(decodeModelEditorValue(next));
      }}
      onBlur={() => setDisplayValue(encodeModelEditorValue(value))}
    />
  );
}

export type EndpointManagerView =
  | { kind: "list" }
  | { kind: "create" }
  | { kind: "edit"; endpointId: string };

export type EndpointCatalogStatus =
  | "blocked"
  | "loading"
  | "ready"
  | "error";

export interface EndpointManagerProps {
  isReady: boolean;
  protocols: ProtocolDescriptor[];
  view: EndpointManagerView;
  endpoints: Endpoint[];
  catalogStatus: EndpointCatalogStatus;
  catalogError: string | null;
  onRefresh: () => void | Promise<void>;
  onViewChange: (view: EndpointManagerView) => void;
  onEndpointSaved: (endpoint: Endpoint) => void;
  onEndpointRemoved: (id: string) => void;
  onDirtyChange: (dirty: boolean) => void;
}

export function EndpointManager({
  isReady,
  protocols,
  view,
  endpoints,
  catalogStatus,
  catalogError,
  onRefresh,
  onViewChange,
  onEndpointSaved,
  onEndpointRemoved,
  onDirtyChange,
}: EndpointManagerProps) {
  const availableProtocols = useMemo(
    () => protocolDescriptors(protocols),
    [protocols],
  );
  const [editing, setEditing] = useState<EndpointRecord | null>(null);
  const [session, setSession] = useState<EndpointDraftSession>(() =>
    createDraftSession(protocols),
  );
  const [baselineSignature, setBaselineSignature] = useState<string | null>(
    null,
  );
  const [loadingRecord, setLoadingRecord] = useState(false);
  const [saving, setSaving] = useState(false);
  const [removingID, setRemovingID] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [focusTargetID, setFocusTargetID] = useState<string | null>(null);
  const editGeneration = useRef(0);
  const recordRequests = useRef(
    new Map<string, Promise<EndpointRecord>>(),
  );
  const draft = activeDraft(session);
  const canKeepCredential = canKeepStoredCredential(session);
  const busy = loadingRecord || saving;
  const viewKey =
    view.kind === "edit" ? `edit:${view.endpointId}` : view.kind;
  const editorLoaded =
    view.kind === "create" ||
    (view.kind === "edit" && editing?.endpoint.id === view.endpointId);
  const currentSignature = useMemo(
    () => sessionSignature(session),
    [session],
  );
  const dirty =
    view.kind !== "list" &&
    editorLoaded &&
    baselineSignature !== null &&
    currentSignature !== baselineSignature;

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

  useEffect(() => {
    const generation = editGeneration.current + 1;
    editGeneration.current = generation;
    setError(null);
    setFocusTargetID(null);
    setBaselineSignature(null);

    if (view.kind === "list") {
      setEditing(null);
      setLoadingRecord(false);
      onDirtyChange(false);
      return;
    }

    setNotice(null);
    if (view.kind === "create") {
      const next = createDraftSession(protocols);
      setEditing(null);
      setSession(next);
      setBaselineSignature(sessionSignature(next));
      setLoadingRecord(false);
      onDirtyChange(false);
      return;
    }

    setEditing(null);
    setLoadingRecord(true);
    const endpointID = view.endpointId;
    let request = recordRequests.current.get(endpointID);
    if (!request) {
      request = getEndpoint(endpointID);
      recordRequests.current.set(endpointID, request);
      const clearRequest = () => {
        if (recordRequests.current.get(endpointID) === request) {
          recordRequests.current.delete(endpointID);
        }
      };
      void request.then(clearRequest, clearRequest);
    }

    void request.then(
      (record) => {
        if (editGeneration.current !== generation) return;
        const next = editDraftSession(record);
        setEditing(record);
        setSession(next);
        setBaselineSignature(sessionSignature(next));
        setError(null);
        setNotice(
          "已完整载入全部能力。API Key 留空会保留原值，且不会回读到界面。",
        );
        setLoadingRecord(false);
        onDirtyChange(false);
      },
      (requestError) => {
        if (editGeneration.current !== generation) return;
        setError(messageOf(requestError));
        setLoadingRecord(false);
        onDirtyChange(false);
      },
    );
  }, [viewKey]);

  useEffect(() => {
    if (!focusTargetID) return;
    const target = document.getElementById(focusTargetID);
    if (!target) return;
    target.focus();
    setFocusTargetID(null);
  }, [draft.advancedOpen, focusTargetID]);

  const remove = async (id: string) => {
    if (!window.confirm("删除这个服务及其本地凭据？此操作不可撤销。")) return;
    setRemovingID(id);
    setError(null);
    setNotice(null);
    try {
      const record = await getEndpoint(id);
      await deleteEndpoint(id, record.etag);
      onEndpointRemoved(id);
      setNotice("服务及其凭据已删除。");
    } catch (requestError) {
      setError(messageOf(requestError));
    } finally {
      setRemovingID(null);
    }
  };

  const setDraft = (update: (current: EndpointDraft) => EndpointDraft) => {
    setSession((current) => updateActiveDraft(current, update));
  };

  const selectProfile = (profileID: DraftProfileID) => {
    const hasExistingDraft = Boolean(session.drafts[profileID]);
    setSession((current) =>
      switchDraftProfile(current, profileID, protocols),
    );
    setError(null);
    setNotice(
      profileID === "current"
        ? "已切换到当前配置草稿。"
        : hasExistingDraft
          ? "已恢复这个服务类型的完整草稿。"
          : profileID === "custom"
            ? "已切换到自定义服务，请在高级配置中添加协议能力。"
            : "已自动配置认证方式与已确认的保守能力集合。",
    );
  };

  const selectAuthScheme = (authScheme: AuthScheme) => {
    if (
      authScheme === "none" &&
      draft.authScheme !== "none" &&
      editing?.endpoint.credential_ref &&
      !window.confirm("改为无认证后，保存时会删除这个服务已存的 API Key。继续吗？")
    ) {
      return;
    }
    setDraft((current) =>
      updateDraftIdentity(current, (identityDraft) => ({
        ...identityDraft,
        authScheme,
        headerName:
          authScheme === "custom_header" ? identityDraft.headerName : "",
        secret: authScheme === "none" ? "" : identityDraft.secret,
        secretIdentity:
          authScheme === "none" ? null : identityDraft.secretIdentity,
        removeCredential:
          authScheme === "none" && Boolean(editing?.endpoint.credential_ref),
      })),
    );
  };

  const addCapability = () => {
    const used = new Set(
      draft.capabilities.map(
        (capability) => `${capability.protocol}\u0000${capability.mode}`,
      ),
    );
    let next:
      | {
          descriptor: ProtocolDescriptor;
          mode: EndpointCapability["mode"];
        }
      | undefined;
    const preferredMode = draft.capabilities[0]?.mode ?? "native";
    const modes =
      preferredMode === "native"
        ? (["native", "delegated"] as const)
        : (["delegated", "native"] as const);
    for (const descriptor of availableProtocols) {
      for (const mode of modes) {
        if (!used.has(`${descriptor.id}\u0000${mode}`)) {
          next = { descriptor, mode };
          break;
        }
      }
      if (next) break;
    }
    let fallbackIndex = draft.capabilities.length + 1;
    while (used.has(`custom.protocol${fallbackIndex}\u0000native`)) {
      fallbackIndex += 1;
    }
    setDraft((current) => ({
      ...current,
      capabilities: [
        ...current.capabilities,
        next
          ? {
              protocol: next.descriptor.id,
              mode: next.mode,
              streaming: next.descriptor.streaming,
            }
          : {
              protocol: `custom.protocol${fallbackIndex}`,
              mode: "native",
              streaming: false,
            },
      ],
    }));
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!isReady || busy || view.kind === "list" || !editorLoaded) return;
    const validationIssue = validateDraftIssue(session);
    if (validationIssue) {
      if (validationIssue.advanced && !draft.advancedOpen) {
        setSession((current) =>
          updateActiveDraft(current, (currentDraft) => ({
            ...currentDraft,
            advancedOpen: true,
          })),
        );
      }
      setFocusTargetID(validationIssue.targetID);
      setError(validationIssue.message);
      return;
    }

    setSaving(true);
    setError(null);
    setNotice(null);
    try {
      if (editing) {
        const patch = buildPatch(session);
        if (Object.keys(patch).length === 0) {
          setBaselineSignature(currentSignature);
          onDirtyChange(false);
          setNotice("配置没有变化。");
          return;
        }
        const updated = await updateEndpoint(
          editing.endpoint.id,
          editing.etag,
          patch,
        );
        const next = editDraftSession(updated);
        setEditing(updated);
        setSession(next);
        setBaselineSignature(sessionSignature(next));
        onEndpointSaved(updated.endpoint);
        onDirtyChange(false);
        setNotice("服务配置已更新。API Key 已从界面清空。");
      } else {
        const created = await createEndpoint(buildCreateInput(session));
        const next = createDraftSession(protocols);
        setSession(next);
        setBaselineSignature(sessionSignature(next));
        onEndpointSaved(created.endpoint);
        onDirtyChange(false);
        setNotice("服务配置已保存。API Key 只保存在本机凭据库中。");
      }
    } catch (requestError) {
      setError(messageOf(requestError));
    } finally {
      setSaving(false);
    }
  };

  const needsCredential =
    draft.authScheme !== "none" &&
    !hasCurrentDraftSecret(draft) &&
    !canKeepCredential;
  const selectedProfileDescription = profileDescription(
    session.activeProfile,
    protocols,
  );

  if (view.kind === "list") {
    const catalogBusy =
      catalogStatus === "loading" || removingID !== null;
    return (
      <section className="endpoint-manager" aria-labelledby="endpoint-heading">
        <div className="endpoint-manager__header">
          <div>
            <span className="section-kicker">API 服务</span>
            <h2 id="endpoint-heading">管理 API 服务</h2>
            <p>
              在这里管理上游订阅与直连服务。API Key 只写入本机凭据库，
              不会回读到列表。
            </p>
          </div>
          <div className="endpoint-manager__actions">
            <button
              className="btn-secondary"
              type="button"
              onClick={() => onViewChange({ kind: "create" })}
              disabled={!isReady || catalogBusy}
            >
              添加服务
            </button>{" "}
            <button
              className="btn-secondary"
              type="button"
              onClick={() => void onRefresh()}
              disabled={!isReady || catalogBusy}
            >
              {catalogStatus === "loading" ? "刷新中…" : "刷新列表"}
            </button>
          </div>
        </div>

        {!isReady || catalogStatus === "blocked" ? (
          <div className="endpoint-manager__unavailable">
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

        <div className="endpoint-manager__grid endpoint-manager__grid--single">
          <div className="endpoint-list" aria-label="已配置服务">
            <div className="panel-title">
              <strong>已配置</strong>
              <span>
                {catalogStatus === "blocked" && endpoints.length === 0
                  ? "—"
                  : `${endpoints.length} 个`}
              </span>
            </div>
            <div className="endpoint-list__scroll">
              {catalogStatus === "blocked" && endpoints.length === 0 ? (
                <p className="endpoint-list__empty">
                  Core 就绪后将读取已配置服务。
                </p>
              ) : catalogStatus === "loading" && endpoints.length === 0 ? (
                <p className="endpoint-list__empty">正在加载 API 服务…</p>
              ) : catalogStatus === "error" && endpoints.length === 0 ? (
                <p className="endpoint-list__empty">
                  暂时无法显示服务列表，请重试。
                </p>
              ) : endpoints.length === 0 ? (
                <p className="endpoint-list__empty">
                  还没有 API 服务。添加第一个订阅后即可开始使用。
                </p>
              ) : (
                endpoints.map((endpoint) => (
                  <article className="endpoint-card" key={endpoint.id}>
                    <div className="endpoint-card__top">
                      <span className="endpoint-card__state">
                        <span
                          className={`dot dot--${endpoint.enabled ? "positive" : "neutral"}`}
                          aria-hidden="true"
                        />
                        {endpoint.enabled ? "已启用" : "已停用"}
                      </span>
                      <strong>{endpoint.name}</strong>
                      <span className="endpoint-card__kind">
                        {endpointKindLabel(endpoint.kind)}
                      </span>
                    </div>
                    <code>{endpoint.base_url}</code>
                    <p>支持 {endpoint.capabilities.length} 项 API 能力</p>
                    <div className="endpoint-card__footer">
                      <span>
                        {endpoint.credential_ref
                          ? "API Key 已安全保存"
                          : endpoint.auth.scheme === "none"
                            ? "无需 API Key"
                            : "尚未保存 API Key"}
                      </span>
                      <div>
                        <button
                          type="button"
                          onClick={() =>
                            onViewChange({
                              kind: "edit",
                              endpointId: endpoint.id,
                            })
                          }
                          disabled={!isReady || catalogBusy}
                        >
                          编辑
                        </button>
                        <button
                          className="danger-link"
                          type="button"
                          onClick={() => void remove(endpoint.id)}
                          disabled={!isReady || catalogBusy}
                        >
                          {removingID === endpoint.id ? "删除中…" : "删除"}
                        </button>
                      </div>
                    </div>
                  </article>
                ))
              )}
            </div>
          </div>
        </div>
      </section>
    );
  }

  return (
    <section
      aria-label={view.kind === "edit" ? "编辑 API 服务" : "添加 API 服务"}
      className="endpoint-manager"
    >
      {!isReady ? (
        <div className="endpoint-manager__unavailable">Core 就绪后才能管理 API 服务。</div>
      ) : null}
      {error ? (
        <div className="form-message form-message--error" role="alert">
          {error}
        </div>
      ) : null}
      {notice ? <div className="form-message form-message--notice">{notice}</div> : null}

      <div className="endpoint-manager__grid endpoint-manager__grid--single">
        {view.kind === "edit" && loadingRecord ? (
          <div className="endpoint-manager__unavailable" aria-busy="true">
            正在载入服务配置…
          </div>
        ) : view.kind === "edit" && !editing ? (
          <div className="endpoint-manager__unavailable">
            无法载入这个 API 服务。请返回列表后重试。
          </div>
        ) : (
        <form
          className="endpoint-form"
          onSubmit={submit}
          aria-busy={busy}
          noValidate
        >
          <fieldset className="endpoint-form__fields" disabled={busy}>
          <div className="simple-form">
            <label>
              <span>服务类型</span>
              <select
                value={session.activeProfile}
                onChange={(event) =>
                  selectProfile(event.target.value as DraftProfileID)
                }
              >
                {session.drafts.current ? (
                  <option value="current">当前配置（原样保留）</option>
                ) : null}
                <optgroup label="推荐：API 订阅">
                  {recommendedProfileIDs.map((profileID) => (
                    <option key={profileID} value={profileID}>
                      {endpointProfileLabel(profileID)}
                    </option>
                  ))}
                </optgroup>
                <optgroup label="其他兼容服务">
                  {otherProfileIDs.map((profileID) => (
                    <option key={profileID} value={profileID}>
                      {endpointProfileLabel(profileID)}
                    </option>
                  ))}
                </optgroup>
              </select>
              <small>{selectedProfileDescription}</small>
            </label>

            <label>
              <span>服务名称</span>
              <input
                id="endpoint-name"
                required
                value={draft.name}
                onChange={(event) =>
                  setDraft((current) => ({
                    ...current,
                    name: event.target.value,
                  }))
                }
                placeholder="例如：我的主订阅"
              />
            </label>

            <label>
              <span>API 地址</span>
              <input
                id="endpoint-base-url"
                required
                type="url"
                maxLength={2048}
                value={draft.baseURL}
                onChange={(event) =>
                  setDraft((current) =>
                    updateDraftIdentity(current, (identityDraft) => ({
                      ...identityDraft,
                      baseURL: event.target.value,
                    })),
                  )
                }
                placeholder={
                  session.activeProfile === "current"
                    ? "https://api.example.com"
                    : endpointPreset(
                        session.activeProfile as EndpointProfileID,
                        protocols,
                      ).baseURLPlaceholder
                }
              />
              <small>填写服务商提供的 API 根地址即可。</small>
            </label>

            {draft.authScheme !== "none" ? (
              <label>
                <span>
                  {editing
                    ? canKeepCredential
                      ? "API Key（留空保留）"
                      : "API Key（需重新填写）"
                    : "API Key"}
                </span>
                <input
                  id="endpoint-secret"
                  required={needsCredential}
                  type="password"
                  autoComplete="new-password"
                  maxLength={16_384}
                  value={draft.secret}
                  onChange={(event) =>
                    setDraft((current) =>
                      bindDraftSecret(current, event.target.value),
                    )
                  }
                  placeholder={canKeepCredential ? "已安全保存，无需重复输入" : "粘贴 API Key"}
                />
                <small>
                  {canKeepCredential
                    ? "已保存的 Key 不会显示；留空即保持不变。"
                    : "Key 仅写入本机凭据库，不会出现在普通配置中。"}
                </small>
              </label>
            ) : (
              <p className="simple-form__no-auth">当前配置无需 API Key。</p>
            )}
          </div>

          <div
            className={`preset-summary${
              draft.capabilities.length === 0
                ? " preset-summary--needs-action"
                : ""
            }`}
          >
            <span aria-hidden="true">
              {draft.capabilities.length > 0 ? "✓" : "!"}
            </span>
            <div>
              <strong>
                {draft.capabilities.length > 0
                  ? "当前草稿包含协议能力"
                  : "还需添加协议能力"}
              </strong>
              <p>
                {draft.capabilities.length > 0
                  ? `当前草稿包含 ${draft.capabilities.length} 项能力；保存前会进行完整校验。`
                  : "请在下方高级配置中至少添加一项；完成前无法保存。"}
              </p>
            </div>
          </div>

          <details
            className="advanced-settings"
            open={draft.advancedOpen}
            aria-disabled={busy}
            onClick={(event) => {
              if (busy) event.preventDefault();
            }}
            onToggle={(event) => {
              if (busy) return;
              const open = event.currentTarget.open;
              setSession((current) =>
                activeDraft(current).advancedOpen === open
                  ? current
                  : updateActiveDraft(current, (currentDraft) => ({
                      ...currentDraft,
                      advancedOpen: open,
                    })),
              );
            }}
          >
            <summary>
              <span>高级配置</span>
              <small>一般无需修改</small>
            </summary>
            <div className="advanced-settings__body">
              <div className="advanced-settings__toolbar">
                <p>更改这里的选项可能影响服务兼容性。</p>
                {session.activeProfile !== "current" ? (
                  <button
                    type="button"
                    onClick={() =>
                      setSession((current) =>
                        restoreActivePreset(current, protocols),
                      )
                    }
                  >
                    恢复此类型默认值
                  </button>
                ) : null}
              </div>

              <div className="advanced-grid">
                <label>
                  <span>认证方式</span>
                  <select
                    value={draft.authScheme}
                    onChange={(event) =>
                      selectAuthScheme(event.target.value as AuthScheme)
                    }
                  >
                    {Object.entries(authLabels).map(([scheme, label]) => (
                      <option key={scheme} value={scheme}>
                        {label}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="advanced-check">
                  <input
                    type="checkbox"
                    checked={draft.enabled}
                    onChange={(event) =>
                      setDraft((current) => ({
                        ...current,
                        enabled: event.target.checked,
                      }))
                    }
                  />
                  <span>启用这个服务</span>
                </label>
                {draft.authScheme === "custom_header" ? (
                  <label className="advanced-grid__wide">
                    <span>认证 Header 名称</span>
                    <input
                      id="endpoint-auth-header"
                      required
                      maxLength={128}
                      value={draft.headerName}
                      onChange={(event) =>
                        setDraft((current) =>
                          updateDraftIdentity(current, (identityDraft) => ({
                            ...identityDraft,
                            headerName: event.target.value,
                          })),
                        )
                      }
                      placeholder="X-Api-Key"
                    />
                  </label>
                ) : null}
                {draft.authScheme === "none" &&
                editing?.endpoint.credential_ref ? (
                  <label className="advanced-check advanced-grid__wide">
                    <input
                      type="checkbox"
                      checked={draft.removeCredential}
                      onChange={(event) =>
                        setDraft((current) => ({
                          ...current,
                          removeCredential: event.target.checked,
                        }))
                      }
                    />
                    <span>保存时删除这个服务已存的 API Key</span>
                  </label>
                ) : null}
              </div>

              <div className="capability-editor">
                <div className="capability-editor__header">
                  <div>
                    <strong>API 能力</strong>
                    <p>一个服务可以同时支持多种协议和处理模式。</p>
                  </div>
                  <button
                    id="endpoint-add-capability"
                    type="button"
                    onClick={addCapability}
                  >
                    + 添加能力
                  </button>
                </div>

                <datalist id="endpoint-protocol-options">
                  {availableProtocols.map((protocol) => (
                    <option key={protocol.id} value={protocol.id}>
                      {protocolLabel(protocol.id)}
                    </option>
                  ))}
                </datalist>

                {draft.capabilities.length === 0 ? (
                  <div className="capability-editor__empty">
                    尚未添加能力。自定义服务至少需要一项。
                  </div>
                ) : (
                  <div className="capability-list">
                    {draft.capabilities.map((capability, index) => {
                      const descriptor = availableProtocols.find(
                        (protocol) => protocol.id === capability.protocol,
                      );
                      return (
                        <div
                          className="capability-row"
                          key={index}
                        >
                          <div className="capability-row__title">
                            <strong>
                              {protocolLabel(capability.protocol)}
                            </strong>
                            <button
                              type="button"
                              onClick={() =>
                                setDraft((current) => ({
                                  ...current,
                                  capabilities: current.capabilities.filter(
                                    (_item, capabilityIndex) =>
                                      capabilityIndex !== index,
                                  ),
                                }))
                              }
                            >
                              删除
                            </button>
                          </div>
                          <div className="capability-row__grid">
                            <label>
                              <span>协议 ID</span>
                              <input
                                id={`endpoint-capability-${index}-protocol`}
                                required
                                list="endpoint-protocol-options"
                                value={capability.protocol}
                                onChange={(event) => {
                                  const protocol = event.target.value;
                                  const metadata = availableProtocols.find(
                                    (item) => item.id === protocol,
                                  );
                                  setDraft((current) =>
                                    updateCapability(
                                      current,
                                      index,
                                      (item) => ({
                                        ...item,
                                        protocol,
                                        streaming: metadata
                                          ? metadata.streaming && item.streaming
                                          : item.streaming,
                                      }),
                                    ),
                                  );
                                }}
                              />
                            </label>
                            <label>
                              <span>处理方式</span>
                              <select
                                value={capability.mode}
                                onChange={(event) =>
                                  setDraft((current) =>
                                    updateCapability(
                                      current,
                                      index,
                                      (item) => ({
                                        ...item,
                                        mode: event.target
                                          .value as EndpointCapability["mode"],
                                      }),
                                    ),
                                  )
                                }
                              >
                                {Object.entries(modeLabels).map(([mode, label]) => (
                                  <option key={mode} value={mode}>
                                    {label}
                                  </option>
                                ))}
                              </select>
                            </label>
                            <fieldset className="capability-row__models">
                              <legend>限定模型（可选，每项独立）</legend>
                              <div className="capability-model-list">
                                {(capability.models ?? []).map(
                                  (model, modelIndex) => (
                                    <div
                                      className="capability-model-row"
                                      key={modelIndex}
                                    >
                                      <ModelValueInput
                                        id={`endpoint-capability-${index}-model-${modelIndex}`}
                                        label={`模型 ${modelIndex + 1}`}
                                        value={model}
                                        onChange={(value) => {
                                          setDraft((current) =>
                                            updateCapability(
                                              current,
                                              index,
                                              (item) => ({
                                                ...item,
                                                models: (
                                                  item.models ?? []
                                                ).map(
                                                  (currentModel, currentIndex) =>
                                                    currentIndex === modelIndex
                                                      ? value
                                                      : currentModel,
                                                ),
                                              }),
                                            ),
                                          );
                                        }}
                                      />
                                      <button
                                        type="button"
                                        onClick={() =>
                                          setDraft((current) =>
                                            updateCapability(
                                              current,
                                              index,
                                              (item) => {
                                                const models = (
                                                  item.models ?? []
                                                ).filter(
                                                  (_currentModel, currentIndex) =>
                                                    currentIndex !== modelIndex,
                                                );
                                                return {
                                                  ...item,
                                                  ...(models.length > 0
                                                    ? { models }
                                                    : { models: undefined }),
                                                };
                                              },
                                            ),
                                          )
                                        }
                                      >
                                        删除模型
                                      </button>
                                    </div>
                                  ),
                                )}
                              </div>
                              <small>
                                {"控制字符使用 \\r、\\n、\\t 或 \\uXXXX 转义显示。"}
                              </small>
                              <button
                                id={`endpoint-capability-${index}-add-model`}
                                className="capability-model-add"
                                type="button"
                                onClick={() =>
                                  setDraft((current) =>
                                    updateCapability(
                                      current,
                                      index,
                                      (item) => ({
                                        ...item,
                                        models: [...(item.models ?? []), ""],
                                      }),
                                    ),
                                  )
                                }
                              >
                                + 添加模型
                              </button>
                            </fieldset>
                            <label className="capability-row__streaming">
                              <input
                                type="checkbox"
                                checked={capability.streaming}
                                disabled={descriptor?.streaming === false}
                                onChange={(event) =>
                                  setDraft((current) =>
                                    updateCapability(
                                      current,
                                      index,
                                      (item) => ({
                                        ...item,
                                        streaming: event.target.checked,
                                      }),
                                    ),
                                  )
                                }
                              />
                              <span>支持流式响应</span>
                            </label>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            </div>
          </details>

          <div className="endpoint-form__actions">
            <button
              className="btn-primary endpoint-form__submit"
              type="submit"
              disabled={!isReady || busy}
            >
              {saving ? "保存中…" : editing ? "保存修改" : "保存服务"}
            </button>
          </div>
          </fieldset>
        </form>
        )}
      </div>
    </section>
  );
}
