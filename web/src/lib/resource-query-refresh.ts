export const liveStatusRefreshIntervalMs = 10_000;

const liveResourcePaths = new Set([
  "/streams",
  "/stream-logs",
  "/service-health",
  "/workers",
  "/nodes",
  "/integrations/oauth-accounts",
  "/observability/incidents",
  "/observability/diagnostics",
  "/observability/remediation-actions",
  "/observability/notification-deliveries",
  "/observability/metrics",
]);

export function isLiveResourcePath(path: string) {
  const [pathname = ""] = path.trim().split(/[?#]/, 1);
  const normalized = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
  return liveResourcePaths.has(normalized);
}
