import { useEffect } from "react";

import { updatePluginHostSettings } from "./bridge";
import { currentLocale, useLocale } from "./i18n";

/**
 * Tells Core the interface language each time Core starts and whenever the
 * user switches it: what plugins inject into requests, and what they show,
 * follows it. A session keeps the language it started with.
 */
export function usePluginLanguageSync(coreSessionKey: string | null): void {
  const locale = useLocale();
  useEffect(() => {
    if (coreSessionKey === null) return;
    updatePluginHostSettings({ language: currentLocale() }).catch(() => {
      // A Core without plugins has no language to keep; the next start or
      // switch tells it again.
    });
  }, [coreSessionKey, locale]);
}
