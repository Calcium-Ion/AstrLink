import { useId } from "react";
import { useT } from "../i18n";
import type { ChannelStickiness } from "../failure-policy-model";
import { Panel, PanelHeader } from "./Panel";
import { Field } from "./Field";
import { HelpPopover } from "./HelpPopover";
import { Switch } from "./ui/switch";
import { Input } from "./ui/input";

/**
 * 会话粘性：同一会话沿用之前智能路由选中的模型和 API 提供商。一个开关、一个闲置
 * 过期时间同时管两者；怎么工作写在 ? 里，界面上不放说明小字。
 */
export function ChannelStickinessEditor({
  value,
  onChange,
}: {
  value: ChannelStickiness;
  onChange: (value: ChannelStickiness) => void;
}) {
  const t = useT();
  const id = useId();
  return (
    <Panel>
      <PanelHeader
        actions={
          <Switch
            aria-labelledby={id}
            checked={value.enabled}
            onCheckedChange={(enabled) => onChange({ ...value, enabled })}
          />
        }
      >
        <div className="flex min-w-0 items-center gap-1.5">
          <h2 id={id} className="text-sm font-semibold">
            {t("binding.settingTitle")}
          </h2>
          <HelpPopover label={t("binding.settingHelpLabel")}>
            {t("binding.settingHelp")}
          </HelpPopover>
        </div>
      </PanelHeader>
      {value.enabled && (
        <div className="grid gap-3 p-4">
          <Field label={t("binding.ttl")} htmlFor={`${id}-ttl`}>
            <div className="flex items-center gap-2">
              <Input
                id={`${id}-ttl`}
                className="max-w-24 tabular-nums"
                type="number"
                min={1}
                max={1440}
                step={1}
                value={value.ttl_seconds / 60}
                onChange={(event) =>
                  onChange({
                    ...value,
                    ttl_seconds: Number(event.target.value) * 60,
                  })
                }
              />
              <span className="text-sm text-text-secondary">
                {t("binding.ttlUnit")}
              </span>
            </div>
          </Field>
        </div>
      )}
    </Panel>
  );
}
