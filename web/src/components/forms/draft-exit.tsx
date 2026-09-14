"use client";

import { createContext, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "@/components/admin/i18n-provider";
import { createDraftExitController, createDraftNavigationGuard, type DraftExitController, type DraftNavigationEvent, type DraftReader } from "@/lib/ui-v2/draft-exit-controller";

import { subscribeDraftSessionExit } from "@/lib/ui-v2/draft-navigation-lifecycle";

export const DraftExitContext = createContext<DraftExitController | null>(null);

export function useDraftExit({ enabled, pending }: { enabled: boolean; pending: boolean | (() => boolean) }) {
  const { locale } = useI18n();
  const [guard] = useState(() => createDraftExitController({
    enabled: () => true, pending: () => true, confirmDiscard: () => false,
  }));
  // Update decision callbacks after commit; draft readers remain registered on this guard.
  useLayoutEffect(() => { guard.updateOptions({
    enabled: () => enabled,
    pending: () => typeof pending === "function" ? pending() : pending,
    confirmDiscard: () => window.confirm(locale === "ja"
      ? "保存していない変更があります。破棄して移動しますか？「キャンセル」で入力を保持します。"
      : "You have unsaved changes. Discard them and leave? Cancel keeps your input."),
  }); });
  useEffect(() => {
    if (!enabled) return;
    const navigation = (window as unknown as { navigation?: EventTarget }).navigation;
    const sessionExit = subscribeDraftSessionExit(window, navigation);
    const navigationGuard = createDraftNavigationGuard(guard, sessionExit.active);
    const onNavigate = (event: Event) => navigationGuard.navigate(event as unknown as DraftNavigationEvent);
    const onUnload = (event: BeforeUnloadEvent) => navigationGuard.beforeUnload(event);
    // Without Navigation API, explicit anchor intent receives one custom decision.
    // Reload/window close still reaches beforeunload independently of any navigate event.
    const onClick = (event: MouseEvent) => {
      if (navigation || event.defaultPrevented || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
      const anchor = event.target instanceof Element ? event.target.closest("a[href]") : null;
      if (!(anchor instanceof HTMLAnchorElement) || anchor.target === "_blank" || anchor.hasAttribute("download")) return;
      navigationGuard.navigate({ destination: { url: anchor.href }, source: "anchor", cancelable: true, defaultPrevented: false, preventDefault: () => { event.preventDefault(); event.stopPropagation(); } });
    };
    navigation?.addEventListener("navigate", onNavigate);
    window.addEventListener("beforeunload", onUnload);
    document.addEventListener("click", onClick, true);
    return () => {
      sessionExit.dispose();
      navigation?.removeEventListener("navigate", onNavigate);
      window.removeEventListener("beforeunload", onUnload);
      document.removeEventListener("click", onClick, true);
    };
  }, [enabled, guard]);
  return guard;
}

// Call only with the explicitly enumerated non-secret fields of the existing owner.
// The baseline is comparison data, not another editable draft; nothing is persisted.
export function useNonSecretDraft(values: readonly (string | number | boolean | null | readonly string[])[], controller?: DraftExitController) {
  const inherited = useContext(DraftExitContext);
  const guard = controller ?? inherited;
  const serialized = JSON.stringify(values);
  const current = useRef(serialized);
  const baseline = useRef(serialized);
  useLayoutEffect(() => { current.current = serialized; });
  const [reader] = useState<DraftReader>(() => ({
    isDirty: () => current.current !== baseline.current,
    saved: () => { baseline.current = current.current; },
  }));
  useLayoutEffect(() => guard?.register(reader), [guard, reader]);
  return useMemo(() => ({ ...reader, acknowledgeSubmitted: () => { baseline.current = serialized; } }), [reader, serialized]);
}

export function useExistingDraft(dirty: boolean | (() => boolean), pending: boolean | (() => boolean), controller?: DraftExitController) {
  const inherited = useContext(DraftExitContext);
  const guard = controller ?? inherited;
  const latest = useRef({ dirty, pending });
  useLayoutEffect(() => { latest.current = { dirty, pending }; });
  useLayoutEffect(() => guard?.register({
    isDirty: () => typeof latest.current.dirty === "function" ? latest.current.dirty() : latest.current.dirty,
    pending: () => typeof latest.current.pending === "function" ? latest.current.pending() : latest.current.pending,
    // Only the existing visual save/refresh owner may clear dirtySections.
    saved: () => {},
  }), [guard]);
  return guard;
}
