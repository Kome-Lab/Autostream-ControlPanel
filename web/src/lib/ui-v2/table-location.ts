import { parseTableURL, writeTableURL, type TableURLPolicy, type TableURLState } from "./table-state";

const changeEvent = "autostream-table-location";
export function currentTableSearch(): string | null {
  return typeof window === "undefined" ? null : window.location.search;
}
export function subscribeTableLocation(listener: () => void) {
  window.addEventListener("popstate", listener);
  window.addEventListener(changeEvent, listener);
  return () => {
    window.removeEventListener("popstate", listener);
    window.removeEventListener(changeEvent, listener);
  };
}
export function updateTableLocation(policy: TableURLPolicy, change: (current: TableURLState) => TableURLState) {
  const current = parseTableURL(window.location.search, policy);
  const query = writeTableURL(window.location.search, change(current), policy);
  const next = window.location.pathname + (query ? "?" + query : "") + window.location.hash;
  if (next !== window.location.pathname + window.location.search + window.location.hash) {
    window.history.replaceState(window.history.state, "", next);
    window.dispatchEvent(new Event(changeEvent));
  }
}
