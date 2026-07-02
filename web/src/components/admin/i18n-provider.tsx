"use client";

import { createContext, useContext, useEffect, useMemo, useState } from "react";
import { localeStorageKey, supportedLocales, translate, type TranslationKey } from "@/lib/i18n";
import type { Locale } from "@/types/domain";

type I18nContextValue = {
  locale: Locale;
  setLocale: (locale: Locale) => void;
  t: (key: TranslationKey) => string;
};

const I18nContext = createContext<I18nContextValue | null>(null);

export function I18nProvider({ children }: { children: React.ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(() => {
    if (typeof window === "undefined") return "ja";
    const stored = window.localStorage.getItem(localeStorageKey) as Locale | null;
    return stored && supportedLocales.includes(stored) ? stored : "ja";
  });

  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);

  const value = useMemo<I18nContextValue>(() => {
    const setLocale = (nextLocale: Locale) => {
      setLocaleState(nextLocale);
      window.localStorage.setItem(localeStorageKey, nextLocale);
      document.documentElement.lang = nextLocale;
    };
    return {
      locale,
      setLocale,
      t: (key) => translate(locale, key),
    };
  }, [locale]);

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n() {
  const context = useContext(I18nContext);
  if (!context) throw new Error("useI18n must be used inside I18nProvider.");
  return context;
}
