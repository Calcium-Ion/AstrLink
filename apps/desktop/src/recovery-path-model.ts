import {
  parseFailurePolicy,
  type FailurePolicy,
  type FailoverStrategy,
} from "./failure-policy-model";
import type { RoutePlanType } from "./route-model";
export interface RecoveryPathNode {
  id: string;
  service_id: string;
  upstream_model?: string;
  upstream_protocol: string;
  plan_type: RoutePlanType;
  max_retries?: number;
}
export interface RecoveryPath {
  id: string;
  name: string;
  protocol: string;
  mode: "automatic" | "steps";
  targets?: RecoveryPathNode[];
  steps?: RecoveryPathNode[];
  strategy?: FailoverStrategy;
  max_attempts?: number;
  failure_policy?: FailurePolicy;
}
export type RecoveryPathInput = Omit<RecoveryPath, "id">;
export interface RecoveryPathReference {
  route_id?: string;
  name: string;
  protocol: string;
  category_id?: string;
  override: boolean;
}
export interface RecoveryPathRecord {
  path: RecoveryPath;
  etag: string;
  references: RecoveryPathReference[];
}
export interface RecoveryPreviewInput {
  path: RecoveryPath;
  model?: string;
  streaming: boolean;
  error: string;
  success_at?: number;
  retry_after?: string;
  route_id?: string;
  category_id?: string;
}
export interface RecoveryPreviewStep {
  step_id: string;
  service_id: string;
  model: string;
  action: "initial" | "retry" | "failover";
  status: "failed" | "succeeded" | "skipped";
  reason?: string;
  wait_min_ms: number;
  wait_max_ms: number;
}
export interface RecoveryPreview {
  steps: RecoveryPreviewStep[];
  stop_reason: string;
  max_attempts: number;
}
const idPattern = /^[a-z][a-z0-9_-]{2,95}$/;
const protocolPattern = /^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/;
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw Error("调用路径数据不是对象");
  return value as Record<string, unknown>;
}
function keys(
  value: Record<string, unknown>,
  allowed: string[],
  required: string[],
) {
  if (
    Object.keys(value).some((key) => !allowed.includes(key)) ||
    required.some((key) => !Object.hasOwn(value, key))
  )
    throw Error("调用路径字段不完整或包含未知字段");
}
function string(value: unknown, max = 256) {
  if (typeof value !== "string" || [...value].length > max)
    throw Error("调用路径文本无效");
  return value;
}
function id(value: unknown) {
  const text = string(value, 96);
  if (!idPattern.test(text)) throw Error("调用路径 ID 无效");
  return text;
}
function protocol(value: unknown) {
  const text = string(value, 96);
  if (!protocolPattern.test(text)) throw Error("入口协议无效");
  return text;
}
function integer(value: unknown, min: number, max: number) {
  if (
    typeof value !== "number" ||
    !Number.isInteger(value) ||
    value < min ||
    value > max
  )
    throw Error(`次数必须为 ${min}–${max}`);
  return value;
}
export const pathNodes = (path: RecoveryPath) =>
  path.mode === "steps" ? (path.steps ?? []) : (path.targets ?? []);
export const pathNodeKey = (node: RecoveryPathNode) =>
  JSON.stringify([
    node.service_id,
    node.upstream_model ?? "",
    node.upstream_protocol,
    node.plan_type,
  ]);
