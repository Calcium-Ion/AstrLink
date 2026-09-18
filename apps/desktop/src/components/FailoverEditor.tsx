import { useT } from "../i18n";
import {
  defaultFailurePolicy,
  type FailurePolicy,
  type FailoverPolicy,
  type FailoverStrategy,
} from "../failure-policy-model";
import { Button } from "./ui/button";
import { Field } from "./Field";
import { Panel, PanelHeader } from "./Panel";
import { FailurePolicyEditor } from "./FailurePolicyEditor";
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "./ui/select";
import { Switch } from "./ui/switch";

export function FailoverControls({
  value,
  onChange,
  switchLabel,
}: {
  value: FailoverPolicy;
  onChange: (value: FailoverPolicy) => void;
  switchLabel?: string;
}) {
  const t = useT();
  return (
    <div className="grid gap-3">
      <FailoverToggle
        checked={value.enabled}
        onCheckedChange={(enabled) => onChange({ ...value, enabled })}
        label={switchLabel ?? t("failure.enableFailover")}
      />
      <RecoveryOrderControls
        value={value}
        onChange={(order) => onChange({ ...value, ...order })}
      />
    </div>
  );
}

export function FailoverToggle({
  checked,
  onCheckedChange,
  label,
}: {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  label: string;
}) {
  return (
    <Label className="flex items-center gap-2">
      <Switch
        checked={checked}
        onCheckedChange={onCheckedChange}
        aria-label={label}
      />
      {label}
    </Label>
  );
}

export function RecoveryOrderControls({
  value,
  onChange,
}: {
  value: Pick<FailoverPolicy, "strategy" | "max_attempts">;
  onChange: (value: Pick<FailoverPolicy, "strategy" | "max_attempts">) => void;
}) {
  const t = useT();
  return (
    <div className="grid grid-cols-2 gap-3 max-[640px]:grid-cols-1">
      <Field label={t("failure.order")}>
        <Select
          value={value.strategy}
          onValueChange={(strategy) =>
            onChange({ ...value, strategy: strategy as FailoverStrategy })
          }
        >
          <SelectTrigger className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="retry_first">
              {t("failure.retryFirst")}
            </SelectItem>
            <SelectItem value="failover_only">
              {t("failure.failoverOnly")}
            </SelectItem>
          </SelectContent>
        </Select>
      </Field>
      <Field
        label={t("failure.maxAttempts")}
        hint={t("failure.maxAttemptsHint")}
      >
        <Input
          type="number"
          min={1}
          max={20}
          value={value.max_attempts}
          onChange={(event) =>
            onChange({ ...value, max_attempts: Number(event.target.value) })
          }
        />
      </Field>
    </div>
  );
}

export function FailoverEditor({
  title,
  scopeHint,
  overrideLabel,
  overrideHint,
  value,
  override,
  onChange,
  onOverrideChange,
  targets = [],
  inheritedFailurePolicy = defaultFailurePolicy(),
  onResetOrder,
  defaultsLoaded = true,
  onReloadDefaults,
}: {
  title: string;
  scopeHint: string;
  overrideLabel: string;
  overrideHint: string;
  value: FailoverPolicy;
  override?: FailurePolicy;
  onChange: (value: FailoverPolicy) => void;
  onOverrideChange: (value: FailurePolicy | undefined) => void;
  inheritedFailurePolicy?: FailurePolicy;
  onResetOrder?: () => void;
  defaultsLoaded?: boolean;
  onReloadDefaults?: () => void;
  targets?: { name: string; policy?: FailurePolicy }[];
}) {
  const t = useT();
  return (
    <div className="grid gap-3">
      <Panel>
        <PanelHeader
          actions={
            onResetOrder ? (
              <Button type="button" variant="ghost" onClick={onResetOrder}>
                {t("failure.resetOrder")}
              </Button>
            ) : undefined
          }
        >
          <h3 className="text-sm font-semibold">{title}</h3>
          <p className="mt-1 text-xs text-muted-foreground">{scopeHint}</p>
        </PanelHeader>
        <div className="grid gap-4 p-4">
          {!defaultsLoaded ? (
            <p className="text-xs text-muted-foreground">
              {t("failure.defaultsUnavailable")}
              {onReloadDefaults ? (
                <Button
                  type="button"
                  variant="ghost"
                  onClick={onReloadDefaults}
                >
                  {t("common.retry")}
                </Button>
              ) : null}
            </p>
          ) : null}
          <fieldset disabled={!defaultsLoaded} className="grid gap-3 min-w-0">
            <FailoverControls value={value} onChange={onChange} />
            {!onResetOrder ? (
              <p className="text-xs text-muted-foreground">
                {t("failure.inheritOrder")}
              </p>
            ) : null}
            <Label className="flex items-center gap-2">
              <Switch
                checked={override !== undefined}
                onCheckedChange={(checked) =>
                  onOverrideChange(
                    checked
                      ? structuredClone(
                          targets[0]?.policy ?? inheritedFailurePolicy,
                        )
                      : undefined,
                  )
                }
              />
              {overrideLabel}
            </Label>
          </fieldset>
          <p className="text-xs text-muted-foreground">
            {override ? overrideHint : t("failure.inheritHint")}
          </p>
          {defaultsLoaded ? (
            <RecoverySummary
              value={value}
              override={override}
              targets={targets}
              inheritedFailurePolicy={inheritedFailurePolicy}
            />
          ) : null}
          <p className="text-xs text-muted-foreground">
            {t("failure.rulesPrecedence")}
          </p>
        </div>
      </Panel>
      {override ? (
        <FailurePolicyEditor value={override} onChange={onOverrideChange} />
      ) : null}
    </div>
  );
}

export function RecoverySummary({
  value,
  override,
  inheritedFailurePolicy,
  targets,
}: {
  value: FailoverPolicy;
  override?: FailurePolicy;
  inheritedFailurePolicy: FailurePolicy;
  targets: { name: string; policy?: FailurePolicy }[];
}) {
  const t = useT();
  return (
    <div
      className="grid gap-1 text-xs text-muted-foreground"
      aria-live="polite"
    >
      {targets.length ? (
        <p>
          {t("failure.targetOrder", {
            order: targets.map((target) => target.name).join(" → "),
          })}
        </p>
      ) : null}
      {targets.map((target, index) => (
        <p key={index}>
          {t("failure.targetRetries", {
            name: target.name,
            count: (override ?? target.policy ?? inheritedFailurePolicy)
              .max_retries,
          })}
        </p>
      ))}
      <p>
        {t(
          value.enabled
            ? value.strategy === "retry_first"
              ? "failure.retryFirstSummary"
              : value.strategy === "failover_only" ? "failure.failoverOnlySummary" : "failure.failoverFirstSummary"
            : "failure.noFailoverSummary",
          { count: value.max_attempts },
        )}
      </p>
    </div>
  );
}
