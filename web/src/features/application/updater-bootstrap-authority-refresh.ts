import { useQueryClient } from "@tanstack/react-query";
import { updaterAuthorityFingerprint, type UpdaterActionAuthority } from "@/features/application/updater-action-policy";
import { useCurrentUser, useUpdaterHostBootstrapJobs } from "@/features/queries";
import { apiGet } from "@/lib/api/client";
import { hasPermission } from "@/lib/auth/permissions";
import { activeUpdaterHostBootstrapStatus, isUpdaterHostBootstrapBulkCandidate, normalizeUpdaterHostBootstrapJobsResponse, updaterHostBootstrapConfirmationContext, updaterHostBootstrapEligibility } from "@/lib/updater-bootstrap";
import { normalizeSystemUpdatesResponse } from "@/lib/system-updates";
import { withBoundedActionRevalidation } from "@/lib/foundation/actions/bounded-revalidation";
import type { CurrentUser, UpdaterSettingsHost } from "@/types/domain";
import { readUpdaterSettingsAuthority } from "./application-authority-readers";
import { latestBootstrapResults } from "./updater-bootstrap-job-state";
import { bootstrapActionAuthoritySnapshot, unavailableBootstrapAuthority } from "./updater-bootstrap-authority";



export function createBootstrapAuthorityRefresher({
  context,
  busy,
}: {
  context: Parameters<typeof bootstrapActionAuthoritySnapshot>[0];
  bootstrapJobs: Pick<ReturnType<typeof useUpdaterHostBootstrapJobs>, "refetch">;
  currentUser: Pick<ReturnType<typeof useCurrentUser>, "refetch">;
  queryClient: ReturnType<typeof useQueryClient>;
  busy: boolean
}) {
  const { actionID: foundationActionID, updater, expectedRevision, expectedAppliedRevision, selectedHostIDs, currentHosts, currentTargets, selectionMode, confirmedContext } = context;
  const refreshFoundationAuthority = async (signal?: AbortSignal): Promise<UpdaterActionAuthority> => withBoundedActionRevalidation(async (readSignal) => {
    const [systemUpdatesRaw, bootstrapJobsRaw, refreshedSettings, refreshedUser] = await Promise.all([
      apiGet<unknown>("/system-updates", readSignal),
      apiGet<unknown>(`/system-updates/updaters/${encodeURIComponent(updater.updater_id)}/bootstrap-jobs`, readSignal),
      readUpdaterSettingsAuthority(updater.updater_id, readSignal),
      apiGet<CurrentUser>("/auth/me", readSignal),
    ]);
    const refreshedBootstrapJobs = normalizeUpdaterHostBootstrapJobsResponse(bootstrapJobsRaw, updater.updater_id);
    const systemUpdates = normalizeSystemUpdatesResponse(systemUpdatesRaw);
    const refreshedUpdater = systemUpdates.updaters.find((candidate) => candidate.updater_id === updater.updater_id);
    if (
      !refreshedUpdater
      || refreshedSettings.revision !== expectedRevision
      || (refreshedSettings.projection_revision ?? refreshedSettings.revision) !== expectedAppliedRevision
    ) {
      return unavailableBootstrapAuthority(updaterAuthorityFingerprint([foundationActionID, updater.updater_id, "authority-changed"]));
    }
    const refreshedSavedHostsByID = new Map(refreshedSettings.hosts.map((host) => [host.host_id, host]));
    const refreshedSelectedHosts = selectedHostIDs
      .map((hostID) => refreshedSavedHostsByID.get(hostID))
      .filter((host): host is UpdaterSettingsHost => Boolean(host));
    const refreshedLatestResults = latestBootstrapResults(refreshedBootstrapJobs.jobs, expectedRevision);
    const refreshedActiveStatus = activeUpdaterHostBootstrapStatus(refreshedBootstrapJobs.jobs);
    const refreshedReady = selectedHostIDs.length > 0 && selectedHostIDs.every((hostID) => {
      const eligibility = updaterHostBootstrapEligibility({
        updater: refreshedUpdater,
        expectedAppliedRevision,
        savedHost: refreshedSavedHostsByID.get(hostID),
        currentHost: currentHosts.find((host) => host.host_id === hostID),
        releaseTokenConfigured: refreshedSettings.github_token_configured,
        bootstrapStatus: refreshedActiveStatus || refreshedLatestResults.get(hostID)?.status,
      });
      return selectionMode === "bulk"
        ? isUpdaterHostBootstrapBulkCandidate(eligibility)
        : eligibility.ready;
    });
    const refreshedConfirmationContext = updaterHostBootstrapConfirmationContext(
      refreshedUpdater,
      expectedRevision,
      selectedHostIDs,
      refreshedSelectedHosts,
    );
    const snapshot = bootstrapActionAuthoritySnapshot({
      actionID: foundationActionID,
      updater: refreshedUpdater,
      expectedRevision,
      expectedAppliedRevision,
      selectedHostIDs,
      savedHosts: refreshedSettings.hosts,
      currentHosts,
      savedTargets: refreshedSettings.targets,
      currentTargets,
      releaseTokenConfigured: refreshedSettings.github_token_configured,
      bootstrapJobs: refreshedBootstrapJobs.jobs,
      selectionMode,
      confirmedContext,
      confirmationContext: refreshedConfirmationContext,
      credentialsPresent: context.credentialsPresent,
      applicable: refreshedReady && confirmedContext === refreshedConfirmationContext && !busy,
    });
    return Object.freeze({
      permission: hasPermission(refreshedUser, "system_updates.execute") ? "allowed" : "denied",
      freshness: "fresh",
      applicability: snapshot.applicable ? "applicable" : "not-applicable",
      authorityFingerprint: snapshot.fingerprint,
    });
  }, signal);
  return refreshFoundationAuthority;
}
