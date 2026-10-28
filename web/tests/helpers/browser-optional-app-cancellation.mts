import type { StubRequest, StubResponse } from "./browser-harness.mts";
import type { RejectableEventWaiters } from "./browser-request-lifecycle.mts";

export function optionalAppCancellationEvent(networkID: unknown) {
  if (typeof networkID !== "string" || networkID.length === 0 || networkID.length > 256) return;
  return `owned-optional-app-cancellation:${networkID}`;
}

// Observe the real terminal event before the first claim. This wait grants no
// cancellation capability: the original complete Network proof still owns it.
export async function waitForOptionalAppCancellation(
  waiters: RejectableEventWaiters,
  request: StubRequest,
  response: StubResponse,
  networkID: unknown,
) {
  if (response.observeAppSettingsCancellation !== true || response.requiredResponse !== false || request.method !== "GET") return;
  if (new URL(request.url).pathname !== "/settings/app") return;
  const event = optionalAppCancellationEvent(networkID);
  if (!event) return;
  const timeout = new Error("Optional app-settings cancellation observation window elapsed");
  const waiter = waiters.wait(event, 5_000, timeout);
  try { await waiter.promise; }
  catch (error) { if (error !== timeout) throw error; }
}
