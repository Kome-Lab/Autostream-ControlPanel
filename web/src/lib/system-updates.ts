import type { SystemUpdateAgentStatus, SystemUpdatePortMapping, SystemUpdatePortMode, SystemUpdateHostStatus, SystemUpdateJob, SystemUpdateOperation, SystemUpdateReachability, SystemUpdateStrategy, SystemUpdateTarget, SystemUpdatesResponse } from "@/types/domain";
import { recordValue, stringValue, optionalNonNegativeIntegerValue, optionalNumberValue, stringRecordValue, normalize, numberValue } from "./system-update-values";
import { normalizeSystemUpdatePortReconfiguration, normalizeSystemUpdatePortResult, normalizeSystemUpdatePortRecoveryObservation } from "./system-update-port-model";

export function normalizeSystemUpdatesResponse(value: unknown): SystemUpdatesResponse {
  const response = recordValue(value);
  const updaters = Array.isArray(response.updaters) ? response.updaters.map(normalizeSystemUpdateAgent).filter((updater) => updater.updater_id) : [];
  const hosts = Array.isArray(response.hosts) ? response.hosts.map(normalizeSystemUpdateHost).filter((host) => host.host_id) : [];
  const targets = Array.isArray(response.targets) ? response.targets.map(normalizeSystemUpdateTarget).filter((target) => target.target_id) : [];
  const jobs = Array.isArray(response.jobs) ? response.jobs.map(normalizeSystemUpdateJob).filter((job) => job.id) : [];
  return { updaters, hosts, targets, jobs };
}

export function systemUpdateJobFromResponse(value: unknown): SystemUpdateJob {
  const response = recordValue(value);
  const nestedJob = recordValue(response.job);
  const job = normalizeSystemUpdateJob(Object.keys(nestedJob).length > 0 ? { ...response, ...nestedJob } : response);
  if (!job.id || !job.target_id) throw new Error("invalid_system_update_response");
  return job;
}

function normalizeSystemUpdateTarget(value: unknown): SystemUpdateTarget {
  const target = recordValue(value);
  const updaterID = stringValue(target.updater_id || target.update_agent_id);
  const blockedReason = stringValue(target.blocked_reason);
  const eligibleOperations = Array.isArray(target.eligible_operations)
    ? target.eligible_operations.filter(
        (operation): operation is SystemUpdateOperation => operation === "software_update" || operation === "port_reconfigure",
      )
    : undefined;
  const rawOperationBlockedReasons = recordValue(target.operation_blocked_reasons);
  const portMapping = normalizeSystemUpdatePortMapping(target.port_mapping);
  const operationBlockedReasons: Partial<Record<SystemUpdateOperation, string>> = {};
  for (const operation of ["software_update", "port_reconfigure"] as const) {
    const reason = stringValue(rawOperationBlockedReasons[operation]);
    if (reason) operationBlockedReasons[operation] = reason;
  }
  return {
    target_id: stringValue(target.target_id),
    port_contract_version: target.port_contract_version === 2 ? 2 : undefined,
    port_policy_snapshot_id: stringValue(target.port_policy_snapshot_id) || undefined,
    local_listen_port: optionalNonNegativeIntegerValue(target.local_listen_port),
    endpoint_revision: optionalNonNegativeIntegerValue(target.endpoint_revision),
    applied_endpoint_revision: optionalNonNegativeIntegerValue(target.applied_endpoint_revision),
    applied_config_revision: optionalNonNegativeIntegerValue(target.applied_config_revision),
    ownership_epoch: optionalNonNegativeIntegerValue(target.ownership_epoch),
    port_modes: Array.isArray(target.port_modes) ? target.port_modes.filter((mode): mode is SystemUpdatePortMode => mode === "local_only" || mode === "local_and_advertised") : undefined,
    target_type: stringValue(target.target_type || target.service_type),
    name: stringValue(target.name || target.target_id),
    host_id: stringValue(target.host_id),
    current_version: stringValue(target.current_version),
    latest_version: stringValue(target.latest_version),
    update_available: Boolean(target.update_available),
    deployment_mode: stringValue(target.deployment_mode),
    updater_id: updaterID,
    updater_online: target.updater_online === true,
    busy: typeof target.busy === "boolean" ? target.busy : undefined,
    current_stream_id: stringValue(target.current_stream_id),
    eligible: Boolean(target.eligible),
    blocked_reason: blockedReason,
    eligible_operations: eligibleOperations,
    operation_blocked_reasons: Object.keys(operationBlockedReasons).length > 0 ? operationBlockedReasons : undefined,
    port_mapping: portMapping,
    update_check_source: stringValue(target.update_check_source),
    update_check_error: stringValue(target.update_check_error),
  };
}

function normalizeSystemUpdatePortMapping(value: unknown): SystemUpdatePortMapping | undefined {
  const mapping = recordValue(value);
  if (mapping.mode !== "docker") return undefined;
  const state = mapping.state === "applied" || mapping.state === "drifted" || mapping.state === "unavailable"
    ? mapping.state
    : "unavailable";
  return {
    mode: "docker",
    advertised_port: optionalNumberValue(mapping.advertised_port),
    published_host_ip: stringValue(mapping.published_host_ip) || undefined,
    published_port: optionalNumberValue(mapping.published_port),
    container_port: optionalNumberValue(mapping.container_port),
    health_port: optionalNumberValue(mapping.health_port),
    config_revision: optionalNumberValue(mapping.config_revision),
    state,
    reported_at: stringValue(mapping.reported_at) || undefined,
  };
}

