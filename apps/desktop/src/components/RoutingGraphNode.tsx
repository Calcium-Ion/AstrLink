import { StatusDot } from "./StatusDot";
import { memo, useEffect } from "react";
import {
  Handle,
  Position,
  useUpdateNodeInternals,
  type Node,
  type NodeProps,
  BaseEdge,
  type EdgeProps,
} from "@xyflow/react";
import { ModelBrandIcon } from "./ModelBrandIcon";
import { Route, Plus, Ban, SlidersHorizontal, ArrowRight } from "./icons";
import { Button } from "./ui/button";
import { Switch } from "./ui/switch";
import { useT } from "../i18n";
import type { GraphNode, GraphStep } from "../routing-graph-model";
import type { RoutableService } from "../service-model";

export type RoutingNodeData = {
  node: GraphNode;
  locked: boolean;
  service?: RoutableService;
  shared: number;
  step?: GraphStep;
  toggle: (id: string) => void;
  append: (id: string) => void;
};
export type FlowRoutingNode = Node<RoutingNodeData, "routing">;

export const RoutingGraphNode = memo(function RoutingGraphNode({
  id,
  data,
  selected,
}: NodeProps<FlowRoutingNode>) {
  const t = useT(),
    update = useUpdateNodeInternals();
  const { node, service, shared, step } = data;
  const disabled =
    !node.enabled && (node.kind === "call" || node.kind === "entry");
  const title =
    node.kind === "entry"
      ? node.model
      : node.kind === "call"
        ? node.upstream_model
        : node.name;
  useEffect(
    () => update(id),
    [id, node.rules?.length, node.unknown_port, update],
  );
  const unavailable =
    node.kind === "call" &&
    service &&
    (!service.enabled || !service.models.includes(node.upstream_model ?? ""));
  const status = disabled
    ? "disabled"
    : (step?.status ??
      (node.kind === "call" && !service
        ? "missing"
        : unavailable
          ? "unavailable"
          : "ready"));
  const ports =
    node.kind === "entry"
      ? [{ id: "next", label: "" }]
      : node.kind === "call"
        ? [{ id: "failure", label: t("graph.failure") }]
        : node.kind === "condition"
          ? [
              ...(node.rules ?? []).map((rule, index) => ({
                id: rule.id,
                label: rule.label || `${t("graph.rule")} ${index + 1}`,
              })),
              { id: "otherwise", label: t("graph.otherwise") },
              ...(node.unknown_port === "unknown"
                ? [{ id: "unknown", label: t("graph.unknown") }]
                : []),
            ]
          : [];
  return (
    <div
      className="routing-node"
      data-kind={node.kind}
      data-disabled={disabled}
      data-selected={selected}
      data-status={status}
      data-node-id={id}
    >
      {node.kind !== "entry" ? (
        <Handle
          type="target"
          position={Position.Left}
          id="in"
          aria-label={t("graph.input")}
        />
      ) : null}
      <div className="routing-node-kicker">
        <span>{t(`graph.kinds.${node.kind}`)}</span>
        {shared > 1 ? (
          <span className="routing-node-shared">
            {t("graph.sharedCount", { count: shared })}
          </span>
        ) : null}
        {node.kind === "entry" || node.kind === "call" ? (
          <Switch
            className="nodrag nopan ml-auto"
            disabled={data.locked}
            checked={node.enabled}
            onCheckedChange={() => data.toggle(id)}
            aria-label={t("graph.toggleNode", { name: title || id })}
          />
        ) : null}
      </div>
      <div className="routing-node-title">
        {node.kind === "entry" ? (
          <Route className="size-4 shrink-0" />
        ) : node.kind === "call" ? (
          <ModelBrandIcon model={node.upstream_model} size={19} />
        ) : node.kind === "condition" ? (
          <SlidersHorizontal className="size-4 shrink-0" />
        ) : (
          <Ban className="size-4 shrink-0" />
        )}
        <strong className="min-w-0 truncate" title={title}>
          {title || t(`graph.empty.${node.kind}`)}
        </strong>
      </div>
      {node.kind === "call" ? (
        <div className="routing-node-service">
          <span className="truncate">
            {service?.name || t("graph.chooseService")}
          </span>
        </div>
      ) : null}
      {node.kind === "condition" ? (
        <div className="routing-node-ports">
          {ports.map((port) => (
            <div key={port.id} className="routing-node-branch">
              <span className="truncate">{port.label}</span>
              <ArrowRight className="size-3" />
              <Handle
                type="source"
                position={Position.Right}
                id={port.id}
                aria-label={port.label}
              />
            </div>
          ))}
        </div>
      ) : null}
      <div className="routing-node-footer">
        <span className="routing-node-status">
          <StatusDot
            tone={
              disabled || status === "skipped"
                ? "neutral"
                : status === "succeeded"
                  ? "positive"
                  : status === "failed" || status === "missing"
                    ? "negative"
                    : status === "unavailable" || status === "attempted"
                      ? "pending"
                      : "neutral"
            }
          />
          {disabled
            ? t(
                node.kind === "call"
                  ? "graph.disabledSkip"
                  : "graph.entryPaused",
              )
            : step
              ? t(`graph.status.${step.status}`, { defaultValue: step.status })
              : node.kind === "call" && !service
                ? t("graph.missingService")
                : unavailable
                  ? t("graph.targetUnavailable")
                  : t("graph.ready")}
        </span>
        {node.kind === "call" || node.kind === "entry" ? (
          <Button
            type="button"
            size="icon-xs"
            variant="ghost"
            disabled={data.locked}
            className="nodrag nopan"
            onClick={() => data.append(id)}
            aria-label={t("graph.addFallback")}
          >
            <Plus className="size-3.5" />
          </Button>
        ) : null}
      </div>
      {node.kind !== "condition"
        ? ports.map((port) => (
            <Handle
              key={port.id}
              type="source"
              position={Position.Right}
              id={port.id}
              aria-label={port.label || t("graph.next")}
            />
          ))
        : null}
    </div>
  );
});

export function RoutingBypassEdge(props: EdgeProps) {
  const t = useT();
  const lower = Math.max(props.sourceY, props.targetY) + 145;
  const path = `M ${props.sourceX} ${props.sourceY} C ${props.sourceX + 45} ${lower}, ${props.targetX - 45} ${lower}, ${props.targetX} ${props.targetY}`;
  const x = (props.sourceX + props.targetX) / 2,
    y = (props.sourceY + props.targetY) / 2 + 108;
  return (
    <BaseEdge
      path={path}
      style={{
        stroke: "var(--muted-foreground)",
        strokeDasharray: "3 5",
        strokeWidth: 1.25,
        opacity: 0.65,
      }}
      label={t("graph.bypassed", { count: Number(props.data?.count ?? 1) })}
      labelX={x}
      labelY={y}
      labelStyle={{ fill: "var(--muted-foreground)", fontSize: 10 }}
      labelBgStyle={{ fill: "var(--background)" }}
    />
  );
}
