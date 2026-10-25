import { getService, updateService } from "./bridge";
import { i18n } from "./i18n";
import type { ConversionEngineCapability } from "./core-model";
import { bestConversionTarget, type Service } from "./service-model";

/**
 * 一个 API 提供商能不能接某种请求格式：原生支持或已开启转换的入口都算；还没有入口
 * 但网关能转换的，可以替用户开启。配置客户端、路由组编辑页和请求详情用同一套判断。
 */

type EntryService = Pick<Service, "enabled" | "capabilities">;

/** 已经有这个格式的入口（原生或转换）。 */
export function serviceHasEntry(
  service: EntryService,
  protocol: string,
): boolean {
  return service.capabilities.some((row) => row.protocol === protocol);
}

/** 开启这个格式的入口时转换成哪种原生格式；转换不了时为 undefined。 */
export function serviceEntryTarget(
  service: EntryService,
  protocol: string,
  engine: ConversionEngineCapability | null | undefined,
): string | undefined {
  return bestConversionTarget(
    protocol,
    service.capabilities
      .filter((row) => !row.convert_to)
      .map((row) => row.protocol),
    engine,
  );
}

/** 启用中，并且已有入口或能开启转换。 */
export function serviceSupportsEntry(
  service: EntryService,
  protocol: string,
  engine: ConversionEngineCapability | null | undefined,
): boolean {
  return (
    service.enabled &&
    (serviceHasEntry(service, protocol) ||
      Boolean(serviceEntryTarget(service, protocol, engine)))
  );
}

/**
 * 给 API 提供商加上一个转换入口。先读最新记录、按 ETag 写回，并发编辑不会被覆盖；
 * 已经有入口时原样返回。转换不了或提供商已停用时抛错，由调用方提示。
 */
export async function enableServiceEntry(
  serviceId: string,
  protocol: string,
  engine: ConversionEngineCapability | null | undefined,
): Promise<Service> {
  const record = await getService(serviceId);
  if (!record.service.enabled)
    throw new Error(i18n.t("clientSetup.entryRequired"));
  if (serviceHasEntry(record.service, protocol)) return record.service;
  const convertTo = serviceEntryTarget(record.service, protocol, engine);
  if (!convertTo) throw new Error(i18n.t("clientSetup.entryRequired"));
  const updated = await updateService(serviceId, record.etag, {
    capabilities: [
      ...record.service.capabilities,
      { protocol, mode: "native", streaming: true, convert_to: convertTo },
    ],
  });
  return updated.service;
}
