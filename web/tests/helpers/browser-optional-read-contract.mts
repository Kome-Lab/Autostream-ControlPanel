import type { RejectableEventWaiters } from "./browser-request-lifecycle.mts";

// Only declared UI Foundation fixtures may opt in. No prefix or arbitrary URL
// matching: an unknown read, required response, or mutation stays on fatal paths.
const paths = new Set([
  "/auth/me", "/settings/app", "/version", "/account/preferences/ui",
  "/auth/oauth/providers", "/auth/mfa/status", "/auth/passkeys", "/auth/oauth-links",
  "/integrations/oauth-accounts", "/integrations/oauth-providers", "/roles",
  "/archive/destinations", "/discord/configs", "/youtube/outputs",
  "/profiles/archive", "/profiles/overlay", "/streams", "/workers", "/nodes",
  "/observability/incidents", "/service-health",
  "/observability/diagnostics", "/observability/metrics", "/audit-logs",
  "/archive/streams", "/archive/processing-streams",
]);
export const isOptionalReadCancellationPath = (path: string) => paths.has(path);

export function optionalReadNetworkStartEvent(id: unknown) {
  if (typeof id === "string" && id.length > 0 && id.length <= 256) return `owned-optional-read-start:${id}`;
}

// Fetch and Network domains can report pause before start. Waiting for the
// matching start supplies identity facts before preparation; it grants no
// cancellation permission and never retries a settlement or repairs a receipt.
// Missing metadata falls back to ordinary settlement, with no new receipt;
// unrelated fatal errors still propagate unchanged.
export async function waitForOptionalReadNetworkStart(
  waiters: RejectableEventWaiters,
  proof: { hasNetworkStartForPause(id: string): boolean } | undefined,
  fetchID: string,
  networkID: unknown,
) {
  if (!proof || proof.hasNetworkStartForPause(fetchID)) return;
  const event = optionalReadNetworkStartEvent(networkID);
  if (!event) return;
  const elapsed = new Error("Optional read Network observation window elapsed");
  try { await waiters.wait(event, 5_000, elapsed).promise; }
  catch (error) { if (error !== elapsed) throw error; }
}
