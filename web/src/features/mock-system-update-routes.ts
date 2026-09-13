import type { SystemUpdateJob, SystemUpdateTarget, SystemUpdatePortReconfiguration, SystemUpdatePortSnapshotRef, UpdaterHostBootstrapJob, UpdaterHostBootstrapRequest } from "@/types/domain";
import { mockWorkers, mockUpdaterSettings, mockSystemUpdateUpdaters, mockUpdaterHostBootstrapJobs, mockSystemUpdateTargets, mockCurrentUser, mockSystemUpdateJobs } from "./mock-state";


function mockSystemUpdatePortPlan(target: SystemUpdateTarget, request: Record<string, unknown>): SystemUpdatePortReconfiguration {
  if (request.protocol_version !== 2 || request.port_contract_version !== 2) throw new Error("system_update_port_contract_required");
  if (request.expected_snapshot_id !== target.port_policy_snapshot_id || request.expected_endpoint_revision !== target.endpoint_revision || request.fence !== target.ownership_epoch) throw new Error("system_update_port_snapshot_stale");
  if (request.mode !== "local_only" && request.mode !== "local_and_advertised" || request.required_capability !== "host.port"
    || request.mode === "local_only" && request.new_advertised_port !== undefined || request.new_port !== undefined) throw new Error("invalid_system_update_port_mode");
  const mapping = target.port_mapping;
  const node = mockWorkers.find((item) => item.service_id === target.target_id);
  const local = Number(target.local_listen_port);
  const advertised = Number(mapping?.advertised_port ?? node?.applied_endpoint?.port);
  const proposed = Number(mapping ? request.new_published_port : request.new_local_listen_port);
  const container = mapping ? Number(request.new_container_port) : undefined;
  const nextAdvertised = request.mode === "local_only" ? advertised : Number(request.new_advertised_port);
  if (!Number.isInteger(proposed) || proposed < 1024 || proposed > 65535 || !Number.isInteger(nextAdvertised) || nextAdvertised < 1 || nextAdvertised > 65535
    || mapping && (!Number.isInteger(container) || Number(container) < 1024 || Number(container) > 65535)) throw new Error("invalid_port_reconfigure_request");
  const changed = proposed !== local || mapping && container !== mapping.container_port;
  const adDelta = Number(nextAdvertised !== advertised);
  if (!changed && adDelta) throw new Error("system_update_advertised_only_unsupported");
  const revision = Number(target.applied_config_revision);
  if (request.desired_revision !== revision + Number(Boolean(changed))) throw new Error("system_update_port_snapshot_stale");
  const digest = (value: string) => `sha256:${value.repeat(64)}`;
  const before: SystemUpdatePortSnapshotRef = { snapshot_id: target.port_policy_snapshot_id!, snapshot_sha256: digest("a"),
    source_policy_revision: 11, projection_revision: 17, executor_policy_revision: 23, executor_policy_sha256: digest("b"),
    endpoint_revision: target.endpoint_revision!, applied_endpoint_revision: target.applied_endpoint_revision!, config_revision: revision, config_sha256: digest("c"),
    local_listen_port: local, advertised_port: advertised, advertised_endpoint_sha256: digest("d"),
    docker: mapping ? { published_host_ip: "127.0.0.1", published_port: mapping.published_port!, container_port: mapping.container_port!, health_port: mapping.health_port!,
      compose_policy_sha256: digest("7"), compose_revision: revision, version_env_sha256: digest("8"), image_id: digest("9"), repository_digest: digest("0") } : undefined };
  const next = (step: number, snapshot: string, executor: string, config: string): SystemUpdatePortSnapshotRef => ({ ...structuredClone(before),
    snapshot_id: `ps1:${snapshot.repeat(64)}`, snapshot_sha256: digest(snapshot),
    source_policy_revision: before.source_policy_revision + step, projection_revision: before.projection_revision + step,
    executor_policy_revision: before.executor_policy_revision + step, executor_policy_sha256: digest(executor),
    config_revision: revision + step, config_sha256: digest(config), endpoint_revision: before.endpoint_revision + step * adDelta,
    applied_endpoint_revision: before.applied_endpoint_revision + step * adDelta });
  const after = changed ? next(1, "e", "f", "1") : structuredClone(before);
  const rollback = changed ? next(2, "3", "4", "5") : structuredClone(before);
  after.local_listen_port = proposed; after.advertised_port = nextAdvertised;
  if (adDelta) after.advertised_endpoint_sha256 = digest("2");
  if (changed && after.docker && rollback.docker) {
    after.docker.published_port = proposed; after.docker.container_port = container!; after.docker.health_port = proposed;
    after.docker.compose_revision++; rollback.docker.compose_revision += 2;
  }
  return { port_contract_version: 2, mode: request.mode, network_namespace: "host", protocol: "tcp", before, target: after, rollback, port_plan_sha256: "6".repeat(64),
    docker_baseline: before.docker ? { expected_container_id: "c".repeat(64), expected_image_id: before.docker.image_id, expected_repository_digest: before.docker.repository_digest,
      expected_version_env_sha256: before.docker.version_env_sha256, approved_compose_config_sha256: "7".repeat(64), approved_compose_revision: before.docker.compose_revision } : undefined };
}

