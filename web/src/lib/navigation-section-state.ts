export type NavigationSectionState = Readonly<{
  open: boolean;
}>;

export function createNavigationSectionState(initiallyOpen: boolean, active: boolean): NavigationSectionState {
  return { open: initiallyOpen || active };
}

export function toggleNavigationSection(state: NavigationSectionState): NavigationSectionState {
  return { ...state, open: !state.open };
}

export function navigationSectionStateKey(sectionKey: string, active: boolean) {
  return `${sectionKey}:${active ? "active" : "inactive"}`;
}
