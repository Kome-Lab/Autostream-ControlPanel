// Presentation-only, document-local notification. It carries no authentication or draft data.
const listeners = new Set<() => void>();
export function notifyDraftSessionExit() { for (const listener of listeners) listener(); }

export function subscribeDraftSessionExit(surface: EventTarget, navigation?: EventTarget) {
  let active = false;
  let expiry: ReturnType<typeof setTimeout> | undefined;
  const reset = () => { active = false; clearTimeout(expiry); expiry = undefined; };
  const notify = () => {
    reset(); active = true;
    // A failed redirect cannot leave a permanent exemption. User input and navigation
    // completion/cancellation end this notification earlier; new subscribers never inherit it.
    expiry = setTimeout(reset, 5_000);
  };
  const input = ["pointerdown", "keydown", "click", "pageshow", "error", "unhandledrejection"];
  const completed = ["navigatesuccess", "navigateerror"];
  listeners.add(notify);
  surface.addEventListener("autostream:draft-session-exit", notify);
  for (const event of input) surface.addEventListener(event, reset, true);
  for (const event of completed) navigation?.addEventListener(event, reset);
  return {
    active: () => active,
    reset,
    dispose() {
      reset(); listeners.delete(notify);
      surface.removeEventListener("autostream:draft-session-exit", notify);
      for (const event of input) surface.removeEventListener(event, reset, true);
      for (const event of completed) navigation?.removeEventListener(event, reset);
    },
  };
}
