import type { SystemUpdatePortReconfiguration, SystemUpdatePortReconfigurationResult, SystemUpdatePortSnapshotRef, SystemUpdatePortResultV2, SystemUpdatePortObservation, SystemUpdateJob } from "@/types/domain";
import { stringValue, recordValue, numberValue, optionalNumberValue, validPort } from "./system-update-values";

export function normalizeSystemUpdatePortReconfiguration(value: Record<string, unknown>): SystemUpdatePortReconfiguration {
  if (value.port_contract_version === 2) {
    return {
      port_contract_version: 2,
      mode: value.mode === "local_only" || value.mode === "local_and_advertised" ? value.mode : undefined,
      network_namespace: stringValue(value.network_namespace), protocol: value.protocol === "tcp" ? "tcp" : undefined,
      before: normalizeSystemUpdatePortSnapshot(value.before), target: normalizeSystemUpdatePortSnapshot(value.target),
      rollback: normalizeSystemUpdatePortSnapshot(value.rollback), port_plan_sha256: stringValue(value.port_plan_sha256),
      docker_baseline: normalizeSystemUpdatePortDockerBaseline(value.docker_baseline),
    };
  }
  const result: SystemUpdatePortReconfigurationResult | undefined = value.result === "applied"
    || value.result === "rolled_back"
    || value.result === "unchanged"
    || value.result === "rollback_failed"
    ? value.result as SystemUpdatePortReconfigurationResult
    : undefined;
  const protocol: "tcp" | "udp" | undefined = value.protocol === "tcp" || value.protocol === "udp"
    ? value.protocol
    : undefined;
  const rawDocker = recordValue(value.docker);
  const docker = Object.keys(rawDocker).length > 0
    ? {
        published_host_ip: stringValue(rawDocker.published_host_ip),
        old_published_port: numberValue(rawDocker.old_published_port),
        new_published_port: numberValue(rawDocker.new_published_port),
        old_container_port: numberValue(rawDocker.old_container_port),
        new_container_port: numberValue(rawDocker.new_container_port),
        old_health_port: numberValue(rawDocker.old_health_port),
        new_health_port: numberValue(rawDocker.new_health_port),
        approved_compose_config_sha256: stringValue(rawDocker.approved_compose_config_sha256),
        approved_compose_revision: numberValue(rawDocker.approved_compose_revision),
        expected_version_env_sha256: stringValue(rawDocker.expected_version_env_sha256),
        expected_container_id: stringValue(rawDocker.expected_container_id),
        expected_image_id: stringValue(rawDocker.expected_image_id),
        expected_repository_digest: stringValue(rawDocker.expected_repository_digest),
      }
    : undefined;
  return {
    network_namespace: stringValue(value.network_namespace),
    protocol,
    old_port: optionalNumberValue(value.old_port),
    new_port: optionalNumberValue(value.new_port),
    expected_endpoint_revision: optionalNumberValue(value.expected_endpoint_revision),
    target_endpoint_revision: optionalNumberValue(value.target_endpoint_revision),
    expected_config_revision: optionalNumberValue(value.expected_config_revision),
    target_config_revision: optionalNumberValue(value.target_config_revision),
    expected_config_sha256: stringValue(value.expected_config_sha256),
    target_config_sha256: stringValue(value.target_config_sha256),
    expected_source_policy_revision: optionalNumberValue(value.expected_source_policy_revision),
    expected_updater_policy_revision: optionalNumberValue(value.expected_updater_policy_revision),
    expected_executor_policy_revision: optionalNumberValue(value.expected_executor_policy_revision),
    expected_executor_policy_sha256: stringValue(value.expected_executor_policy_sha256),
    port_plan_sha256: stringValue(value.port_plan_sha256),
    docker,
    result,
  };
}

function normalizeSystemUpdatePortDockerBaseline(value: unknown): SystemUpdatePortReconfiguration["docker_baseline"] {
  if (value === undefined) return undefined;
  const raw = recordValue(value);
  if (!/^[a-f0-9]{12,64}$/.test(stringValue(raw.expected_container_id)) || !/^[a-f0-9]{64}$/.test(stringValue(raw.approved_compose_config_sha256))
    || !Number.isSafeInteger(raw.approved_compose_revision) || Number(raw.approved_compose_revision) < 1
    || ["expected_image_id", "expected_repository_digest", "expected_version_env_sha256"].some((field) => !validSystemUpdateDigest(stringValue(raw[field])))) return undefined;
  return { expected_container_id: stringValue(raw.expected_container_id), expected_image_id: stringValue(raw.expected_image_id),
    expected_repository_digest: stringValue(raw.expected_repository_digest), expected_version_env_sha256: stringValue(raw.expected_version_env_sha256),
    approved_compose_config_sha256: stringValue(raw.approved_compose_config_sha256), approved_compose_revision: Number(raw.approved_compose_revision) };
}

