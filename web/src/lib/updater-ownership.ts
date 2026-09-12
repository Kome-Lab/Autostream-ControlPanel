import type { PullUpdaterOwnershipActivationRequest, PullUpdaterOwnershipActivationResponse, PullUpdaterOwnershipDeactivationRequest, PullUpdaterOwnershipDeactivationResponse, SystemUpdateAgentStatus, SystemUpdateJob, UpdaterSettings } from "@/types/domain";
import { type SystemUpdateRequestState, isSystemUpdateJobActive } from "./system-update-target-policy";
import { recordValue, stringValue, nonNegativeIntegerValue } from "./system-update-values";
import { validSystemUpdateDigest } from "./system-update-port-model";

export function pullUpdaterOwnershipActivationEligibility({
  updater,
  settings,
  jobs,
  requestState,
}: {
  updater: SystemUpdateAgentStatus;
  settings: UpdaterSettings;
  jobs: SystemUpdateJob[];
  requestState: SystemUpdateRequestState;
}) {
  if (updater.transport_mode !== "pull_v2" || settings.transport_mode !== "pull_v2") {
    return { ready: false, reason: "unsupported_transport" };
  }
  if (!pullUpdaterOwnershipActivationContract(updater, settings)) {
    return { ready: false, reason: "pull_ownership_contract_unavailable" };
  }
  if (requestState === "pending") return { ready: false, reason: "request_pending" };
  if (requestState === "ambiguous") return { ready: false, reason: "request_ambiguous" };
  const activation = settings.pull_activation;
  if (!activation) return { ready: false, reason: "pull_ownership_contract_unavailable" };
  if (!updater.online || activation.status.toLowerCase() !== "online" || !activation.last_heartbeat_at) {
    return { ready: false, reason: "observer_offline" };
  }
  if (activation.recovery_pending) return { ready: false, reason: "recovery_required" };
  const activeTargetIDs = new Set(settings.targets.map((target) => target.target_id));
  if (jobs.some((job) => (
    (job.updater_id === updater.updater_id || activeTargetIDs.has(job.target_id))
    && (job.recovery_required || isSystemUpdateJobActive(job.status))
  ))) {
    return { ready: false, reason: "active_job" };
  }
  if (
    !activation.ready
    || Boolean(activation.blocked_reason)
    || !activation.observe_only
    || !activation.update_executor
    || activation.mutation_enabled
    || activation.reported_ownership_epoch !== 0
    || activation.reported_projection_revision !== settings.projection_revision
  ) {
    return { ready: false, reason: "observer_not_ready" };
  }
  return { ready: true, reason: "" };
}

export function pullUpdaterOwnershipActivationRequest(
  updater: SystemUpdateAgentStatus,
  settings: UpdaterSettings,
): PullUpdaterOwnershipActivationRequest {
  const contract = pullUpdaterOwnershipActivationContract(updater, settings);
  if (!contract) throw new Error("pull_ownership_contract_unavailable");
  return contract;
}

export function normalizePullUpdaterOwnershipActivationResponse(value: unknown): PullUpdaterOwnershipActivationResponse {
  const response = recordValue(value);
  const normalized: PullUpdaterOwnershipActivationResponse = {
    updater_id: stringValue(response.updater_id),
    execution_host_id: stringValue(response.execution_host_id),
    transport_mode: response.transport_mode === "pull_v2" ? "pull_v2" : "pull_v2",
    agent_service_id: stringValue(response.agent_service_id),
    ownership_epoch: nonNegativeIntegerValue(response.ownership_epoch, -1),
    source_policy_revision: nonNegativeIntegerValue(response.source_policy_revision, 0),
    projection_revision: nonNegativeIntegerValue(response.projection_revision, 0),
    local_executor_policy_revision: nonNegativeIntegerValue(response.local_executor_policy_revision, 0),
    local_executor_policy_sha256: stringValue(response.local_executor_policy_sha256).toLowerCase(),
  };
  if (
    !normalized.updater_id
    || !normalized.execution_host_id
    || response.transport_mode !== "pull_v2"
    || !normalized.agent_service_id
    || normalized.ownership_epoch < 1
    || normalized.source_policy_revision < 1
    || normalized.projection_revision < 1
    || normalized.local_executor_policy_revision < 1
    || !validSystemUpdateDigest(normalized.local_executor_policy_sha256)
  ) {
    throw new Error("invalid_pull_ownership_activation_response");
  }
  return normalized;
}

