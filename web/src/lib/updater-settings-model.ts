import type { SystemUpdateTarget, UpdaterSettings, UpdaterSettingsHost, UpdaterSettingsTarget } from "@/types/domain";
import { recordValue, stringValue, positiveIntegerValue, optionalNonNegativeIntegerValue, nonNegativeIntegerValue } from "./system-update-values";

const supportedUpdaterServiceTypes = new Set([
  "control_panel",
  "encoder_recorder",
  "observability",
  "discord_bot",
  "worker",
]);

const updaterDatabaseOwnerServiceTypes = new Set(["control_panel", "observability"]);

const updaterDatabaseNamePattern = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/;

const standardSystemdLocalListenPorts: Record<string, number> = {
  encoder_recorder: 8081,
  observability: 8082,
  discord_bot: 8083,
  worker: 8084,
};

export function isUpdaterPolicyHostID(value: string) {
  return /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(value);
}

export function updaterSettingsTargetRequiresDatabase(
  transportMode: UpdaterSettings["transport_mode"],
  target: Pick<UpdaterSettingsTarget, "service_type" | "deployment_mode">,
) {
  return transportMode === "pull_v2"
    && target.deployment_mode.trim() === "systemd"
    && updaterDatabaseOwnerServiceTypes.has(target.service_type.trim());
}

export function updaterSettingsTargetRequiresLocalListenPort(
  transportMode: UpdaterSettings["transport_mode"],
  target: Pick<UpdaterSettingsTarget, "service_type" | "deployment_mode">,
) {
  return transportMode === "pull_v2"
    && target.deployment_mode.trim() === "systemd"
    && target.service_type.trim() !== "control_panel";
}

export function normalizeUpdaterSettingsTargetLocalListenPort(
  transportMode: UpdaterSettings["transport_mode"],
  target: Pick<UpdaterSettingsTarget, "service_type" | "deployment_mode" | "local_listen_port">,
  label: string,
) {
  if (!updaterSettingsTargetRequiresLocalListenPort(transportMode, target)) {
    if (target.local_listen_port !== undefined) {
      throw new Error(`${label}ではローカル待受ポートを指定できません。`);
    }
    return undefined;
  }
  const port = Number(target.local_listen_port);
  if (!Number.isInteger(port) || port < 1024 || port > 65535) {
    throw new Error(`${label}のローカル待受ポートは1024〜65535の整数で入力してください。`);
  }
  return port;
}

export function normalizeUpdaterSettingsTargetDatabaseName(
  transportMode: UpdaterSettings["transport_mode"],
  target: Pick<UpdaterSettingsTarget, "service_type" | "deployment_mode" | "database_name">,
  label: string,
) {
  const databaseName = String(target.database_name || "").trim();
  if (!updaterSettingsTargetRequiresDatabase(transportMode, target)) {
    if (databaseName) {
      throw new Error(`${label}ではMariaDBデータベース名を指定できません。`);
    }
    return undefined;
  }
  if (!databaseName) {
    throw new Error(`${label}のMariaDBデータベース名を入力してください。`);
  }
  if (!updaterDatabaseNamePattern.test(databaseName)) {
    throw new Error(`${label}のMariaDBデータベース名は英数字で始まり、英数字・_・-の64文字以内で入力してください。`);
  }
  return databaseName;
}

export function applyUpdaterSettingsTargetPatch(
  transportMode: UpdaterSettings["transport_mode"],
  target: UpdaterSettingsTarget,
  patch: Partial<UpdaterSettingsTarget>,
) {
  const nextTarget = { ...target, ...patch };
  const identityChanged = (
    (patch.target_id !== undefined && patch.target_id !== target.target_id)
    || (patch.service_id !== undefined && patch.service_id !== target.service_id)
    || (patch.host_id !== undefined && patch.host_id !== target.host_id)
    || (patch.service_type !== undefined && patch.service_type !== target.service_type)
  );
  if (identityChanged || !updaterSettingsTargetRequiresDatabase(transportMode, nextTarget)) {
    delete nextTarget.database_name;
  }
  if (identityChanged || !updaterSettingsTargetRequiresLocalListenPort(transportMode, nextTarget)) {
    delete nextTarget.local_listen_port;
  }
  return nextTarget;
}

export type UpdaterSettingsTargetOption = {
  value: string;
  label: string;
  serviceType: string;
  current: boolean;
  stale: boolean;
};

