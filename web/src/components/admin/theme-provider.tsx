"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { useQuery } from "@tanstack/react-query";
import { usePathname } from "next/navigation";
import { apiGet } from "@/lib/api/client";
import {
  applyThemeToRoot,
  readThemeMirror,
  safeUserUIPreference,
  uiPreferenceQueryKey,
  writeThemeMirror,
  type SafeUserUIPreference,
  type UserColorMode,
  type UserThemeID,
  type UserUIPreference,
} from "@/features/account/ui-preferences";

type ThemeContextValue = {
  dark: boolean;
  themeID: UserThemeID;
  colorMode: UserColorMode;
  preference: SafeUserUIPreference;
  toggleTheme: () => void;
  previewPreference: (preference: Partial<UserUIPreference>) => void;
  applyPersistedPreference: (preference: UserUIPreference) => void;
};

const ThemeContext = createContext<ThemeContextValue | null>(null);
const subscribeHydration = () => () => {};
const clientHydrated = () => true;
const serverHydrated = () => false;

export function ThemeProvider({ children }: { children: React.ReactNode }) {
	const pathname = usePathname();
  const bootstrapReady = useSyncExternalStore(subscribeHydration, clientHydrated, serverHydrated);
  const bootstrap = useMemo(() => bootstrapReady
    ? readThemeMirror(window.localStorage)
    : safeUserUIPreference({ theme_id: "autostream", color_mode: "system", revision: 0 }),
  [bootstrapReady]);
  const [preview, setPreview] = useState<SafeUserUIPreference | null>(null);
  const [systemDark, setSystemDark] = useState(() => typeof window !== "undefined" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  const preferenceQuery = useQuery({
    queryKey: uiPreferenceQueryKey,
    queryFn: () => apiGet<UserUIPreference>("/account/preferences/ui"),
    retry: false,
		enabled: bootstrapReady && pathname.startsWith("/admin"),
  });
  const persistedPreference = useMemo(
    () => preferenceQuery.data ? safeUserUIPreference(preferenceQuery.data) : bootstrap,
    [bootstrap, preferenceQuery.data],
  );
  const preference = preview ?? persistedPreference;
  const dark = preference.color_mode === "dark" || (preference.color_mode === "system" && systemDark);

  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const update = () => setSystemDark(media.matches);
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);

  useEffect(() => {
		if (!bootstrapReady) return;
    applyThemeToRoot(document.documentElement, preference, systemDark);
	}, [bootstrapReady, preference, systemDark]);

  useEffect(() => {
    // On authenticated pages the DB-backed value is authoritative. Keep the
    // local mirror limited to pre-hydration bootstrap and never persist an
    // unsaved preview.
		if (!bootstrapReady) return;
    if (pathname.startsWith("/admin")) {
			if (!preferenceQuery.data) return;
			writeThemeMirror(window.localStorage, persistedPreference);
			return;
		}
		writeThemeMirror(window.localStorage, preference);
	}, [bootstrapReady, pathname, persistedPreference, preference, preferenceQuery.data]);

  const previewPreference = useCallback((next: Partial<UserUIPreference>) => {
    setPreview((current) => safeUserUIPreference({ ...(current ?? persistedPreference), ...next }));
  }, [persistedPreference]);
  const applyPersistedPreference = useCallback(() => {
    setPreview(null);
  }, []);

  const value = useMemo<ThemeContextValue>(() => {
    const toggleTheme = () => {
      previewPreference({ color_mode: dark ? "light" : "dark" });
    };
    return { dark, themeID: preference.theme_id, colorMode: preference.color_mode, preference, toggleTheme, previewPreference, applyPersistedPreference };
  }, [applyPersistedPreference, dark, preference, previewPreference]);

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme() {
  const context = useContext(ThemeContext);
  if (!context) throw new Error("useTheme must be used inside ThemeProvider.");
  return context;
}
