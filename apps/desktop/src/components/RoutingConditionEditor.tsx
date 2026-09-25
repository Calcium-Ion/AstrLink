import type { GraphField, GraphPredicate } from "../routing-graph-model";
import type { RoutableService } from "../service-model";
import { useT } from "../i18n";
import { Field } from "./Field";
import { Input } from "./ui/input";
import { Button } from "./ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "./ui/select";
import { Plus, X } from "./icons";

export function GraphSelect({
  value,
  options,
  onChange,
  label,
  disabled,
}: {
  value: string;
  options: { value: string; label: string }[];
  onChange: (value: string) => void;
  label: string;
  disabled?: boolean;
}) {
  return (
    <Select
      value={value || "__none"}
      onValueChange={(value) => onChange(value === "__none" ? "" : value)}
      disabled={disabled}
    >
      <SelectTrigger aria-label={label} className="w-full min-w-0">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="__none">—</SelectItem>
        {options
          .filter((option) => option.value)
          .map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
      </SelectContent>
    </Select>
  );
}

export const defaultPredicate = (): GraphPredicate => ({
  field: "request.streaming",
  operator: "eq",
  value: true,
});
const fields: GraphField[] = [
  "request.model",
  "request.streaming",
  "request.protocol",
  "request.has_tools",
  "request.has_images",
  "quota.exhausted",
  "quota.used_percent",
  "last.status",
  "last.error",
  "attempts",
];

export function RoutingConditionEditor({
  value,
  onChange,
  services,
  depth = 0,
}: {
  value: GraphPredicate;
  onChange: (value: GraphPredicate) => void;
  services: readonly RoutableService[];
  depth?: number;
}) {
  const t = useT();
  const children = value.all ?? value.any;
  if (children) {
    const kind = value.all ? "all" : "any";
    return (
      <div className="grid gap-2 rounded-md border p-2">
        <div className="flex items-center gap-2">
          <GraphSelect
            label={t("graph.groupLogic")}
            value={kind}
            options={[
              { value: "all", label: t("graph.all") },
              { value: "any", label: t("graph.any") },
            ]}
            onChange={(next) => onChange({ [next || "all"]: children })}
          />
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label={t("graph.unwrap")}
            onClick={() => onChange(children[0] ?? defaultPredicate())}
          >
            <X className="size-3" />
          </Button>
        </div>
        {children.map((child, index) => (
          <div key={index} className="relative border-l-2 pl-2">
            <RoutingConditionEditor
              value={child}
              services={services}
              depth={depth + 1}
              onChange={(next) =>
                onChange({
                  [kind]: children.map((item, i) =>
                    i === index ? next : item,
                  ),
                })
              }
            />
            {children.length > 1 ? (
              <Button
                variant="ghost"
                size="sm"
                onClick={() =>
                  onChange({ [kind]: children.filter((_, i) => i !== index) })
                }
              >
                {t("common.delete")}
              </Button>
            ) : null}
          </div>
        ))}
        {children.length < 16 ? (
          <Button
            size="sm"
            variant="outline"
            onClick={() =>
              onChange({ [kind]: [...children, defaultPredicate()] })
            }
          >
            <Plus className="size-3" />
            {t("graph.addCondition")}
          </Button>
        ) : null}
      </div>
    );
  }
  const field = value.field ?? "request.streaming";
  const isBoolean = [
    "request.streaming",
    "request.has_tools",
    "request.has_images",
    "quota.exhausted",
  ].includes(field);
  const isNumber = ["last.status", "attempts", "quota.used_percent"].includes(
    field,
  );
  const quota = field.startsWith("quota.");
  return (
    <div className="grid gap-2">
      <GraphSelect
        label={t("graph.conditionField")}
        value={field}
        options={fields.map((value) => ({
          value,
          label: t(`graph.fields.${value}`),
        }))}
        onChange={(field) => {
          const numeric = [
            "last.status",
            "attempts",
            "quota.used_percent",
          ].includes(field);
          const string = [
            "request.model",
            "request.protocol",
            "last.error",
          ].includes(field);
          onChange({
            field: (field || "request.streaming") as GraphField,
            operator: "eq",
            value: numeric
              ? field === "last.status"
                ? 429
                : 0
              : string
                ? field === "request.protocol"
                  ? "openai.responses"
                  : "http_429"
                : true,
            ...(field.startsWith("quota.")
              ? {
                  service_id: services[0]?.id ?? "",
                  window: "primary" as const,
                }
              : {}),
          });
        }}
      />
      {quota ? (
        <>
          <GraphSelect
            label={t("graph.quotaService")}
            value={value.service_id ?? ""}
            options={services.map((service) => ({
              value: service.id,
              label: service.name,
            }))}
            onChange={(service_id) => onChange({ ...value, service_id })}
          />
          <GraphSelect
            label={t("graph.quotaWindow")}
            value={value.window ?? "primary"}
            options={[
              { value: "primary", label: t("graph.primaryWindow") },
              { value: "secondary", label: t("graph.secondaryWindow") },
            ]}
            onChange={(window) =>
              onChange({
                ...value,
                window: window === "secondary" ? "secondary" : "primary",
              })
            }
          />
        </>
      ) : null}
      <div className="grid grid-cols-2 gap-2">
        <GraphSelect
          label={t("graph.operator")}
          value={value.operator ?? "eq"}
          options={(isNumber
            ? ["eq", "ne", "gt", "gte", "lt", "lte"]
            : ["eq", "ne"]
          ).map((op) => ({ value: op, label: t(`graph.operators.${op}`) }))}
          onChange={(op) =>
            onChange({
              ...value,
              operator: (op || "eq") as GraphPredicate["operator"],
            })
          }
        />
        {isBoolean ? (
          <GraphSelect
            label={t("graph.conditionValue")}
            value={value.value === false ? "false" : "true"}
            options={[
              { value: "true", label: t("graph.yes") },
              { value: "false", label: t("graph.no") },
            ]}
            onChange={(next) => onChange({ ...value, value: next === "true" })}
          />
        ) : (
          <Input
            aria-label={t("graph.conditionValue")}
            type={isNumber ? "number" : "text"}
            value={String(value.value ?? "")}
            onChange={(event) =>
              onChange({
                ...value,
                value: isNumber
                  ? Number(event.target.value)
                  : event.target.value,
              })
            }
          />
        )}
      </div>
      {depth < 3 ? (
        <Button
          size="sm"
          variant="ghost"
          className="justify-start"
          onClick={() => onChange({ all: [value, defaultPredicate()] })}
        >
          <Plus className="size-3" />
          {t("graph.combine")}
        </Button>
      ) : null}
      {quota ? (
        <Field label={t("graph.freshness")} hint={t("graph.quotaHint")}>
          <span className="text-xs font-normal text-muted-foreground">
            {t("graph.freshnessValue")}
          </span>
        </Field>
      ) : null}
    </div>
  );
}
