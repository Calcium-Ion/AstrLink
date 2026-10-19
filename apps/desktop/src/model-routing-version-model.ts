import {
  parseModelRoutingGroups,
  type ModelRoutingGroup,
} from "./model-routing-group-model";

/**
 * 路由组保存过的一版配置。请求记录写下版本号，这里把它换回当时的规则。
 * 每个组单独从 1 计数，只有路由方式变了才加一，开关组不算。
 */
export interface ModelRoutingGroupVersion {
  group_id: string;
  version: number;
  /** 读取时这个组最新的版本号，用来标出「这不是现在的配置」。 */
  latest_version: number;
  created_at: string;
  group: ModelRoutingGroup;
  /** Core 原样返回的组文档，给 JSON 视图用。 */
  document: unknown;
}

function fail(path: string, message: string): never {
  throw new Error(`model routing version: ${path} ${message}`);
}

/** 旧配置里出现过的每个 `field`。当时用的自定义维度后来可能删了，只读的历史视图仍要能显示。 */
function referencedFields(value: unknown, into: Set<string>): Set<string> {
  if (Array.isArray(value)) {
    for (const item of value) referencedFields(item, into);
  } else if (value && typeof value === "object") {
    for (const [key, item] of Object.entries(value)) {
      if (key === "field" && typeof item === "string") into.add(item);
      else referencedFields(item, into);
    }
  }
  return into;
}

export function parseModelRoutingGroupVersion(
  value: unknown,
): ModelRoutingGroupVersion {
  if (!value || typeof value !== "object" || Array.isArray(value))
    fail("", "应为对象");
  const root = value as Record<string, unknown>;
  const positive = (key: string) => {
    const number = root[key];
    if (!Number.isSafeInteger(number) || (number as number) < 1)
      fail(key, "应为正整数");
    return number as number;
  };
  if (typeof root.group_id !== "string") fail("group_id", "应为字符串");
  if (
    typeof root.created_at !== "string" ||
    !Number.isFinite(Date.parse(root.created_at))
  )
    fail("created_at", "应为时间");
  const [group] = parseModelRoutingGroups(
    [root.group],
    [...referencedFields(root.group, new Set())],
  );
  return {
    group_id: root.group_id,
    version: positive("version"),
    latest_version: positive("latest_version"),
    created_at: root.created_at,
    group,
    document: root.group,
  };
}
