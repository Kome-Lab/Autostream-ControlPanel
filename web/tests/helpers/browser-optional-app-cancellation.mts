import type { StubRequest, StubResponse } from "./browser-harness.mts";
import type { RejectableEventWaiters } from "./browser-request-lifecycle.mts";

export function optionalBootstrapCancellationEvent(networkID: unknown) {
  if (typeof networkID !== "string" || networkID.length === 0 || networkID.length > 256) return;
  return `owned-optional-app-cancellation:${networkID}`;
}

// This retained file owns only auth/app/version bootstrap fixture observation.
// Observe the real terminal event before the first claim. This wait grants no
// cancellation capability: the original complete Network proof still owns it.
export async function waitForOptionalBootstrapCancellation(
  waiters: RejectableEventWaiters,
  request: StubRequest,
  response: StubResponse,
  networkID: unknown,
) {
  if (response.observeBootstrapCancellation !== true || response.requiredResponse !== false || request.method !== "GET") return;
  if (!["/auth/me", "/settings/app", "/version"].includes(new URL(request.url).pathname)) return;
  const event = optionalBootstrapCancellationEvent(networkID);
  if (!event) return;
  const timeout = new Error("Optional bootstrap cancellation observation window elapsed");
  const waiter = waiters.wait(event, 5_000, timeout);
  try { await waiter.promise; }
  catch (error) { if (error !== timeout) throw error; }
}
