import { useQueryClient } from "@tanstack/react-query";
import { useUpdaterHostBootstrapJobs } from "@/features/queries";
import { apiGet } from "@/lib/api/client";
import { activeUpdaterHostBootstrapStatus, isUpdaterHostBootstrapBulkCandidate, updaterHostBootstrapConfirmationContext, updaterHostBootstrapEligibility } from "@/lib/updater-bootstrap";
import { normalizeSystemUpdatesResponse } from "@/lib/system-updates";
import { normalizeUpdaterSettingsResponse } from "@/lib/updater-settings-model";
import type { SystemUpdateAgentStatus, UpdaterSettingsHost } from "@/types/domain";
import { latestBootstrapResults } from "./updater-bootstrap-job-state";


export async function refreshBootstrapSubmissionSelection({
  selection: { updaterID, expectedRevision, expectedAppliedRevision, selectedHostIDs, currentHosts, selectionMode, confirmedContext },
  bootstrapJobs,
  queryClient,
  operationStillCurrent,
}: {
  selection: { updaterID: string; expectedRevision: number; expectedAppliedRevision: number; selectedHostIDs: string[]; currentHosts: UpdaterSettingsHost[]; selectionMode: "single" | "bulk" | null; confirmedContext: string };
  bootstrapJobs: Pick<ReturnType<typeof useUpdaterHostBootstrapJobs>, "refetch">;
  queryClient: ReturnType<typeof useQueryClient>;
  operationStillCurrent: () => boolean
}): Promise<SystemUpdateAgentStatus | undefined> {

      const [refreshedSystemUpdatesRaw, refreshedBootstrapJobs, refreshedSettingsRaw] = await Promise.all([
        apiGet<unknown>("/system-updates"),
        bootstrapJobs.refetch(),
        apiGet<unknown>(`/system-updates/updaters/${encodeURIComponent(updaterID)}/settings`),
      ]);
      if (!operationStillCurrent()) return;
      const refreshedSystemUpdates = normalizeSystemUpdatesResponse(refreshedSystemUpdatesRaw);
      const refreshedSettings = normalizeUpdaterSettingsResponse(refreshedSettingsRaw, updaterID);
      queryClient.setQueryData(["system-updates"], refreshedSystemUpdates);
      queryClient.setQueryData(["system-updates", "updaters", updaterID, "settings"], refreshedSettings);
      const refreshedUpdater = refreshedSystemUpdates.updaters.find((candidate) => candidate.updater_id === updaterID);
      if (
        !refreshedUpdater
        || refreshedSettings.revision !== expectedRevision
        || (refreshedSettings.projection_revision ?? refreshedSettings.revision) !== expectedAppliedRevision
        || refreshedBootstrapJobs.isError
        || !refreshedBootstrapJobs.data
      ) {
        throw new Error("updater_host_bootstrap_status_unavailable");
      }
      const refreshedSavedHostsByID = new Map(refreshedSettings.hosts.map((host) => [host.host_id, host]));
      const refreshedSelectedHosts = selectedHostIDs
        .map((hostID) => refreshedSavedHostsByID.get(hostID))
        .filter((host): host is UpdaterSettingsHost => Boolean(host));
      if (
        confirmedContext !== updaterHostBootstrapConfirmationContext(
          refreshedUpdater,
          expectedRevision,
          selectedHostIDs,
          refreshedSelectedHosts,
        )
      ) {
        throw new Error("bootstrap_host_keys_unconfirmed");
      }
      const refreshedLatestResults = latestBootstrapResults(refreshedBootstrapJobs.data.jobs, expectedRevision);
      const refreshedActiveStatus = activeUpdaterHostBootstrapStatus(refreshedBootstrapJobs.data.jobs);
      const refreshedSelectionReady = selectedHostIDs.every((hostID) => {
        const currentHost = currentHosts.find((host) => host.host_id === hostID);
        const eligibility = updaterHostBootstrapEligibility({
          updater: refreshedUpdater,
          expectedAppliedRevision,
          savedHost: refreshedSavedHostsByID.get(hostID),
          currentHost,
          releaseTokenConfigured: refreshedSettings.github_token_configured,
          bootstrapStatus: refreshedActiveStatus || refreshedLatestResults.get(hostID)?.status,
        });
        return selectionMode === "bulk"
          ? isUpdaterHostBootstrapBulkCandidate(eligibility)
          : eligibility.ready;
      });
      if (!refreshedSelectionReady) throw new Error("updater_host_bootstrap_not_ready");
  return refreshedUpdater;
}
