import { useCurrentUser, useNodes, useServiceHealth, useSystemUpdates } from "@/features/queries";
import { updaterAuthorityFingerprint, type UpdaterActionAuthority } from "@/features/application/updater-action-policy";
import { hasPermission } from "@/lib/auth/permissions";
import { APIError, apiGet } from "@/lib/api/client";
import { withBoundedActionRevalidation } from "@/lib/foundation/actions/bounded-revalidation";
import { normalizeSystemUpdatesResponse } from "@/lib/system-updates";
import { emptyUpdaterSettings, normalizeUpdaterSettingsResponse } from "@/lib/updater-settings-model";
import type { SystemUpdateRequestState } from "@/lib/system-update-target-policy";
import type { CurrentUser, SystemUpdatePortReconfigureCreateRequest, WorkerNode } from "@/types/domain";
import { type PortReconfigureAuthorityContext } from "./application-operation-types";
import { mergeRegisteredNodeRows, nodeIdentity } from "./registered-services-model";
import { compareUpdateJobs, latestJobsByTarget, availableSystemUpdateTargets } from "./system-update-selection";
import { unavailableUpdaterAuthority, softwareUpdateAuthoritySnapshot, freshUpdaterAuthority, batchUpdateAuthoritySnapshot, cancelUpdateAuthoritySnapshot, portReconfigureAuthoritySnapshot } from "./port-reconfigure-authority";



export async function readUpdaterSettingsAuthority(updaterID: string, signal: AbortSignal) {
  try {
    return normalizeUpdaterSettingsResponse(await apiGet<unknown>(`/system-updates/updaters/${encodeURIComponent(updaterID)}/settings`, signal), updaterID);
  } catch (error) {
    if (error instanceof APIError && error.status === 404 && error.code === "updater_policy_not_configured") return emptyUpdaterSettings(updaterID);
    throw error;
  }
}

export function readApplicationAuthority(signal?: AbortSignal) {
  return withBoundedActionRevalidation(async (readSignal) => {
    const [updates, user] = await Promise.all([
      apiGet<unknown>("/system-updates", readSignal),
      apiGet<CurrentUser>("/auth/me", readSignal),
    ]);
    return { updates: normalizeSystemUpdatesResponse(updates), user };
  }, signal);
}

export function createApplicationAuthorityReaders({ readFresh = readApplicationAuthority }: {
  systemUpdates: ReturnType<typeof useSystemUpdates>;
  currentUser: ReturnType<typeof useCurrentUser>;
  readFresh?: typeof readApplicationAuthority;
}) {
  const refreshTargetAuthority = async (actionID: "UPD-01" | "UPD-02", targetID: string, signal?: AbortSignal): Promise<UpdaterActionAuthority> => {
    let fresh: Awaited<ReturnType<typeof readApplicationAuthority>>;
    try { fresh = await readFresh(signal); } catch {
      return unavailableUpdaterAuthority(updaterAuthorityFingerprint([actionID, targetID, "unavailable"]));
    }
    const target = fresh.updates.targets.find((candidate) => candidate.target_id === targetID);
    const snapshot = target
      ? softwareUpdateAuthoritySnapshot(actionID, target, fresh.updates)
      : { applicable: false, fingerprint: updaterAuthorityFingerprint([actionID, targetID, "missing"]) };
    return freshUpdaterAuthority(hasPermission(fresh.user, "system_updates.execute"), snapshot.applicable, snapshot.fingerprint);
  };
  const refreshBatchAuthority = async (signal?: AbortSignal): Promise<UpdaterActionAuthority> => {
    let fresh: Awaited<ReturnType<typeof readApplicationAuthority>>;
    try { fresh = await readFresh(signal); } catch {
      return unavailableUpdaterAuthority(updaterAuthorityFingerprint(["UPD-03", "fleet", "unavailable"]));
    }
    const refreshedTargets = availableSystemUpdateTargets(fresh.updates);
    const snapshot = batchUpdateAuthoritySnapshot(refreshedTargets, fresh.updates);
    return freshUpdaterAuthority(hasPermission(fresh.user, "system_updates.execute"), snapshot.applicable, snapshot.fingerprint);
  };
  const refreshCancelAuthority = async (jobID: string, signal?: AbortSignal): Promise<UpdaterActionAuthority> => {
    let fresh: Awaited<ReturnType<typeof readApplicationAuthority>>;
    try { fresh = await readFresh(signal); } catch {
      return unavailableUpdaterAuthority(updaterAuthorityFingerprint(["UPD-04", jobID, "unavailable"]));
    }
    const job = fresh.updates.jobs.find((candidate) => candidate.id === jobID);
    const snapshot = cancelUpdateAuthoritySnapshot(jobID, job);
    return freshUpdaterAuthority(hasPermission(fresh.user, "system_updates.execute"), snapshot.applicable, snapshot.fingerprint);
  };

  return { refreshTargetAuthority, refreshBatchAuthority, refreshCancelAuthority };
}


