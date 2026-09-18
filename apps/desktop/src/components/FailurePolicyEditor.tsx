import { useId, useState } from "react";
import { useT } from "../i18n";
import {
  defaultFailurePolicy,
  failureActions,
  parseFailurePolicy,
  type FailureAction,
  type FailurePolicy,
} from "../failure-policy-model";
import { Field } from "./Field";
import { FormMessage } from "./FormMessage";
import { Panel, PanelHeader } from "./Panel";
import { Button } from "./ui/button";
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

function ErrorAction({
  label,
  value,
  onChange,
}: {
  label: string;
  value: FailureAction;
  onChange: (value: FailureAction) => void;
}) {
  const t = useT();
  return (
    <Field label={label}>
      <Select
        value={value}
        onValueChange={(value) => onChange(value as FailureAction)}
      >
        <SelectTrigger aria-label={label} className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {failureActions.map((action) => (
            <SelectItem key={action} value={action}>
              {t(`failure.actions.${action}`)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Field>
  );
}

export function FailurePolicyEditor({
  value,
  onChange,
  title,
  hint,
  headingLevel = 3,
}: {
  value: FailurePolicy;
  onChange: (value: FailurePolicy) => void;
  title?: string;
  hint?: string;
  headingLevel?: 2 | 3;
}) {
  const t = useT();
  const Heading = headingLevel === 2 ? "h2" : "h3";
  const id = useId();
  const [newStatus, setNewStatus] = useState("");
  const update = (patch: Partial<FailurePolicy>) =>
    onChange({ ...value, ...patch });
  let invalid = false;
  try {
    parseFailurePolicy(value);
  } catch {
    invalid = true;
  }
  const statusLabel = (code: string) =>
    t("failure.httpCode", { code }) +
    (t(`failure.statuses.${code}`, { defaultValue: "" })
      ? ` · ${t(`failure.statuses.${code}`)}`
      : "");
  return (
    <Panel>
      <PanelHeader
        actions={
          <Button
            type="button"
            variant="ghost"
            onClick={() => onChange(defaultFailurePolicy())}
          >
            {t("failure.reset")}
          </Button>
        }
      >
        <Heading className="text-sm font-semibold">
          {title ?? t("failure.title")}
        </Heading>
        <p className="mt-1 text-xs text-muted-foreground">
          {hint ?? t("failure.serviceHint")}
        </p>
      </PanelHeader>
      <div className="grid gap-4 p-4">
        <Field
          label={t("failure.thinkingSignatureRecovery")}
          hint={t("failure.thinkingSignatureRecoveryHint")}
        >
          <Switch
            aria-label={t("failure.thinkingSignatureRecovery")}
            checked={value.thinking_signature_recovery !== false}
            onCheckedChange={(checked) =>
              update({ thinking_signature_recovery: checked })
            }
          />
        </Field>
        <Field
          label={t("failure.openaiReasoningRecovery")}
          hint={t("failure.openaiReasoningRecoveryHint")}
        >
          <Switch
            aria-label={t("failure.openaiReasoningRecovery")}
            checked={value.openai_reasoning_recovery !== false}
            onCheckedChange={(checked) =>
              update({ openai_reasoning_recovery: checked })
            }
          />
        </Field>
        <Field
          label={t("failure.openaiFunctionOutputRecovery")}
          hint={t("failure.openaiFunctionOutputRecoveryHint")}
        >
          <Switch
            aria-label={t("failure.openaiFunctionOutputRecovery")}
            checked={value.openai_function_output_recovery === true}
            onCheckedChange={(checked) =>
              update({ openai_function_output_recovery: checked })
            }
          />
        </Field>
        <div className="grid grid-cols-2 gap-3 max-[640px]:grid-cols-1">
          <Field
            label={t("failure.maxRetries")}
            hint={t("failure.maxRetriesHint")}
          >
            <Input
              type="number"
              min={0}
              max={5}
              value={value.max_retries}
              onChange={(event) =>
                update({ max_retries: Number(event.target.value) })
              }
            />
          </Field>
          <Field label={t("failure.initialDelay")}>
            <Input
              type="number"
              min={0}
              max={60000}
              step={100}
              value={value.initial_delay_ms}
              onChange={(event) =>
                update({ initial_delay_ms: Number(event.target.value) })
              }
            />
          </Field>
          <Field label={t("failure.maxDelay")} hint={t("failure.backoffHint")}>
            <Input
              type="number"
              min={value.initial_delay_ms}
              max={60000}
              step={100}
              value={value.max_delay_ms}
              onChange={(event) =>
                update({ max_delay_ms: Number(event.target.value) })
              }
            />
          </Field>
        </div>
        <Label className="flex items-center gap-2">
          <Switch
            checked={value.response_start_timeout_seconds === undefined}
            onCheckedChange={(checked) => {
              const next = { ...value };
              if (checked) delete next.response_start_timeout_seconds;
              else next.response_start_timeout_seconds = 60;
              onChange(next);
            }}
          />
          {t("failure.useGlobalTimeout")}
        </Label>
        {value.response_start_timeout_seconds !== undefined ? (
          <Field label={t("failure.timeout")} hint={t("failure.timeoutHint")}>
            <Input
              type="number"
              min={0}
              max={86400}
              value={value.response_start_timeout_seconds}
              onChange={(event) =>
                update({
                  response_start_timeout_seconds: Number(event.target.value),
                })
              }
            />
          </Field>
        ) : null}
        <div className="border-t pt-3">
          <h4 className="mb-3 text-sm font-medium">
            {t("failure.errorRules")}
          </h4>
          <div className="grid gap-3">
            <ErrorAction
              label={t("failure.networkError")}
              value={value.network_error}
              onChange={(network_error) => update({ network_error })}
            />
            <ErrorAction
              label={t("failure.responseTimeout")}
              value={value.response_timeout}
              onChange={(response_timeout) => update({ response_timeout })}
            />
            {Object.entries(value.http_status)
              .sort(([a], [b]) => Number(a) - Number(b))
              .map(([code, action]) => (
                <div
                  className="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-2"
                  key={code}
                >
                  <ErrorAction
                    label={statusLabel(code)}
                    value={action}
                    onChange={(action) =>
                      update({
                        http_status: { ...value.http_status, [code]: action },
                      })
                    }
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    aria-label={t("failure.removeCode", { code })}
                    onClick={() => {
                      const next = { ...value.http_status };
                      delete next[code];
                      update({ http_status: next });
                    }}
                  >
                    {t("common.remove", { defaultValue: t("routes.remove") })}
                  </Button>
                </div>
              ))}
            <div className="flex items-end gap-2">
              <Field htmlFor={`${id}-status`} label={t("failure.addStatus")}>
                <Input
                  id={`${id}-status`}
                  inputMode="numeric"
                  maxLength={3}
                  placeholder="502"
                  value={newStatus}
                  onChange={(event) => setNewStatus(event.target.value)}
                />
              </Field>
              <Button
                type="button"
                variant="outline"
                disabled={
                  !/^[45]\d{2}$/.test(newStatus) ||
                  Object.hasOwn(value.http_status, newStatus)
                }
                onClick={() => {
                  update({
                    http_status: {
                      ...value.http_status,
                      [newStatus]: "retry_and_failover",
                    },
                  });
                  setNewStatus("");
                }}
              >
                {t("failure.addRule")}
              </Button>
            </div>
            <p className="text-xs text-muted-foreground">
              {t("failure.otherErrors")}
            </p>
          </div>
        </div>
        {invalid ? (
          <FormMessage tone="error">{t("failure.invalid")}</FormMessage>
        ) : (
          <p className="text-xs text-muted-foreground">
            {t("failure.serviceSummary", {
              count: value.max_retries,
              delay: value.initial_delay_ms,
            })}
          </p>
        )}
      </div>
    </Panel>
  );
}
