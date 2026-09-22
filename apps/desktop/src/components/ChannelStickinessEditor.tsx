import { useId } from "react";
import { useT } from "../i18n";
import type { ChannelStickiness } from "../failure-policy-model";
import { Panel, PanelHeader } from "./Panel";
import { Field } from "./Field";
import { Switch } from "./ui/switch";
import { Input } from "./ui/input";

export function ChannelStickinessEditor({ value, onChange }: { value: ChannelStickiness; onChange: (value: ChannelStickiness) => void }) {
  const t = useT();
  const id = useId();
  return <Panel>
    <PanelHeader actions={<Switch aria-labelledby={id} checked={value.enabled} onCheckedChange={(enabled) => onChange({ ...value, enabled })} />}>
      <h2 id={id} className="text-sm font-semibold">{t("binding.settingTitle")}</h2>
      <p className="mt-1 text-xs text-muted-foreground">{t("binding.settingHint")}</p>
    </PanelHeader>
    {value.enabled && <div className="grid gap-3 p-4">
      <Field label={t("binding.ttl")} hint={t("binding.ttlHint")} htmlFor={`${id}-ttl`}>
        <Input id={`${id}-ttl`} className="max-w-40" type="number" min={1} max={1440} step={1} value={value.ttl_seconds / 60} onChange={(event) => onChange({ ...value, ttl_seconds: Number(event.target.value) * 60 })} />
      </Field>
      <p className="text-xs text-muted-foreground">{t("binding.manageHint")}</p>
    </div>}
  </Panel>;
}
