export type NavigationSectionsState = Readonly<{
  openByKey: Readonly<Record<string, boolean>>;
  activeKey: string | null;
}>;

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