export async function refreshApplicationPortAuthority(
  context: PortReconfigureAuthorityContext,
  { canReadRegisteredNodes, canReadServiceHealth, hasActivePortRequestTarget, unresolvedAmbiguousPortRequest }: {
    systemUpdates: ReturnType<typeof useSystemUpdates>;
    currentUser: ReturnType<typeof useCurrentUser>;
    registeredNodes: ReturnType<typeof useNodes>;
    serviceHealth: ReturnType<typeof useServiceHealth>;
    canReadRegisteredNodes: boolean;
    canReadServiceHealth: boolean;
    hasActivePortRequestTarget: (targetID: string) => boolean;
    unresolvedAmbiguousPortRequest: SystemUpdatePortReconfigureCreateRequest | null;
  },
  signal?: AbortSignal,
): Promise<UpdaterActionAuthority> {
    let fresh: { updates: Awaited<ReturnType<typeof readApplicationAuthority>>["updates"]; user: CurrentUser; nodes: WorkerNode[]; health: WorkerNode[] };
    try {
      fresh = await withBoundedActionRevalidation(async (signal) => {
        const [updates, user, nodes, health] = await Promise.all([
          apiGet<unknown>("/system-updates", signal),
          apiGet<CurrentUser>("/auth/me", signal),
          canReadRegisteredNodes ? apiGet<WorkerNode[]>("/nodes", signal) : Promise.resolve([]),
          canReadServiceHealth ? apiGet<WorkerNode[]>("/service-health", signal) : Promise.resolve([]),
        ]);
        return { updates: normalizeSystemUpdatesResponse(updates), user, nodes, health };
      }, signal);
    } catch {
      return unavailableUpdaterAuthority(updaterAuthorityFingerprint(["UPD-05", context.targetID, "unavailable"]));
    }
    const refreshedNodeRows = mergeRegisteredNodeRows(
      fresh.nodes,
      fresh.health,
    );
    const target = fresh.updates.targets.find((candidate) => candidate.target_id === context.targetID);
    const updater = target?.updater_id
      ? fresh.updates.updaters.find((candidate) => candidate.updater_id === target.updater_id)
      : undefined;
    const node = refreshedNodeRows.find((candidate) => nodeIdentity(candidate) === context.targetID);
    const latestJob = latestJobsByTarget([...fresh.updates.jobs].sort(compareUpdateJobs)).get(context.targetID);
    const requestState: SystemUpdateRequestState = hasActivePortRequestTarget(context.targetID)
      ? "pending"
      : unresolvedAmbiguousPortRequest?.target_id === context.targetID
        ? "ambiguous"
        : "idle";
    const snapshot = portReconfigureAuthoritySnapshot({
      target,
      updater,
      node,
      latestJob,
      requestState,
      proposal: context.proposal,
    });
    return freshUpdaterAuthority(hasPermission(fresh.user, "system_updates.execute"), snapshot.applicable, snapshot.fingerprint);

}
