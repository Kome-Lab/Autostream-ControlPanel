export type ServiceHealthLike = {
  status?: string;
  health_status?: string;
};

const availableStatuses = new Set(["online", "assigned", "healthy", "ok"]);
const healthyStatuses = new Set(["", "healthy", "ok", "online", "assigned"]);

/**
 * Assignment is ownership of a stream, not an incident. Heartbeat/health is
 * the signal that decides whether an operational service needs attention.
 */
export function isServiceAvailable(service: ServiceHealthLike) {
  const status = String(service.status || "").toLowerCase();
  const health = String(service.health_status || "").toLowerCase();
  return availableStatuses.has(status) && healthyStatuses.has(health);
}
