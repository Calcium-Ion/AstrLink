import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { useSyncExternalStore } from "react";

import { DEFAULT_LOCALE, type Locale } from "./locale";
import en from "./locales/en.json";
import zhCN from "./locales/zh-CN.json";

export { DEFAULT_LOCALE, isLocale, LOCALES, type Locale } from "./locale";

export function applyDocumentLocale(locale: Locale): void {
  if (typeof document === "undefined") return;
  document.documentElement.lang = locale;
}

const i18nReady = i18n.use(initReactI18next).init({
  lng: DEFAULT_LOCALE,
  fallbackLng: DEFAULT_LOCALE,
  resources: {
    en: { translation: en },
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

const translate = i18n.t.bind(i18n);

export function useT(): typeof i18n.t {
  useSyncExternalStore(subscribeLanguage, languageSnapshot, languageSnapshot);
  return translate;
}

export { i18n };
