import { updaterAuthorityFingerprint, type UpdaterActionAuthority } from "@/features/application/updater-action-policy";
import type { SystemUpdateAgentStatus, UpdaterHostBootstrapJobsResponse, UpdaterSettingsHost, UpdaterSettingsTarget } from "@/types/domain";


export function bootstrapActionAuthoritySnapshot({
  actionID,
  updater,
  expectedRevision,
  expectedAppliedRevision,
  selectedHostIDs,
  savedHosts,
  currentHosts,
  savedTargets,
  currentTargets,
  releaseTokenConfigured,
  bootstrapJobs,
  selectionMode,
  confirmedContext,
  confirmationContext,
  credentialsPresent,
  applicable,
}: Readonly<{
  actionID: "UPD-09" | "UPD-10";
  updater: SystemUpdateAgentStatus;
  expectedRevision: number;
  expectedAppliedRevision: number;
  selectedHostIDs: string[];
  savedHosts: UpdaterSettingsHost[];
  currentHosts: UpdaterSettingsHost[];
  savedTargets: UpdaterSettingsTarget[];
  currentTargets: UpdaterSettingsTarget[];
  releaseTokenConfigured: boolean;
  bootstrapJobs: UpdaterHostBootstrapJobsResponse["jobs"];
  selectionMode: "single" | "bulk" | null;
  confirmedContext: string;
  confirmationContext: string;
  credentialsPresent: boolean;
  applicable: boolean;
}>) {
  const selected = new Set(selectedHostIDs);
  const hostParts = (hosts: UpdaterSettingsHost[]) => hosts
    .filter((host) => selected.has(host.host_id))
    .sort((left, right) => left.host_id.localeCompare(right.host_id))
    .flatMap((host) => [
      host.host_id,
      host.address,
      host.port,
      host.user,
      host.arch,
      host.host_key_fingerprint,
      host.host_public_key_fingerprint,
      host.ssh_client_key_fingerprint,
    ]);
  const targetParts = (targets: UpdaterSettingsTarget[]) => targets
    .filter((target) => selected.has(target.host_id))
    .sort((left, right) => left.target_id.localeCompare(right.target_id))
    .flatMap((target) => [
      target.target_id,
      target.service_id,
      target.host_id,
      target.service_type,
      target.deployment_mode,
      target.database_name,
      target.local_listen_port,
    ]);
  const jobParts = bootstrapJobs
    .filter((job) => job.host_ids.some((hostID) => selected.has(hostID)))
    .sort((left, right) => left.id.localeCompare(right.id))
    .flatMap((job) => [job.id, job.idempotency_key, job.expected_revision, job.status, ...job.host_ids]);
  return {
    applicable,
    fingerprint: updaterAuthorityFingerprint([
      actionID,
      updater.updater_id,
      updater.online,
      updater.transport_mode,
      updater.execution_host_id,
      updater.ownership_epoch,
      updater.desired_revision,
      updater.applied_revision,
      updater.policy_status,
      updater.bootstrap_encryption_key_fingerprint,
      expectedRevision,
      expectedAppliedRevision,
      selectionMode,
      releaseTokenConfigured,
      credentialsPresent,
      confirmedContext,
      confirmationContext,
      ...selectedHostIDs,
      ...hostParts(savedHosts),
      ...hostParts(currentHosts),
      ...targetParts(savedTargets),
      ...targetParts(currentTargets),
      ...jobParts,
    ]),
  };
}

export function unavailableBootstrapAuthority(authorityFingerprint: string): UpdaterActionAuthority {
  return Object.freeze({
    permission: "unknown",
    freshness: "unavailable",
    applicability: "unknown",
    authorityFingerprint,
  });
}
