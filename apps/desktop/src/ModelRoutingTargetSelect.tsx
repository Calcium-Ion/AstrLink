import { useMemo } from "react";

import { ServiceKindIcon } from "@/components/ServiceKindIcon";
import { Combobox, type ComboboxOption } from "@/components/ui/combobox";
import { useT } from "@/i18n";
import type { RoutingTarget } from "@/model-routing-editor-model";
import type { RoutableService } from "@/service-model";

const separator = "\u001f";
const usedPrefix = `used${separator}`;

function targetKey(target: RoutingTarget): string {
  return `${target.serviceId}${separator}${target.model}`;
}

function parseTargetKey(key: string): RoutingTarget | null {
  const raw = key.startsWith(usedPrefix) ? key.slice(usedPrefix.length) : key;
  const index = raw.indexOf(separator);
  if (index < 0) return null;
  return { serviceId: raw.slice(0, index), model: raw.slice(index + 1) };
}

/**
 * 「提供商 · 模型」选择：一个下拉同时定下用哪个 API 提供商的哪个模型。
 *
 * 只列已启用提供商列出的模型，是封闭集合；新模型请先在 API 提供商里加。当前值
 * 指向已删除或停用的提供商时仍照样显示，并在右侧注明，不悄悄替用户换掉。
 * `used` 里的模型排在最前，同一组里反复选同一个模型时不必再翻提供商。
 */
export function ModelRoutingTargetSelect({
  services,
  value,
  onChange,
  used = [],
  disabled = false,
  className,
  warning,
  "aria-label": ariaLabel,
}: {
  services: readonly RoutableService[];
  value: RoutingTarget | undefined;
  onChange: (target: RoutingTarget) => void;
  used?: readonly RoutingTarget[];
  disabled?: boolean;
  className?: string;
  /** 这个模型不兼容组里别的模型兼容的请求格式时，写明哪些请求会失败。 */
  warning?: string;
  "aria-label": string;
}) {
  const t = useT();
  const enabled = useMemo(
    () => services.filter((service) => service.enabled),
    [services],
  );
  const serviceOf = (serviceId: string) =>
    services.find((service) => service.id === serviceId);

  const options = useMemo(() => {
    const usedGroup = t("routingGroups.target.used");
    const offered = new Set(
      enabled.flatMap((service) =>
        service.models.map((model) =>
          targetKey({ serviceId: service.id, model }),
        ),
      ),
    );
    const seen = new Set<string>();
    const usedOptions: ComboboxOption[] = [];
    for (const target of used) {
      const key = targetKey(target);
      if (seen.has(key) || !offered.has(key)) continue;
      seen.add(key);
      usedOptions.push({
        value: `${usedPrefix}${key}`,
        label: target.model,
        group: usedGroup,
        description: enabled.find((service) => service.id === target.serviceId)
          ?.name,
      });
    }
    const serviceOptions = enabled.flatMap((service) =>
      [...new Set(service.models)].filter(Boolean).map((model) => ({
        value: targetKey({ serviceId: service.id, model }),
        label: model,
        group: service.name,
      })),
    );
    return [...usedOptions, ...serviceOptions];
  }, [enabled, used, t]);

  const kindByGroup = useMemo(() => {
    const kinds = new Map<string, RoutableService["kind"]>();
    for (const service of enabled)
      if (!kinds.has(service.name)) kinds.set(service.name, service.kind);
    return kinds;
  }, [enabled]);

  const current = value ? serviceOf(value.serviceId) : undefined;
  const hint = value
    ? current && current.enabled
      ? current.name
      : t("routingGroups.target.missing")
    : undefined;

  return (
    <Combobox
      className={className}
      value={value ? targetKey(value) : ""}
      displayValue={value?.model ?? ""}
      options={options}
      allowCustomValue={false}
      disabled={disabled}
      placeholder={t("routingGroups.target.placeholder")}
      emptyMessage={t("routingGroups.target.empty")}
      hint={hint}
      warning={warning}
      leadingIcon={
        current ? <ServiceKindIcon kind={current.kind} size={16} /> : undefined
      }
      renderGroup={(group) => {
        const kind = kindByGroup.get(group);
        return (
          <>
            {kind ? <ServiceKindIcon kind={kind} size={12} /> : null}
            {group}
          </>
        );
      }}
      aria-label={ariaLabel}
      onValueChange={(key) => {
        const target = parseTargetKey(key);
        if (target) onChange(target);
      }}
    />
  );
}
