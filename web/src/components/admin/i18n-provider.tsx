"use client";

import { createContext, useContext, useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { localeStorageKey, supportedLocales, translate, type TranslationKey, type TranslationValues } from "@/lib/i18n";
import type { Locale } from "@/types/domain";

type I18nContextValue = {
  locale: Locale;
  setLocale: (locale: Locale) => void;
  t: (key: TranslationKey, values?: TranslationValues) => string;
};

const I18nContext = createContext<I18nContextValue | null>(null);
const subscribeHydration = () => () => {};
const clientHydrated = () => true;
const serverHydrated = () => false;

export function I18nProvider({ children, initialLocale = "ja" }: { children?: React.ReactNode; initialLocale?: Locale }) {
  const bootstrapReady = useSyncExternalStore(subscribeHydration, clientHydrated, serverHydrated);
  const bootstrapLocale = useMemo(() => {
    if (!bootstrapReady) return initialLocale;
    let stored: Locale | null = null;
    try {
      stored = window.localStorage.getItem(localeStorageKey) as Locale | null;
    } catch {
      // Browser storage is optional; keep the default locale when unavailable.
    }
    return stored && supportedLocales.includes(stored) ? stored : initialLocale;
  }, [bootstrapReady, initialLocale]);
  const [selectedLocale, setLocaleState] = useState<Locale | null>(null);
  const locale = selectedLocale ?? bootstrapLocale;

  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);

  const value = useMemo<I18nContextValue>(() => {
    const setLocale = (nextLocale: Locale) => {
      setLocaleState(nextLocale);
      try {
        window.localStorage.setItem(localeStorageKey, nextLocale);
      } catch {
        // The selected locale still applies in memory when persistence fails.
      }
      document.documentElement.lang = nextLocale;
    };
    return {
      locale,
      setLocale,
      t: (key, values) => translate(locale, key, values),
    };
  }, [locale]);

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n() {
  const context = useContext(I18nContext);
  if (!context) throw new Error("useI18n must be used inside I18nProvider.");
  return context;
}
