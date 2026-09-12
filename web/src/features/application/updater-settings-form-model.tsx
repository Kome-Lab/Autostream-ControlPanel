"use client";

import { isUpdaterPolicyHostID, normalizeUpdaterSettingsTargetDatabaseName, normalizeUpdaterSettingsTargetLocalListenPort } from "@/lib/updater-settings-model";
import { systemUpdateErrorMessage } from "@/lib/system-update-presentation";
import type { PullUpdaterOwnershipDeactivationRequest, UpdaterSettings, UpdaterSettingsHost, UpdaterSettingsTarget, UpdaterSettingsUpdate } from "@/types/domain";

export type UpdaterSettingsFormState = {
  pollInterval: string;
  heartbeatInterval: string;
  localExecutorPolicySHA256: string;
  hosts: UpdaterSettingsHost[];
  targets: UpdaterSettingsTarget[];
};

export type PullOwnershipDeactivationAttempt = {
  request: PullUpdaterOwnershipDeactivationRequest;
};

const serviceTypes = [
  { value: "control_panel", label: "Control Panel" },
  { value: "observability", label: "Observability" },
  { value: "worker", label: "Worker" },
  { value: "encoder_recorder", label: "Encoder / Recorder" },
  { value: "discord_bot", label: "Discord Bot" },
] as const;

export const deploymentModes = [
  { value: "systemd", label: "systemd" },
  { value: "docker", label: "Docker" },
] as const;

export function settingsToForm(settings: UpdaterSettings): UpdaterSettingsFormState {
  return {
    pollInterval: String(settings.poll_interval_seconds || 15),
    heartbeatInterval: String(settings.heartbeat_interval_seconds || 30),
    localExecutorPolicySHA256: settings.local_executor_policy_sha256 || "",
    hosts: settings.hosts.map((host) => ({ ...host })),
    targets: settings.targets.map((target) => ({ ...target })),
  };
}

export function buildUpdaterSettingsPayload(
  expectedRevision: number,
  form: UpdaterSettingsFormState,
  settings: UpdaterSettings,
  secretUpdate?: { githubToken: string; deleteGitHubToken: boolean },
): UpdaterSettingsUpdate {
  const pollInterval = requiredInterval(form.pollInterval, "更新確認間隔");
  const heartbeatInterval = requiredHeartbeatInterval(form.heartbeatInterval);
  const executionHostID = String(settings.execution_host_id || "").trim();
  if (!executionHostID) throw new Error("Host Agentの実行ホスト割り当てがありません。Nodeを再登録してください。");
  if (form.hosts.length > 128) throw new Error("bootstrap対象ホストは128件まで登録できます。");
  if (form.targets.length === 0) throw new Error("更新するサービスを1件以上追加してください。");
  if (form.targets.length > 1024) throw new Error("更新するサービスは1024件まで登録できます。");

  const hosts = form.hosts.map((host, index) => normalizeHostForSave(host, index));
  if (new Set(hosts.map((host) => host.host_id)).size !== hosts.length) throw new Error("ホストIDが重複しています。");
  const executionHostIDs = new Set([executionHostID]);
  const targets = form.targets.map((target, index) => normalizeTargetForSave(
    { ...target, host_id: executionHostID },
    index,
    executionHostIDs,
    "pull_v2",
  ));
  if (new Set(targets.map((target) => target.target_id)).size !== targets.length) throw new Error("更新対象IDが重複しています。");
  if (new Set(targets.map((target) => target.service_id)).size !== targets.length) throw new Error("NodeサービスIDが重複しています。");
  const digest = form.localExecutorPolicySHA256.trim().toLowerCase();
  if (digest && !/^sha256:[0-9a-f]{64}$/.test(digest)) {
    throw new Error("Local Executor policy SHA-256は sha256: に続く64桁の16進数で入力してください。");
  }
  const payload: UpdaterSettingsUpdate = {
    expected_revision: expectedRevision,
    poll_interval_seconds: pollInterval,
    heartbeat_interval_seconds: heartbeatInterval,
    hosts,
    targets,
    local_executor_policy_sha256: digest,
  };
  if (secretUpdate?.deleteGitHubToken) payload.github_token = "";
  else if (secretUpdate?.githubToken.trim()) payload.github_token = secretUpdate.githubToken.trim();
  return payload;
}

