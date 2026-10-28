import { apiGet } from "@/lib/api/client";
import type { PermissionSnapshot } from "@/lib/foundation/permissions/evaluator";
import type { CurrentUser } from "@/types/domain";

export async function readWorkerPermissions(signal: AbortSignal): Promise<PermissionSnapshot> {
  const user = await apiGet<CurrentUser>("/auth/me", signal);
  return Array.isArray(user.permissions) && user.permissions.every((permission) => typeof permission === "string")
    ? Object.freeze({ kind: "ready", permissions: Object.freeze([...user.permissions]) })
    : Object.freeze({ kind: "unavailable" });
}

export async function readWorkerRestartAuthority(signal: AbortSignal) {
  const [permissions, workers] = await Promise.all([
    readWorkerPermissions(signal),
    apiGet<unknown>("/workers", signal),
  ]);
  return Object.freeze({ permissions, workers });
}
