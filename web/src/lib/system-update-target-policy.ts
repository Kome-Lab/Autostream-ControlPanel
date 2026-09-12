import type { SystemUpdateOperation, SystemUpdateStrategy, SystemUpdateTarget } from "@/types/domain";
import { normalize } from "./system-update-values";

const activeStatuses = new Set([
  "accepted",
  "pending",
  "queued",
  "claimed",
  "reconciling",
  "waiting",
  "waiting_for_idle",
  "downloading",
  "verifying",
  "preparing",
  "staging",
  "staged",
  "stopping",
  "installing",
  "applying",
  "starting",
  "restarting",
  "health_checking",
  "rolling_back",
  "running",
]);

const cancellableStatuses = new Set(["queued"]);

export function isControlPanelUpdateTarget(target: Pick<SystemUpdateTarget, "target_id" | "target_type">) {
  return target.target_type === "control_panel" || target.target_id === "control-panel";
}

export function isSystemUpdateJobActive(status?: string) {
  return activeStatuses.has(normalize(status));
}

export function isSystemUpdateJobCancellable(status?: string) {
  return cancellableStatuses.has(normalize(status));
}

export function systemUpdateMayDisconnectPanel(status?: string) {
  return new Set(["stopping", "installing", "applying", "starting", "restarting", "health_checking", "rolling_back", "reconciling"]).has(normalize(status));
}

export function systemUpdateStrategyForTarget(target: Pick<SystemUpdateTarget, "busy" | "current_stream_id">): SystemUpdateStrategy {
  const busy = typeof target.busy === "boolean" ? target.busy : Boolean(target.current_stream_id);
  return busy ? "when_idle" : "maintenance";
}

export type SystemUpdateRequestState = "idle" | "pending" | "ambiguous";

export type SystemUpdateTargetOperationEligibility = {
  ready: boolean;
  reason: string;
};

export function systemUpdateTargetOperationEligibility(
  target: SystemUpdateTarget | undefined,
  operation: SystemUpdateOperation,
): SystemUpdateTargetOperationEligibility {
  if (!target || !Array.isArray(target.eligible_operations)) {
    return { ready: false, reason: "operation_eligibility_unavailable" };
  }
  if (target.eligible_operations.includes(operation)) {
    return { ready: true, reason: "" };
  }
  return {
    ready: false,
    reason: target.operation_blocked_reasons?.[operation] || `system_update_${operation}_not_ready`,
  };
}

export function systemUpdateSoftwareOperationEligibility(
  target: SystemUpdateTarget | undefined,
): SystemUpdateTargetOperationEligibility {
  if (!target) return { ready: false, reason: "operation_eligibility_unavailable" };
  if (Array.isArray(target.eligible_operations)) {
    return systemUpdateTargetOperationEligibility(target, "software_update");
  }
  return target.eligible
    ? { ready: true, reason: "" }
    : { ready: false, reason: target.blocked_reason || "system_update_software_update_not_ready" };
}

export function acquireSystemUpdateTargetRequestLock(activeTargetIDs: Set<string>, targetID: string) {
  const normalizedTargetID = String(targetID || "").trim();
  if (!normalizedTargetID || activeTargetIDs.has(normalizedTargetID)) return false;
  activeTargetIDs.add(normalizedTargetID);
  return true;
}
