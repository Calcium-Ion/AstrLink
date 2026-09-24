import { ModelSelect } from "./ModelSelect";
import { useT } from "../i18n";
import type { RouteTarget } from "../route-model";
import type { RoutableService } from "../service-model";
import { Field } from "./Field";
import { Panel } from "./Panel";
import { Button } from "./ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "./ui/select";

export function BackupTargetsEditor({
  value,
  onChange,
  services,
  protocol,
  onPromote,
}: {
  onPromote?: (index: number) => void;
  value: RouteTarget[];
  onChange: (value: RouteTarget[]) => void;
  services: RoutableService[];
  protocol: string;
}) {
  const t = useT();
  const compatible = services.filter(
    (service) =>
      service.models.length &&
      service.capabilities.some(
        (capability) => capability.protocol === protocol,
      ),
  );
  const replace = (targets: RouteTarget[]) =>
    onChange(
      targets.map((target, index) => ({
        ...target,
        priority: (index + 1) * 10,
      })),
    );
  const update = (index: number, patch: Partial<RouteTarget>) =>
    replace(
      value.map((target, at) =>
        at === index ? { ...target, ...patch } : target,
      ),
    );
  const move = (index: number, offset: number) => {
    const next = [...value];
    [next[index], next[index + offset]] = [next[index + offset], next[index]];
    replace(next);
  };
  return (
    <div className="grid gap-2 border-t pt-3">
      {value.map((target, index) => {
        const service = services.find(
          (service) => service.id === target.service_id,
        );
        return (
          <Panel tone="inset" className="grid gap-2 p-3" key={index}>
            <p className="text-xs font-medium">
              {t("failure.backup", { count: index + 1 })}
            </p>
            <Field label={t("routes.service")}>
              <Select
                value={target.service_id}
                onValueChange={(serviceID) => {
                  const service = compatible.find(
                    (service) => service.id === serviceID,
                  );
                  if (service)
                    update(index, {
                      service_id: serviceID,
                      upstream_model: service.models[0],
                      plan_type:
                        service.capabilities.find(
                          (capability) => capability.protocol === protocol,
                        )?.mode ?? "native",
                      upstream_protocol: protocol,
                    });
                }}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {services
                    .filter(
                      (service) =>
                        compatible.includes(service) ||
                        service.id === target.service_id,
                    )
                    .map((service) => (
                      <SelectItem value={service.id} key={service.id}>
                        {service.name}
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
            </Field>
            <Field label={t("routes.upstreamModel")}>
              <ModelSelect
                aria-label={`${t("routes.upstreamModel")} ${index + 1}`}
                value={target.upstream_model ?? ""}
                options={service?.models ?? []}
                allowCustomValue={false}
                onValueChange={(upstream_model) =>
                  update(index, { upstream_model })
                }
              />
            </Field>
            <Field label={t("routes.planType")}>
              <Select
                value={target.plan_type}
                onValueChange={(plan_type) =>
                  update(index, {
                    plan_type: plan_type as RouteTarget["plan_type"],
                  })
                }
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {[
                    ...new Set(
                      service?.capabilities
                        .filter(
                          (capability) => capability.protocol === protocol,
                        )
                        .map((capability) => capability.mode) ?? [],
                    ),
                  ].map((mode) => (
                    <SelectItem value={mode} key={mode}>
                      {mode === "native"
                        ? t("failure.nativeTarget")
                        : t("failure.delegatedTarget")}
                    </SelectItem>
                  ))}
                  {target.plan_type === "relaykit" ? (
                    <SelectItem value="relaykit">
                      {t("failure.convertedTarget")} ·{" "}
                      {target.upstream_protocol}
                    </SelectItem>
                  ) : null}
                </SelectContent>
              </Select>
            </Field>
            <div className="flex flex-wrap gap-1">
              {onPromote ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => onPromote(index)}
                >
                  {t("failure.promote")}
                </Button>
              ) : null}
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={index === 0}
                onClick={() => move(index, -1)}
              >
                {t("failure.moveUp")}
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={index === value.length - 1}
                onClick={() => move(index, 1)}
              >
                {t("failure.moveDown")}
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => replace(value.filter((_, at) => at !== index))}
              >
                {t("routes.remove")}
              </Button>
            </div>
          </Panel>
        );
      })}
      <Button
        type="button"
        variant="outline"
        disabled={!compatible.length || value.length >= 199}
        onClick={() => {
          const service = compatible[0];
          replace([
            ...value,
            {
              service_id: service.id,
              upstream_model: service.models[0],
              upstream_protocol: protocol,
              plan_type:
                service.capabilities.find(
                  (capability) => capability.protocol === protocol,
                )?.mode ?? "native",
              priority: 0,
            },
          ]);
        }}
      >
        {t("failure.addBackup")}
      </Button>
    </div>
  );
}
