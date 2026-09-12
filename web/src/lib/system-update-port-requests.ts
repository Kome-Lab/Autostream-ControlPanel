import type { SystemUpdateAgentStatus, SystemUpdateDockerPortReconfigureCreateRequest, SystemUpdatePortMapping, SystemUpdatePortReconfigureCreateRequest, SystemUpdatePortReconfigurationResult, SystemUpdatePortMode, SystemUpdateJob, SystemUpdateTarget, WorkerNode } from "@/types/domain";
import { type SystemUpdateRequestState, isControlPanelUpdateTarget, isSystemUpdateJobActive, systemUpdateTargetOperationEligibility } from "./system-update-target-policy";
import { normalize, validPort } from "./system-update-values";
import { systemUpdateUpdaterPolicyState } from "./system-update-presentation";
import { systemUpdateJobFromResponse } from "./system-updates";
import { ambiguousSystemUpdateCreateError, SystemUpdateRequestAmbiguousError } from "./system-update-requests";

export type SystemUpdatePortReconfigureEligibility = {
  ready: boolean;
  reason: string;
  deploymentMode?: "systemd" | "docker";
  currentPort?: number;
  currentLocalListenPort?: number;
  snapshotID?: string;
  appliedConfigRevision?: number;
  fence?: number;
  endpointRevision?: number;
  dockerMapping?: SystemUpdatePortMapping;
};

export function systemUpdatePortReconfigureEligibility({
  target,
  updater,
  node,
  latestJob,
  requestState,
}: {
  target?: SystemUpdateTarget;
  updater?: SystemUpdateAgentStatus;
  node?: WorkerNode;
  latestJob?: SystemUpdateJob;
  requestState: SystemUpdateRequestState;
}): SystemUpdatePortReconfigureEligibility {
  if (!target || !node || !updater) return { ready: false, reason: "port_contract_unavailable" };
  if (isControlPanelUpdateTarget(target) || node.service_type === "control_panel" || node.service_type === "update_agent") {
    return { ready: false, reason: "unsupported_target" };
  }
  const deploymentMode = normalize(target.deployment_mode);
  if (deploymentMode !== "systemd" && deploymentMode !== "docker") {
    return { ready: false, reason: "unsupported_deployment" };
  }
  if (updater.transport_mode !== "pull_v2") return { ready: false, reason: "unsupported_transport" };
  const nodeID = String(node.service_id || node.id || "").trim();
  if (!nodeID || nodeID !== target.target_id) return { ready: false, reason: "port_contract_unavailable" };
  const currentPort = Number(node.applied_endpoint?.port);
  const endpointRevision = Number(node.endpoint_revision);
  if (
    !Number.isSafeInteger(currentPort)
    || currentPort < 1
    || currentPort > 65535
    || !Number.isSafeInteger(endpointRevision)
    || endpointRevision < 1
  ) {
    return { ready: false, reason: "port_contract_unavailable" };
  }
  const base = {
    deploymentMode,
    currentPort,
    endpointRevision,
    currentLocalListenPort: target.local_listen_port,
    snapshotID: target.port_policy_snapshot_id,
    appliedConfigRevision: target.applied_config_revision,
    fence: target.ownership_epoch,
  } as const;
  if (target.port_contract_version !== 2
    || !/^ps1:[a-f0-9]{64}$/.test(target.port_policy_snapshot_id || "")
    || !validPort(Number(target.local_listen_port), 1024)
    || !Number.isSafeInteger(target.applied_config_revision) || Number(target.applied_config_revision) < 1
    || !Number.isSafeInteger(target.applied_endpoint_revision) || Number(target.applied_endpoint_revision) < 1
    || !Number.isSafeInteger(target.ownership_epoch) || Number(target.ownership_epoch) < 1
    || target.endpoint_revision !== endpointRevision
    || !target.port_modes?.includes("local_only") || !target.port_modes.includes("local_and_advertised")) {
    return { ...base, ready: false, reason: "port_contract_unavailable" };
  }
  const dockerMapping = deploymentMode === "docker" ? target.port_mapping : undefined;
  if (deploymentMode === "docker" && !validAppliedDockerPortMapping(dockerMapping, currentPort)) {
    return {
      ...base,
      dockerMapping,
      ready: false,
      reason: dockerMapping?.state === "drifted" ? "docker_mapping_drifted" : "docker_mapping_unavailable",
    };
  }
  const endpointStatus = normalize(node.endpoint_status);
  if (endpointStatus !== "applied") {
    return { ...base, dockerMapping, ready: false, reason: endpointStatus.includes("rollback") ? "endpoint_recovery" : "endpoint_not_applied" };
  }
  if (requestState === "pending") return { ...base, dockerMapping, ready: false, reason: "request_pending" };
  if (requestState === "ambiguous") return { ...base, dockerMapping, ready: false, reason: "request_ambiguous" };
  if (target.busy === true || Boolean(target.current_stream_id)) return { ...base, dockerMapping, ready: false, reason: "target_busy" };
  if (latestJob?.recovery_required) return { ...base, dockerMapping, ready: false, reason: "recovery_required" };
  if (latestJob && isSystemUpdateJobActive(latestJob.status)) return { ...base, dockerMapping, ready: false, reason: "active_job" };
  if (!updater.online || !systemUpdateUpdaterPolicyState(updater).ready) {
    return { ...base, dockerMapping, ready: false, reason: "updater_not_ready" };
  }
  const operationEligibility = systemUpdateTargetOperationEligibility(target, "port_reconfigure");
  if (!operationEligibility.ready) {
    return {
      ready: false,
      reason: operationEligibility.reason,
      ...base,
      dockerMapping,
    };
  }
  return { ...base, dockerMapping, ready: true, reason: "" };
}

