import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ReactFlow,
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  ReactFlowProvider,
  useReactFlow,
  type Connection,
  type Edge,
  type NodeChange,
  type OnConnectStartParams,
} from "@xyflow/react";
import {
  getRequestRecord,
  getRoutingGraphRevision,
  previewRoutingGraph,
} from "./bridge";
import { useRoutingGraph } from "./use-routing-graph";
import {
  affectedEntries,
  appendCall,
  canConnect,
  connectGraph,
  disabledBypasses,
  graphEdge,
  graphID,
  graphPort,
  isolateNode,
  linearCalls,
  reachable,
  removeGraphNode,
  reorderCalls,
  type GraphKind,
  type GraphNode,
  type GraphPreview,
  type GraphStep,
  type RoutingGraph,
} from "./routing-graph-model";
import {
  RoutingGraphNode,
  RoutingBypassEdge,
  type FlowRoutingNode,
} from "./components/RoutingGraphNode";
import { RoutingGraphTrace } from "./components/RoutingGraphTrace";
import {
  GraphSelect,
  RoutingConditionEditor,
  defaultPredicate,
} from "./components/RoutingConditionEditor";
import { OrderedList } from "./components/OrderedList";
import { Field } from "./components/Field";
import { FormMessage } from "./components/FormMessage";
import { EmptyState } from "./components/EmptyState";
import { HelpPopover } from "./components/HelpPopover";
import { FlowCanvasRecovery } from "./components/FlowCanvasRecovery";
import { ModelSelect } from "./components/ModelSelect";
import { Panel } from "./components/Panel";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";
import { Switch } from "./components/ui/switch";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "./components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "./components/ui/dropdown-menu";
import {
  Plus,
  Route,
  SlidersHorizontal,
  Settings,
  RotateCcw,
  Copy,
  X,
  Flask,
  Maximize,
  LoaderCircle,
} from "./components/icons";
import type { RoutableService } from "./service-model";
import { useT } from "./i18n";
import { PageHeader } from "./PageHeader";
import { FailurePolicyEditor } from "./components/FailurePolicyEditor";
import { defaultFailurePolicy } from "./failure-policy-model";

const noSteps: GraphStep[] = [];
const nodeTypes = { routing: RoutingGraphNode };
const edgeTypes = { bypass: RoutingBypassEdge };
interface Props {
  ready: boolean;
  services: RoutableService[];
  onDirtyChange: (dirty: boolean) => void;
  onSettings: () => void;
}
interface AddDraft {
  kind: GraphKind;
  after?: string;
  port?: string;
  publicModel: string;
  service: string;
  model: string;
  x?: number;
  y?: number;
}

export function RoutingGraphEditor(props: Props) {
  return (
    <ReactFlowProvider>
      <GraphEditor {...props} />
    </ReactFlowProvider>
  );
}

