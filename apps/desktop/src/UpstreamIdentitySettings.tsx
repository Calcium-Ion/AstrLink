import { CapabilityToggle } from "./components/CapabilityToggle";
import { DataRow } from "./components/DataRow";
import { Panel, PanelHeader } from "./components/Panel";
import { identitySettingKeys, type RoutingSettings } from "./failure-policy-model";
import { useT } from "./i18n";

const providers = ["Codex", "Claude", "Grok"] as const;

export function UpstreamIdentitySettings({ value, onChange }: {
  value: RoutingSettings;
  onChange: (value: RoutingSettings) => void;
}) {
  const t = useT();
  return (
    <Panel data-testid="upstream-identity-settings">
      <PanelHeader>
        <h2 className="text-sm font-semibold">{t("routing.identityTitle")}</h2>
        <p className="mt-1 text-xs text-muted-foreground">{t("routing.identityHint")}</p>
      </PanelHeader>
      {identitySettingKeys.map((key, index) => (
        <DataRow key={key}>
          <CapabilityToggle
            size="default"
            checked={value[key] ?? true}
            label={t("routing.identityLabel", { provider: providers[index] })}
            description={t(`routing.${providers[index].toLowerCase()}IdentityHint`)}
            onCheckedChange={(next) => onChange({ ...value, [key]: next })}
          />
        </DataRow>
      ))}
    </Panel>
  );
}
