import { useCurrentUser, useNodes, useServiceHealth, useSystemUpdates } from "@/features/queries";
import { updaterAuthorityFingerprint, type UpdaterActionAuthority } from "@/features/application/updater-action-policy";
import { hasPermission } from "@/lib/auth/permissions";
import type { SystemUpdateRequestState } from "@/lib/system-update-target-policy";
import type { SystemUpdatePortReconfigureCreateRequest } from "@/types/domain";
import { type PortReconfigureAuthorityContext } from "./application-operation-types";
import { mergeRegisteredNodeRows, nodeIdentity } from "./registered-services-model";
import { compareUpdateJobs, latestJobsByTarget, availableSystemUpdateTargets } from "./system-update-selection";
import { unavailableUpdaterAuthority, softwareUpdateAuthoritySnapshot, freshUpdaterAuthority, batchUpdateAuthoritySnapshot, cancelUpdateAuthoritySnapshot, portReconfigureAuthoritySnapshot } from "./port-reconfigure-authority";



export function createApplicationAuthorityReaders({
  systemUpdates,
  currentUser,
}: {
  systemUpdates: ReturnType<typeof useSystemUpdates>;
  currentUser: ReturnType<typeof useCurrentUser>;
}) {
  const refreshTargetAuthority = async (actionID: "UPD-01" | "UPD-02", targetID: string): Promise<UpdaterActionAuthority> => {
    const [refreshedUpdates, refreshedUser] = await Promise.all([systemUpdates.refetch(), currentUser.refetch()]);
    if (refreshedUpdates.isError || !refreshedUpdates.data || refreshedUser.isError || !refreshedUser.data) {
      return unavailableUpdaterAuthority(updaterAuthorityFingerprint([actionID, targetID, "unavailable"]));
    }
    const target = refreshedUpdates.data.targets.find((candidate) => candidate.target_id === targetID);
    const snapshot = target
      ? softwareUpdateAuthoritySnapshot(actionID, target, refreshedUpdates.data)
      : { applicable: false, fingerprint: updaterAuthorityFingerprint([actionID, targetID, "missing"]) };
    return freshUpdaterAuthority(hasPermission(refreshedUser.data, "system_updates.execute"), snapshot.applicable, snapshot.fingerprint);
  };
  const refreshBatchAuthority = async (): Promise<UpdaterActionAuthority> => {
    const [refreshedUpdates, refreshedUser] = await Promise.all([systemUpdates.refetch(), currentUser.refetch()]);
    if (refreshedUpdates.isError || !refreshedUpdates.data || refreshedUser.isError || !refreshedUser.data) {
      return unavailableUpdaterAuthority(updaterAuthorityFingerprint(["UPD-03", "fleet", "unavailable"]));
    }
    const refreshedTargets = availableSystemUpdateTargets(refreshedUpdates.data);
    const snapshot = batchUpdateAuthoritySnapshot(refreshedTargets, refreshedUpdates.data);
    return freshUpdaterAuthority(hasPermission(refreshedUser.data, "system_updates.execute"), snapshot.applicable, snapshot.fingerprint);
  };
  const refreshCancelAuthority = async (jobID: string): Promise<UpdaterActionAuthority> => {
    const [refreshedUpdates, refreshedUser] = await Promise.all([systemUpdates.refetch(), currentUser.refetch()]);
    if (refreshedUpdates.isError || !refreshedUpdates.data || refreshedUser.isError || !refreshedUser.data) {
      return unavailableUpdaterAuthority(updaterAuthorityFingerprint(["UPD-04", jobID, "unavailable"]));
    }
    const job = refreshedUpdates.data.jobs.find((candidate) => candidate.id === jobID);
    const snapshot = cancelUpdateAuthoritySnapshot(jobID, job);
    return freshUpdaterAuthority(hasPermission(refreshedUser.data, "system_updates.execute"), snapshot.applicable, snapshot.fingerprint);
  };

  return { refreshTargetAuthority, refreshBatchAuthority, refreshCancelAuthority };
}


export async function refreshApplicationPortAuthority(
  context: PortReconfigureAuthorityContext,
  { systemUpdates, currentUser, registeredNodes, serviceHealth, canReadRegisteredNodes, canReadServiceHealth, hasActivePortRequestTarget, unresolvedAmbiguousPortRequest }: {
    systemUpdates: ReturnType<typeof useSystemUpdates>;
    currentUser: ReturnType<typeof useCurrentUser>;
    registeredNodes: ReturnType<typeof useNodes>;
    serviceHealth: ReturnType<typeof useServiceHealth>;
    canReadRegisteredNodes: boolean;
    canReadServiceHealth: boolean;
    hasActivePortRequestTarget: (targetID: string) => boolean;
    unresolvedAmbiguousPortRequest: SystemUpdatePortReconfigureCreateRequest | null;
  },
): Promise<UpdaterActionAuthority> {
    const [refreshedUpdates, refreshedUser, refreshedRegisteredNodes, refreshedServiceHealth] = await Promise.all([
      systemUpdates.refetch(),
      currentUser.refetch(),
      canReadRegisteredNodes ? registeredNodes.refetch() : Promise.resolve(undefined),
      canReadServiceHealth ? serviceHealth.refetch() : Promise.resolve(undefined),
    ]);
    if (
      refreshedUpdates.isError
      || !refreshedUpdates.data
      || refreshedUser.isError
      || !refreshedUser.data
      || refreshedRegisteredNodes?.isError
      || refreshedServiceHealth?.isError
    ) {
      return unavailableUpdaterAuthority(updaterAuthorityFingerprint(["UPD-05", context.targetID, "unavailable"]));
    }
    const refreshedNodeRows = mergeRegisteredNodeRows(
      refreshedRegisteredNodes?.data || [],
      refreshedServiceHealth?.data || [],
    );
    const target = refreshedUpdates.data.targets.find((candidate) => candidate.target_id === context.targetID);
    const updater = target?.updater_id
      ? refreshedUpdates.data.updaters.find((candidate) => candidate.updater_id === target.updater_id)
      : undefined;
    const node = refreshedNodeRows.find((candidate) => nodeIdentity(candidate) === context.targetID);
    const latestJob = latestJobsByTarget([...refreshedUpdates.data.jobs].sort(compareUpdateJobs)).get(context.targetID);
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
    return freshUpdaterAuthority(hasPermission(refreshedUser.data, "system_updates.execute"), snapshot.applicable, snapshot.fingerprint);

}
