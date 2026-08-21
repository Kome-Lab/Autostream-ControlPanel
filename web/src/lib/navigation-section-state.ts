export type NavigationSectionsState = Readonly<{
  openByKey: Readonly<Record<string, boolean>>;
  activeKey: string | null;
}>;

export const navigationSectionsStorageKey = "autostream.admin.navigation-sections:v1";

export function createNavigationSectionsState(
  sectionKeys: readonly string[],
  initiallyOpenKey: string | null,
  activeKey: string | null,
): NavigationSectionsState {
  const openByKey: Record<string, boolean> = {};
  for (const sectionKey of sectionKeys) openByKey[sectionKey] = sectionKey === initiallyOpenKey || sectionKey === activeKey;
  return { openByKey, activeKey };
}

export function synchronizeNavigationSectionsState(state: NavigationSectionsState, activeKey: string | null): NavigationSectionsState {
  if (state.activeKey === activeKey) return state;
  if (!activeKey || state.openByKey[activeKey]) return { ...state, activeKey };
  return {
    activeKey,
    openByKey: { ...state.openByKey, [activeKey]: true },
  };
}

export function toggleNavigationSection(state: NavigationSectionsState, sectionKey: string): NavigationSectionsState {
  return {
    ...state,
    openByKey: { ...state.openByKey, [sectionKey]: !state.openByKey[sectionKey] },
  };
}

export function isNavigationSectionOpen(state: NavigationSectionsState, sectionKey: string) {
  return state.openByKey[sectionKey] === true;
}

export function navigationSectionStateKey(sectionKey: string) {
  return sectionKey;
}

export function serializeNavigationSectionsState(state: NavigationSectionsState, sectionKeys: readonly string[]) {
  const openByKey: Record<string, boolean> = {};
  for (const sectionKey of sectionKeys) openByKey[sectionKey] = state.openByKey[sectionKey] === true;
  return JSON.stringify({ openByKey });
}

export function restoreNavigationSectionsState(
  sectionKeys: readonly string[],
  serialized: string | null,
  initiallyOpenKey: string | null,
  activeKey: string | null,
): NavigationSectionsState {
  const fallback = createNavigationSectionsState(sectionKeys, initiallyOpenKey, activeKey);
  if (!serialized) return fallback;
  try {
    const parsed = JSON.parse(serialized) as unknown;
    if (!isRecord(parsed) || !isRecord(parsed.openByKey)) return fallback;
    const openByKey: Record<string, boolean> = {};
    for (const sectionKey of sectionKeys) {
      const stored = parsed.openByKey[sectionKey];
      openByKey[sectionKey] = typeof stored === "boolean" ? stored : fallback.openByKey[sectionKey] === true;
    }
    return { openByKey, activeKey };
  } catch {
    return fallback;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
