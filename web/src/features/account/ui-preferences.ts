export const userThemeIDs = [
  "autostream", "slate", "ocean", "cyan", "indigo", "violet",
  "magenta", "rose", "crimson", "amber", "emerald", "monochrome",
] as const;

export const userColorModes = ["system", "light", "dark"] as const;
export const uiPreferenceQueryKey = ["account", "preferences", "ui"] as const;

export type UserThemeID = (typeof userThemeIDs)[number];
export type UserColorMode = (typeof userColorModes)[number];

export type UserUIPreference = {
  theme_id: string;
  color_mode: string;
  revision: number;
  updated_at?: string;
  fallback?: boolean;
};

export type SafeUserUIPreference = Omit<UserUIPreference, "theme_id" | "color_mode"> & {
  theme_id: UserThemeID;
  color_mode: UserColorMode;
  fallback: boolean;
};

export const themeLabels: Readonly<Record<UserThemeID, string>> = Object.freeze({
  autostream: "AutoStream",
  slate: "Slate",
  ocean: "Ocean",
  cyan: "Cyan",
  indigo: "Indigo",
  violet: "Violet",
  magenta: "Magenta",
  rose: "Rose",
  crimson: "Crimson",
  amber: "Amber",
  emerald: "Emerald",
  monochrome: "Monochrome",
});

// Themes may only change surface/accent tokens. These semantic token names are
// deliberately fixed and are never derived from a selected theme.
export const semanticStatusTokenNames = Object.freeze([
  "live", "running", "healthy", "warning", "critical", "offline", "pending", "completed", "disabled",
] as const);

export const themeMirrorStorageKey = "autostream.ui_preference";
export const legacyThemeStorageKey = "autostream.theme";

export function safeUserUIPreference(value: Partial<UserUIPreference> | null | undefined): SafeUserUIPreference {
  const theme = userThemeIDs.includes(value?.theme_id as UserThemeID) ? value?.theme_id as UserThemeID : "autostream";
  const mode = userColorModes.includes(value?.color_mode as UserColorMode) ? value?.color_mode as UserColorMode : "system";
  return {
    theme_id: theme,
    color_mode: mode,
    revision: Number.isSafeInteger(value?.revision) && Number(value?.revision) >= 0 ? Number(value?.revision) : 0,
    updated_at: typeof value?.updated_at === "string" ? value.updated_at : undefined,
    fallback: theme !== value?.theme_id || mode !== value?.color_mode || value?.fallback === true,
  };
}

export function readThemeMirror(storage: Pick<Storage, "getItem">): SafeUserUIPreference {
  const raw = storage.getItem(themeMirrorStorageKey);
  if (raw) {
    try {
      return safeUserUIPreference(JSON.parse(raw) as UserUIPreference);
    } catch {
      // A corrupt mirror is non-authoritative and falls through to legacy/default.
    }
  }
  const legacy = storage.getItem(legacyThemeStorageKey);
  if (legacy === "light" || legacy === "dark") {
    return safeUserUIPreference({ theme_id: "autostream", color_mode: legacy, revision: 0 });
  }
  return safeUserUIPreference({ theme_id: "autostream", color_mode: "system", revision: 0 });
}

export function legacyThemeMigrationMode(storage: Pick<Storage, "getItem">): UserColorMode | null {
  if (storage.getItem(themeMirrorStorageKey) !== null) return null;
  const legacy = storage.getItem(legacyThemeStorageKey);
  return legacy === "light" || legacy === "dark" ? legacy : null;
}

export function writeThemeMirror(storage: Pick<Storage, "setItem">, preference: SafeUserUIPreference) {
  storage.setItem(themeMirrorStorageKey, JSON.stringify({ theme_id: preference.theme_id, color_mode: preference.color_mode }));
}

export function resolvedDarkMode(mode: UserColorMode, systemDark: boolean) {
  return mode === "dark" || (mode === "system" && systemDark);
}

export function applyThemeToRoot(
  root: Pick<HTMLElement, "dataset" | "classList">,
  preference: SafeUserUIPreference,
  systemDark: boolean,
) {
  root.dataset.theme = preference.theme_id;
  root.dataset.colorMode = preference.color_mode;
  root.classList.toggle("dark", resolvedDarkMode(preference.color_mode, systemDark));
}

export function buildUIPreferenceUpdate(preference: SafeUserUIPreference) {
  return {
    theme_id: preference.theme_id,
    color_mode: preference.color_mode,
    expected_revision: preference.revision,
  };
}
