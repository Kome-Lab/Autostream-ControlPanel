"use client";

import { updaterAuthorityFingerprint, type UpdaterActionAuthority } from "@/features/application/updater-action-policy";
import { isControlPanelUpdateTarget, isSystemUpdateJobCancellable, systemUpdateStrategyForTarget } from "@/lib/system-update-target-policy";
import { systemUpdateConnectivity, systemUpdateUpdaterPolicyState } from "@/lib/system-update-presentation";
import { systemUpdatePortReconfigureEligibility } from "@/lib/system-update-port-requests";
import type { SystemUpdateRequestState } from "@/lib/system-update-target-policy";
import type { SystemUpdateAgentStatus, SystemUpdateJob, SystemUpdateTarget, SystemUpdatesResponse, WorkerNode } from "@/types/domain";
import { nodeIdentity } from "./registered-services-model";
import { latestJobsByTarget, compareUpdateJobs, updateCanStart } from "./system-update-selection";
import { type PortReconfigureProposal } from "./application-operation-types";

export function portControlKey(node: WorkerNode, target?: SystemUpdateTarget) {
  const mapping = target?.port_mapping;
  return [
    nodeIdentity(node),
    node.endpoint_revision || 0,
    node.applied_endpoint?.port || 0,
    mapping?.state || "",
    mapping?.advertised_port || 0,
    mapping?.published_port || 0,
    mapping?.container_port || 0,
    mapping?.config_revision || 0,
  ].join(":");
}

export function softwareUpdateAuthoritySnapshot(actionID: "UPD-01" | "UPD-02", target: SystemUpdateTarget, response?: SystemUpdatesResponse) {
  const jobs = response?.jobs || [];
  const updaters = response?.updaters || [];
  const hosts = response?.hosts || [];
  const latestJob = latestJobsByTarget([...jobs].sort(compareUpdateJobs)).get(target.target_id);
  const connectivity = systemUpdateConnectivity(target, updaters, hosts);
  const updater = connectivity.updater;
  const host = connectivity.host;
  const policy = updater ? systemUpdateUpdaterPolicyState(updater) : undefined;
  return {
    applicable: Boolean(response) && updateCanStart(target, latestJob, updaters, hosts),
    fingerprint: updaterAuthorityFingerprint([
      actionID,
      target.target_id,
      target.target_type,
      target.host_id,
      target.updater_id,
      target.current_version,
      target.latest_version,
      target.update_available,
      target.eligible,
      target.busy,
      target.current_stream_id,
      target.blocked_reason,
      target.deployment_mode,
      systemUpdateStrategyForTarget(target),
      latestJob?.id,
      latestJob?.status,
      latestJob?.updated_at,
      latestJob?.recovery_required,
      updater?.updater_id,
      updater?.online,
      updater?.last_heartbeat_at,
      updater?.desired_revision,
      updater?.applied_revision,
      updater?.policy_status,
      updater?.transport_mode,
      updater?.execution_host_id,
      updater?.ownership_epoch,
      policy?.label,
      host?.host_id,
      host?.reachability,
      host?.reachability_checked_at,
    ]),
  };
}

export function batchUpdateAuthoritySnapshot(targets: SystemUpdateTarget[], response?: SystemUpdatesResponse) {
  const fingerprints = targets.map((target) => softwareUpdateAuthoritySnapshot(
    isControlPanelUpdateTarget(target) ? "UPD-02" : "UPD-01",
    target,
    response,
  ).fingerprint);
  return {
    applicable: Boolean(response) && targets.length > 0,
    fingerprint: updaterAuthorityFingerprint(["UPD-03", "fleet", targets.length, ...fingerprints]),
  };
}

export function cancelUpdateAuthoritySnapshot(jobID: string, job?: SystemUpdateJob) {
  return {
    applicable: Boolean(job && isSystemUpdateJobCancellable(job.status)),
    fingerprint: updaterAuthorityFingerprint([
      "UPD-04",
      jobID,
      job?.target_id,
      job?.updater_id,
      job?.status,
      job?.updated_at,
      job?.sequence,
      job?.report_sequence,
      job?.lease_generation,
      job?.recovery_required,
    ]),
  };
}