function GraphEditor({ ready, services, onDirtyChange, onSettings }: Props) {
  const t = useT(),
    flow = useReactFlow<FlowRoutingNode>();
  const state = useRoutingGraph(ready, onDirtyChange);
  const [selected, setSelected] = useState<string | null>(null);
  const [focus, setFocus] = useState<string | null>(null);
  const [add, setAdd] = useState<AddDraft | null>(null);
  const [testOpen, setTestOpen] = useState(false);
  const [testEntry, setTestEntry] = useState("");
  const [protocol, setProtocol] = useState("openai.responses");
  const [streaming, setStreaming] = useState(true);
  const [hasTools, setHasTools] = useState(false);
  const [hasImages, setHasImages] = useState(false);
  const [outcomes, setOutcomes] = useState<Record<string, string>>({});
  const [preview, setPreview] = useState<GraphPreview | null>(null);
  const [testing, setTesting] = useState(false);
  const [traceOpen, setTraceOpen] = useState(false);
  const [requestID, setRequestID] = useState("");
  const [requestDialog, setRequestDialog] = useState(false);
  const [replay, setReplay] = useState<{
    graph: RoutingGraph;
    steps: GraphStep[];
    revision: number;
  } | null>(null);
  const [layoutBusy, setLayoutBusy] = useState(false);
  const connectStart = useRef<OnConnectStartParams | null>(null);
  const graph = replay?.graph ?? state.draft.graph;
  const readonly = replay !== null || !ready;
  const entries = graph.nodes.filter((node) => node.kind === "entry");
  useEffect(() => {
    if (focus === null && entries.length) setFocus(entries[0].id);
  }, [focus, entries]);
  const current = graph.nodes.find((node) => node.id === selected);
  const visible = useMemo(
    () =>
      focus
        ? reachable(graph, focus)
        : new Set(graph.nodes.map((node) => node.id)),
    [graph, focus],
  );
  const steps = replay?.steps ?? preview?.steps ?? noSteps;
  const lastStep = useMemo(
    () => new Map(steps.map((step) => [step.node_id, step])),
    [steps],
  );
  const editGraph = useCallback(
    (change: (graph: RoutingGraph) => RoutingGraph) => {
      state.edit((draft) => ({ ...draft, graph: change(draft.graph) }));
      setPreview(null);
    },
    [state.edit],
  );
  const patchNode = (id: string, patch: Partial<GraphNode>) =>
    editGraph((graph) => ({
      ...graph,
      nodes: graph.nodes.map((node) =>
        node.id === id ? { ...node, ...patch } : node,
      ),
    }));
  const toggle = useCallback(
    (id: string) => {
      if (!readonly)
        editGraph((graph) => ({
          ...graph,
          nodes: graph.nodes.map((node) =>
            node.id === id ? { ...node, enabled: !node.enabled } : node,
          ),
        }));
    },
    [editGraph, readonly],
  );
  const beginAdd = useCallback(
    (
      kind: GraphKind,
      after?: string,
      port?: string,
      position?: { x: number; y: number },
    ) => {
      const service =
        services.find((service) => service.enabled && service.models.length) ??
        services[0];
      setAdd({
        kind,
        after,
        port,
        publicModel: "",
        service: service?.id ?? "",
        model: service?.models[0] ?? "",
        ...position,
      });
    },
    [services],
  );
  const append = useCallback(
    (id: string) => {
      if (!readonly) beginAdd("call", id);
    },
    [beginAdd, readonly],
  );
  const nodeData = useMemo(() => {
    const counts = new Map<string, number>();
    for (const entry of graph.nodes.filter((node) => node.kind === "entry"))
      for (const id of reachable(graph, entry.id))
        counts.set(id, (counts.get(id) ?? 0) + 1);
    return new Map(
      graph.nodes.map((node) => [
        node.id,
        {
          node,
          locked: readonly,
          service: services.find((service) => service.id === node.service_id),
          shared: counts.get(node.id) ?? 0,
          step: lastStep.get(node.id),
          toggle,
          append,
        },
      ]),
    );
  }, [graph, services, lastStep, toggle, append, readonly]);
  const nodes: FlowRoutingNode[] = useMemo(
    () =>
      graph.nodes
        .filter((node) => visible.has(node.id))
        .map((node, index) => ({
          id: node.id,
          type: "routing",
          selected: node.id === selected,
          position: state.draft.layout[node.id] ?? {
            x: (index % 4) * 320 + 40,
            y: Math.floor(index / 4) * 230 + 40,
          },
          data: nodeData.get(node.id)!,
        })),
    [graph, visible, selected, state.draft.layout, nodeData],
  );
  const edges: Edge[] = useMemo(() => {
    const configured: Edge[] = graph.edges
      .filter((edge) => visible.has(edge.source) && visible.has(edge.target))
      .map((edge) => {
        const source = graph.nodes.find((node) => node.id === edge.source),
          target = graph.nodes.find((node) => node.id === edge.target);
        const muted = source?.enabled === false || target?.enabled === false;
        const active = steps.some(
          (step) =>
            step.node_id === edge.source &&
            (step.port === edge.port ||
              (edge.port === "failure" && step.status === "failed")),
        );
        const rule = source?.rules?.find((rule) => rule.id === edge.port);
        return {
          id: edge.id,
          source: edge.source,
          sourceHandle: edge.port,
          target: edge.target,
          targetHandle: "in",
          type: "smoothstep",
          animated: false,
          label: edge.port === "failure" ? t("graph.failure") : rule?.label,
          style: {
            stroke: active
              ? "var(--success-foreground)"
              : muted
                ? "var(--border)"
                : "var(--muted-foreground)",
            strokeWidth: active ? 2 : 1.3,
            strokeDasharray: edge.port === "failure" ? "5 4" : undefined,
          },
          labelStyle: { fill: "var(--muted-foreground)", fontSize: 10 },
          labelBgStyle: { fill: "var(--background)" },
        };
      });
    return [
      ...configured,
      ...disabledBypasses(graph)
        .filter((edge) => visible.has(edge.source) && visible.has(edge.target))
        .map(
          (edge): Edge => ({
            id: `bypass-${edge.source}-${edge.target}`,
            source: edge.source,
            sourceHandle: edge.port,
            target: edge.target,
            targetHandle: "in",
            type: "bypass",
            selectable: false,
            deletable: false,
            data: { count: edge.skipped.length },
          }),
        ),
    ];
  }, [graph, visible, steps, t]);

  const center = (id?: string) => {
    void flow.fitView({
      nodes: id ? [{ id }] : undefined,
      padding: 0.22,
      maxZoom: 1,
      duration: window.matchMedia("(prefers-reduced-motion: reduce)").matches
        ? 0
        : 200,
    });
  };
  const onConnect = (connection: Connection) => {
    if (
      !readonly &&
      connection.source &&
      connection.target &&
      connection.sourceHandle
    )
      editGraph((graph) =>
        connectGraph(
          graph,
          connection.source!,
          connection.sourceHandle!,
          connection.target!,
        ),
      );
  };
  const onNodesChange = (changes: NodeChange<FlowRoutingNode>[]) => {
    for (const change of changes)
      if (change.type === "select") {
        if (change.selected) setSelected(change.id);
        else setSelected((current) => (current === change.id ? null : current));
      }
    if (readonly) return;
    const positions = changes.filter(
      (change) => change.type === "position" && change.position,
    );
    if (positions.length)
      state.edit((draft) => {
        const layout = { ...draft.layout };
        for (const change of positions)
          if (change.type === "position" && change.position)
            layout[change.id] = change.position;
        return { ...draft, layout };
      }, false);
  };
  const createNode = () => {
    if (!add) return;
    const id = graphID();
    const node: GraphNode = {
      id,
      kind: add.kind,
      enabled: true,
      ...(add.kind === "entry"
        ? { model: add.publicModel.trim() }
        : add.kind === "call"
          ? { service_id: add.service, upstream_model: add.model.trim() }
          : add.kind === "condition"
            ? {
                name: t("graph.newCondition"),
                unknown_port: "otherwise",
                rules: [
                  {
                    id: graphID(),
                    predicate: defaultPredicate(),
                    label: t("graph.matched"),
                  },
                ],
              }
            : { name: t("graph.stop") }),
    };
    state.edit((draft) => {
      let next = { ...draft.graph, nodes: [...draft.graph.nodes, node] };
      const previous = add.after
        ? draft.graph.nodes.find((node) => node.id === add.after)
        : null;
      if (previous) {
        const port = add.port ?? graphPort(previous),
          edge = draft.graph.edges.find(
            (edge) => edge.source === previous.id && edge.port === port,
          );
        if (
          node.kind === "call" &&
          port === graphPort(previous) &&
          previous.kind !== "condition"
        )
          next = appendCall(draft.graph, previous.id, node);
        else
          next = {
            ...next,
            edges: [
              ...draft.graph.edges.filter((item) => item !== edge),
              graphEdge(previous.id, port, id),
              ...(edge && node.kind === "condition"
                ? [
                    graphEdge(id, "otherwise", edge.target),
                    graphEdge(id, node.rules![0].id, edge.target),
                  ]
                : []),
            ],
          };
      }
      const previousPosition = add.after ? draft.layout[add.after] : null;
      return {
        graph: next,
        layout: {
          ...draft.layout,
          [id]: {
            x: add.x ?? (previousPosition ? previousPosition.x + 330 : 60),
            y:
              add.y ??
              (previousPosition
                ? previousPosition.y
                : entries.length * 220 + 60),
          },
        },
      };
    });
    setSelected(id);
    setAdd(null);
    setPreview(null);
    requestAnimationFrame(() => requestAnimationFrame(() => center(id)));
    if (node.kind === "entry") {
      setFocus("");
      setTestEntry(id);
    }
  };
  const arrange = async () => {
    setLayoutBusy(true);
    try {
      const { default: ELK } = await import("elkjs/lib/elk.bundled.js");
      const result = await new ELK().layout({
        id: "routing",
        layoutOptions: {
          "elk.algorithm": "layered",
          "elk.direction": "RIGHT",
          "elk.spacing.nodeNode": "72",
          "elk.layered.spacing.nodeNodeBetweenLayers": "100",
        },
        children: graph.nodes.map((node) => ({
          id: node.id,
          width: 250,
          height:
            node.kind === "condition"
              ? 135 + (node.rules?.length ?? 0) * 30
              : 142,
        })),
        edges: graph.edges.map((edge) => ({
          id: edge.id,
          sources: [edge.source],
          targets: [edge.target],
        })),
      });
      state.edit((draft) => ({
        ...draft,
        layout: Object.fromEntries(
          (result.children ?? []).map((node) => [
            node.id,
            { x: node.x ?? 0, y: node.y ?? 0 },
          ]),
        ),
      }));
      requestAnimationFrame(() => center());
    } catch (error) {
      state.setError(String(error));
    } finally {
      setLayoutBusy(false);
    }
  };
  const simulate = async () => {
    setTesting(true);
    state.setError(null);
    try {
      const result = await previewRoutingGraph({
        graph: state.draft.graph,
        entry_id: testEntry || entries[0]?.id || "",
        facts: {
          protocol,
          streaming,
          has_tools: hasTools,
          has_images: hasImages,
        },
        outcomes,
      });
      setPreview(result);
      setTraceOpen(true);
      setTestOpen(false);
    } catch (error) {
      state.setError(String(error));
    } finally {
      setTesting(false);
    }
  };
  const openRequest = async () => {
    setTesting(true);
    state.setError(null);
    try {
      const record = await getRequestRecord(requestID.trim());
      const recovery = record.recovery;
      if (!recovery?.graph_revision || !recovery.graph_trace)
        throw new Error(t("graph.noRequestTrace"));
      const graph = await getRoutingGraphRevision(recovery.graph_revision);
      setReplay({
        graph,
        revision: recovery.graph_revision,
        steps: recovery.graph_trace,
      });
      setFocus(recovery.graph_entry_id ?? "");
      setSelected(null);
      setTraceOpen(true);
      setRequestDialog(false);
    } catch (error) {
      state.setError(String(error));
    } finally {
      setTesting(false);
    }
  };
  const selectTrace = (id: string) => {
    setSelected(id);
    center(id);
  };
  const calls =
    current?.kind === "entry" ? linearCalls(graph, current.id) : null;
  const affected = current ? affectedEntries(graph, current.id) : [];
  const selectedEntry = focus || (affected.length === 1 ? affected[0].id : "");
  const portEditor = (port: string, label: string) => (
    <Field key={port} label={label}>
      <GraphSelect
        label={label}
        value={
          graph.edges.find(
            (edge) => edge.source === current?.id && edge.port === port,
          )?.target ?? ""
        }
        options={graph.nodes
          .filter(
            (node) =>
              node.kind !== "entry" &&
              current &&
              canConnect(graph, current.id, node.id),
          )
          .map((node) => ({
            value: node.id,
            label:
              node.model ||
              node.upstream_model ||
              node.name ||
              t(`graph.kinds.${node.kind}`),
          }))}
        onChange={(target) => {
          if (!current) return;
          editGraph((graph) =>
            target
              ? connectGraph(graph, current.id, port, target)
              : {
                  ...graph,
                  edges: graph.edges.filter(
                    (edge) => edge.source !== current.id || edge.port !== port,
                  ),
                },
          );
        }}
      />
      <Button
        size="sm"
        variant="ghost"
        className="justify-start"
        onClick={() => beginAdd("call", current?.id, port)}
      >
        <Plus className="size-3" />
        {t("graph.newDestination")}
      </Button>
    </Field>
  );

  return (
    <div
      className="routing-workspace"
      data-testid="routing-graph-workspace"
      onKeyDown={(event) => {
        if (
          event.nativeEvent.isComposing ||
          (event.target as HTMLElement).closest(
            "input,textarea,[contenteditable=true]",
          )
        )
          return;
        if (
          (event.metaKey || event.ctrlKey) &&
          event.key.toLowerCase() === "z" &&
          !readonly
        ) {
          event.preventDefault();
          if (event.shiftKey) state.redo();
          else state.undo();
        }
      }}
    >
      <PageHeader
        title={t("graph.advanced")}
        titleId="route-manager-title"
        className="mb-0"
        back={{ label: t("graph.backToSettings"), onClick: onSettings }}
        titleSuffix={
          <span className="flex shrink-0 items-center gap-1 text-xs text-muted-foreground">
            {t("graph.entryCount", { count: entries.length })}
            <HelpPopover label={t("graph.routingHelp")}>
              {t("graph.defaultHint")}
            </HelpPopover>
          </span>
        }
        actions={
          <div className="ml-auto flex min-w-0 items-center gap-1">
            <span className="routing-save-status" role="status">
              {replay
                ? t("graph.replaying", { revision: replay.revision })
                : state.saving
                  ? t("graph.saving")
                  : state.dirty
                    ? t("graph.unsaved")
                    : state.unpublished
                      ? t("graph.draftSaved")
                      : t("graph.applied")}
            </span>
            {replay ? (
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setReplay(null);
                  setTraceOpen(false);
                  setFocus("");
                }}
              >
                {t("graph.exitReplay")}
              </Button>
            ) : (
              <>
                <Button
                  size="icon-xs"
                  variant="ghost"
                  aria-label={t("graph.undo")}
                  disabled={!state.canUndo}
                  onClick={state.undo}
                >
                  <RotateCcw className="size-3.5" />
                </Button>
                <Button
                  size="icon-xs"
                  variant="ghost"
                  aria-label={t("graph.redo")}
                  disabled={!state.canRedo}
                  onClick={state.redo}
                >
                  <RotateCcw className="size-3.5 -scale-x-100" />
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={!entries.length || !state.document}
                  onClick={() => {
                    setTestEntry(focus || entries[0]?.id || "");
                    setTestOpen(true);
                  }}
                >
                  <Flask className="size-3.5" />
                  <span className="max-[700px]:sr-only">
                    {t("graph.simulate")}
                  </span>
                </Button>
                <Button
                  size="sm"
                  disabled={
                    !state.document ||
                    state.saving ||
                    !ready ||
                    !state.unpublished
                  }
                  onClick={() => void state.apply()}
                >
                  {state.saving ? (
                    <LoaderCircle className="size-3.5 animate-spin" />
                  ) : null}
                  {t("graph.apply")}
                </Button>
              </>
            )}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  size="icon-xs"
                  variant="ghost"
                  aria-label={t("graph.more")}
                >
                  <Settings className="size-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onSelect={() => setRequestDialog(true)}>
                  {t("graph.openRequest")}
                </DropdownMenuItem>
                {state.document?.history.map((item) => (
                  <DropdownMenuItem
                    key={item.revision}
                    disabled={readonly}
                    onSelect={() => {
                      void getRoutingGraphRevision(item.revision)
                        .then((graph) => {
                          editGraph(() => graph);
                          setFocus("");
                          setSelected(null);
                        })
                        .catch((error) => state.setError(String(error)));
                    }}
                  >
                    {t("graph.restoreRevision", { revision: item.revision })}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        }
      />
      {state.error ? (
        <FormMessage tone="error">
          <span className="break-words">{state.error}</span>
          <Button
            size="sm"
            variant="ghost"
            onClick={() =>
              void (state.document ? state.retry() : state.reload())
            }
          >
            {t("common.retry")}
          </Button>
          <Button size="sm" variant="ghost" onClick={() => void state.reload()}>
            {t("graph.reload")}
          </Button>
        </FormMessage>
      ) : null}
      <div className="routing-body">
        <div className="routing-canvas" data-testid="routing-primary-region">
          <div className="routing-canvas-tools">
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={readonly || !state.document}
                >
                  <Plus className="size-3.5" />
                  {t("graph.addNode")}
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent>
                {(["entry", "call", "condition", "stop"] as GraphKind[]).map(
                  (kind) => (
                    <DropdownMenuItem
                      key={kind}
                      onSelect={() => beginAdd(kind)}
                    >
                      {t(`graph.kinds.${kind}`)}
                    </DropdownMenuItem>
                  ),
                )}
              </DropdownMenuContent>
            </DropdownMenu>
            <div className="w-44 max-[700px]:w-32">
              <GraphSelect
                label={t("graph.focus")}
                value={focus ?? ""}
                options={entries.map((entry) => ({
                  value: entry.id,
                  label: entry.model ?? entry.id,
                }))}
                onChange={(value) => {
                  setFocus(value);
                  requestAnimationFrame(() => center());
                }}
              />
            </div>
            <Button
              size="icon-sm"
              variant="outline"
              aria-label={t("graph.fit")}
              onClick={() => center()}
            >
              <Maximize className="size-3.5" />
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={readonly || !graph.nodes.length || layoutBusy}
              onClick={() => void arrange()}
            >
              {layoutBusy ? (
                <LoaderCircle className="size-3 animate-spin" />
              ) : (
                <SlidersHorizontal className="size-3.5" />
              )}
              <span className="max-[900px]:sr-only">{t("graph.arrange")}</span>
            </Button>
          </div>
          <ReactFlow<FlowRoutingNode>
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            edgeTypes={edgeTypes}
            onNodesChange={onNodesChange}
            onConnect={onConnect}
            nodesDraggable={!readonly}
            nodesConnectable={!readonly}
            edgesReconnectable={false}
            deleteKeyCode={null}
            minZoom={0.25}
            maxZoom={1.5}
            fitView
            fitViewOptions={{ padding: 0.25, minZoom: 0.75, maxZoom: 1 }}
            onlyRenderVisibleElements
            onNodeClick={(_, node) => setSelected(node.id)}
            onPaneClick={() => setSelected(null)}
            isValidConnection={(connection) =>
              !!connection.source &&
              !!connection.target &&
              canConnect(graph, connection.source, connection.target)
            }
            onConnectStart={(_, params) => {
              connectStart.current = params;
            }}
            onConnectEnd={(event, connection) => {
              const start = connectStart.current;
              connectStart.current = null;
              if (
                readonly ||
                connection.isValid ||
                !start?.nodeId ||
                start.handleType !== "source"
              )
                return;
              const point =
                "changedTouches" in event ? event.changedTouches[0] : event;
              if (point)
                beginAdd(
                  "call",
                  start.nodeId,
                  start.handleId ?? undefined,
                  flow.screenToFlowPosition({
                    x: point.clientX,
                    y: point.clientY,
                  }),
                );
            }}
          >
            <Background
              variant={BackgroundVariant.Dots}
              gap={22}
              size={1}
              color="var(--border)"
            />
            <Controls showInteractive={false} position="bottom-left" />
            <FlowCanvasRecovery
              label={t("graph.returnToContent")}
              onReturn={() => {
                const target =
                  nodes.find((node) => node.id === selected) ??
                  nodes.find((node) => node.id === focus) ??
                  nodes[0];
                if (target) center(target.id);
              }}
            />
            {graph.nodes.length > 12 ? (
              <MiniMap
                pannable
                zoomable
                position="bottom-right"
                nodeColor={(node) =>
                  graph.nodes.find((item) => item.id === node.id)?.enabled
                    ? "var(--primary)"
                    : "var(--border)"
                }
              />
            ) : null}
          </ReactFlow>
          {!graph.nodes.length && !state.error ? (
            <div className="routing-empty">
              <EmptyState
                variant="page"
                illustration={<Route className="size-9 text-primary" />}
                title={t("graph.welcome")}
                description={t("graph.welcomeHint")}
                action={
                  <Button
                    disabled={!ready || !state.document}
                    onClick={() => beginAdd("entry")}
                  >
                    <Plus className="size-4" />
                    {t("graph.addEntry")}
                  </Button>
                }
              />
            </div>
          ) : null}
          <div className="routing-canvas-caption">{t("graph.canvasHint")}</div>
        </div>
        {current ? (
          <Panel className="routing-inspector">
            <div className="flex shrink-0 items-center justify-between border-b px-4 py-3">
              <strong className="text-sm">
                {t(`graph.kinds.${current.kind}`)}
              </strong>
              <Button
                size="icon-xs"
                variant="ghost"
                aria-label={t("common.close")}
                onClick={() => setSelected(null)}
              >
                <X className="size-4" />
              </Button>
            </div>
            <fieldset
              disabled={readonly}
              className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4"
            >
              {affected.length > 1 ? (
                <div className="rounded-md border border-primary/20 bg-primary/5 p-3 text-xs">
                  <strong>
                    {t("graph.sharedImpact", { count: affected.length })}
                  </strong>
                  <p className="mt-1 break-words text-muted-foreground">
                    {affected.map((entry) => entry.model).join(" · ")}
                  </p>
                  <GraphSelect
                    label={t("graph.isolateEntry")}
                    value={selectedEntry}
                    options={affected.map((entry) => ({
                      value: entry.id,
                      label: entry.model ?? entry.id,
                    }))}
                    onChange={setFocus}
                  />
                  <Button
                    variant="outline"
                    size="sm"
                    className="mt-2 w-full"
                    disabled={!selectedEntry}
                    onClick={() => {
                      const isolated = isolateNode(
                        graph,
                        selectedEntry,
                        current.id,
                      );
                      state.edit((draft) => {
                        const layout = { ...draft.layout };
                        for (const [from, to] of isolated.copied) {
                          const old = layout[from] ?? { x: 100, y: 100 };
                          layout[to] = { x: old.x, y: old.y + 210 };
                        }
                        return { graph: isolated.graph, layout };
                      });
                      setSelected(
                        isolated.copied.get(current.id) ?? current.id,
                      );
                    }}
                  >
                    <Copy className="size-3" />
                    {t("graph.isolate")}
                  </Button>
                  <p className="mt-1 text-muted-foreground">
                    {t("graph.isolateHint")}
                  </p>
                </div>
              ) : null}
              {current.kind === "entry" || current.kind === "call" ? (
                <div className="flex items-center justify-between gap-3">
                  <span className="text-sm">
                    {t(
                      current.kind === "call"
                        ? "graph.nodeEnabled"
                        : "graph.entryEnabled",
                    )}
                  </span>
                  <Switch
                    checked={current.enabled}
                    onCheckedChange={() => toggle(current.id)}
                    aria-label={t("graph.nodeEnabled")}
                  />
                </div>
              ) : null}
              {!current.enabled && current.kind === "call" ? (
                <FormMessage>{t("graph.disabledHint")}</FormMessage>
              ) : null}
              {current.kind === "entry" ? (
                <>
                  <Field
                    label={t("graph.publicModel")}
                    hint={t("graph.publicModelHint")}
                  >
                    <Input
                      aria-label={t("graph.publicModel")}
                      value={current.model ?? ""}
                      onChange={(event) =>
                        patchNode(current.id, { model: event.target.value })
                      }
                    />
                  </Field>
                  {portEditor("next", t("graph.firstSource"))}
                  <Field
                    label={t("graph.maxAttempts")}
                    hint={t("graph.maxAttemptsHint")}
                  >
                    <Input
                      type="number"
                      min={0}
                      max={20}
                      value={current.max_attempts ?? 0}
                      onChange={(event) =>
                        patchNode(current.id, {
                          max_attempts: Number(event.target.value),
                        })
                      }
                    />
                  </Field>
                  {calls && calls.length > 0 ? (
                    <div>
                      <strong className="mb-2 block text-xs">
                        {t("graph.quickOrder")}
                      </strong>
                      <OrderedList
                        items={calls}
                        label={t("graph.quickOrder")}
                        compact
                        onChange={(nodes) =>
                          editGraph((graph) =>
                            reorderCalls(
                              graph,
                              current.id,
                              nodes.map((node) => node.id),
                            ),
                          )
                        }
                      >
                        {(node, _, controls) => (
                          <div className="flex min-w-0 items-center gap-2 py-2">
                            {controls}
                            <Button
                              variant="ghost"
                              size="sm"
                              type="button"
                              className="h-auto min-w-0 flex-1 flex-col items-start px-1 py-0 text-left text-xs"
                              onClick={() => setSelected(node.id)}
                            >
                              <strong className="block truncate">
                                {node.upstream_model}
                              </strong>
                              <span className="text-muted-foreground">
                                {node.enabled
                                  ? services.find(
                                      (service) =>
                                        service.id === node.service_id,
                                    )?.name
                                  : t("graph.disabledSkip")}
                              </span>
                            </Button>
                          </div>
                        )}
                      </OrderedList>
                    </div>
                  ) : null}
                </>
              ) : null}
              {current.kind === "call" ? (
                <>
                  <Field label={t("graph.service")}>
                    <GraphSelect
                      value={current.service_id ?? ""}
                      label={t("graph.service")}
                      options={services.map((service) => ({
                        value: service.id,
                        label: service.name,
                      }))}
                      onChange={(service_id) =>
                        patchNode(current.id, { service_id })
                      }
                    />
                  </Field>
                  <Field
                    label={t("graph.actualModel")}
                    hint={t("graph.mappingHint")}
                  >
                    <ModelSelect
                      aria-label={t("graph.actualModel")}
                      options={
                        services.find(
                          (service) => service.id === current.service_id,
                        )?.models ?? []
                      }
                      value={current.upstream_model ?? ""}
                      onValueChange={(upstream_model) =>
                        patchNode(current.id, { upstream_model })
                      }
                    />
                  </Field>
                  {portEditor("failure", t("graph.onFailure"))}
                  <details className="rounded-md border p-3">
                    <summary className="cursor-pointer text-xs font-medium">
                      {t("graph.advancedPolicy")}
                    </summary>
                    <div className="mt-3 space-y-3">
                      <Label className="flex items-center justify-between gap-2 text-xs">
                        {t("graph.customPolicy")}
                        <Switch
                          checked={!!current.failure_policy}
                          onCheckedChange={(enabled) =>
                            patchNode(current.id, {
                              failure_policy: enabled
                                ? { ...defaultFailurePolicy(), max_retries: 0 }
                                : undefined,
                            })
                          }
                        />
                      </Label>
                      {current.failure_policy ? (
                        <FailurePolicyEditor
                          value={current.failure_policy}
                          onChange={(failure_policy) =>
                            patchNode(current.id, { failure_policy })
                          }
                          title={t("graph.advancedPolicy")}
                        />
                      ) : (
                        <p className="text-xs text-muted-foreground">
                          {t("graph.inheritPolicy")}
                        </p>
                      )}
                    </div>
                  </details>

                  <Button
                    size="sm"
                    variant="outline"
                    className="w-full"
                    onClick={() => beginAdd("condition", current.id, "failure")}
                  >
                    <SlidersHorizontal className="size-3" />
                    {t("graph.insertCondition")}
                  </Button>
                </>
              ) : null}
              {current.kind === "condition" ? (
                <>
                  <Field label={t("graph.name")}>
                    <Input
                      value={current.name ?? ""}
                      onChange={(event) =>
                        patchNode(current.id, { name: event.target.value })
                      }
                    />
                  </Field>
                  {(current.rules ?? []).map((rule, index) => (
                    <section
                      key={rule.id}
                      className="space-y-2 rounded-md border p-3"
                    >
                      <div className="flex items-center justify-between">
                        <strong className="text-xs">
                          {t("graph.rule")} {index + 1}
                        </strong>
                        <Button
                          size="icon-xs"
                          variant="ghost"
                          aria-label={t("graph.removeRule")}
                          onClick={() => {
                            editGraph((graph) => ({
                              ...graph,
                              nodes: graph.nodes.map((node) =>
                                node.id === current.id
                                  ? {
                                      ...node,
                                      rules: node.rules?.filter(
                                        (item) => item.id !== rule.id,
                                      ),
                                      unknown_port:
                                        node.unknown_port === rule.id
                                          ? "otherwise"
                                          : node.unknown_port,
                                    }
                                  : node,
                              ),
                              edges: graph.edges.filter(
                                (edge) =>
                                  edge.source !== current.id ||
                                  edge.port !== rule.id,
                              ),
                            }));
                          }}
                        >
                          <X className="size-3" />
                        </Button>
                      </div>
                      <Input
                        aria-label={t("graph.ruleName")}
                        value={rule.label ?? ""}
                        onChange={(event) =>
                          patchNode(current.id, {
                            rules: current.rules?.map((item) =>
                              item.id === rule.id
                                ? { ...item, label: event.target.value }
                                : item,
                            ),
                          })
                        }
                      />
                      <RoutingConditionEditor
                        value={rule.predicate}
                        services={services}
                        onChange={(predicate) =>
                          patchNode(current.id, {
                            rules: current.rules?.map((item) =>
                              item.id === rule.id
                                ? { ...item, predicate }
                                : item,
                            ),
                          })
                        }
                      />
                      {portEditor(rule.id, t("graph.whenMatched"))}
                    </section>
                  ))}
                  <Button
                    size="sm"
                    variant="outline"
                    className="w-full"
                    onClick={() =>
                      patchNode(current.id, {
                        rules: [
                          ...(current.rules ?? []),
                          {
                            id: graphID(),
                            label: t("graph.rule"),
                            predicate: defaultPredicate(),
                          },
                        ],
                      })
                    }
                  >
                    <Plus className="size-3" />
                    {t("graph.addRule")}
                  </Button>
                  {portEditor("otherwise", t("graph.otherwise"))}
                  <Field
                    label={t("graph.unknownHandling")}
                    hint={t("graph.unknownHint")}
                  >
                    <GraphSelect
                      label={t("graph.unknownHandling")}
                      value={current.unknown_port || "otherwise"}
                      options={[
                        { value: "otherwise", label: t("graph.otherwise") },
                        ...(current.rules ?? []).map((rule) => ({
                          value: rule.id,
                          label: rule.label || rule.id,
                        })),
                        { value: "unknown", label: t("graph.separateUnknown") },
                      ]}
                      onChange={(unknown_port) =>
                        patchNode(current.id, {
                          unknown_port: unknown_port || "otherwise",
                        })
                      }
                    />
                  </Field>
                  {current.unknown_port === "unknown"
                    ? portEditor("unknown", t("graph.unknown"))
                    : null}
                </>
              ) : null}
              {current.kind === "stop" ? (
                <Field label={t("graph.name")} hint={t("graph.stopHint")}>
                  <Input
                    value={current.name ?? ""}
                    onChange={(event) =>
                      patchNode(current.id, { name: event.target.value })
                    }
                  />
                </Field>
              ) : null}
              <Button
                size="sm"
                variant="ghost"
                className="w-full text-danger-foreground"
                onClick={() => {
                  editGraph((graph) => removeGraphNode(graph, current.id));
                  setSelected(null);
                }}
              >
                {t("graph.deleteNode")}
              </Button>
            </fieldset>
          </Panel>
        ) : null}
      </div>
      {traceOpen && steps.length > 0 && !state.error ? (
        <Panel className="routing-trace-panel">
          <div className="flex items-center justify-between border-b px-3 py-2">
            <strong className="text-xs">
              {replay ? t("graph.actualTrace") : t("graph.simulatedTrace")}
              {preview && !replay
                ? ` · ${t("graph.attempts", { count: preview.attempts })}`
                : ""}
            </strong>
            <div className="flex items-center gap-2">
              <span className="text-xs text-muted-foreground">
                {!replay && preview
                  ? t(`graph.reasons.${preview.stop_reason}`, {
                      defaultValue: preview.stop_reason,
                    })
                  : ""}
              </span>
              <Button
                size="icon-xs"
                variant="ghost"
                aria-label={t("common.close")}
                onClick={() => setTraceOpen(false)}
              >
                <X className="size-3" />
              </Button>
            </div>
          </div>
          <div className="min-h-0 overflow-y-auto p-2">
            <RoutingGraphTrace
              steps={steps}
              graph={graph}
              onSelect={selectTrace}
            />
          </div>
        </Panel>
      ) : null}
      <Dialog
        open={add !== null}
        onOpenChange={(open) => {
          if (!open) setAdd(null);
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              {t(add?.after ? "graph.addFallback" : "graph.addNode")}
            </DialogTitle>
            <DialogDescription>{t("graph.addHint")}</DialogDescription>
          </DialogHeader>
          {add ? (
            <div className="grid gap-4">
              <GraphSelect
                value={add.kind}
                label={t("graph.nodeKind")}
                options={(["entry", "call", "condition", "stop"] as GraphKind[])
                  .filter((kind) => !add.after || kind !== "entry")
                  .map((kind) => ({
                    value: kind,
                    label: t(`graph.kinds.${kind}`),
                  }))}
                onChange={(kind) =>
                  setAdd({ ...add, kind: (kind || "call") as GraphKind })
                }
              />
              {add.kind === "entry" ? (
                <Field
                  label={t("graph.publicModel")}
                  hint={t("graph.publicModelHint")}
                >
                  <ModelSelect
                    aria-label={t("graph.publicModel")}
                    value={add.publicModel}
                    options={[
                      ...new Set(services.flatMap((service) => service.models)),
                    ]}
                    onValueChange={(publicModel) =>
                      setAdd({ ...add, publicModel })
                    }
                  />
                </Field>
              ) : add.kind === "call" ? (
                <>
                  <Field label={t("graph.service")}>
                    <GraphSelect
                      label={t("graph.service")}
                      value={add.service}
                      options={services.map((service) => ({
                        value: service.id,
                        label: service.name,
                      }))}
                      onChange={(service) =>
                        setAdd({
                          ...add,
                          service,
                          model:
                            services.find((item) => item.id === service)
                              ?.models[0] ?? "",
                        })
                      }
                    />
                  </Field>
                  <Field
                    label={t("graph.actualModel")}
                    hint={t("graph.mappingHint")}
                  >
                    <ModelSelect
                      aria-label={t("graph.actualModel")}
                      value={add.model}
                      options={
                        services.find((service) => service.id === add.service)
                          ?.models ?? []
                      }
                      onValueChange={(model) => setAdd({ ...add, model })}
                    />
                  </Field>
                </>
              ) : null}
            </div>
          ) : null}
          <DialogFooter>
            <Button variant="outline" onClick={() => setAdd(null)}>
              {t("common.cancel")}
            </Button>
            <Button
              disabled={
                !add ||
                (add.kind === "entry" &&
                  (!add.publicModel.trim() ||
                    entries.some(
                      (entry) => entry.model === add.publicModel.trim(),
                    ))) ||
                (add.kind === "call" && (!add.service || !add.model.trim()))
              }
              onClick={createNode}
            >
              {t("graph.addToCanvas")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog open={testOpen} onOpenChange={setTestOpen}>
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t("graph.simulate")}</DialogTitle>
            <DialogDescription>{t("graph.simulateHint")}</DialogDescription>
          </DialogHeader>
          <div className="grid gap-3">
            <Field label={t("graph.entry")}>
              <GraphSelect
                label={t("graph.entry")}
                value={testEntry}
                options={entries.map((entry) => ({
                  value: entry.id,
                  label: entry.model ?? entry.id,
                }))}
                onChange={setTestEntry}
              />
            </Field>
            <Field label={t("graph.protocol")}>
              <GraphSelect
                label={t("graph.protocol")}
                value={protocol}
                options={[
                  "openai.responses",
                  "openai.chat",
                  "anthropic.messages",
                  "google.generate_content",
                ].map((value) => ({ value, label: value }))}
                onChange={setProtocol}
              />
            </Field>
            <div className="flex flex-wrap gap-4">
              {[
                { value: streaming, set: setStreaming, key: "streaming" },
                { value: hasTools, set: setHasTools, key: "tools" },
                { value: hasImages, set: setHasImages, key: "images" },
              ].map((item) => (
                <Label
                  key={item.key}
                  className="flex items-center gap-2 text-xs"
                >
                  <Switch checked={item.value} onCheckedChange={item.set} />
                  {t(`graph.${item.key}`)}
                </Label>
              ))}
            </div>
            {graph.nodes
              .filter(
                (node) =>
                  node.kind === "call" &&
                  reachable(graph, testEntry).has(node.id),
              )
              .map((node) => (
                <Field
                  key={node.id}
                  label={`${node.upstream_model} · ${services.find((service) => service.id === node.service_id)?.name ?? ""}`}
                  hint={!node.enabled ? t("graph.disabledHint") : undefined}
                >
                  <GraphSelect
                    disabled={!node.enabled}
                    label={t("graph.simulatedResult")}
                    value={outcomes[node.id] ?? "success"}
                    options={[
                      "success",
                      "429",
                      "503",
                      "401",
                      "400",
                      "network_error",
                      "response_timeout",
                      "unavailable",
                    ].map((value) => ({
                      value,
                      label: t(`graph.outcomes.${value}`),
                    }))}
                    onChange={(value) =>
                      setOutcomes({
                        ...outcomes,
                        [node.id]: value || "success",
                      })
                    }
                  />
                </Field>
              ))}
          </div>
          <DialogFooter>
            <Button
              disabled={testing || !testEntry}
              onClick={() => void simulate()}
            >
              {testing ? t("graph.testing") : t("graph.runSimulation")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog open={requestDialog} onOpenChange={setRequestDialog}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("graph.openRequest")}</DialogTitle>
            <DialogDescription>{t("graph.openRequestHint")}</DialogDescription>
          </DialogHeader>
          <Input
            aria-label={t("graph.requestID")}
            value={requestID}
            onChange={(event) => setRequestID(event.target.value)}
            placeholder="request_…"
          />
          <DialogFooter>
            <Button
              disabled={!requestID.trim() || testing}
              onClick={() => void openRequest()}
            >
              {t("graph.openTrace")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
