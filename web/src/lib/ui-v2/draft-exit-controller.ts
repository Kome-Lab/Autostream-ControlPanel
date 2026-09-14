export type DraftReader = Readonly<{ isDirty: () => boolean; saved: () => void; pending?: () => boolean }>;
export type DraftExitController = ReturnType<typeof createDraftExitController>;

// This registry owns no input, credential, payload or mutation. Drafts stay in their forms.
export function createDraftExitController(options: {
  enabled: () => boolean;
  pending: () => boolean;
  confirmDiscard: () => boolean;
}) {
  const readers = new Set<DraftReader>();
  let deciding = false;
  const dirty = () => options.enabled() && [...readers].some((reader) => reader.isDirty());
  return {
    updateOptions(next: typeof options) { options = next; },
    register(reader: DraftReader) { readers.add(reader); return () => { readers.delete(reader); }; },
    dirty,
    saved() { for (const reader of readers) reader.saved(); },
    request(leave: () => void, forced = false) {
      if (forced || !options.enabled()) { leave(); return true; }
      if (deciding || options.pending() || [...readers].some((reader) => reader.pending?.())) return false;
      deciding = true;
      try {
        if (dirty() && !options.confirmDiscard()) return false;
        leave();
        return true;
      } finally { deciding = false; }
    },
  };
}

export type DraftNavigationEvent = {
  destination: { url: string; sameDocument?: boolean };
  source?: "anchor";
  cancelable: boolean;
  defaultPrevented: boolean;
  preventDefault: () => void;
};

// Known cross-document navigation uses native beforeunload only. The fallback
// anchor approval is scoped to its event turn, so it cannot mask later dirty exits.
export function createDraftNavigationGuard(controller: DraftExitController, forcedExit: () => boolean = () => false) {
  let approvedAnchor = false;
  return {
    navigate(event: DraftNavigationEvent) {
      if (event.defaultPrevented || forcedExit()) return;
      if (!event.cancelable || (!event.destination.sameDocument && event.source !== "anchor")) return;
      if (!controller.request(() => {})) event.preventDefault();
      else if (event.source === "anchor") {
        approvedAnchor = true;
        queueMicrotask(() => { approvedAnchor = false; });
      }
    },
    beforeUnload(event: { preventDefault: () => void; returnValue: string; defaultPrevented?: boolean }) {
      if (event.defaultPrevented || forcedExit() || approvedAnchor || !controller.dirty()) return;
      event.preventDefault();
      event.returnValue = "";
    },
  };
}