export function portReconfigureAuthoritySnapshot({
  target,
  updater,
  node,
  latestJob,
  requestState,
  proposal,
}: Readonly<{
  target?: SystemUpdateTarget;
  updater?: SystemUpdateAgentStatus;
  node?: WorkerNode;
  latestJob?: SystemUpdateJob;
  requestState: SystemUpdateRequestState;
  proposal: PortReconfigureProposal;
}>) {
  const eligibility = systemUpdatePortReconfigureEligibility({ target, updater, node, latestJob, requestState });
  const localSame = proposal.mode === "docker"
    ? proposal.newPublishedPort === eligibility.dockerMapping?.published_port && proposal.newContainerPort === eligibility.dockerMapping?.container_port
    : proposal.newPort === eligibility.currentLocalListenPort;
  const advertisedValid = proposal.portMode === "local_only" || (validPortNumber(Number(proposal.newAdvertisedPort), 1)
    && (!localSame || proposal.newAdvertisedPort === eligibility.currentPort));
  const proposalApplicable = advertisedValid && (proposal.mode === "docker"
    ? validPortNumber(proposal.newPublishedPort, 1024)
      && validPortNumber(proposal.newContainerPort, 1024)
      && Boolean(eligibility.dockerMapping)
    : validPortNumber(proposal.newPort, 1024));
  return {
    applicable: eligibility.ready && proposalApplicable,
    fingerprint: updaterAuthorityFingerprint([
      "UPD-05",
      target?.port_policy_snapshot_id, target?.local_listen_port, target?.applied_config_revision, target?.ownership_epoch,
      proposal.portMode, proposal.newAdvertisedPort,
      target?.target_id,
      target?.host_id,
      target?.updater_id,
      target?.deployment_mode,
      target?.eligible_operations?.join(","),
      target?.operation_blocked_reasons?.port_reconfigure,
      target?.port_mapping?.state,
      target?.port_mapping?.advertised_port,
      target?.port_mapping?.published_host_ip,
      target?.port_mapping?.published_port,
      target?.port_mapping?.container_port,
      target?.port_mapping?.config_revision,
      updater?.updater_id,
      updater?.online,
      updater?.last_heartbeat_at,
      updater?.desired_revision,
      updater?.applied_revision,
      updater?.policy_status,
      updater?.transport_mode,
      updater?.execution_host_id,
      updater?.ownership_epoch,
      node ? nodeIdentity(node) : undefined,
      node?.endpoint_revision,
      node?.applied_endpoint?.public_url,
      node?.applied_endpoint?.port,
      latestJob?.id,
      latestJob?.status,
      latestJob?.updated_at,
      latestJob?.recovery_required,
      requestState,
      proposal.mode,
      proposal.mode === "docker" ? proposal.newAdvertisedPort : proposal.newPort,
      proposal.mode === "docker" ? proposal.newPublishedPort : undefined,
      proposal.mode === "docker" ? proposal.newContainerPort : undefined,
    ]),
  };
}

function validPortNumber(value: number, minimum: number) {
  return Number.isSafeInteger(value) && value >= minimum && value <= 65_535;
}

export function freshUpdaterAuthority(allowed: boolean, applicable: boolean, authorityFingerprint: string): UpdaterActionAuthority {
  return Object.freeze({
    permission: allowed ? "allowed" : "denied",
    freshness: "fresh",
    applicability: applicable ? "applicable" : "not-applicable",
    authorityFingerprint,
  });
}

export function unavailableUpdaterAuthority(authorityFingerprint: string): UpdaterActionAuthority {
  return Object.freeze({
    permission: "unknown",
    freshness: "unavailable",
    applicability: "unknown",
    authorityFingerprint,
  });
}
