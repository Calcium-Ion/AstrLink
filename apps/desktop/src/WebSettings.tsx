import { i18n } from "./i18n";
import { PageHeader } from "./PageHeader";
import { useEffect, useState } from "react";
import { ConsoleLanguage, type ConsoleStatus } from "./ConsoleEntry";
import { Panel } from "./components/Panel";
import { Button } from "./components/ui/button";
import { FormMessage } from "./components/FormMessage";
import { RawPasswordEntry } from "./RawSealingControls";
import { applyTheme } from "./theme";
import { consoleRequest } from "./web-transport";
import { useWebText } from "./web-copy";

export function WebSettings({
  coreSessionKey,
  isReady,
}: {
  coreSessionKey: string | null;
  isReady: boolean;
}) {
  const t = useWebText();
  const [status, setStatus] = useState<ConsoleStatus | null>(null);
  useEffect(() => {
    void consoleRequest("/console/v1/status")
      .then((r) => setStatus(r.value))
      .catch(() => {});
  }, []);
  return (
    <section className="min-h-0 flex-1 overflow-y-auto">
      <PageHeader title={t("settings")} />
      <Panel className="grid gap-4 p-4">
        <div className="flex items-center justify-between">
          <span>{t("language")}</span>
          <ConsoleLanguage />
        </div>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span>{t("theme")}</span>
          <div className="flex gap-2">
            {(["light", "dark", "system"] as const).map((theme) => (
              <Button
                variant="outline"
                size="sm"
                key={theme}
                onClick={() => applyTheme(theme)}
              >
                {theme === "light" ? "☀" : theme === "dark" ? "☾" : "◐"}
                <span>{i18n.t(`settings.themeOptions.${theme}`)}</span>
              </Button>
            ))}
          </div>
        </div>
        <div>
          <RawPasswordEntry coreSessionKey={coreSessionKey} isReady={isReady} />
          <p className="mt-2 text-sm text-muted-foreground">{t("change")}</p>
        </div>
        <p className="text-sm text-muted-foreground">{t("forgot")}</p>
        {status?.password_reset && (
          <FormMessage tone="warning">
            {t("reset", { variable: status.reset_variable })}
          </FormMessage>
        )}
      </Panel>
    </section>
  );
}