function validAppliedDockerPortMapping(
  mapping: SystemUpdatePortMapping | undefined,
  advertisedPort: number,
) {
  return Boolean(
    mapping
    && mapping.mode === "docker"
    && mapping.state === "applied"
    && mapping.published_host_ip === "127.0.0.1"
    && mapping.advertised_port === advertisedPort
    && validPort(Number(mapping.advertised_port), 1)
    && validPort(Number(mapping.published_port), 1024)
    && validPort(Number(mapping.container_port), 1024)
    && mapping.health_port === mapping.published_port
    && Number.isSafeInteger(Number(mapping.config_revision))
    && Number(mapping.config_revision) >= 1,
  );
}

export function systemUpdatePortReconfigureRequest({
  targetID,
  currentLocalListenPort,
  newLocalListenPort,
  currentAdvertisedPort,
  newAdvertisedPort,
  mode,
  expectedSnapshotID,
  appliedConfigRevision,
  fence,
  expectedEndpointRevision,
  idempotencyKey,
}: {
  targetID: string;
  currentLocalListenPort: number;
  newLocalListenPort: number;
  currentAdvertisedPort: number;
  newAdvertisedPort?: number;
  mode: SystemUpdatePortMode;
  expectedSnapshotID: string;
  appliedConfigRevision: number;
  fence: number;
  expectedEndpointRevision: number;
  idempotencyKey: string;
}): SystemUpdatePortReconfigureCreateRequest {
  const normalizedTargetID = String(targetID || "").trim();
  const normalizedKey = String(idempotencyKey || "").trim();
  if (!normalizedTargetID || !normalizedKey) throw new Error("invalid_port_reconfigure_request");
  if (!validPort(newLocalListenPort, 1024)) throw new Error("invalid_service_port");
  if (!validPort(currentLocalListenPort, 1024)) throw new Error("invalid_current_service_port");
  if (!Number.isSafeInteger(expectedEndpointRevision) || expectedEndpointRevision < 1) throw new Error("invalid_endpoint_revision");
  const advertised = systemUpdatePortAdvertisedInput(mode, currentAdvertisedPort, newAdvertisedPort, newLocalListenPort !== currentLocalListenPort);
  const identity = systemUpdatePortCreateIdentity(expectedSnapshotID, appliedConfigRevision, fence, newLocalListenPort !== currentLocalListenPort);
  return {
    ...identity,
    ...advertised,
    operation: "port_reconfigure",
    target_id: normalizedTargetID,
    new_local_listen_port: newLocalListenPort,
    expected_endpoint_revision: expectedEndpointRevision,
    idempotency_key: normalizedKey,
  };
}

export function systemUpdateDockerPortReconfigureRequest({
  targetID,
  currentMapping,
  newAdvertisedPort,
  newPublishedPort,
  newContainerPort,
  mode,
  expectedSnapshotID,
  appliedConfigRevision,
  fence,
  expectedEndpointRevision,
  idempotencyKey,
}: {
  targetID: string;
  currentMapping: SystemUpdatePortMapping;
  newAdvertisedPort?: number;
  newPublishedPort: number;
  newContainerPort: number;
  mode: SystemUpdatePortMode;
  expectedSnapshotID: string;
  appliedConfigRevision: number;
  fence: number;
  expectedEndpointRevision: number;
  idempotencyKey: string;
}): SystemUpdateDockerPortReconfigureCreateRequest {
  const normalizedTargetID = String(targetID || "").trim();
  const normalizedKey = String(idempotencyKey || "").trim();
  if (!normalizedTargetID || !normalizedKey) throw new Error("invalid_port_reconfigure_request");
  if (!validAppliedDockerPortMapping(currentMapping, Number(currentMapping.advertised_port))) {
    throw new Error("docker_mapping_unavailable");
  }
  if (!validPort(newPublishedPort, 1024)) throw new Error("invalid_published_port");
  if (!validPort(newContainerPort, 1024)) throw new Error("invalid_container_port");
  const localChanged = newPublishedPort !== Number(currentMapping.published_port) || newContainerPort !== Number(currentMapping.container_port);
  const advertised = systemUpdatePortAdvertisedInput(mode, Number(currentMapping.advertised_port), newAdvertisedPort, localChanged);
  const identity = systemUpdatePortCreateIdentity(expectedSnapshotID, appliedConfigRevision, fence, localChanged);
  if (!Number.isSafeInteger(expectedEndpointRevision) || expectedEndpointRevision < 1) {
    throw new Error("invalid_endpoint_revision");
  }
  return {
    ...identity,
    ...advertised,
    operation: "port_reconfigure",
    target_id: normalizedTargetID,
    new_published_port: newPublishedPort,
    new_container_port: newContainerPort,
    expected_endpoint_revision: expectedEndpointRevision,
    idempotency_key: normalizedKey,
  };
}