function pullUpdaterOwnershipActivationContract(
  updater: SystemUpdateAgentStatus,
  settings: UpdaterSettings,
): PullUpdaterOwnershipActivationRequest | null {
  const executionHostID = String(settings.execution_host_id || "").trim();
  const ownership = settings.execution_host_ownership;
  const digest = String(settings.local_executor_policy_sha256 || "").trim().toLowerCase();
  if (
    updater.transport_mode !== "pull_v2"
    || updater.execution_host_id !== executionHostID
    || updater.ownership_epoch !== 0
    || !executionHostID
    || !ownership
    || ownership.transport_mode !== "pull_v2"
    || (ownership.agent_service_id && ownership.agent_service_id !== updater.updater_id)
    || !Number.isSafeInteger(ownership.ownership_epoch)
    || ownership.ownership_epoch < 0
    || !Number.isSafeInteger(settings.revision)
    || settings.revision < 1
    || !Number.isSafeInteger(settings.projection_revision)
    || Number(settings.projection_revision) < 1
    || !Number.isSafeInteger(settings.local_executor_policy_revision)
    || Number(settings.local_executor_policy_revision) < 1
    || !validSystemUpdateDigest(digest)
  ) {
    return null;
  }
  return {
    expected_execution_host_id: executionHostID,
    expected_ownership_epoch: ownership.ownership_epoch,
    expected_source_policy_revision: settings.revision,
    expected_projection_revision: Number(settings.projection_revision),
    expected_local_executor_policy_revision: Number(settings.local_executor_policy_revision),
    expected_local_executor_policy_sha256: digest,
  };
}

export function pullOwnershipMutationFenceAdvanced(
  attempt: Pick<
    PullUpdaterOwnershipActivationRequest,
    "expected_execution_host_id" | "expected_ownership_epoch"
  >,
  settings: Pick<UpdaterSettings, "execution_host_id" | "execution_host_ownership">,
) {
  const ownership = settings.execution_host_ownership;
  return (
    String(settings.execution_host_id || "").trim() === attempt.expected_execution_host_id
    && Boolean(ownership)
    && Number.isSafeInteger(ownership?.ownership_epoch)
    && Number(ownership?.ownership_epoch) > attempt.expected_ownership_epoch
  );
}

export function pullUpdaterOwnershipDeactivationEligibility({
  updater,
  settings,
  jobs,
  requestState,
}: {
  updater: SystemUpdateAgentStatus;
  settings: UpdaterSettings;
  jobs: SystemUpdateJob[];
  requestState: SystemUpdateRequestState;
}) {
  if (!pullUpdaterOwnershipDeactivationContract(updater, settings)) {
    return { ready: false, reason: "pull_rollback_contract_unavailable" };
  }
  if (requestState === "pending") return { ready: false, reason: "request_pending" };
  if (requestState === "ambiguous") return { ready: false, reason: "request_ambiguous" };
  if (settings.pull_activation?.recovery_pending) {
    return { ready: false, reason: "recovery_required" };
  }
  const activeTargetIDs = new Set(settings.targets.map((target) => target.target_id));
  if (jobs.some((job) => (
    (job.updater_id === updater.updater_id || activeTargetIDs.has(job.target_id))
    && (job.recovery_required || isSystemUpdateJobActive(job.status))
  ))) {
    return { ready: false, reason: "active_job" };
  }
  return { ready: true, reason: "" };
}