function normalizeHostForSave(host: UpdaterSettingsHost, index: number): UpdaterSettingsHost {
  const prefix = `ホスト ${index + 1}`;
  const hostID = requiredText(host.host_id, `${prefix}のホストID`);
  if (!isUpdaterPolicyHostID(hostID)) throw new Error(`${prefix}のホストIDは英数字で始まり、英数字・.・_・-のみで入力してください。`);
  const address = requiredText(host.address, `${prefix}のIPアドレス / ホスト名`);
  if (address.includes("://")) throw new Error(`${prefix}の接続先にはURLではなくIPアドレスまたはホスト名を入力してください。`);
  const hostPublicKey = requiredText(host.host_public_key, `${prefix}のSSHホスト公開鍵`);
  if (!/^ssh-ed25519\s+[A-Za-z0-9+/=]+(?:\s+.*)?$/.test(hostPublicKey)) {
    throw new Error(`${prefix}のSSHホスト公開鍵はssh-ed25519のOpenSSH形式全文を入力してください。`);
  }
  const name = requiredText(host.name, `${prefix}の表示名`);
  if ([...name].length > 128) throw new Error(`${prefix}の表示名は128文字以内で入力してください。`);
  const user = requiredText(host.user, `${prefix}のSSHユーザー`);
  if (!/^[a-z_][a-z0-9_-]{0,31}$/.test(user) || user === "root") {
    throw new Error(`${prefix}のSSHユーザーにはroot以外のLinuxユーザー名を入力してください。`);
  }
  return {
    host_id: hostID,
    name,
    address,
    port: requiredPort(host.port, `${prefix}のSSHポート`),
    user,
    arch: requiredText(host.arch, `${prefix}のCPUアーキテクチャ`),
    host_public_key: hostPublicKey,
  };
}

function normalizeTargetForSave(
  target: UpdaterSettingsTarget,
  index: number,
  hostIDs: Set<string>,
  transportMode: UpdaterSettings["transport_mode"],
): UpdaterSettingsTarget {
  const prefix = `サービス ${index + 1}`;
  const hostID = requiredText(target.host_id, `${prefix}のホスト`);
  if (!hostIDs.has(hostID)) throw new Error(`${prefix}で選択したホストが見つかりません。`);
  const targetID = requiredText(target.target_id, `${prefix}の対象ID`);
  if (!validPolicyIdentifier(targetID)) throw new Error(`${prefix}の対象IDは英数字で始まり、英数字・.・_・:・-のみで入力してください。`);
  const serviceID = requiredText(target.service_id || targetID, `${prefix}のNodeサービスID`);
  if (!validPolicyIdentifier(serviceID)) throw new Error(`${prefix}のNodeサービスIDは英数字で始まり、英数字・.・_・:・-のみで入力してください。`);
  const normalizedTarget: UpdaterSettingsTarget = {
    target_id: targetID,
    service_id: serviceID,
    host_id: hostID,
    service_type: requiredText(target.service_type, `${prefix}のサービス種別`),
    deployment_mode: requiredText(target.deployment_mode, `${prefix}の配備方式`),
  };
  const databaseName = normalizeUpdaterSettingsTargetDatabaseName(
    transportMode,
    { ...normalizedTarget, database_name: target.database_name },
    prefix,
  );
  if (databaseName) normalizedTarget.database_name = databaseName;
  const localListenPort = normalizeUpdaterSettingsTargetLocalListenPort(
    transportMode,
    { ...normalizedTarget, local_listen_port: target.local_listen_port },
    prefix,
  );
  if (localListenPort !== undefined) normalizedTarget.local_listen_port = localListenPort;
  return normalizedTarget;
}

function requiredText(value: unknown, label: string) {
  const text = String(value || "").trim();
  if (!text) throw new Error(`${label}を入力してください。`);
  return text;
}

function validPolicyIdentifier(value: string) {
  return /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value);
}

function requiredPort(value: unknown, label: string) {
  const number = Number(value);
  if (!Number.isInteger(number) || number < 1 || number > 65535) throw new Error(`${label}は1〜65535の整数で入力してください。`);
  return number;
}

function requiredInterval(value: unknown, label: string) {
  const number = Number(value);
  if (!Number.isInteger(number) || number < 5 || number > 3600) throw new Error(`${label}は5〜3600秒の整数で入力してください。`);
  return number;
}

function requiredHeartbeatInterval(value: unknown) {
  const number = Number(value);
  if (!Number.isInteger(number) || number < 5 || number > 60) {
    throw new Error("Heartbeat間隔は5〜60秒の整数で入力してください。現在の値を5〜60秒に変更してから保存してください。");
  }
  return number;
}

export function newHost(index: number, preferredHostID: string): UpdaterSettingsHost {
  return {
    host_id: preferredHostID || `host-${index + 1}`,
    name: `ホスト ${index + 1}`,
    address: "",
    port: 22,
    user: "autostream-update-host",
    arch: "amd64",
    host_public_key: "",
  };
}

export function serviceTypeLabel(value: string) {
  return serviceTypes.find((option) => option.value === value)?.label || value || "未設定";
}

export function selectOptionsWithCurrent(
  options: readonly { value: string; label: string }[],
  current: string,
) {
  if (!current || options.some((option) => option.value === current)) return options;
  return [...options, { value: current, label: current }];
}

export function updaterSettingsErrorMessage(error: unknown) {
  if (error instanceof Error && !("status" in error)) return error.message;
  return systemUpdateErrorMessage(error, "Updater設定を保存できませんでした。入力内容と権限を確認してください。");
}
