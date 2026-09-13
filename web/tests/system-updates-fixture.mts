import { registerSourceResolution } from "./helpers/moved-source.mts";
import type { SystemUpdateTarget, SystemUpdatePortSnapshotRef, SystemUpdatePortReconfiguration } from "../src/types/domain.ts";


registerSourceResolution();

export const { mockGet, mockPost, mockPut } = await import("../src/features/mock-data.ts");

export const { acquireSystemUpdateTargetRequestLock, isControlPanelUpdateTarget, isSystemUpdateJobActive, isSystemUpdateJobCancellable, systemUpdateMayDisconnectPanel, systemUpdateSoftwareOperationEligibility, systemUpdateStrategyForTarget, systemUpdateTargetOperationEligibility } = await import("../src/lib/system-update-target-policy.ts");
export const { activeUpdaterHostBootstrapStatus, isUpdaterHostBootstrapBulkCandidate, isUpdaterHostBootstrapJobActive, normalizeUpdaterHostBootstrapJobsResponse, recoverUpdaterHostBootstrapRequest, requestUpdaterHostBootstrapWithRecovery, updaterHostBootstrapConfirmationContext, updaterHostBootstrapEligibility, updaterHostBootstrapEligibilityMessage, updaterHostBootstrapRequestIdentity, UpdaterHostBootstrapRequestAmbiguousError } = await import("../src/lib/updater-bootstrap.ts");
export const { applyUpdaterSettingsTargetSelection, applyUpdaterSettingsTargetPatch, isUpdaterPolicyHostID, firstUnusedUpdaterSettingsTarget, normalizeUpdaterSettingsResponse, normalizeUpdaterSettingsTargetDatabaseName, normalizeUpdaterSettingsTargetLocalListenPort, updaterSettingsTargetRequiresDatabase, updaterSettingsTargetRequiresLocalListenPort, updaterSettingsTargetOptions } = await import("../src/lib/updater-settings-model.ts");
export const { compareSystemUpdateVersions } = await import("../src/lib/system-update-version.ts");
export const { isSystemUpdateEndpointRevisionConflict, requestSystemUpdatePortReconfigureWithRecovery, systemUpdateDockerPortReconfigureRequest, systemUpdatePortReconfigureEligibility, systemUpdatePortReconfigureRequest, systemUpdatePortReconfigureResultLabel, systemUpdatePortJobResultLabel, systemUpdatePortRequestMatchesJob } = await import("../src/lib/system-update-port-requests.ts");
export const { normalizeSystemUpdatesResponse, systemUpdateJobFromResponse } = await import("../src/lib/system-updates.ts");
export const { normalizePullUpdaterOwnershipActivationResponse, normalizePullUpdaterOwnershipDeactivationResponse, pullUpdaterOwnershipActivationEligibility, pullUpdaterOwnershipActivationRequest, pullUpdaterOwnershipDeactivationEligibility, pullUpdaterOwnershipDeactivationRequest, pullOwnershipMutationFenceAdvanced } = await import("../src/lib/updater-ownership.ts");
export const { requestSystemUpdateWithRecovery, runSystemUpdatesSequentially, systemUpdateRequest, SystemUpdateRequestAmbiguousError } = await import("../src/lib/system-update-requests.ts");
export const { systemUpdateDeploymentLabel, systemUpdateErrorMessage, systemUpdateConnectivity, systemUpdateHostReachabilityLabel, systemUpdateHostReachabilityMessage, systemUpdateJobStatusLabel, systemUpdatePolicyErrorMessage, systemUpdateUpdaterPolicyState, systemUpdateProgress, systemUpdateTargetBlockedReason } = await import("../src/lib/system-update-presentation.ts");