function normalizeSystemUpdateAgent(value: unknown): SystemUpdateAgentStatus {
  const updater = recordValue(value);
  const updaterID = stringValue(updater.updater_id);
  const normalized: SystemUpdateAgentStatus = {
    updater_id: updaterID,
    name: stringValue(updater.name) || updaterID,
    status: stringValue(updater.status),
    online: updater.online === true,
    version: stringValue(updater.version),
    last_heartbeat_at: stringValue(updater.last_heartbeat_at),
  };
  if (updater.transport_mode === "pull_v2") {
    normalized.transport_mode = "pull_v2";
  }
  const executionHostID = stringValue(updater.execution_host_id);
  const ownershipEpoch = optionalNumberValue(updater.ownership_epoch);
  if (executionHostID) normalized.execution_host_id = executionHostID;
  if (ownershipEpoch !== undefined) normalized.ownership_epoch = ownershipEpoch;
  const desiredRevision = optionalNumberValue(updater.desired_revision);
  const appliedRevision = optionalNumberValue(updater.applied_revision);
  const policyStatus = stringValue(updater.policy_status);
  const policyError = stringValue(updater.policy_error_code || updater.policy_error);
  const publicKeys = stringRecordValue(updater.ssh_client_public_keys);
  const keyFingerprints = stringRecordValue(updater.ssh_client_key_fingerprints);
  const bootstrapEncryptionPublicKey = stringValue(updater.bootstrap_encryption_public_key);
  const bootstrapEncryptionKeyFingerprint = stringValue(
    updater.bootstrap_encryption_key_fingerprint || updater.bootstrap_encryption_public_key_fingerprint,
  );
  if (desiredRevision !== undefined) normalized.desired_revision = desiredRevision;
  if (appliedRevision !== undefined) normalized.applied_revision = appliedRevision;
  if (policyStatus) normalized.policy_status = policyStatus;
  if (policyError) {
    normalized.policy_error_code = policyError;
    normalized.policy_error = policyError;
  }
  if (Object.keys(publicKeys).length > 0) normalized.ssh_client_public_keys = publicKeys;
  if (Object.keys(keyFingerprints).length > 0) normalized.ssh_client_key_fingerprints = keyFingerprints;
  if (bootstrapEncryptionPublicKey) normalized.bootstrap_encryption_public_key = bootstrapEncryptionPublicKey;
  if (bootstrapEncryptionKeyFingerprint) normalized.bootstrap_encryption_key_fingerprint = bootstrapEncryptionKeyFingerprint;
  return normalized;
}

function normalizeSystemUpdateHost(value: unknown): SystemUpdateHostStatus {
  const host = recordValue(value);
  const hostID = stringValue(host.host_id);
  return {
    host_id: hostID,
    name: stringValue(host.name) || hostID,
    updater_id: stringValue(host.updater_id),
    reachability: normalizeSystemUpdateReachability(host.reachability),
    reachability_checked_at: stringValue(host.reachability_checked_at),
    reachability_code: stringValue(host.reachability_code),
  };
}

function normalizeSystemUpdateReachability(value: unknown): SystemUpdateReachability {
  const reachability = normalize(stringValue(value));
  return reachability === "reachable" || reachability === "unreachable" ? reachability : "unknown";
}

function normalizeSystemUpdateJob(value: unknown): SystemUpdateJob {
  const job = recordValue(value);
  const operation = job.operation === "port_reconfigure" || job.operation === "software_update"
    ? job.operation
    : undefined;
  const portReconfigure = recordValue(job.port_reconfigure);
  const portPlan = operation === "port_reconfigure" && Object.keys(portReconfigure).length > 0
    ? normalizeSystemUpdatePortReconfiguration(portReconfigure) : undefined;
  return {
    id: stringValue(job.id),
    idempotency_key: stringValue(job.idempotency_key),
    target_id: stringValue(job.target_id),
    target_type: stringValue(job.target_type || job.target_service_type),
    host_id: stringValue(job.host_id),
    transport_mode: job.transport_mode === "pull_v2" ? "pull_v2" : undefined,
    ownership_epoch: optionalNonNegativeIntegerValue(job.ownership_epoch),
    policy_revision: optionalNonNegativeIntegerValue(job.policy_revision),
    updater_id: stringValue(job.updater_id),
    operation,
    port_reconfigure: portPlan,
    port_result: normalizeSystemUpdatePortResult(job.port_result, portPlan, stringValue(job.status)),
    last_recovery_observation: normalizeSystemUpdatePortRecoveryObservation(job.last_recovery_observation),
    current_version: stringValue(job.current_version),
    target_version: stringValue(job.target_version),
    deployment_mode: stringValue(job.deployment_mode),
    strategy: stringValue(job.strategy) as SystemUpdateStrategy || undefined,
    status: stringValue(job.status),
    progress: numberValue(job.progress),
    code: stringValue(job.code),
    message: stringValue(job.message),
    requested_by: stringValue(job.requested_by || job.requested_by_username),
    created_at: stringValue(job.created_at),
    updated_at: stringValue(job.updated_at),
    completed_at: stringValue(job.completed_at),
    sequence: optionalNumberValue(job.sequence),
    report_sequence: optionalNumberValue(job.report_sequence),
    lease_generation: optionalNumberValue(job.lease_generation),
    recovery_required: typeof job.recovery_required === "boolean" ? job.recovery_required : undefined,
    last_status: stringValue(job.last_status),
  };
}
