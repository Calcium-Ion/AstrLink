import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { useSyncExternalStore } from "react";

import { DEFAULT_LOCALE, isLocale, type Locale } from "./locale";
import en from "./locales/en.json";
import ja from "./locales/ja.json";
import zhCN from "./locales/zh-CN.json";

export {
  DEFAULT_LOCALE,
  isLocale,
  LOCALE_NAMES,
  LOCALES,
  type Locale,
} from "./locale";

export function applyDocumentLocale(locale: Locale): void {
  if (typeof document === "undefined") return;
  document.documentElement.lang = locale;
}

const i18nReady = i18n.use(initReactI18next).init({
  lng: DEFAULT_LOCALE,
  fallbackLng: DEFAULT_LOCALE,
  resources: {
    en: { translation: en },
    ja: { translation: ja },
    "zh-CN": { translation: zhCN },
  },
  interpolation: {
    escapeValue: false,
  },
  returnNull: false,
  saveMissing: false,
  react: {
    useSuspense: false,
  },
});

export async function applyLocale(locale: Locale): Promise<void> {
  await i18nReady;
  if (i18n.language !== locale) {
    await i18n.changeLanguage(locale);
  }
  applyDocumentLocale(locale);
}

applyDocumentLocale(DEFAULT_LOCALE);

function subscribeLanguage(onStoreChange: () => void): () => void {
  i18n.on("languageChanged", onStoreChange);
  return () => {
    i18n.off("languageChanged", onStoreChange);
  };
}

function languageSnapshot(): string {
  return i18n.language;
}

/** The active catalog, as a BCP 47 tag for `Intl` formatting and collation. */
export function currentLocale(): Locale {
  return isLocale(i18n.language) ? i18n.language : DEFAULT_LOCALE;
}

const translate = i18n.t.bind(i18n);

/**
 * The active language, re-rendering the caller when it changes. Derived text
 * that is memoised across renders must depend on this, since `i18n.t` reads the
 * language imperatively and its identity never changes.
 */
export function useLocale(): string {
  return useSyncExternalStore(
    subscribeLanguage,
    languageSnapshot,
    languageSnapshot,
  );
}

export function useT(): typeof i18n.t {
  useLocale();
  return translate;
}

export { i18n };