export const portBefore: SystemUpdatePortSnapshotRef = {
  snapshot_id: `ps1:${"a".repeat(64)}`, snapshot_sha256: `sha256:${"a".repeat(64)}`,
  source_policy_revision: 11, projection_revision: 17, executor_policy_revision: 23,
  executor_policy_sha256: `sha256:${"b".repeat(64)}`,
  endpoint_revision: 7, applied_endpoint_revision: 7,
  config_revision: 11, config_sha256: `sha256:${"c".repeat(64)}`,
  local_listen_port: 8084, advertised_port: 8084, advertised_endpoint_sha256: `sha256:${"d".repeat(64)}`,
};
export const portIdentity = { expectedSnapshotID: portBefore.snapshot_id, appliedConfigRevision: portBefore.config_revision, fence: 5 };
export const portWireIdentity = { protocol_version: 2, port_contract_version: 2, expected_snapshot_id: portBefore.snapshot_id,
  desired_revision: portBefore.config_revision + 1, fence: 5, required_capability: "host.port" };
export function portPlan(before: SystemUpdatePortSnapshotRef = portBefore, localPort = 18084, advertisedPort = before.advertised_port): SystemUpdatePortReconfiguration {
  const changed = localPort !== before.local_listen_port;
  const advertisedChanged = advertisedPort !== before.advertised_port;
  const target = changed ? { ...before, snapshot_id: `ps1:${"e".repeat(64)}`, snapshot_sha256: `sha256:${"e".repeat(64)}`,
    source_policy_revision: before.source_policy_revision + 1, projection_revision: before.projection_revision + 1,
    executor_policy_revision: before.executor_policy_revision + 1, executor_policy_sha256: `sha256:${"f".repeat(64)}`,
    config_revision: before.config_revision + 1, config_sha256: `sha256:${"1".repeat(64)}`,
    endpoint_revision: before.endpoint_revision + Number(advertisedChanged), applied_endpoint_revision: before.applied_endpoint_revision + Number(advertisedChanged),
    local_listen_port: localPort, advertised_port: advertisedPort,
    advertised_endpoint_sha256: advertisedChanged ? `sha256:${"2".repeat(64)}` : before.advertised_endpoint_sha256 } : before;
  const rollback = changed ? { ...before, snapshot_id: `ps1:${"3".repeat(64)}`, snapshot_sha256: `sha256:${"3".repeat(64)}`,
    source_policy_revision: before.source_policy_revision + 2, projection_revision: before.projection_revision + 2,
    executor_policy_revision: before.executor_policy_revision + 2, executor_policy_sha256: `sha256:${"4".repeat(64)}`,
    config_revision: before.config_revision + 2, config_sha256: `sha256:${"5".repeat(64)}`,
    endpoint_revision: before.endpoint_revision + Number(advertisedChanged) * 2,
    applied_endpoint_revision: before.applied_endpoint_revision + Number(advertisedChanged) * 2 } : before;
  return { port_contract_version: 2, mode: advertisedChanged ? "local_and_advertised" : "local_only",
    network_namespace: "host", protocol: "tcp", before, target, rollback, port_plan_sha256: "6".repeat(64) };
}

export const baseTarget: SystemUpdateTarget = {
  target_id: "worker-main",
  target_type: "worker",
  name: "Main Worker",
  host_id: "host-main",
  current_version: "v1.0.0",
  latest_version: "v1.1.0",
  update_available: true,
  deployment_mode: "systemd",
  updater_id: "updater-main",
  updater_online: true,
  eligible: true,
  eligible_operations: ["software_update", "port_reconfigure"],
  port_contract_version: 2, port_policy_snapshot_id: portBefore.snapshot_id,
  local_listen_port: portBefore.local_listen_port, endpoint_revision: portBefore.endpoint_revision,
  applied_endpoint_revision: portBefore.applied_endpoint_revision, applied_config_revision: portBefore.config_revision,
  ownership_epoch: portIdentity.fence, port_modes: ["local_only", "local_and_advertised"],
};

export function toBase64URL(bytes: Uint8Array) {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

export function fromBase64URL(value: string) {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(value.length / 4) * 4, "=");
  return Uint8Array.from(atob(padded), (character) => character.charCodeAt(0));
}