function systemUpdatePortCreateIdentity(snapshotID: string, configRevision: number, fence: number, changed: boolean) {
  const desired = configRevision + (changed ? 1 : 0);
  if (!/^ps1:[a-f0-9]{64}$/.test(snapshotID) || !Number.isSafeInteger(configRevision) || configRevision < 1
    || !Number.isSafeInteger(desired) || !Number.isSafeInteger(fence) || fence < 1) throw new Error("invalid_port_reconfigure_request");
  return { protocol_version: 2 as const, port_contract_version: 2 as const, expected_snapshot_id: snapshotID,
    desired_revision: desired, fence, required_capability: "host.port" as const };
}

function systemUpdatePortAdvertisedInput(mode: SystemUpdatePortMode, current: number, proposed: number | undefined, localChanged: boolean) {
  if (!validPort(current, 1)) throw new Error("invalid_current_service_port");
  if (mode === "local_only" && proposed === undefined) return { mode };
  if (mode !== "local_and_advertised" || proposed === undefined || !validPort(proposed, 1)) throw new Error("invalid_system_update_port_mode");
  if (!localChanged && proposed !== current) throw new Error("system_update_advertised_only_unsupported");
  return { mode, new_advertised_port: proposed };
}

export function systemUpdatePortRequestMatchesJob(
  request: SystemUpdatePortReconfigureCreateRequest,
  job: SystemUpdateJob,
) {
  if (
    job.idempotency_key !== request.idempotency_key
    || job.target_id !== request.target_id
    || job.operation !== "port_reconfigure"
    || job.port_reconfigure?.port_contract_version !== 2
    || job.port_reconfigure.mode !== request.mode
    || job.port_reconfigure.before?.snapshot_id !== request.expected_snapshot_id
    || job.port_reconfigure.before.endpoint_revision !== request.expected_endpoint_revision
    || job.port_reconfigure.target?.config_revision !== request.desired_revision
    || job.ownership_epoch !== request.fence
  ) {
    return false;
  }
  const target = job.port_reconfigure.target;
  if (request.mode === "local_and_advertised" && target.advertised_port !== request.new_advertised_port) return false;
  if ("new_local_listen_port" in request) return !target.docker && target.local_listen_port === request.new_local_listen_port;
  return target.docker?.published_port === request.new_published_port && target.docker?.container_port === request.new_container_port;
}

export async function requestSystemUpdatePortReconfigureWithRecovery(
  request: SystemUpdatePortReconfigureCreateRequest,
  send: (request: SystemUpdatePortReconfigureCreateRequest) => Promise<unknown>,
  refreshJobs: () => Promise<SystemUpdateJob[]>,
) {
  try {
    return systemUpdateJobFromResponse(await send(request));
  } catch (originalError) {
    if (!ambiguousSystemUpdateCreateError(originalError)) throw originalError;
    try {
      const recovered = (await refreshJobs()).find((job) => systemUpdatePortRequestMatchesJob(request, job));
      if (recovered) return recovered;
    } catch {
      // The original POST may have committed. Never issue a second POST while
      // the request identity remains unresolved.
    }
    throw new SystemUpdateRequestAmbiguousError(request, originalError);
  }
}

export function isSystemUpdateEndpointRevisionConflict(error: unknown) {
  if (!error || typeof error !== "object") return false;
  const record = error as { code?: unknown; status?: unknown };
  return record.code === "system_update_endpoint_revision_conflict" && Number(record.status) === 409;
}

export function systemUpdatePortReconfigureResultLabel(result?: SystemUpdatePortReconfigurationResult) {
  const labels: Record<SystemUpdatePortReconfigurationResult, string> = {
    applied: "適用済み",
    rolled_back: "復旧済み",
    unchanged: "変更不要",
    rollback_failed: "復旧未完了／要再照合",
  };
  return result ? labels[result] : "";
}

export function systemUpdatePortJobResultLabel(job?: SystemUpdateJob) {
  if (!job || job.operation !== "port_reconfigure") return "";
  if (job.recovery_required) return "復旧未完了／要再照合";
  if (job.port_reconfigure?.port_contract_version === 2) return job.port_result ? systemUpdatePortReconfigureResultLabel(job.port_result.result) : "確認中";
  return systemUpdatePortReconfigureResultLabel(job.port_reconfigure?.result);
}
