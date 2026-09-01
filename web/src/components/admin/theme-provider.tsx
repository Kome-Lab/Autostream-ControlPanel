"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { usePathname } from "next/navigation";
import { apiGet, apiPut } from "@/lib/api/client";
import {
  applyThemeToRoot,
  buildUIPreferenceUpdate,
  legacyThemeMigrationMode,
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

export function ThemeProvider({ children }: { children: React.ReactNode }) {
	const pathname = usePathname();
	const queryClient = useQueryClient();
  const [bootstrap] = useState(() => {
    if (typeof window === "undefined") {
      return { preference: safeUserUIPreference({ theme_id: "autostream", color_mode: "system", revision: 0 }), legacyMode: null as UserColorMode | null };
    }
    return { preference: readThemeMirror(window.localStorage), legacyMode: legacyThemeMigrationMode(window.localStorage) };
  });
  const [preview, setPreview] = useState<SafeUserUIPreference | null>(null);
  const [legacyMigrationFailed, setLegacyMigrationFailed] = useState(false);
	const legacyMigrationAttempted = useRef(false);
  const [systemDark, setSystemDark] = useState(() => typeof window !== "undefined" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  const preferenceQuery = useQuery({
    queryKey: uiPreferenceQueryKey,
    queryFn: () => apiGet<UserUIPreference>("/account/preferences/ui"),
    retry: false,
		enabled: pathname.startsWith("/admin"),
  });
  const persistedPreference = useMemo(
    () => preferenceQuery.data ? safeUserUIPreference(preferenceQuery.data) : bootstrap.preference,
    [bootstrap.preference, preferenceQuery.data],
  );
	const legacyMigrationEligible = bootstrap.legacyMode !== null
		&& preferenceQuery.data !== undefined
		&& preferenceQuery.data.revision === 0
		&& preferenceQuery.data.theme_id === "autostream"
		&& preferenceQuery.data.color_mode === "system"
		&& preferenceQuery.data.fallback !== true;
	const legacyPreference = legacyMigrationEligible && !legacyMigrationFailed
		? safeUserUIPreference({ theme_id: "autostream", color_mode: bootstrap.legacyMode ?? "system", revision: 0 })
		: null;
  const preference = preview ?? legacyPreference ?? persistedPreference;
  const dark = preference.color_mode === "dark" || (preference.color_mode === "system" && systemDark);

  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const update = () => setSystemDark(media.matches);
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);

	useEffect(() => {
		if (!legacyMigrationEligible || legacyMigrationFailed || legacyMigrationAttempted.current || !legacyPreference) return;
		legacyMigrationAttempted.current = true;
		void apiPut<UserUIPreference>("/account/preferences/ui", buildUIPreferenceUpdate(legacyPreference)).then((saved) => {
			queryClient.setQueryData(uiPreferenceQueryKey, saved);
		}).catch(() => {
			// Do not auto-resend an ambiguous or rejected migration. Leaving the
			// old key intact permits a later explicit page load to try again.
			setLegacyMigrationFailed(true);
		});
	}, [legacyMigrationEligible, legacyMigrationFailed, legacyPreference, queryClient]);

  useEffect(() => {
    applyThemeToRoot(document.documentElement, preference, systemDark);
  }, [preference, systemDark]);

  useEffect(() => {
    // On authenticated pages the DB-backed value is authoritative. Keep the
    // local mirror limited to pre-hydration bootstrap and never persist an
    // unsaved preview. Public auth pages retain the legacy local-only toggle.
    if (pathname.startsWith("/admin")) {
			if (!preferenceQuery.data || (bootstrap.legacyMode !== null && (legacyMigrationEligible || legacyMigrationFailed))) return;
			writeThemeMirror(window.localStorage, persistedPreference);
			return;
		}
		if (bootstrap.legacyMode !== null && preview === null) return;
		writeThemeMirror(window.localStorage, preference);
  }, [bootstrap.legacyMode, legacyMigrationEligible, legacyMigrationFailed, pathname, persistedPreference, preference, preferenceQuery.data, preview]);

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