function normalizeSystemUpdatePortSnapshot(value: unknown): SystemUpdatePortSnapshotRef | undefined {
  const raw = recordValue(value);
  const digest = stringValue(raw.snapshot_sha256);
  if (!validSystemUpdateDigest(digest) || raw.snapshot_id !== `ps1:${digest.slice(7)}`) return undefined;
  for (const field of ["source_policy_revision", "projection_revision", "executor_policy_revision", "endpoint_revision", "applied_endpoint_revision", "config_revision"] as const) {
    if (!Number.isSafeInteger(raw[field]) || Number(raw[field]) < 1) return undefined;
  }
  for (const field of ["executor_policy_sha256", "config_sha256", "advertised_endpoint_sha256"] as const) if (!validSystemUpdateDigest(stringValue(raw[field]))) return undefined;
  if (!validPort(Number(raw.local_listen_port), 1024) || !validPort(Number(raw.advertised_port), 1)) return undefined;
  const docker = recordValue(raw.docker);
  if (raw.docker !== undefined && (docker.published_host_ip !== "127.0.0.1" || docker.published_port !== raw.local_listen_port
    || docker.health_port !== docker.published_port || !validPort(Number(docker.container_port), 1024)
    || !Number.isSafeInteger(docker.compose_revision) || Number(docker.compose_revision) < 1
    || ["compose_policy_sha256", "version_env_sha256", "image_id", "repository_digest"].some((field) => !validSystemUpdateDigest(stringValue(docker[field]))))) return undefined;
  return {
    snapshot_id: stringValue(raw.snapshot_id), snapshot_sha256: digest,
    source_policy_revision: Number(raw.source_policy_revision), projection_revision: Number(raw.projection_revision),
    executor_policy_revision: Number(raw.executor_policy_revision), executor_policy_sha256: stringValue(raw.executor_policy_sha256),
    endpoint_revision: Number(raw.endpoint_revision), applied_endpoint_revision: Number(raw.applied_endpoint_revision),
    config_revision: Number(raw.config_revision), config_sha256: stringValue(raw.config_sha256),
    advertised_port: Number(raw.advertised_port), advertised_endpoint_sha256: stringValue(raw.advertised_endpoint_sha256),
    local_listen_port: Number(raw.local_listen_port),
    docker: raw.docker === undefined ? undefined : {
      published_host_ip: "127.0.0.1", published_port: Number(docker.published_port), container_port: Number(docker.container_port),
      health_port: Number(docker.health_port), compose_policy_sha256: stringValue(docker.compose_policy_sha256),
      compose_revision: Number(docker.compose_revision), version_env_sha256: stringValue(docker.version_env_sha256),
      image_id: stringValue(docker.image_id), repository_digest: stringValue(docker.repository_digest),
    },
  };
}

function normalizeSystemUpdatePortObservation(value: unknown): SystemUpdatePortObservation | undefined {
  const raw = recordValue(value);
  const fields = ["policy_disk_verified", "policy_memory_verified", "agent_projection_verified", "listener_verified"] as const;
  if (fields.some((field) => typeof raw[field] !== "boolean") || typeof raw.observed_at !== "string" || !Number.isFinite(Date.parse(raw.observed_at))) return undefined;
  return { policy_disk_verified: raw.policy_disk_verified === true, policy_memory_verified: raw.policy_memory_verified === true,
    agent_projection_verified: raw.agent_projection_verified === true, listener_verified: raw.listener_verified === true, observed_at: raw.observed_at };
}

export function normalizeSystemUpdatePortRecoveryObservation(value: unknown): SystemUpdateJob["last_recovery_observation"] {
  const raw = recordValue(value); const observation = normalizeSystemUpdatePortObservation(raw.observation);
  if (raw.result !== "rollback_failed" || !observation) return undefined;
  return { result: "rollback_failed", observation };
}

export function normalizeSystemUpdatePortResult(value: unknown, plan: SystemUpdatePortReconfiguration | undefined, status: string): SystemUpdatePortResultV2 | undefined {
  const raw = recordValue(value); const observation = normalizeSystemUpdatePortObservation(raw.observation);
  if (plan?.port_contract_version !== 2 || !observation || !observation.policy_disk_verified || !observation.policy_memory_verified
    || !observation.agent_projection_verified || !observation.listener_verified) return undefined;
  const result = raw.result;
  if (result !== "applied" && result !== "unchanged" && result !== "rolled_back") return undefined;
  if (status !== (result === "rolled_back" ? "rolled_back" : "succeeded")) return undefined;
  const noOp = Boolean(plan.before && plan.target && plan.rollback
    && JSON.stringify(plan.before) === JSON.stringify(plan.target) && JSON.stringify(plan.before) === JSON.stringify(plan.rollback));
  if (result === "unchanged" ? !noOp : noOp) return undefined;
  const expected = result === "applied" ? plan.target : result === "rolled_back" ? plan.rollback : plan.before;
  if (!expected) return undefined;
  if (raw.observed_snapshot_id !== expected.snapshot_id || raw.observed_snapshot_sha256 !== expected.snapshot_sha256
    || raw.observed_config_revision !== expected.config_revision || raw.observed_config_sha256 !== expected.config_sha256
    || raw.observed_executor_policy_revision !== expected.executor_policy_revision || raw.observed_executor_policy_sha256 !== expected.executor_policy_sha256) return undefined;
  const runtime = recordValue(raw.runtime_instance);
  if (expected.docker && (!/^[a-f0-9]{12,64}$/.test(stringValue(runtime.container_id)) || runtime.image_id !== expected.docker.image_id || runtime.repository_digest !== expected.docker.repository_digest)) return undefined;
  return { result, observed_snapshot_id: expected.snapshot_id, observed_snapshot_sha256: expected.snapshot_sha256,
    observed_config_revision: expected.config_revision, observed_config_sha256: expected.config_sha256,
    observed_executor_policy_revision: expected.executor_policy_revision, observed_executor_policy_sha256: expected.executor_policy_sha256,
    observation, runtime_instance: expected.docker ? { container_id: stringValue(runtime.container_id), image_id: stringValue(runtime.image_id), repository_digest: stringValue(runtime.repository_digest) } : undefined };
}

export function validSystemUpdateDigest(value: string) {
  return /^sha256:[a-f0-9]{64}$/.test(value);
}
