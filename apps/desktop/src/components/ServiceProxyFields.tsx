import { useTranslation } from "react-i18next";
import { useState } from "react";
import { Button } from "./ui/button";
import { FormMessage } from "./FormMessage";
import { Field } from "./Field";
import { Panel, PanelHeader } from "./Panel";
import { Input } from "./ui/input";
import { Checkbox } from "./ui/checkbox";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "./ui/select";
import {
  proxyDraftWithURL,
  validProxyDraft,
  type ProxyDraft,
  type ProxyMode,
  type ServiceProxyProbeResult,
} from "../service-proxy-model";

export function ServiceProxyFields({
  value,
  onChange,
  hasCredential = false,
  onTest,
  testTarget,
  testDisabled = false,
}: {
  value: ProxyDraft;
  onChange: (value: ProxyDraft) => void;
  hasCredential?: boolean;
  onTest: () => Promise<ServiceProxyProbeResult>;
  testTarget: string;
  testDisabled?: boolean;
}) {
  const { t } = useTranslation();
  const [testing, setTesting] = useState(false);
  const [test, setTest] = useState<{
    draft: ProxyDraft;
    target: string;
    result?: ServiceProxyProbeResult;
    failed?: boolean;
  } | null>(null);
  const testConnection = async () => {
    setTesting(true);
    setTest(null);
    try {
      const result = await onTest();
      setTest({ draft: value, target: testTarget, result });
    } catch {
      setTest({ draft: value, target: testTarget, failed: true });
    } finally {
      setTesting(false);
    }
  };
  const currentTest =
    test?.draft === value && test.target === testTarget ? test : null;
  const update = (patch: Partial<ProxyDraft>) =>
    onChange({ ...value, ...patch });
  return (
    <Panel className="@[760px]:col-span-2" data-testid="service-proxy-fields">
      <PanelHeader
        actions={
          value.mode === "custom" ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={
                testing ||
                testDisabled ||
                !testTarget ||
                !validProxyDraft(value)
              }
              onClick={() => void testConnection()}
            >
              {t(testing ? "serviceProxy.testing" : "serviceProxy.test")}
            </Button>
          ) : undefined
        }
      >
        <h2 className="text-sm font-semibold">{t("serviceProxy.title")}</h2>
        {currentTest && (
          <FormMessage
            className="mt-2"
            role="status"
            tone={currentTest.failed ? "error" : "notice"}
          >
            {currentTest.result
              ? t("serviceProxy.testSuccess", {
                  latency: currentTest.result.latency_ms,
                  status: currentTest.result.status_code,
                })
              : t("serviceProxy.testFailed")}
          </FormMessage>
        )}
      </PanelHeader>
      <div className="grid gap-3 p-4 @[600px]:grid-cols-2">
        <Field label={t("serviceProxy.mode")}>
          <Select
            value={value.mode}
            onValueChange={(mode) => update({ mode: mode as ProxyMode })}
          >
            <SelectTrigger
              aria-label={t("serviceProxy.mode")}
              className="w-full"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {(["inherit", "custom", "direct"] as const).map((mode) => (
                <SelectItem key={mode} value={mode}>
                  {t(`serviceProxy.${mode}`)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        {value.mode === "custom" && (
          <>
            <Field label={t("serviceProxy.url")}>
              <Input
                aria-label={t("serviceProxy.url")}
                placeholder="socks5://127.0.0.1:1080"
                value={value.url}
                onChange={(e) =>
                  onChange(proxyDraftWithURL(value, e.target.value))
                }
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <Field label={t("serviceProxy.username")}>
              <Input
                aria-label={t("serviceProxy.username")}
                disabled={value.removeCredential}
                value={value.username}
                onChange={(e) => update({ username: e.target.value })}
                autoComplete="off"
              />
            </Field>
            <Field label={t("serviceProxy.password")}>
              <Input
                aria-label={t("serviceProxy.password")}
                disabled={value.removeCredential}
                type="password"
                value={value.password}
                onChange={(e) => update({ password: e.target.value })}
                autoComplete="new-password"
              />
            </Field>
            {hasCredential && (
              <label className="flex items-center gap-2 text-xs text-muted-foreground @[600px]:col-span-2">
                <Checkbox
                  checked={value.removeCredential}
                  onCheckedChange={(checked) =>
                    update({ removeCredential: checked === true })
                  }
                />
                {t("serviceProxy.removeCredential")}
              </label>
            )}
          </>
        )}
        <p className="text-xs text-muted-foreground @[600px]:col-span-2">
          {t(
            value.mode === "custom"
              ? hasCredential
                ? "serviceProxy.savedHint"
                : "serviceProxy.customHint"
              : value.mode === "direct"
                ? "serviceProxy.directHint"
                : "serviceProxy.inheritHint",
          )}
        </p>
        {value.mode === "custom" && (
          <p className="text-xs text-muted-foreground @[600px]:col-span-2">
            {t("serviceProxy.testHint", { target: testTarget })}
          </p>
        )}
      </div>
    </Panel>
  );
}