export function updaterSettingsTargetOptions(
  availableTargets: SystemUpdateTarget[],
  configuredTargets: UpdaterSettingsTarget[],
  currentIndex: number,
): UpdaterSettingsTargetOption[] {
  const currentTarget = configuredTargets[currentIndex];
  const currentID = updaterSettingsTargetID(currentTarget);
  const usedByOtherTargets = new Set<string>();
  configuredTargets.forEach((target, index) => {
    if (index === currentIndex) return;
    updaterSettingsTargetIDs(target).forEach((targetID) => usedByOtherTargets.add(targetID));
  });

  const options = selectableUpdaterSettingsTargets(availableTargets)
    .filter((target) => target.target_id === currentID || !usedByOtherTargets.has(target.target_id))
    .map((target): UpdaterSettingsTargetOption => {
      const current = target.target_id === currentID;
      const name = target.name.trim() || target.target_id;
      return {
        value: target.target_id,
        label: `${name}（${target.target_id}）${current ? "（現在の設定）" : ""}`,
        serviceType: target.target_type,
        current,
        stale: false,
      };
    });

  if (currentID && !options.some((option) => option.value === currentID)) {
    options.push({
      value: currentID,
      label: `現在の設定（登録対象に未検出）: ${currentID}`,
      serviceType: currentTarget?.service_type.trim() || "",
      current: true,
      stale: true,
    });
  }
  return options;
}

export function firstUnusedUpdaterSettingsTarget(
  transportMode: UpdaterSettings["transport_mode"],
  availableTargets: SystemUpdateTarget[],
  configuredTargets: UpdaterSettingsTarget[],
  hostID: string,
): UpdaterSettingsTarget | undefined {
  const usedTargetIDs = new Set(configuredTargets.flatMap(updaterSettingsTargetIDs));
  const candidate = selectableUpdaterSettingsTargets(availableTargets)
    .find((target) => !usedTargetIDs.has(target.target_id));
  if (!candidate) return undefined;
  const target: UpdaterSettingsTarget = {
    target_id: candidate.target_id,
    service_id: candidate.target_id,
    host_id: hostID,
    service_type: candidate.target_type,
    deployment_mode: "systemd",
  };
  const standardPort = standardSystemdLocalListenPorts[candidate.target_type];
  if (transportMode === "pull_v2" && standardPort !== undefined) {
    target.local_listen_port = standardPort;
  }
  return target;
}

export function applyUpdaterSettingsTargetSelection(
  transportMode: UpdaterSettings["transport_mode"],
  target: UpdaterSettingsTarget,
  selectedTarget: Pick<SystemUpdateTarget, "target_id" | "target_type">,
) {
  const targetID = selectedTarget.target_id.trim();
  const serviceType = selectedTarget.target_type.trim();
  if (!targetID || !supportedUpdaterServiceTypes.has(serviceType)) return target;
  const selected = applyUpdaterSettingsTargetPatch(transportMode, target, {
    target_id: targetID,
    service_id: targetID,
    service_type: serviceType,
  });
  if (updaterSettingsTargetRequiresLocalListenPort(transportMode, selected)) {
    selected.local_listen_port = standardSystemdLocalListenPorts[serviceType];
  }
  return selected;
}

function selectableUpdaterSettingsTargets(availableTargets: SystemUpdateTarget[]) {
  const seenTargetIDs = new Set<string>();
  return availableTargets.flatMap((target) => {
    const targetID = target.target_id.trim();
    const targetType = target.target_type.trim();
    if (!targetID || !supportedUpdaterServiceTypes.has(targetType) || seenTargetIDs.has(targetID)) return [];
    seenTargetIDs.add(targetID);
    return [{ ...target, target_id: targetID, target_type: targetType }];
  });
}

function updaterSettingsTargetID(target?: Pick<UpdaterSettingsTarget, "target_id" | "service_id">) {
  return String(target?.service_id || target?.target_id || "").trim();
}

function updaterSettingsTargetIDs(target: Pick<UpdaterSettingsTarget, "target_id" | "service_id">) {
  return [...new Set([target.target_id.trim(), target.service_id.trim()].filter(Boolean))];
}

export function emptyUpdaterSettings(updaterID: string): UpdaterSettings {
  return {
    updater_id: updaterID,
    revision: 0,
    transport_mode: "pull_v2",
    execution_host_id: "",
    local_executor_policy_sha256: "",
    api: {
      bind_host: "127.0.0.1",
      host: "127.0.0.1",
      port: 8090,
      ssl_enabled: false,
      tls_cert_file: "",
      tls_key_file: "",
    },
    poll_interval_seconds: 15,
    heartbeat_interval_seconds: 30,
    hosts: [],
    targets: [],
    github_token_configured: false,
    github_token_fingerprint: "",
    updated_at: "",
  };
}

