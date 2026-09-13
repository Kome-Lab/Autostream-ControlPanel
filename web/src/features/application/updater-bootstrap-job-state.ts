import type { SystemUpdateAgentStatus, UpdaterHostBootstrapHostResult, UpdaterHostBootstrapJobsResponse, UpdaterHostBootstrapRequest, UpdaterSettingsHost, UpdaterSettingsTarget } from "@/types/domain";


export function latestBootstrapResults(
  jobs: UpdaterHostBootstrapJobsResponse["jobs"],
  expectedRevision: number,
) {
  const latest = new Map<string, UpdaterHostBootstrapHostResult>();
  const ordered = jobs.filter((job) => job.expected_revision === expectedRevision).sort((left, right) => {
    const leftTime = Date.parse(left.updated_at || left.created_at || "") || 0;
    const rightTime = Date.parse(right.updated_at || right.created_at || "") || 0;
    return rightTime - leftTime;
  });
  for (const job of ordered) {
    const results = job.hosts.length > 0
      ? job.hosts
      : job.host_ids.map((hostID) => ({ host_id: hostID, status: job.status }));
    for (const result of results) {
      if (!latest.has(result.host_id)) latest.set(result.host_id, result);
    }
  }
  return latest;
}

export function mergeBootstrapJobs(
  incoming: UpdaterHostBootstrapJobsResponse["jobs"],
  current: UpdaterHostBootstrapJobsResponse["jobs"],
) {
  const jobs = new Map(current.map((job) => [job.id, job]));
  for (const job of incoming) jobs.set(job.id, job);
  return Array.from(jobs.values());
}

export function clearBootstrapRequestEnvelope(request: UpdaterHostBootstrapRequest) {
  request.envelope.ephemeral_public_key = "";
  request.envelope.nonce = "";
  request.envelope.ciphertext = "";
}

export function bootstrapOperationContext({
  confirmationContext,
  confirmedContext,
  updater,
  expectedRevision,
  expectedAppliedRevision,
  savedHosts,
  currentHosts,
  savedTargets,
  currentTargets,
  selectedHostIDs,
  selectionMode,
  releaseTokenConfigured,
  canEdit,
}: {
  confirmationContext: string;
  confirmedContext: string;
  updater: SystemUpdateAgentStatus;
  expectedRevision: number;
  expectedAppliedRevision: number;
  savedHosts: UpdaterSettingsHost[];
  currentHosts: UpdaterSettingsHost[];
  savedTargets: UpdaterSettingsTarget[];
  currentTargets: UpdaterSettingsTarget[];
  selectedHostIDs: string[];
  selectionMode: "single" | "bulk" | null;
  releaseTokenConfigured: boolean;
  canEdit: boolean;
}) {
  const selectedOperationHostIDs = new Set(selectedHostIDs);
  return JSON.stringify({
    confirmation_context: confirmationContext,
    confirmed_context: confirmedContext,
    updater_state: {
      updater_id: updater.updater_id,
      online: updater.online,
      desired_revision: updater.desired_revision,
      applied_revision: updater.applied_revision,
      policy_status: updater.policy_status,
    },
    expected_revision: expectedRevision,
    expected_applied_revision: expectedAppliedRevision,
    saved_hosts: savedHosts.filter((host) => selectedOperationHostIDs.has(host.host_id)),
    current_hosts: currentHosts.filter((host) => selectedOperationHostIDs.has(host.host_id)),
    saved_targets: savedTargets.filter((target) => selectedOperationHostIDs.has(target.host_id)),
    current_targets: currentTargets.filter((target) => selectedOperationHostIDs.has(target.host_id)),
    selected_host_ids: selectedHostIDs,
    selection_mode: selectionMode,
    release_token_configured: releaseTokenConfigured,
    can_edit: canEdit,
  });
}