export function parseRecoveryPath(value: unknown): RecoveryPath {
  const path = object(value);
  keys(
    path,
    [
      "id",
      "name",
      "protocol",
      "mode",
      "targets",
      "steps",
      "strategy",
      "max_attempts",
      "failure_policy",
    ],
    ["id", "name", "protocol", "mode"],
  );
  const name = string(path.name, 128);
  if (!name.trim()) throw Error("请填写调用路径名称");
  const mode = path.mode;
  if (mode !== "automatic" && mode !== "steps") throw Error("请选择编辑方式");
  if (
    (mode === "steps" &&
      (Object.hasOwn(path, "targets") || Object.hasOwn(path, "strategy"))) ||
    (mode === "automatic" && Object.hasOwn(path, "steps"))
  )
    throw Error("两种编辑方式不能混用");
  const ingress = protocol(path.protocol),
    raw = mode === "steps" ? path.steps : path.targets;
  if (
    !Array.isArray(raw) ||
    raw.length < 1 ||
    raw.length > (mode === "steps" ? 20 : 200)
  )
    throw Error(
      mode === "steps"
        ? "调用路径需要 1–20 个步骤"
        : "调用路径需要 1–200 个目标",
    );
  const ids = new Set<string>(),
    counts = new Map<string, number>();
  const nodes = raw.map((value) => {
    const node = object(value);
    keys(
      node,
      [
        "id",
        "service_id",
        "upstream_model",
        "upstream_protocol",
        "plan_type",
        "max_retries",
      ],
      ["id", "service_id", "upstream_protocol", "plan_type"],
    );
    const plan = node.plan_type;
    if (plan !== "native" && plan !== "delegated" && plan !== "relaykit")
      throw Error("执行方式无效");
    const upstream = protocol(node.upstream_protocol);
    if (plan !== "relaykit" && upstream !== ingress)
      throw Error("目标必须支持所选入口协议");
    const result: RecoveryPathNode = {
      id: id(node.id),
      service_id: id(node.service_id),
      upstream_protocol: upstream,
      plan_type: plan,
    };
    if (node.upstream_model !== undefined)
      result.upstream_model = string(node.upstream_model);
    if (node.max_retries !== undefined) {
      if (mode === "steps") throw Error("逐步模式由步骤决定次数");
      result.max_retries = integer(node.max_retries, 0, 5);
    }
    if (ids.has(result.id)) throw Error("步骤 ID 重复");
    ids.add(result.id);
    const key = pathNodeKey(result),
      count = (counts.get(key) ?? 0) + 1;
    counts.set(key, count);
    if (count > (mode === "steps" ? 6 : 1))
      throw Error(
        mode === "steps"
          ? "同一目标最多出现 6 次"
          : "自动模式不能重复添加同一目标",
      );
    return result;
  });
  const result: RecoveryPath = {
    id: id(path.id),
    name,
    protocol: ingress,
    mode,
    ...(mode === "steps" ? { steps: nodes } : { targets: nodes }),
  };
  if (Object.hasOwn(path, "strategy")) {
    if (path.strategy !== "retry_first" && path.strategy !== "failover_first")
      throw Error("重试顺序无效");
    result.strategy = path.strategy;
  }
  if (Object.hasOwn(path, "max_attempts"))
    result.max_attempts = integer(path.max_attempts, 1, 20);
  if (Object.hasOwn(path, "failure_policy"))
    result.failure_policy = parseFailurePolicy(path.failure_policy);
  return result;
}
export function parseRecoveryPathRecord(value: unknown): RecoveryPathRecord {
  const record = object(value);
  keys(record, ["path", "etag", "references"], ["path", "etag", "references"]);
  const etag = string(record.etag, 80);
  if (!/^"sha256:[0-9a-f]{64}"$/.test(etag)) throw Error("调用路径版本无效");
  if (!Array.isArray(record.references)) throw Error("引用列表无效");
  return {
    path: parseRecoveryPath(record.path),
    etag,
    references: record.references.map((value) => {
      const ref = object(value);
      keys(
        ref,
        ["route_id", "name", "protocol", "category_id", "override"],
        ["name", "protocol", "override"],
      );
      if (typeof ref.override !== "boolean") throw Error("引用覆盖设置无效");
      return {
        name: string(ref.name, 128),
        protocol: protocol(ref.protocol),
        override: ref.override,
        ...(ref.route_id === undefined ? {} : { route_id: id(ref.route_id) }),
        ...(ref.category_id === undefined
          ? {}
          : { category_id: string(ref.category_id, 64) }),
      };
    }),
  };
}
export function parseRecoveryPathPage(value: unknown): RecoveryPathRecord[] {
  const page = object(value);
  keys(page, ["items"], ["items"]);
  if (!Array.isArray(page.items)) throw Error("调用路径列表无效");
  return page.items.map(parseRecoveryPathRecord);
}
export function parseRecoveryPreview(value: unknown): RecoveryPreview {
  const preview = object(value);
  keys(
    preview,
    ["steps", "stop_reason", "max_attempts"],
    ["steps", "stop_reason", "max_attempts"],
  );
  if (!Array.isArray(preview.steps)) throw Error("预览步骤无效");
  return {
    stop_reason: string(preview.stop_reason, 128),
    max_attempts: integer(preview.max_attempts, 1, 20),
    steps: preview.steps.map((value) => {
      const step = object(value);
      keys(
        step,
        [
          "step_id",
          "service_id",
          "model",
          "action",
          "status",
          "reason",
          "wait_min_ms",
          "wait_max_ms",
        ],
        [
          "step_id",
          "service_id",
          "model",
          "action",
          "status",
          "wait_min_ms",
          "wait_max_ms",
        ],
      );
      if (
        !["initial", "retry", "failover"].includes(step.action as string) ||
        !["failed", "succeeded", "skipped"].includes(step.status as string)
      )
        throw Error("预览状态无效");
      return {
        step_id: id(step.step_id),
        service_id: id(step.service_id),
        model: string(step.model),
        action: step.action as RecoveryPreviewStep["action"],
        status: step.status as RecoveryPreviewStep["status"],
        ...(step.reason === undefined
          ? {}
          : { reason: string(step.reason, 128) }),
        wait_min_ms: integer(step.wait_min_ms, 0, 2147483647000),
        wait_max_ms: integer(step.wait_max_ms, 0, 2147483647000),
      };
    }),
  };
}
