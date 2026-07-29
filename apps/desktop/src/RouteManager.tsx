import {
  type FormEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import { AutoRoutingShowcase } from "./AutoRoutingShowcase";
import {
  createRoute,
  deleteRoute,
  getRoute,
  listRoutes,
  updateRoute,
} from "./bridge";
import { PageHeader } from "./PageHeader";
import {
  type RoutableService,
  type ServiceCapability,
} from "./service-model";
import { protocolLabel, type ProtocolDescriptor } from "./service-presets";
import type {
  Route,
  RouteCreateInput,
  RouteRecord,
  RouteTarget,
} from "./route-model";

interface RouteDraftTarget {
  serviceId: string;
  planType: "native" | "delegated";
  priority: string;
  upstreamModel: string;
}

interface RouteDraft {
  name: string;
  enabled: boolean;
  priority: string;
  protocol: string;
  publicModel: string;
  targets: RouteDraftTarget[];
}

type CatalogState = {
  status: "blocked" | "loading" | "ready" | "error";
  items: Route[];
  error: string | null;
  stale: boolean;
};

type EditorState =
  | { kind: "create"; record: null }
  | { kind: "edit"; record: RouteRecord };

interface RouteManagerProps {
  coreSessionKey: string | null;
  services: RoutableService[];
  isReady: boolean;
  onDirtyChange: (dirty: boolean) => void;
  onManageServices: () => void;
  protocols: ProtocolDescriptor[];
}

const inferenceProtocolIDs = new Set([
  "openai.responses",
  "openai.responses.compact",
  "anthropic.messages",
  "google.generate_content",
  "openai.chat",
  "openai.completions",
]);

const emptyCatalog: CatalogState = {
  status: "blocked",
  items: [],
  error: null,
  stale: false,
};

const modeLabels: Record<"native" | "delegated", string> = {
  native: "原生",
  delegated: "委托",
};

function messageOf(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

function modesFor(
  service: RoutableService | undefined,
  protocol: string,
): Array<"native" | "delegated"> {
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
): ServiceCapability | undefined {
  return service?.capabilities.find(
    (capability) =>
      capability.protocol === protocol && capability.mode === mode,
  );
}

function compatibleServices(
  services: RoutableService[],
  protocol: string,
): RoutableService[] {
  return services.filter((service) =>
    service.capabilities.some(
      (capability) => capability.protocol === protocol,
    ),
  );
}

function availableProtocolIDs(
  services: RoutableService[],
  protocols: ProtocolDescriptor[],
): string[] {
  const serviceProtocolIDs = new Set(
    services.flatMap((service) =>
      service.capabilities
        .filter((capability) => inferenceProtocolIDs.has(capability.protocol))
        .map((capability) => capability.protocol),
    ),
  );
  const ordered = protocols
    .filter(
      (protocol) =>
        protocol.phase === "alpha" &&
        inferenceProtocolIDs.has(protocol.id) &&
        serviceProtocolIDs.has(protocol.id),
    )
    .map((protocol) => protocol.id);
  for (const id of serviceProtocolIDs) {
    if (!ordered.includes(id)) ordered.push(id);
  }
  return ordered;
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
    name: "默认路由",
    enabled: true,
    priority: "100",
    protocol,
    publicModel: "",
    targets: target ? [target] : [],
  };
}

function draftFromRecord(record: RouteRecord): RouteDraft {
  const route = record.route;
  if (route.selection?.mode === "auto" || !route.targets) {
    throw new Error("自动分类路由仍在训练门槛内，当前客户端不会编辑它。");
  }
  return {
    name: route.name,
    enabled: route.enabled,
    priority: String(route.priority),
    protocol: route.match.protocol,
    publicModel: route.match.model ?? "",
    targets: route.targets.map((target) => ({
      serviceId: target.service_id,
      planType:
        target.plan_type === "delegated" ? "delegated" : "native",
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
  const name = draft.name.trim();
  if (name.length === 0 || [...name].length > 128) {
    return "路由名称需要包含 1 到 128 个字符。";
  }
  if (draft.publicModel === "astrlink/auto") {
    return "astrlink/auto 仍在分类器训练门槛内，不能作为普通路由或别名保存。";
  }
  if ([...draft.publicModel].length > 256) {
    return "公开模型名不能超过 256 个字符。";
  }
  if (parsePriority(draft.priority) === null) {
    return "路由优先级需要是 0 到 1000000 之间的整数。";
  }
  if (draft.targets.length === 0) {
    return "至少需要一个可执行目标。";
  }
  const seenServices = new Set<string>();
  for (const [index, target] of draft.targets.entries()) {
    const service = services.find(
      (candidate) => candidate.id === target.serviceId,
    );
    if (!service) return `目标 ${index + 1} 引用的 API 服务不存在。`;
    if (seenServices.has(service.id)) {
      return "同一路由中每个 API 服务只能出现一次；多个模型请创建独立别名路由。";
    }
    seenServices.add(service.id);
    const capability = capabilityFor(
      service,
      draft.protocol,
      target.planType,
    );
    if (!capability) {
      return `目标 ${index + 1} 不支持所选协议与执行方式。`;
    }
    if (parsePriority(target.priority) === null) {
      return `目标 ${index + 1} 的优先级需要是 0 到 1000000 之间的整数。`;
    }
    if ([...target.upstreamModel].length > 256) {
      return `目标 ${index + 1} 的上游模型名不能超过 256 个字符。`;
    }
    if (!draft.publicModel && target.upstreamModel) {
      return "只有精确公开模型路由才能重写上游模型。";
    }
    const effectiveModel = target.upstreamModel || draft.publicModel;
    if (
      effectiveModel &&
      capability.models &&
      capability.models.length > 0 &&
      !capability.models.includes(effectiveModel)
    ) {
      return `目标 ${index + 1} 的有效上游模型不在该 API 服务声明的模型范围内。`;
    }
  }
  return null;
}

function createInputFromDraft(draft: RouteDraft): RouteCreateInput {
  const targets: RouteTarget[] = draft.targets.map((target) => ({
    service_id: target.serviceId,
    plan_type: target.planType,
    upstream_protocol: draft.protocol,
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
      ...(draft.publicModel.trim()
        ? { model: draft.publicModel.trim() }
        : {}),
    },
    selection: { mode: "priority" },
    targets,
  };
}

function sortRoutes(routes: Route[]): Route[] {
  return [...routes].sort((left, right) => {
    if (left.priority !== right.priority) return left.priority - right.priority;
    const exact = Number(Boolean(right.match.model)) - Number(Boolean(left.match.model));
    return exact || left.id.localeCompare(right.id);
  });
}

export function RouteManager({
  coreSessionKey,
  services,
  isReady,
  onDirtyChange,
  onManageServices,
  protocols,
}: RouteManagerProps) {
  const protocolIDs = useMemo(
    () => availableProtocolIDs(services, protocols),
    [services, protocols],
  );
  const [catalog, setCatalog] = useState<CatalogState>(emptyCatalog);
  const [editor, setEditor] = useState<EditorState | null>(null);
  const [draft, setDraft] = useState<RouteDraft>(() =>
    createDraft(services, protocolIDs),
  );
  const [baseline, setBaseline] = useState<string | null>(null);
  const [loadingRecord, setLoadingRecord] = useState(false);
  const [saving, setSaving] = useState(false);
  const [mutatingID, setMutatingID] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [cancelPending, setCancelPending] = useState(false);
  const [deletePending, setDeletePending] = useState<RouteRecord | null>(null);
  const generation = useRef(0);
  const dirty =
    editor !== null &&
    baseline !== null &&
    draftSignature(draft) !== baseline;

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

  const refresh = useCallback(async () => {
    const requestGeneration = generation.current + 1;
    generation.current = requestGeneration;
    if (!isReady) {
      setCatalog((current) => ({
        ...current,
        status: "blocked",
        error: null,
        stale: current.items.length > 0,
      }));
      return;
    }
    setCatalog((current) => ({
      ...current,
      status: "loading",
      error: null,
      stale: current.items.length > 0,
    }));
    try {
      const page = await listRoutes();
      if (generation.current !== requestGeneration) return;
      setCatalog({
        status: "ready",
        items: sortRoutes(page.items),
        error: null,
        stale: false,
      });
    } catch (loadError) {
      if (generation.current !== requestGeneration) return;
      setCatalog((current) => ({
        ...current,
        status: "error",
        error: messageOf(loadError, "无法读取路由。"),
        stale: current.items.length > 0,
      }));
    }
  }, [isReady]);

  useEffect(() => {
    if (!isReady) {
      generation.current += 1;
      setCatalog((current) => ({
        ...current,
        status: "blocked",
        error: null,
        stale: current.items.length > 0,
      }));
      return;
    }
    void refresh();
  }, [coreSessionKey, isReady, refresh]);

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
    setNotice(null);
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
      setNotice(null);
    } catch (loadError) {
      setError(messageOf(loadError, "无法读取路由详情。"));
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
              targets: input.targets,
              categories: null,
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
      setNotice(editor.kind === "create" ? "路由已创建。" : "路由已保存。");
      closeEditor();
    } catch (saveError) {
      setError(messageOf(saveError, "无法保存路由。"));
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
      setNotice(updated.route.enabled ? "路由已启用。" : "路由已停用。");
    } catch (toggleError) {
      setError(messageOf(toggleError, "无法更新路由状态。"));
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
      setError(messageOf(loadError, "无法读取待删除路由。"));
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
      setNotice("路由已删除。");
    } catch (deleteError) {
      setError(messageOf(deleteError, "无法删除路由。"));
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
      const target = nextTarget(
        services,
        current.protocol,
        new Set(current.targets.map((item) => item.serviceId)),
      );
      return target
        ? { ...current, targets: [...current.targets, target] }
        : current;
    });
  };

  const servicesForProtocol = compatibleServices(services, draft.protocol);
  const canAddTarget = servicesForProtocol.some(
    (service) =>
      !draft.targets.some((target) => target.serviceId === service.id),
  );

  return (
    <section aria-labelledby="route-manager-title" className="route-manager">
      <PageHeader
        actions={
          editor ? (
            <button
              className="btn-secondary"
              disabled={saving}
              onClick={() => (dirty ? setCancelPending(true) : closeEditor())}
              type="button"
            >
              返回
            </button>
          ) : (
            <span className="routing-preview__state">训练中 · 不可启用</span>
          )
        }
        description={
          editor
            ? "用确定性优先级连接 API 服务、设置 fallback，或把公开模型别名映射到真实上游模型。"
            : "客户端使用 astrlink/auto，AstrLink 按任务分类从对应模型池中选择。"
        }
        eyebrow="本地执行策略"
        title={
          editor
            ? editor.kind === "create"
              ? "新建固定路由"
              : "编辑固定路由"
            : "自动选择合适的模型"
        }
        titleId="route-manager-title"
      />

      {notice ? <p className="notice notice--success">{notice}</p> : null}
      {error || catalog.error ? (
        <p className="notice notice--error" role="alert">
          {error ?? catalog.error}
        </p>
      ) : null}

      {editor ? (
        <form aria-busy={saving} className="route-form" onSubmit={save}>
          <div className="route-form__grid">
            <label>
              <span>路由名称</span>
              <input
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
            </label>
            <label>
              <span>路由优先级</span>
              <input
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
              <small>数值越小越先匹配。</small>
            </label>
            <label>
              <span>入口协议</span>
              <select
                onChange={(event) => changeProtocol(event.target.value)}
                value={draft.protocol}
              >
                {protocolIDs.map((protocol) => (
                  <option key={protocol} value={protocol}>
                    {protocolLabel(protocol)}
                  </option>
                ))}
              </select>
            </label>
            <label>
              <span>公开模型名（可选）</span>
              <input
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
                placeholder="留空匹配该协议的全部模型"
                value={draft.publicModel}
              />
              <small>填写后可为每个目标设置真实上游模型，形成模型别名。</small>
            </label>
          </div>

          <label className="route-form__enabled">
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
            <span>
              <strong>保存后立即启用</strong>
              <small>停用路由会保留配置，但不会参与请求匹配。</small>
            </span>
          </label>

          <section className="route-target-editor">
            <header>
              <div>
                <span>执行目标</span>
                <strong>按优先级依次尝试</strong>
              </div>
              <button
                className="btn-secondary"
                disabled={!canAddTarget}
                onClick={addTarget}
                type="button"
              >
                添加 fallback
              </button>
            </header>

            {draft.targets.length === 0 ? (
              <div className="route-target-editor__empty">
                <p>没有 API 服务声明 {protocolLabel(draft.protocol)} 能力。</p>
                <button
                  className="btn-secondary"
                  onClick={onManageServices}
                  type="button"
                >
                  管理 API 服务
                </button>
              </div>
            ) : (
              <ol className="route-target-list">
                {draft.targets.map((target, index) => {
                  const service = services.find(
                    (candidate) => candidate.id === target.serviceId,
                  );
                  const modes = modesFor(service, draft.protocol);
                  return (
                    <li key={`${target.serviceId}:${index}`}>
                      <span className="route-target-list__order">
                        {String(index + 1).padStart(2, "0")}
                      </span>
                      <div className="route-target-list__fields">
                        <label>
                          <span>API 服务</span>
                          <select
                            onChange={(event) => {
                              const nextService = services.find(
                                (candidate) =>
                                  candidate.id === event.target.value,
                              );
                              const nextModes = modesFor(
                                nextService,
                                draft.protocol,
                              );
                              updateTarget(index, (current) => ({
                                ...current,
                                serviceId: event.target.value,
                                planType: nextModes.includes(current.planType)
                                  ? current.planType
                                  : (nextModes[0] ?? "native"),
                              }));
                            }}
                            value={target.serviceId}
                          >
                            {servicesForProtocol.map((candidate) => (
                              <option
                                disabled={draft.targets.some(
                                  (item, itemIndex) =>
                                    itemIndex !== index &&
                                    item.serviceId === candidate.id,
                                )}
                                key={candidate.id}
                                value={candidate.id}
                              >
                                {candidate.name}
                                {candidate.enabled ? "" : "（已停用）"}
                              </option>
                            ))}
                          </select>
                        </label>
                        <label>
                          <span>执行方式</span>
                          <select
                            onChange={(event) =>
                              updateTarget(index, (current) => ({
                                ...current,
                                planType: event.target
                                  .value as RouteDraftTarget["planType"],
                              }))
                            }
                            value={target.planType}
                          >
                            {modes.map((mode) => (
                              <option key={mode} value={mode}>
                                {modeLabels[mode]}
                              </option>
                            ))}
                          </select>
                        </label>
                        <label>
                          <span>目标优先级</span>
                          <input
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
                        </label>
                        <label>
                          <span>上游模型</span>
                          <input
                            disabled={!draft.publicModel}
                            maxLength={256}
                            onChange={(event) =>
                              updateTarget(index, (current) => ({
                                ...current,
                                upstreamModel: event.target.value,
                              }))
                            }
                            placeholder={
                              draft.publicModel
                                ? "例如 gpt-5.2"
                                : "先填写公开模型名"
                            }
                            value={target.upstreamModel}
                          />
                        </label>
                      </div>
                      <button
                        aria-label={`移除目标 ${index + 1}`}
                        className="danger-link"
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
                      >
                        移除
                      </button>
                    </li>
                  );
                })}
              </ol>
            )}
          </section>

          <div className="route-form__actions">
            <button
              className="btn-secondary"
              disabled={saving}
              onClick={() => (dirty ? setCancelPending(true) : closeEditor())}
              type="button"
            >
              取消
            </button>
            <button
              className="btn-primary"
              disabled={saving || draft.targets.length === 0}
              type="submit"
            >
              {saving
                ? "保存中…"
                : editor.kind === "create"
                  ? "创建固定路由"
                  : "保存修改"}
            </button>
          </div>
        </form>
      ) : (
        <div className="route-manager__body">
          <AutoRoutingShowcase />

          <section
            aria-labelledby="manual-routes-title"
            className="route-manual-section"
          >
            <div className="route-manual-section__heading">
              <div>
                <span>高级</span>
                <h3 id="manual-routes-title">固定路由与别名</h3>
                <p>
                  显式模型名与别名走确定性优先级，不经过任务分类。用于固定服务、fallback
                  或公开模型别名映射。
                </p>
              </div>
              <div className="route-manual-section__actions">
                <button
                  className="btn-secondary"
                  disabled={!isReady || catalog.status === "loading"}
                  onClick={() => void refresh()}
                  type="button"
                >
                  刷新
                </button>
                <button
                  className="btn-primary"
                  disabled={!isReady || loadingRecord}
                  onClick={beginCreate}
                  type="button"
                >
                  新建固定路由
                </button>
              </div>
            </div>

            <div className="route-list">
              {!isReady && catalog.items.length === 0 ? (
                <div className="route-list__empty">
                  <strong>等待 Core 就绪</strong>
                  <p>Core 就绪后会读取本机固定路由配置。</p>
                </div>
              ) : catalog.status === "loading" && catalog.items.length === 0 ? (
                <div className="route-list__empty">
                  <strong>正在读取路由</strong>
                </div>
              ) : catalog.items.length === 0 ? (
                <div className="route-list__empty">
                  <strong>还没有固定路由</strong>
                  <p>
                    默认请求走 astrlink/auto（就绪后）。创建固定路由可绕过任务分类，固定服务、安排
                    fallback 或建立模型别名。
                  </p>
                  <button
                    className="btn-primary"
                    disabled={!isReady}
                    onClick={beginCreate}
                    type="button"
                  >
                    创建第一条固定路由
                  </button>
                </div>
              ) : (
                <ol className="route-card-list">
                  {catalog.items.map((route) => {
                    const targets = route.targets ?? [];
                    const aliasTarget = targets.find(
                      (target) => target.upstream_model,
                    );
                    return (
                      <li className="route-card" key={route.id}>
                        <div className="route-card__priority">
                          <span>优先级</span>
                          <strong>{route.priority}</strong>
                        </div>
                        <div className="route-card__main">
                          <header>
                            <div>
                              <strong>{route.name}</strong>
                              <span
                                className={`route-card__state${
                                  route.enabled ? "" : " route-card__state--off"
                                }`}
                              >
                                {route.enabled ? "已启用" : "已停用"}
                              </span>
                            </div>
                            <code>{route.match.model ?? "全部模型"}</code>
                          </header>
                          <p>
                            {protocolLabel(route.match.protocol)} ·{" "}
                            {targets.length} 个目标
                            {aliasTarget
                              ? ` · 别名映射至 ${aliasTarget.upstream_model}`
                              : ""}
                          </p>
                          <div className="route-card__targets">
                            {targets.map((target, index) => {
                              const service = services.find(
                                (candidate) =>
                                  candidate.id === target.service_id,
                              );
                              return (
                                <span
                                  key={`${target.service_id}:${target.priority}:${index}`}
                                >
                                  {index + 1}.{" "}
                                  {service?.name ?? target.service_id}
                                  <small>
                                    {modeLabels[
                                      target.plan_type as "native" | "delegated"
                                    ] ?? target.plan_type}
                                  </small>
                                </span>
                              );
                            })}
                          </div>
                        </div>
                        <div className="route-card__actions">
                          <button
                            className="btn-secondary"
                            disabled={mutatingID === route.id || loadingRecord}
                            onClick={() => void beginEdit(route)}
                            type="button"
                          >
                            编辑
                          </button>
                          <button
                            className="btn-secondary"
                            disabled={mutatingID === route.id}
                            onClick={() => void toggleRoute(route)}
                            type="button"
                          >
                            {route.enabled ? "停用" : "启用"}
                          </button>
                          <button
                            className="danger-link"
                            disabled={mutatingID === route.id}
                            onClick={() => void askDelete(route)}
                            type="button"
                          >
                            删除
                          </button>
                        </div>
                      </li>
                    );
                  })}
                </ol>
              )}
              {catalog.stale ? (
                <p className="route-list__stale">当前显示上次读取的路由。</p>
              ) : null}
            </div>
          </section>
        </div>
      )}

      {cancelPending ? (
        <div className="token-dialog-backdrop" role="presentation">
          <section
            aria-describedby="route-cancel-description"
            aria-labelledby="route-cancel-title"
            aria-modal="true"
            className="token-dialog"
            role="dialog"
          >
            <h3 id="route-cancel-title">放弃未保存的路由修改？</h3>
            <p id="route-cancel-description">本次修改尚未写入 Core。</p>
            <div className="token-dialog__actions">
              <button
                className="btn-secondary"
                onClick={() => setCancelPending(false)}
                type="button"
              >
                继续编辑
              </button>
              <button
                className="btn-primary"
                onClick={closeEditor}
                type="button"
              >
                放弃修改
              </button>
            </div>
          </section>
        </div>
      ) : null}

      {deletePending ? (
        <div className="token-dialog-backdrop" role="presentation">
          <section
            aria-describedby="route-delete-description"
            aria-labelledby="route-delete-title"
            aria-modal="true"
            className="token-dialog"
            role="dialog"
          >
            <h3 id="route-delete-title">删除路由？</h3>
            <p id="route-delete-description">
              “{deletePending.route.name}”将被永久删除，后续请求不再匹配它。
            </p>
            <div className="token-dialog__actions">
              <button
                className="btn-secondary"
                disabled={mutatingID === deletePending.route.id}
                onClick={() => setDeletePending(null)}
                type="button"
              >
                取消
              </button>
              <button
                className="btn-danger"
                disabled={mutatingID === deletePending.route.id}
                onClick={() => void confirmDelete()}
                type="button"
              >
                {mutatingID === deletePending.route.id ? "删除中…" : "确认删除"}
              </button>
            </div>
          </section>
        </div>
      ) : null}
    </section>
  );
}
