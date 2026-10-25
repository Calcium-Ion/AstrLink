import type { ConversionEngineCapability } from "./core-model";
import type {
  ModelRoutingGroup,
  ModelRoutingMember,
} from "./model-routing-group-model";
import { serviceEntryTarget } from "./service-entry-model";
import type { RoutableService } from "./service-model";

/**
 * 路由组按规则选出模型之后，还要在那个模型的 API 提供商上找到能接这种请求格式的
 * 入口。组里的模型接受的格式不一样时，用某些格式发来的请求一命中对应规则就会失败
 * （model_routing_member_unsupported）。这里算出哪些格式、哪些模型、在哪几条规则上
 * 会出问题，编辑页、配置客户端和请求详情共用。
 */

/**
 * 客户端发给路由组的请求格式。Gemini 原生路径的模型名不能带 `/`，到不了 `astrlink/<组>`，
 * 不在这里。
 */
export const routingRequestFormats = [
  "anthropic.messages",
  "openai.responses",
  "openai.chat",
] as const;

/** 一个不兼容某种格式的模型，以及它在组里出现的位置。 */
export interface RoutingFormatGap {
  member: ModelRoutingMember;
  service: RoutableService | undefined;
  /** 指向这个模型的规则编号（从 1 开始）。 */
  rules: number[];
  /** 它也是「其他情况」的模型。 */
  fallback: boolean;
  /** 提供商能开启转换来接这种格式。 */
  convertible: boolean;
}

export interface RoutingFormatRow {
  protocol: string;
  gaps: RoutingFormatGap[];
}

export interface RoutingFormatReport {
  /** 至少有一个模型兼容的格式；组里没有任何模型接的格式不算这个组服务的格式。 */
  rows: RoutingFormatRow[];
  /** 有格式只被部分模型兼容。 */
  mixed: boolean;
  /** 每个模型不兼容、但组里别的模型兼容的格式，按成员 ID。 */
  memberGaps: ReadonlyMap<string, readonly string[]>;
  /** 能靠开启转换补上的入口，按 API 提供商。 */
  fixable: ReadonlyMap<
    string,
    { service: RoutableService; protocols: readonly string[] }
  >;
}

/** 规则或「其他情况」实际用到的模型；没用到的成员保存时会被清掉，不参与判断。 */
export function usedMembers(group: ModelRoutingGroup): ModelRoutingMember[] {
  const used = new Set([
    group.default_member_id,
    ...group.rules.map((rule) => rule.target_member_id),
  ]);
  return group.members.filter((member) => used.has(member.id));
}

/** 一个模型的 API 提供商怎么接某种请求格式。 */
export type RoutingMemberEntry =
  | { kind: "accepts" }
  | { kind: "convertible"; service: RoutableService; target: string }
  | { kind: "incompatible"; service: RoutableService | undefined };

/**
 * 只有能流式的入口才算兼容：编程类客户端每一轮都流式请求，而入口不支持流式时
 * Core 会拒绝这条请求。已有入口但不能流式的，只能到提供商设置里改，不能靠开启转换补。
 */
export function routingMemberEntry(
  member: ModelRoutingMember,
  services: readonly RoutableService[],
  protocol: string,
  engine: ConversionEngineCapability | null | undefined,
): RoutingMemberEntry {
  const service = services.find((item) => item.id === member.service_id);
  if (!service?.enabled) return { kind: "incompatible", service };
  const rows = service.capabilities.filter((row) => row.protocol === protocol);
  if (rows.some((row) => row.streaming)) return { kind: "accepts" };
  if (rows.length > 0) return { kind: "incompatible", service };
  const target = serviceEntryTarget(service, protocol, engine);
  return target
    ? { kind: "convertible", service, target }
    : { kind: "incompatible", service };
}

export function routingFormatReport(
  group: ModelRoutingGroup,
  services: readonly RoutableService[],
  engine: ConversionEngineCapability | null | undefined,
): RoutingFormatReport {
  const members = usedMembers(group);
  const accepts = (member: ModelRoutingMember, protocol: string) =>
    routingMemberEntry(member, services, protocol, engine).kind === "accepts";
  const rows: RoutingFormatRow[] = [];
  const memberGaps = new Map<string, string[]>();
  const fixable = new Map<
    string,
    { service: RoutableService; protocols: string[] }
  >();
  for (const protocol of routingRequestFormats) {
    if (!members.some((member) => accepts(member, protocol))) continue;
    const gaps = members
      .filter((member) => !accepts(member, protocol))
      .map((member): RoutingFormatGap => {
        const entry = routingMemberEntry(member, services, protocol, engine);
        const service =
          entry.kind === "accepts"
            ? services.find((item) => item.id === member.service_id)
            : entry.service;
        const convertible = entry.kind === "convertible";
        return {
          member,
          service,
          rules: group.rules.flatMap((rule, index) =>
            rule.target_member_id === member.id ? [index + 1] : [],
          ),
          fallback: group.default_member_id === member.id,
          convertible,
        };
      });
    for (const gap of gaps) {
      memberGaps.set(gap.member.id, [
        ...(memberGaps.get(gap.member.id) ?? []),
        protocol,
      ]);
      if (gap.convertible && gap.service) {
        const entry = fixable.get(gap.service.id) ?? {
          service: gap.service,
          protocols: [],
        };
        if (!entry.protocols.includes(protocol)) entry.protocols.push(protocol);
        fixable.set(gap.service.id, entry);
      }
    }
    rows.push({ protocol, gaps });
  }
  return {
    rows,
    mixed: rows.some((row) => row.gaps.length > 0),
    memberGaps,
    fixable,
  };
}

/** 一个模型兼容的格式（在路由组服务的格式里），用来写「只接受 …」。 */
export function memberAcceptedFormats(
  member: ModelRoutingMember,
  services: readonly RoutableService[],
): string[] {
  return routingRequestFormats.filter(
    (protocol) =>
      routingMemberEntry(member, services, protocol, null).kind === "accepts",
  );
}