export function normalizeUpdaterSettingsResponse(value: unknown, fallbackUpdaterID = ""): UpdaterSettings {
  const settings = recordValue(value);
  if (settings.transport_mode !== "pull_v2") {
    throw new Error("invalid_updater_settings_transport");
  }
  const updaterID = stringValue(settings.updater_id) || fallbackUpdaterID;
  const defaults = emptyUpdaterSettings(updaterID);
  const api = recordValue(settings.api);
  const executionHostOwnership = recordValue(settings.execution_host_ownership);
  if (Object.keys(executionHostOwnership).length > 0 && executionHostOwnership.transport_mode !== "pull_v2") {
    throw new Error("invalid_updater_settings_ownership");
  }
  const pullActivation = recordValue(settings.pull_activation);
  const hosts = Array.isArray(settings.hosts)
    ? settings.hosts.map((value) => {
      const host = recordValue(value);
      const fingerprint = stringValue(host.host_key_fingerprint || host.host_public_key_fingerprint);
      return {
        host_id: stringValue(host.host_id),
        name: stringValue(host.name || host.host_id),
        address: stringValue(host.address),
        port: positiveIntegerValue(host.port, 22),
        user: stringValue(host.user) || "autostream-update-host",
        arch: stringValue(host.arch) || "amd64",
        host_public_key: stringValue(host.host_public_key),
        host_key_fingerprint: fingerprint,
        host_public_key_fingerprint: fingerprint,
        ssh_client_public_key: stringValue(host.ssh_client_public_key),
        ssh_client_key_fingerprint: stringValue(host.ssh_client_key_fingerprint),
      };
    }).filter((host) => host.host_id)
    : [];
  const targets = Array.isArray(settings.targets)
    ? settings.targets.map((value) => {
      const target = recordValue(value);
      return {
        target_id: stringValue(target.target_id),
        service_id: stringValue(target.service_id || target.target_id),
        host_id: stringValue(target.host_id),
        service_type: stringValue(target.service_type || target.target_type),
        deployment_mode: stringValue(target.deployment_mode),
        database_name: stringValue(target.database_name) || undefined,
        local_listen_port: optionalNonNegativeIntegerValue(target.local_listen_port),
      };
    }).filter((target) => target.target_id)
    : [];
  return {
    updater_id: updaterID,
    revision: nonNegativeIntegerValue(settings.revision, 0),
    projection_revision: optionalNonNegativeIntegerValue(settings.projection_revision),
    local_executor_policy_revision: optionalNonNegativeIntegerValue(settings.local_executor_policy_revision),
    transport_mode: "pull_v2",
    execution_host_id: stringValue(settings.execution_host_id),
    execution_host_ownership: Object.keys(executionHostOwnership).length > 0
      ? {
          transport_mode: "pull_v2",
          agent_service_id: stringValue(executionHostOwnership.agent_service_id),
          ownership_epoch: nonNegativeIntegerValue(executionHostOwnership.ownership_epoch, -1),
          policy_revision: nonNegativeIntegerValue(executionHostOwnership.policy_revision, -1),
        }
      : undefined,
    pull_activation: Object.keys(pullActivation).length > 0
      ? {
          ready: pullActivation.ready === true,
          blocked_reason: stringValue(pullActivation.blocked_reason),
          status: stringValue(pullActivation.status),
          last_heartbeat_at: stringValue(pullActivation.last_heartbeat_at),
          observe_only: pullActivation.observe_only === true,
          update_executor: pullActivation.update_executor === true,
          mutation_enabled: pullActivation.mutation_enabled === true,
          recovery_pending: pullActivation.recovery_pending === true,
          reported_ownership_epoch: nonNegativeIntegerValue(pullActivation.reported_ownership_epoch, -1),
          reported_projection_revision: nonNegativeIntegerValue(pullActivation.reported_projection_revision, -1),
        }
      : undefined,
    local_executor_policy_sha256: stringValue(settings.local_executor_policy_sha256),
    api: {
      bind_host: stringValue(api.bind_host) || defaults.api.bind_host,
      host: stringValue(api.host) || defaults.api.host,
      port: positiveIntegerValue(api.port, defaults.api.port),
      ssl_enabled: api.ssl_enabled === true,
      tls_cert_file: stringValue(api.tls_cert_file),
      tls_key_file: stringValue(api.tls_key_file),
    },
    poll_interval_seconds: positiveIntegerValue(settings.poll_interval_seconds, defaults.poll_interval_seconds),
    heartbeat_interval_seconds: positiveIntegerValue(settings.heartbeat_interval_seconds, defaults.heartbeat_interval_seconds),
    hosts,
    targets,
    github_token_configured: settings.github_token_configured === true,
    github_token_fingerprint: stringValue(settings.github_token_fingerprint),
    updated_at: stringValue(settings.updated_at),
  };
}

export function sameUpdaterSettingsHost(left: UpdaterSettingsHost, right: UpdaterSettingsHost) {
  return left.host_id.trim() === right.host_id.trim()
    && left.name.trim() === right.name.trim()
    && left.address.trim() === right.address.trim()
    && Number(left.port) === Number(right.port)
    && left.user.trim() === right.user.trim()
    && left.arch.trim() === right.arch.trim()
    && left.host_public_key.trim() === right.host_public_key.trim();
}

export function sameStringSet(left: string[], right: string[]) {
  if (left.length !== right.length) return false;
  const normalizedLeft = [...left].sort();
  const normalizedRight = [...right].sort();
  return normalizedLeft.every((value, index) => value === normalizedRight[index]);
}
