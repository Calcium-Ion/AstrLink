import {
  type FormEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { ChevronDown } from "lucide-react";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { DataRow } from "@/components/DataRow";
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

import { AutoRoutingShowcase } from "./AutoRoutingShowcase";
import {
  createRoute,
  deleteRoute,
  getRoute,
  listRoutes,
  updateRoute,
} from "./bridge";
import { PageHeader } from "./PageHeader";
import { type RoutableService } from "./service-model";
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
      !service.models.includes(effectiveModel)
    ) {
      return `目标 ${index + 1} 的有效上游模型不在该 API 服务的模型白名单内。`;
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
    <section aria-labelledby="route-manager-title" className="mx-auto flex min-h-0 w-full max-w-[1120px] flex-1 flex-col overflow-y-auto">
      <PageHeader
        actions={
          editor ? (
            <Button
              variant="outline"
              disabled={saving}
              onClick={() => (dirty ? setCancelPending(true) : closeEditor())}
              type="button"
            >
              返回
            </Button>
          ) : (
            <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">训练中 · 不可启用</Badge>
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

      {notice ? <FormMessage className="mb-2.5" tone="success">{notice}</FormMessage> : null}
      {error || catalog.error ? (
        <FormMessage className="mb-2.5" tone="error">
          {error ?? catalog.error}
        </FormMessage>
      ) : null}

      {editor ? (
        <form aria-busy={saving} className="mx-auto w-full max-w-[860px] min-w-0 rounded-lg border bg-card p-4 aria-busy:pointer-events-none aria-busy:opacity-70" onSubmit={save}>
          <div className="grid grid-cols-2 gap-3 max-[720px]:grid-cols-1">
            <Field label="路由名称">
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
            <Field hint="数值越小越先匹配。" label="路由优先级">
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
            <Field label="入口协议">
              <Select
                onValueChange={changeProtocol}
                value={draft.protocol}
              >
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
              hint="填写后可为每个目标设置真实上游模型，形成模型别名。"
              label="公开模型名（可选）"
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
                placeholder="留空匹配该协议的全部模型"
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
              <strong className="text-sm font-medium text-foreground">保存后立即启用</strong>
              <small className="text-xs font-normal text-muted-foreground">停用路由会保留配置，但不会参与请求匹配。</small>
            </span>
          </Label>

          <section className="mt-4 border-t pt-3.5">
            <header className="flex items-center justify-between gap-2.5">
              <div className="flex flex-col gap-0.5">
                <SectionKicker>执行目标</SectionKicker>
                <strong className="text-sm font-medium">按优先级依次尝试</strong>
              </div>
              <Button
                variant="outline"
                disabled={!canAddTarget}
                onClick={addTarget}
                type="button"
              >
                添加 fallback
              </Button>
            </header>

            {draft.targets.length === 0 ? (
              <div className="mt-2.5 flex items-center justify-between gap-3 rounded-md border border-dashed bg-muted p-3 text-text-secondary">
                <p className="text-xs">没有 API 服务声明 {protocolLabel(draft.protocol)} 能力。</p>
                <Button
                  variant="outline"
                  onClick={onManageServices}
                  type="button"
                >
                  管理 API 服务
                </Button>
              </div>
            ) : (
              <ol className="mt-2.5 grid list-none gap-2 p-0">
                {draft.targets.map((target, index) => {
                  const service = services.find(
                    (candidate) => candidate.id === target.serviceId,
                  );
                  const modes = modesFor(service, draft.protocol);
                  return (
                    <li className="grid min-w-0 grid-cols-[24px_minmax(0,1fr)_max-content] items-center gap-2 rounded-md border bg-muted p-2.5 max-[760px]:grid-cols-[24px_minmax(0,1fr)]" key={`${target.serviceId}:${index}`}>
                      <span className="grid size-6 place-items-center rounded-sm border bg-card text-micro font-medium tabular-nums">
                        {String(index + 1).padStart(2, "0")}
                      </span>
                      <div className="grid min-w-0 grid-cols-[minmax(150px,1.2fr)_minmax(100px,.7fr)_minmax(90px,.55fr)_minmax(150px,1fr)] gap-2 max-[960px]:grid-cols-2 max-[600px]:grid-cols-1">
                        <Field label="API 服务">
                          <Select
                            onValueChange={(serviceId) => {
                              const nextService = services.find(
                                (candidate) =>
                                  candidate.id === serviceId,
                              );
                              const nextModes = modesFor(
                                nextService,
                                draft.protocol,
                              );
                              updateTarget(index, (current) => ({
                                ...current,
                                serviceId,
                                planType: nextModes.includes(current.planType)
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
                              </SelectItem>
                            ))}
                            </SelectContent>
                          </Select>
                        </Field>
                        <Field label="执行方式">
                          <Select
                            onValueChange={(value) =>
                              updateTarget(index, (current) => ({
                                ...current,
                                planType: value as RouteDraftTarget["planType"],
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
                                {modeLabels[mode]}
                              </SelectItem>
                            ))}
                            </SelectContent>
                          </Select>
                        </Field>
                        <Field label="目标优先级">
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
                            <span className="col-span-full">上游模型</span>
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
                                  ? "例如 gpt-5.2"
                                  : "先填写公开模型名"
                              }
                              value={target.upstreamModel}
                            />
                          </Label>
                          <DropdownMenu>
                            <DropdownMenuTrigger asChild>
                              <Button
                                aria-label={`选择目标 ${index + 1} 的上游模型`}
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
                        aria-label={`移除目标 ${index + 1}`}
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
                        移除
                      </Button>
                    </li>
                  );
                })}
              </ol>
            )}
          </section>

          <div className="mt-4 flex justify-end gap-2 border-t pt-3">
            <Button
              variant="outline"
              disabled={saving}
              onClick={() => (dirty ? setCancelPending(true) : closeEditor())}
              type="button"
            >
              取消
            </Button>
            <Button
              disabled={saving || draft.targets.length === 0}
              type="submit"
            >
              {saving
                ? "保存中…"
                : editor.kind === "create"
                  ? "创建固定路由"
                  : "保存修改"}
            </Button>
          </div>
        </form>
      ) : (
        <div className="flex min-w-0 flex-1 flex-col gap-[22px] pb-6">
          <AutoRoutingShowcase />

          <section
            aria-labelledby="manual-routes-title"
            className="flex min-w-0 flex-col gap-3 border-t pt-1"
          >
            <div className="flex min-w-0 items-start justify-between gap-4 max-[720px]:flex-col">
              <div className="min-w-0">
                <SectionKicker>高级</SectionKicker>
                <h3
                  className="mt-1 text-base font-semibold tracking-tight"
                  id="manual-routes-title"
                >
                  固定路由与别名
                </h3>
                <p className="mt-1 max-w-[560px] text-xs text-text-secondary">
                  显式模型名与别名走确定性优先级，不经过任务分类。用于固定服务、fallback
                  或公开模型别名映射。
                </p>
              </div>
              <div className="flex shrink-0 flex-wrap justify-end gap-2">
                <Button
                  variant="outline"
                  disabled={!isReady || catalog.status === "loading"}
                  onClick={() => void refresh()}
                  type="button"
                >
                  刷新
                </Button>
                <Button
                  disabled={!isReady || loadingRecord}
                  onClick={beginCreate}
                  type="button"
                >
                  新建固定路由
                </Button>
              </div>
            </div>

            <div className="flex min-w-0 flex-col">
              {!isReady && catalog.items.length === 0 ? (
                <EmptyState
                  description="Core 就绪后会读取本机固定路由配置。"
                  title="等待 Core 就绪"
                />
              ) : catalog.status === "loading" && catalog.items.length === 0 ? (
                <EmptyState title="正在读取路由" />
              ) : catalog.items.length === 0 ? (
                <EmptyState
                  action={
                    <Button
                      disabled={!isReady}
                      onClick={beginCreate}
                      type="button"
                    >
                      创建第一条固定路由
                    </Button>
                  }
                  description="默认请求走 astrlink/auto（就绪后）。创建固定路由可绕过任务分类，固定服务、安排 fallback 或建立模型别名。"
                  title="还没有固定路由"
                />
              ) : (
                <Panel asChild>
                  <ol className="list-none p-0">
                  {catalog.items.map((route) => {
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
                              优先级
                            </span>
                            <strong className="text-base tabular-nums">
                              {route.priority}
                            </strong>
                          </div>
                          <div className="min-w-0 flex-1">
                            <header className="flex min-w-0 items-center justify-between gap-3">
                              <div className="flex min-w-0 items-center gap-1.5">
                                <StatusDot
                                  tone={route.enabled ? "positive" : "neutral"}
                                />
                                <strong className="truncate text-sm font-medium">
                                  {route.name}
                                </strong>
                                <span
                                  className={cn(
                                    "shrink-0 text-micro text-success-foreground",
                                    !route.enabled && "text-muted-foreground",
                                  )}
                                >
                                  {route.enabled ? "已启用" : "已停用"}
                                </span>
                              </div>
                              <code className="max-w-[40%] truncate rounded-sm border px-1.5 py-px font-mono text-micro text-foreground">
                                {route.match.model ?? "全部模型"}
                              </code>
                            </header>
                            <p className="mt-1 truncate text-xs text-muted-foreground">
                              {protocolLabel(route.match.protocol)} ·{" "}
                              {targets.length} 个目标
                              {aliasTarget
                                ? ` · 别名映射至 ${aliasTarget.upstream_model}`
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
                                      {modeLabels[
                                        target.plan_type as
                                          | "native"
                                          | "delegated"
                                      ] ?? target.plan_type}
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
                              disabled={mutatingID === route.id || loadingRecord}
                              onClick={() => void beginEdit(route)}
                              type="button"
                            >
                              编辑
                            </Button>
                            <Button
                              size="sm"
                              variant="outline"
                              disabled={mutatingID === route.id}
                              onClick={() => void toggleRoute(route)}
                              type="button"
                            >
                              {route.enabled ? "停用" : "启用"}
                            </Button>
                            <Button
                              className="text-danger-foreground hover:bg-danger-wash hover:text-danger-foreground"
                              disabled={mutatingID === route.id}
                              onClick={() => void askDelete(route)}
                              type="button"
                              size="sm"
                              variant="ghost"
                            >
                              删除
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
                  当前显示上次读取的路由。
                </p>
              ) : null}
            </div>
          </section>
        </div>
      )}

      <ConfirmDialog
        cancelLabel="继续编辑"
        confirmLabel="放弃修改"
        description={<p>本次修改尚未写入 Core。</p>}
        onCancel={() => setCancelPending(false)}
        onConfirm={closeEditor}
        open={cancelPending}
        title="放弃未保存的路由修改？"
      />

      <ConfirmDialog
        confirmLabel={
          mutatingID === deletePending?.route.id ? "删除中…" : "确认删除"
        }
        description={
          <p>
            “{deletePending?.route.name ?? ""}”将被永久删除，后续请求不再匹配它。
          </p>
        }
        destructive
        disabled={mutatingID === deletePending?.route.id}
        onCancel={() => setDeletePending(null)}
        onConfirm={() => void confirmDelete()}
        open={deletePending !== null}
        title="删除路由？"
      />
    </section>
  );
}