export function postMockBootstrapJob(updaterBootstrapJobs: RegExpMatchArray, body?: unknown): unknown {
    const updaterID = decodeURIComponent(updaterBootstrapJobs[1]);
    if (updaterID !== mockUpdaterSettings.updater_id) throw new Error("updater_not_found");
    const request = (body || {}) as UpdaterHostBootstrapRequest;
    if (request.expected_revision !== mockUpdaterSettings.revision) throw new Error("updater_policy_revision_conflict");
    const hostIDs = [...new Set((request.host_ids || []).map((hostID) => String(hostID || "").trim()).filter(Boolean))].sort();
    const recipientFingerprint = (request as { recipient_key_fingerprint?: unknown }).recipient_key_fingerprint;
    if (
      !request.job_id
      || !request.idempotency_key
      || hostIDs.length === 0
      || request.envelope?.version !== 1
      || typeof recipientFingerprint !== "string"
      || !recipientFingerprint
      || recipientFingerprint !== recipientFingerprint.trim()
    ) {
      throw new Error("invalid_updater_host_bootstrap_request");
    }
    const currentRecipientFingerprint = mockSystemUpdateUpdaters.find(
      (updater) => updater.updater_id === updaterID,
    )?.bootstrap_encryption_key_fingerprint;
    if (!currentRecipientFingerprint || recipientFingerprint !== currentRecipientFingerprint) {
      throw new Error("bootstrap_recipient_key_changed");
    }
    const configuredHostIDs = new Set(mockUpdaterSettings.hosts.map((host) => host.host_id));
    if (hostIDs.some((hostID) => !configuredHostIDs.has(hostID))) throw new Error("updater_host_not_found");
    const existing = mockUpdaterHostBootstrapJobs.find((job) => job.idempotency_key === request.idempotency_key);
    if (existing) return { jobs: [structuredClone(existing)] };
    const now = new Date().toISOString();
    const job: UpdaterHostBootstrapJob = {
      id: request.job_id,
      idempotency_key: request.idempotency_key,
      updater_id: updaterID,
      expected_revision: request.expected_revision,
      status: "succeeded",
      host_ids: hostIDs,
      hosts: hostIDs.map((hostID) => ({
        host_id: hostID,
        status: "succeeded",
        progress: 100,
        message: "helperの導入と動作確認が完了しました。",
        updated_at: now,
        completed_at: now,
      })),
      created_at: now,
      updated_at: now,
      completed_at: now,
    };
    mockUpdaterHostBootstrapJobs.unshift(job);
    return { jobs: [structuredClone(job)] };
  }


export function postMockSystemUpdate(body?: unknown): unknown {
    const request = body as Partial<{
      operation: "software_update" | "port_reconfigure";
      target_id: string;
      strategy: string;
      idempotency_key: string;
      expected_endpoint_revision: number;
      protocol_version: number;
      port_contract_version: number;
      mode: "local_only" | "local_and_advertised";
      expected_snapshot_id: string;
      desired_revision: number;
      fence: number;
      required_capability: string;
      new_local_listen_port: number;
      new_advertised_port: number;
      new_published_port: number;
      new_container_port: number;
    }>;
    const target = mockSystemUpdateTargets.find((item) => item.target_id === request.target_id);
    if (!target) throw new Error("target_not_found");
    const now = new Date().toISOString();
    const portPlan = request.operation === "port_reconfigure" ? mockSystemUpdatePortPlan(target, request) : undefined;
    const job: SystemUpdateJob = {
      id: `update-demo-${Date.now()}`,
      idempotency_key: request.idempotency_key,
      target_id: target.target_id,
      target_type: target.target_type,
      current_version: target.current_version,
      target_version: target.latest_version,
      deployment_mode: target.deployment_mode,
      operation: request.operation || "software_update",
      port_reconfigure: portPlan,
      ownership_epoch: portPlan ? request.fence : undefined,
      strategy: request.strategy === "when_idle" ? "when_idle" : "maintenance",
      status: "queued",
      progress: 0,
      message: request.operation === "port_reconfigure"
        ? "Host Agentがポート変更を受け取るまで待機しています。"
        : request.strategy === "when_idle"
          ? "配信終了後に更新を開始します。"
          : "Updaterの実行待ちです。",
      requested_by: mockCurrentUser.user.username,
      created_at: now,
      updated_at: now,
    };
    mockSystemUpdateJobs.unshift(job);
    return job;
  }