export function pullUpdaterOwnershipDeactivationRequest(
  updater: SystemUpdateAgentStatus,
  settings: UpdaterSettings,
): PullUpdaterOwnershipDeactivationRequest {
  const contract = pullUpdaterOwnershipDeactivationContract(updater, settings);
  if (!contract) throw new Error("pull_rollback_contract_unavailable");
  return contract;
}

export function normalizePullUpdaterOwnershipDeactivationResponse(
  value: unknown,
): PullUpdaterOwnershipDeactivationResponse {
  const response = recordValue(value);
  const normalized: PullUpdaterOwnershipDeactivationResponse = {
    updater_id: stringValue(response.updater_id),
    execution_host_id: stringValue(response.execution_host_id),
    transport_mode: "pull_v2",
    agent_service_id: stringValue(response.agent_service_id),
    ownership_epoch: nonNegativeIntegerValue(response.ownership_epoch, -1),
    agent_ownership_epoch: nonNegativeIntegerValue(response.agent_ownership_epoch, -1) as 0,
    source_policy_revision: nonNegativeIntegerValue(response.source_policy_revision, 0),
    projection_revision: nonNegativeIntegerValue(response.projection_revision, 0),
    local_executor_policy_revision: nonNegativeIntegerValue(response.local_executor_policy_revision, 0),
    local_executor_policy_sha256: stringValue(response.local_executor_policy_sha256).toLowerCase(),
  };
  if (
    !normalized.updater_id
    || !normalized.execution_host_id
    || response.transport_mode !== "pull_v2"
    || !normalized.agent_service_id
    || normalized.ownership_epoch < 1
    || normalized.agent_ownership_epoch !== 0
    || normalized.source_policy_revision < 1
    || normalized.projection_revision < 1
    || normalized.local_executor_policy_revision < 1
    || !validSystemUpdateDigest(normalized.local_executor_policy_sha256)
  ) {
    throw new Error("invalid_pull_ownership_deactivation_response");
  }
  return normalized;
}

function pullUpdaterOwnershipDeactivationContract(
  updater: SystemUpdateAgentStatus,
  settings: UpdaterSettings,
): PullUpdaterOwnershipDeactivationRequest | null {
  const executionHostID = String(settings.execution_host_id || "").trim();
  const ownership = settings.execution_host_ownership;
  const digest = String(settings.local_executor_policy_sha256 || "").trim().toLowerCase();
  if (
    updater.transport_mode !== "pull_v2"
    || settings.transport_mode !== "pull_v2"
    || updater.execution_host_id !== executionHostID
    || !executionHostID
    || !ownership
    || ownership.transport_mode !== "pull_v2"
    || ownership.agent_service_id !== updater.updater_id
    || !Number.isSafeInteger(ownership.ownership_epoch)
    || ownership.ownership_epoch < 1
    || !Number.isSafeInteger(updater.ownership_epoch)
    || Number(updater.ownership_epoch) !== ownership.ownership_epoch
    || !Number.isSafeInteger(settings.revision)
    || settings.revision < 1
    || !Number.isSafeInteger(settings.projection_revision)
    || Number(settings.projection_revision) < 1
    || ownership.policy_revision !== Number(settings.projection_revision)
    || !Number.isSafeInteger(settings.local_executor_policy_revision)
    || Number(settings.local_executor_policy_revision) < 1
    || !validSystemUpdateDigest(digest)
  ) {
    return null;
  }
  return {
    expected_execution_host_id: executionHostID,
    expected_ownership_epoch: ownership.ownership_epoch,
    expected_source_policy_revision: settings.revision,
    expected_projection_revision: Number(settings.projection_revision),
    expected_local_executor_policy_revision: Number(settings.local_executor_policy_revision),
    expected_local_executor_policy_sha256: digest,
  };
}
