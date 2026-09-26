import { useT } from "../i18n";
import {
  type FailoverPolicy,
  type FailoverStrategy,
} from "../failure-policy-model";
import { Field } from "./Field";
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
