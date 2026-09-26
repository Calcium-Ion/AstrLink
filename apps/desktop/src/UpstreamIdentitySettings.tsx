import { Fragment, useEffect, useId, useState } from "react";

import { CapabilityToggle } from "./components/CapabilityToggle";
import { DataRow } from "./components/DataRow";
import { Field } from "./components/Field";
import { Panel, PanelHeader } from "./components/Panel";
import { Input } from "./components/ui/input";
import {
  identitySettingKeys,
  maxIdentityVersionLength,
  subscriptionProtectionKeys,
  validIdentityVersion,
  type IdentityLearningKey,
  type IdentityVersionKey,
  type RoutingSettings,
} from "./failure-policy-model";
import { useT } from "./i18n";

const providers = ["Codex", "Claude", "Grok"] as const;

const protectionLabels = {
  official_client_passthrough: "officialPassthrough",
  subscription_risk_protection: "risk",
  codex_request_normalization: "codexRequests",
  claude_request_normalization: "claudeRequests",
  subscription_session_isolation: "sessionIsolation",
} as const;

const learnedClients: readonly {
  client: "codex" | "claude";
  learn: IdentityLearningKey;
  version: IdentityVersionKey;
}[] = [
  {
    client: "codex",
    learn: "codex_identity_auto_learn",
    version: "codex_identity_version",
  },
  {
    client: "claude",
    learn: "claude_identity_auto_learn",
    version: "claude_identity_version",
  },
];

/**
 * A version override edited locally and committed on blur or Enter, so a
 * partly typed version is never autosaved. An empty value clears it.
 */
function IdentityVersionField({
  versionKey,
  label,
  hint,
  invalidHint,
  value,
  onCommit,
}: {
  versionKey: IdentityVersionKey;
  label: string;
  hint: string;
  invalidHint: string;
  value: string | undefined;
  onCommit: (value: string) => void;
}) {
  const t = useT();
  const id = useId();
  const [text, setText] = useState(value ?? "");
  const [invalid, setInvalid] = useState(false);
  useEffect(() => {
    setText(value ?? "");
    setInvalid(false);
  }, [value]);
  const commit = () => {
    const next = text.trim();
    setText(next);
    if (!validIdentityVersion(versionKey, next)) {
      setInvalid(true);
      return;
    }
    if (next !== (value ?? "")) onCommit(next);
  };
  return (
    <Field
      className="w-full"
      htmlFor={id}
      label={label}
      hint={
        invalid ? (
          <span className="text-danger-foreground">{invalidHint}</span>
        ) : (
          hint
        )
      }
    >
      <Input
        id={id}
        className="max-w-60 font-mono"
        value={text}
        maxLength={maxIdentityVersionLength}
        placeholder={t("routing.learning.versionPlaceholder")}
        autoComplete="off"
        spellCheck={false}
        aria-invalid={invalid || undefined}
        onChange={(event) => {
          setText(event.target.value);
          setInvalid(false);
        }}
        onBlur={commit}
        onKeyDown={(event) => {
          if (event.key !== "Enter") return;
          event.preventDefault();
          commit();
        }}
      />
    </Field>
  );
}

export function UpstreamIdentitySettings({
  value,
  onChange,
}: {
  value: RoutingSettings;
  onChange: (value: RoutingSettings) => void;
}) {
  const t = useT();
  return (
    <>
      <Panel data-testid="upstream-identity-settings">
        <PanelHeader>
          <h2 className="text-sm font-semibold">
            {t("routing.identityTitle")}
          </h2>
          <p className="mt-1 text-xs text-muted-foreground">
            {t("routing.identityHint")}
          </p>
        </PanelHeader>
        {identitySettingKeys.map((key, index) => (
          <DataRow key={key}>
            <CapabilityToggle
              size="default"
              checked={value[key] ?? true}
              label={t("routing.identityLabel", { provider: providers[index] })}
              description={t(
                `routing.${providers[index].toLowerCase()}IdentityHint`,
              )}
              onCheckedChange={(next) => onChange({ ...value, [key]: next })}
            />
          </DataRow>
        ))}
      </Panel>
      <Panel data-testid="subscription-protection-settings">
        <PanelHeader>
          <h2 className="text-sm font-semibold">
            {t("routing.protectionTitle")}
          </h2>
          <p className="mt-1 text-xs text-muted-foreground">
            {t("routing.protectionHint")}
          </p>
        </PanelHeader>
        {subscriptionProtectionKeys.map((key) => (
          <DataRow key={key}>
            <CapabilityToggle
              size="default"
              checked={value[key] ?? true}
              label={t(`routing.protection.${protectionLabels[key]}`)}
              description={t(`routing.protection.${protectionLabels[key]}Hint`)}
              onCheckedChange={(next) => onChange({ ...value, [key]: next })}
            />
          </DataRow>
        ))}
      </Panel>
      <Panel data-testid="identity-learning-settings">
        <PanelHeader>
          <h2 className="text-sm font-semibold">
            {t("routing.learningTitle")}
          </h2>
          <p className="mt-1 text-xs text-muted-foreground">
            {t("routing.learningHint")}
          </p>
        </PanelHeader>
        {learnedClients.map(({ client, learn, version }) => (
          <Fragment key={client}>
            <DataRow>
              <CapabilityToggle
                size="default"
                checked={value[learn] ?? true}
                label={t(`routing.learning.${client}`)}
                description={t(`routing.learning.${client}Hint`)}
                onCheckedChange={(next) =>
                  onChange({ ...value, [learn]: next })
                }
              />
            </DataRow>
            <DataRow>
              <IdentityVersionField
                versionKey={version}
                label={t(`routing.learning.${client}Version`)}
                hint={t(`routing.learning.${client}VersionHint`)}
                invalidHint={t(`routing.learning.${client}VersionInvalid`)}
                value={value[version]}
                onCommit={(next) => onChange({ ...value, [version]: next })}
              />
            </DataRow>
          </Fragment>
        ))}
      </Panel>
    </>
  );
}
