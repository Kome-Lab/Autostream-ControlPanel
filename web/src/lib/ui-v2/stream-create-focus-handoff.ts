import { subscribeDraftSessionExit } from "./draft-navigation-lifecycle";

// Only the initiating focus target crosses the shell/creation boundary. Open,
// dirty, mutation and session authority remain with their existing owners.
type FocusOwner = { document: Document; pathname: string; search: string };
export type StreamCreateFocus = { active(): boolean; cancel(): void; connect(owner: FocusOwner): () => void; restoreAfterClose(): void };
type Handoff = { claimed: boolean; lease: StreamCreateFocus };
let current: Handoff | null = null;

export function cancelStreamCreateFocus() { current?.lease.cancel(); }

export function offerStreamCreateFocus(target: HTMLButtonElement | null): StreamCreateFocus | null {
  cancelStreamCreateFocus();
  if (!target) return null;
  const session = subscribeDraftSessionExit(window);
  let cancelled = false, queued = false, connected = false, revision = 0;
  let context: FocusOwner | undefined;
  const owner: Handoff = { claimed: false, lease: {
    active: () => !cancelled && current === owner && !session.active() && (!owner.claimed || connected),
    cancel() {
      cancelled = true;
      session.dispose();
      if (current === owner) current = null;
    },
    connect(next) {
      if (cancelled || queued || current !== owner || session.active() || target.ownerDocument !== next.document ||
        context && (context.document !== next.document || context.pathname !== next.pathname || context.search !== next.search)) {
        owner.lease.cancel(); return () => {};
      }
      context = next; connected = true; const token = ++revision;
      return () => {
        if (token !== revision || !connected) return;
        connected = false;
        // Replay can reconnect only this lease, document and route before this
        // boundary. A true unmount or an older generation cannot restore focus.
        queueMicrotask(() => { if (token === revision && !connected) owner.lease.cancel(); });
      };
    },
    restoreAfterClose() {
      if (cancelled || queued || !owner.claimed) return;
      queued = true;
      // Radix invokes onCloseAutoFocus inside its unmount cleanup, before
      // removing that focus scope. A microtask runs after that same cleanup.
      queueMicrotask(() => {
        try {
          if (cancelled || !connected || current !== owner || session.active() || !context || target.ownerDocument !== context.document ||
            window.location.pathname !== context.pathname || window.location.search !== context.search || !target.isConnected || target.disabled || target.getAttribute("aria-disabled") === "true" || target.closest('[hidden],[inert],[aria-hidden="true"]')) return;
          const rect = target.getBoundingClientRect(), style = getComputedStyle(target);
          if ([rect.width, rect.height].every(Number.isFinite) && rect.width > 0 && rect.height > 0 && target.getClientRects().length && style.visibility !== "hidden" && style.display !== "none" && style.opacity !== "0") target.focus({ preventScroll: true });
        } finally { owner.lease.cancel(); }
      });
    },
  } };
  current = owner;
  return owner.lease;
}

export function takeStreamCreateFocus(): StreamCreateFocus | null {
  if (!current || current.claimed) return null;
  current.claimed = true;
  return current.lease;
}
