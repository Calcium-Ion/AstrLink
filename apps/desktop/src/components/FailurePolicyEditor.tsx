import { useT } from "../i18n";
import {
  defaultFailurePolicy,
  parseFailurePolicy,
  type FailurePolicy,
} from "../failure-policy-model";
import { Field } from "./Field";
import { FailureRulesEditor } from "./FailureRulesEditor";
import { FormMessage } from "./FormMessage";
import { Panel, PanelHeader } from "./Panel";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import { Switch } from "./ui/switch";

const sectionFields = {
  retry: [
    "max_retries",
    "initial_delay_ms",
    "max_delay_ms",
    "response_start_timeout_seconds",
  ],
  repair: [
    "thinking_signature_recovery",
    "openai_reasoning_recovery",
    "openai_function_output_recovery",
  ],
  rules: ["network_error", "response_timeout", "http_status"],
} as const satisfies Record<string, readonly (keyof FailurePolicy)[]>;

export type FailurePolicySection = keyof typeof sectionFields;

export function FailurePolicyEditor({
  value,
  onChange,
  title,
  hint,
  headingLevel = 3,
  section = "all",
}: {
  value: FailurePolicy;
  onChange: (value: FailurePolicy) => void;
  title?: string;
  hint?: string;
  headingLevel?: 2 | 3;
  section?: FailurePolicySection | "all";
}) {
  const t = useT();
  const Heading = headingLevel === 2 ? "h2" : "h3";
  const update = (patch: Partial<FailurePolicy>) =>
    onChange({ ...value, ...patch });
  const reset = () => {
    const defaults = defaultFailurePolicy();
    if (section === "all") {
      onChange(defaults);
      return;
    }
    // Reset only the visible fields, including removing optional overrides.
    const next = { ...value };
    for (const key of sectionFields[section]) {
      if (Object.hasOwn(defaults, key))
        Object.assign(next, { [key]: defaults[key] });
      else Reflect.deleteProperty(next, key);
    }
    onChange(next);
  };
  let invalid = false;
  try {
    parseFailurePolicy(value);
  } catch {
    invalid = true;
  }
  if (section === "rules") {
    return (
      <div className="flex min-h-0 flex-1 flex-col gap-2">
        <FailureRulesEditor
          className="flex-1"
          value={value}
          onChange={onChange}
          title={title ?? t("failure.errorRules")}
          hint={hint}
          headingLevel={headingLevel}
          onReset={reset}
        />
        {invalid ? (
          <FormMessage tone="error">{t("failure.invalid")}</FormMessage>
        ) : null}
      </div>
    );
  }
  return (
    <Panel>
      <PanelHeader
        actions={
          <Button type="button" variant="ghost" onClick={reset}>
            {t(section === "all" ? "failure.reset" : "failure.resetSection")}
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
        {(section === "all" || section === "repair") && (
          <>
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
          </>
        )}
        {(section === "all" || section === "retry") && (
          <>
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
              <Field
                label={t("failure.maxDelay")}
                hint={t("failure.backoffHint")}
              >
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
              <Field
                label={t("failure.timeout")}
                hint={t("failure.timeoutHint")}
              >
                <Input
                  type="number"
                  min={0}
                  max={86400}
                  value={value.response_start_timeout_seconds}
                  onChange={(event) =>
                    update({
                      response_start_timeout_seconds: Number(
                        event.target.value,
                      ),
                    })
                  }
                />
              </Field>
            ) : null}
          </>
        )}
        {section === "all" && (
          <FailureRulesEditor
            className="border-t pt-3"
            title={t("failure.errorRules")}
            headingLevel={4}
            value={value}
            onChange={onChange}
          />
        )}
        {invalid ? (
          <FormMessage tone="error">{t("failure.invalid")}</FormMessage>
        ) : section === "all" || section === "retry" ? (
          <p className="text-xs text-muted-foreground">
            {t("failure.serviceSummary", {
              count: value.max_retries,
              delay: value.initial_delay_ms,
            })}
          </p>
        ) : null}
      </div>
    </Panel>
  );
}
