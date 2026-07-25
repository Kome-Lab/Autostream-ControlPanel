import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { auditActionLabel } from "../src/lib/audit-action.ts";
import {
  compareSystemUpdateVersions,
  isControlPanelUpdateTarget,
  isSystemUpdateJobActive,
  isSystemUpdateJobCancellable,
  isUpdaterPolicyHostID,
  normalizeUpdaterSettingsResponse,
  normalizeSystemUpdatesResponse,
  requestSystemUpdateWithRecovery,
  runSystemUpdatesSequentially,
  systemUpdateDeploymentLabel,
  systemUpdateErrorMessage,
  systemUpdateConnectivity,
  systemUpdateHostReachabilityLabel,
  systemUpdateHostReachabilityMessage,
  systemUpdateJobStatusLabel,
  systemUpdateMayDisconnectPanel,
  systemUpdatePolicyErrorMessage,
  systemUpdateJobFromResponse,
  systemUpdateUpdaterPolicyState,
  systemUpdateProgress,
  systemUpdateRequest,
  systemUpdateStrategyForTarget,
  systemUpdateTargetBlockedReason,
} from "../src/lib/system-updates.ts";
import type { SystemUpdateAgentStatus, SystemUpdateHostStatus, SystemUpdateTarget, UpdaterSettings } from "../src/types/domain.ts";
import { mockGet, mockPost, mockPut } from "../src/features/mock-data.ts";
import {
  canRegenerateNodeConfigureToken,
  canIssueNodeConfiguration,
  canRotateNodeRuntimeToken,
} from "../src/lib/node-configuration.ts";

const baseTarget: SystemUpdateTarget = {
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
};

test("an active stream is always queued with the when_idle strategy", () => {
  const target = { ...baseTarget, current_stream_id: "stream-live" };

  assert.equal(systemUpdateStrategyForTarget(target), "when_idle");
  assert.deepEqual(systemUpdateRequest(target, "request-1"), {
    target_id: "worker-main",
    strategy: "when_idle",
    idempotency_key: "request-1",
  });
  assert.equal(systemUpdateStrategyForTarget(baseTarget), "maintenance");
  assert.equal(systemUpdateStrategyForTarget({ ...baseTarget, busy: false, current_stream_id: "stale-stream" }), "maintenance");
});

test("bulk update requests are created sequentially", async () => {
  const targets = [baseTarget, { ...baseTarget, target_id: "worker-standby" }, { ...baseTarget, target_id: "control-panel", target_type: "control_panel" }];
  const order: string[] = [];
  let active = 0;
  let maxActive = 0;

  const results = await runSystemUpdatesSequentially(targets, async (target) => {
    active += 1;
    maxActive = Math.max(maxActive, active);
    order.push(target.target_id);
    await new Promise((resolve) => setTimeout(resolve, 1));
    active -= 1;
    return target.target_id;
  });

  assert.equal(maxActive, 1);
  assert.deepEqual(order, ["worker-main", "worker-standby", "control-panel"]);
  assert.deepEqual(results, order);
});

test("response loss recovers the committed job with the same idempotency key", async () => {
  const key = "web-worker-main-stable-operation";
  const requests: Array<{ idempotency_key: string }> = [];
  const committed = {
    id: "job-response-loss",
    idempotency_key: key,
    target_id: baseTarget.target_id,
    target_type: baseTarget.target_type,
    status: "queued",
    created_at: "2026-07-18T00:00:00Z",
    updated_at: "2026-07-18T00:00:00Z",
  };
  const recovered = await requestSystemUpdateWithRecovery(
    baseTarget,
    key,
    async (request) => {
      requests.push(request);
      throw new Error("response_lost_after_commit");
    },
    async () => [committed],
  );
  assert.equal(recovered.id, committed.id);
  assert.equal(recovered.idempotency_key, key);
  assert.deepEqual(requests.map((request) => request.idempotency_key), [key]);

  const retryRequests: string[] = [];
  await assert.rejects(() => requestSystemUpdateWithRecovery(
    baseTarget,
    key,
    async (request) => { retryRequests.push(request.idempotency_key); throw new Error("network_down"); },
    async () => [],
  ));
  const retried = await requestSystemUpdateWithRecovery(
    baseTarget,
    key,
    async (request) => { retryRequests.push(request.idempotency_key); return committed; },
    async () => [],
  );
  assert.equal(retried.id, committed.id);
  assert.deepEqual(retryRequests, [key, key]);
});

test("control panel targets and job lifecycle states are classified", () => {
  assert.equal(isControlPanelUpdateTarget({ target_id: "control-panel", target_type: "control_panel" }), true);
  assert.equal(isSystemUpdateJobActive("restarting"), true);
  assert.equal(isSystemUpdateJobActive("staging"), true);
  assert.equal(isSystemUpdateJobActive("applying"), true);
  assert.equal(isSystemUpdateJobActive("reconciling"), true);
  assert.equal(isSystemUpdateJobActive("succeeded"), false);
  assert.equal(isSystemUpdateJobCancellable("queued"), true);
  assert.equal(isSystemUpdateJobCancellable("claimed"), false);
  assert.equal(isSystemUpdateJobCancellable("installing"), false);
  assert.equal(systemUpdateJobStatusLabel("health_checking"), "動作確認中");
  assert.equal(systemUpdateJobStatusLabel("reconciling"), "適用状態を確認中");
  assert.equal(systemUpdateMayDisconnectPanel("queued"), false);
  assert.equal(systemUpdateMayDisconnectPanel("downloading"), false);
  assert.equal(systemUpdateMayDisconnectPanel("stopping"), true);
  assert.equal(systemUpdateMayDisconnectPanel("restarting"), true);
});

test("service update versions follow SemVer prerelease precedence", () => {
  const cases: Array<[string, string, -1 | 0 | 1 | null]> = [
    ["v1.2.3-rc.1", "v1.2.3", -1],
    ["v1.2.3-rc.1", "v1.2.3-rc.2", -1],
    ["v1.2.3", "v1.2.3-rc.2", 1],
    ["v1.2.3-alpha", "v1.2.3-1", 1],
    ["v1.2.3-rc.1", "v1.2.3-rc.1.1", -1],
    ["v1.2.3+build.1", "v1.2.3+build.2", 0],
    ["v1.2.3-rc.01", "v1.2.3-rc.1", null],
    ["dev", "v1.2.3", null],
  ];
  for (const [current, latest, expected] of cases) {
    assert.equal(compareSystemUpdateVersions(current, latest), expected, `${current} vs ${latest}`);
  }
});

test("wire responses are normalized across the public and legacy field names", () => {
  const response = normalizeSystemUpdatesResponse({
    updaters: [{ updater_id: "updater-1", name: "Central Updater", status: "online", online: true, version: "v1.7.0", last_heartbeat_at: "2026-07-18T00:00:00Z" }],
    hosts: [{ host_id: "host-main", name: "Main Host", updater_id: "updater-1", reachability: "reachable", reachability_checked_at: "2026-07-18T00:00:00Z" }],
    targets: [{ target_id: "worker-main", service_type: "worker", name: "Worker", host_id: "host-main", update_agent_id: "updater-1", updater_online: true, eligible: true, update_available: true, update_check_source: "github_release", update_check_error: "rate_limited" }],
    jobs: [{ id: "job-1", idempotency_key: "request-1", target_id: "worker-main", target_service_type: "worker", requested_by_username: "ops", status: "queued", progress: 0, sequence: 3, lease_generation: 2, created_at: "2026-07-18T00:00:00Z", updated_at: "2026-07-18T00:00:00Z" }],
  });
  assert.equal(response.targets[0].target_type, "worker");
  assert.equal(response.targets[0].host_id, "host-main");
  assert.equal(response.targets[0].updater_id, "updater-1");
  assert.equal(response.targets[0].updater_online, true);
  assert.deepEqual(response.updaters[0], { updater_id: "updater-1", name: "Central Updater", status: "online", online: true, version: "v1.7.0", last_heartbeat_at: "2026-07-18T00:00:00Z" });
  assert.deepEqual(response.hosts[0], { host_id: "host-main", name: "Main Host", updater_id: "updater-1", reachability: "reachable", reachability_checked_at: "2026-07-18T00:00:00Z", reachability_code: "" });
  assert.equal(response.targets[0].update_check_source, "github_release");
  assert.equal(response.targets[0].update_check_error, "rate_limited");
  assert.equal(response.jobs[0].target_type, "worker");
  assert.equal(response.jobs[0].idempotency_key, "request-1");
  assert.equal(response.jobs[0].requested_by, "ops");
  assert.equal(response.jobs[0].sequence, 3);
  assert.equal(response.jobs[0].lease_generation, 2);
  assert.equal(systemUpdateJobFromResponse({ job: response.jobs[0] }).id, "job-1");

  const claimJob = systemUpdateJobFromResponse({
    job: { id: "claim-job", target_id: "worker-main", target_type: "worker", status: "reconciling", created_at: "2026-07-18T00:00:00Z", updated_at: "2026-07-18T00:00:00Z" },
    report_sequence: 4,
    lease_generation: 2,
    recovery_required: true,
    last_status: "installing",
  });
  assert.equal(claimJob.report_sequence, 4);
  assert.equal(claimJob.lease_generation, 2);
  assert.equal(claimJob.recovery_required, true);
  assert.equal(claimJob.last_status, "installing");

  const legacy = normalizeSystemUpdatesResponse({
    targets: [{ target_id: "legacy", target_type: "worker", name: "Legacy", update_agent_id: "legacy-updater", eligible: false, update_available: false }],
    jobs: [{ id: "legacy-job", target_id: "legacy", status: "queued", created_at: "2026-07-18T00:00:00Z", updated_at: "2026-07-18T00:00:00Z" }],
  });
  assert.equal(legacy.targets[0].update_check_source, "");
  assert.equal(legacy.targets[0].update_check_error, "");
  assert.equal(legacy.targets[0].host_id, "");
  assert.equal(legacy.targets[0].updater_online, false);
  assert.deepEqual(legacy.updaters, []);
  assert.deepEqual(legacy.hosts, []);
  assert.equal(legacy.jobs[0].sequence, undefined);
  assert.equal(legacy.jobs[0].report_sequence, undefined);
  assert.equal(legacy.jobs[0].lease_generation, undefined);
  assert.equal(legacy.jobs[0].recovery_required, undefined);
  assert.equal(legacy.jobs[0].last_status, "");
});

test("central updater policy status and generated SSH client keys are normalized fail closed", () => {
  const response = normalizeSystemUpdatesResponse({
    updaters: [{
      updater_id: "updater-main",
      name: "Central Updater",
      status: "online",
      online: true,
      desired_revision: 4,
      applied_revision: 3,
      policy_status: "pending",
      policy_error_code: "",
      ssh_client_public_keys: { "host-main": "ssh-ed25519 AAAATEST autostream-updater@central" },
      ssh_client_key_fingerprints: { "host-main": "SHA256:client-key" },
    }],
  });
  assert.equal(response.updaters[0].desired_revision, 4);
  assert.equal(response.updaters[0].applied_revision, 3);
  assert.equal(response.updaters[0].ssh_client_public_keys?.["host-main"], "ssh-ed25519 AAAATEST autostream-updater@central");
  assert.deepEqual(systemUpdateUpdaterPolicyState(response.updaters[0]), { label: "反映待ち", tone: "secondary", ready: false });
  assert.deepEqual(systemUpdateUpdaterPolicyState({ ...response.updaters[0], applied_revision: 4, policy_status: "applied" }), { label: "反映済み", tone: "default", ready: true });
  assert.deepEqual(systemUpdateUpdaterPolicyState({ ...response.updaters[0], policy_status: "failed" }), { label: "反映失敗", tone: "destructive", ready: false });
  assert.deepEqual(systemUpdateUpdaterPolicyState({ ...response.updaters[0], online: false }), { label: "オフライン", tone: "destructive", ready: false });
  assert.deepEqual(systemUpdateUpdaterPolicyState({ updater_id: "new", name: "New", status: "online", online: true, version: "" }), { label: "未設定", tone: "outline", ready: false });
  assert.match(systemUpdatePolicyErrorMessage("active_job_pending"), /処理完了後に自動で反映/);
});

test("updater settings response keeps only the panel-managed policy contract", () => {
  const settings = normalizeUpdaterSettingsResponse({
    updater_id: "updater-main",
    revision: 7,
    api: { bind_host: "127.0.0.1", host: "127.0.0.1", port: 8090, ssl_enabled: false },
    poll_interval_seconds: 30,
    heartbeat_interval_seconds: 15,
    hosts: [{
      host_id: "host-main",
      name: "Main",
      address: "10.0.0.10",
      port: 55850,
      user: "autostream-update-host",
      arch: "amd64",
      host_public_key: "ssh-ed25519 AAAAHOST root@main",
      host_public_key_fingerprint: "SHA256:host-key",
      ssh_client_public_key: "ssh-ed25519 AAAACLIENT autostream-updater@central",
      ssh_client_key_fingerprint: "SHA256:client-key",
    }],
    targets: [{ target_id: "worker-main", host_id: "host-main", service_type: "worker", deployment_mode: "systemd" }],
    github_token_configured: true,
    github_token_fingerprint: "sha256:token",
    updated_at: "2026-07-25T00:00:00Z",
    github_token: "must-not-be-returned",
    known_hosts_file: "/etc/autostream/updater/ssh/known_hosts",
  });

  assert.equal(settings.api.port, 8090);
  assert.equal(settings.hosts[0].host_public_key, "ssh-ed25519 AAAAHOST root@main");
  assert.equal(settings.hosts[0].host_key_fingerprint, "SHA256:host-key");
  assert.equal(settings.hosts[0].ssh_client_public_key, "ssh-ed25519 AAAACLIENT autostream-updater@central");
  assert.equal(settings.github_token_configured, true);
  assert.equal("github_token" in settings, false);
  assert.equal("known_hosts_file" in settings, false);
});

test("updater policy host IDs exclude colon while retaining safe separators", () => {
  assert.equal(isUpdaterPolicyHostID("host-main_01.example"), true);
  assert.equal(isUpdaterPolicyHostID("host:main"), false);
  assert.equal(isUpdaterPolicyHostID("-host-main"), false);
  assert.equal(isUpdaterPolicyHostID(`h${"a".repeat(127)}`), true);
  assert.equal(isUpdaterPolicyHostID(`h${"a".repeat(128)}`), false);
});

test("updater settings save preserves, deletes, and replaces the write-only GitHub token explicitly", () => {
  const path = "/system-updates/updaters/updater-central/settings";
  const current = mockGet(path) as UpdaterSettings;
  const request = {
    api: current.api,
    poll_interval_seconds: current.poll_interval_seconds,
    heartbeat_interval_seconds: current.heartbeat_interval_seconds,
    hosts: current.hosts,
    targets: current.targets,
  };

  const preserved = mockPut(path, { ...request, expected_revision: current.revision }) as UpdaterSettings;
  assert.equal(preserved.github_token_configured, true);
  const deleted = mockPut(path, { ...request, expected_revision: preserved.revision, github_token: "" }) as UpdaterSettings;
  assert.equal(deleted.github_token_configured, false);
  const replaced = mockPut(path, { ...request, expected_revision: deleted.revision, github_token: "github_pat_test" }) as UpdaterSettings;
  assert.equal(replaced.github_token_configured, true);
});

test("system update UI manages updater policy in the panel and never instructs manual trust files", () => {
  const applicationSource = readFileSync(new URL("../src/features/application/application-info-view.tsx", import.meta.url), "utf8");
  const settingsSource = readFileSync(new URL("../src/features/application/updater-settings-panel.tsx", import.meta.url), "utf8");
  const nodeSource = readFileSync(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url), "utf8");

  assert.match(applicationSource, /canManageUpdaterSecrets=\{hasPermission\(currentUser\.data, "secrets\.update"\)\}/);
  assert.match(applicationSource, /canEdit=\{canExecute && canManageUpdaterSecrets\}/);
  assert.match(settingsSource, /設定を保存/);
  assert.match(settingsSource, /Updaterが自動で反映/);
  assert.match(settingsSource, /別の安全な経路.*照合/s);
  assert.match(settingsSource, /設定の変更には system_updates\.execute と secrets\.update の両方の権限が必要/);
  assert.match(settingsSource, /system_updates\.execute/);
  assert.match(settingsSource, /secrets\.update/);
  assert.match(settingsSource, /GitHub Release Token/);
  assert.match(settingsSource, /公開・非公開Releaseのどちらでも必須/);
  assert.match(settingsSource, /host_public_key/);
  assert.match(settingsSource, /ssh_client_public_keys/);
  assert.match(settingsSource, /const \[baseRevision, setBaseRevision\] = useState\(settings\.revision\)/);
  assert.match(settingsSource, /expected_revision: expectedRevision/);
  assert.doesNotMatch(settingsSource, /expected_revision: settings\.revision/);
  assert.match(settingsSource, /\{ value: "docker", label: "Docker" \}/);
  assert.match(settingsSource, /min=\{5\}[\s\S]*max=\{3600\}/);
  const heartbeatField = settingsSource.match(/label="Heartbeat間隔（秒）"[\s\S]*?<\/Field>/)?.[0] ?? "";
  assert.match(heartbeatField, /hint="5〜60秒の範囲で設定してください。"/);
  assert.match(heartbeatField, /min=\{5\}[\s\S]*max=\{60\}/);
  assert.doesNotMatch(heartbeatField, /max=\{3600\}/);
  assert.match(settingsSource, /requiredHeartbeatInterval\(form\.heartbeatInterval\)/);
  assert.match(settingsSource, /Heartbeat間隔は5〜60秒の整数で入力してください。現在の値を5〜60秒に変更してから保存してください。/);
  assert.match(settingsSource, /中央UpdaterのSSH秘密鍵は廃棄され、再追加時は新しい鍵になります/);
  assert.match(settingsSource, /authorized_keys・sudoers・helper設定を撤去/);
  assert.match(settingsSource, /\^ssh-ed25519\\s\+/);
  assert.match(nodeSource, /Updaterの登録とRuntime Tokenの発行には、secrets\.update 権限が必要/);
  assert.doesNotMatch(`${settingsSource}\n${nodeSource}`, /known_hosts|JSON手動設定|updater\.json.*編集|updater\.json.*設定を完成/);
});

test("central updater availability and target host reachability stay independent and fail closed", () => {
  const online: SystemUpdateAgentStatus = {
    updater_id: "updater-main",
    name: "Central Updater",
    status: "online",
    online: true,
    version: "v1.7.0",
    desired_revision: 2,
    applied_revision: 2,
    policy_status: "applied",
  };
  const offline: SystemUpdateAgentStatus = { ...online, status: "offline", online: false };
  const reachable: SystemUpdateHostStatus = { host_id: "host-main", name: "Main Host", updater_id: online.updater_id, reachability: "reachable" };
  const unreachable: SystemUpdateHostStatus = { ...reachable, reachability: "unreachable", reachability_code: "ssh_timeout" };
  const unknown: SystemUpdateHostStatus = { ...reachable, reachability: "unknown" };

  assert.deepEqual(systemUpdateConnectivity(baseTarget, [online], [reachable]), { updater: online, host: reachable, agentOnline: true, reachability: "reachable", ready: true });
  assert.deepEqual(systemUpdateConnectivity(baseTarget, [online], [unreachable]), { updater: online, host: unreachable, agentOnline: true, reachability: "unreachable", ready: false });
  assert.deepEqual(systemUpdateConnectivity(baseTarget, [offline], [reachable]), { updater: offline, host: reachable, agentOnline: false, reachability: "reachable", ready: false });
  assert.deepEqual(systemUpdateConnectivity(baseTarget, [online], [unknown]), { updater: online, host: unknown, agentOnline: true, reachability: "unknown", ready: false });
  assert.deepEqual(systemUpdateConnectivity(baseTarget, [], [reachable]), { updater: undefined, host: undefined, agentOnline: false, reachability: "unknown", ready: false });
  assert.deepEqual(systemUpdateConnectivity(baseTarget, [online], [{ ...reachable, updater_id: "other-updater" }]), { updater: online, host: undefined, agentOnline: true, reachability: "unknown", ready: false });
  assert.equal(systemUpdateConnectivity(baseTarget, [{ ...online, applied_revision: 1, policy_status: "pending" }], [reachable]).ready, false);
  assert.equal(systemUpdateConnectivity(baseTarget, [{ ...online, policy_status: "failed" }], [reachable]).ready, false);
  assert.equal(systemUpdateHostReachabilityLabel("reachable"), "到達可");
  assert.equal(systemUpdateHostReachabilityLabel("unreachable"), "接続不可");
  assert.equal(systemUpdateHostReachabilityLabel("unknown"), "未確認");
  assert.match(systemUpdateHostReachabilityMessage("ssh_host_key_mismatch"), /ホスト鍵/);

  const malformed = normalizeSystemUpdatesResponse({
    updaters: [{ updater_id: "updater-main", online: "true" }],
    hosts: [{ host_id: "host-main", updater_id: "updater-main", reachability: "healthy" }],
    targets: [{ target_id: "worker-main", host_id: "host-main", updater_id: "updater-main", updater_online: "true" }],
  });
  assert.equal(malformed.updaters[0].online, false);
  assert.equal(malformed.hosts[0].reachability, "unknown");
  assert.equal(malformed.targets[0].updater_online, false);
});

test("update API codes are shown as actionable Japanese guidance", () => {
  assert.equal(systemUpdateErrorMessage({ code: "updater_offline" }), "中央Updaterがオフラインです。接続状態を確認してください。");
  assert.match(systemUpdateErrorMessage({ code: "checksum_mismatch" }), /検証に失敗/);
  assert.match(systemUpdateErrorMessage({ code: "release_version_invalid", status: 409, message: "manifest tag v1.bad" }), /公開された更新バージョン.*manifest tag v1\.bad/);
  assert.match(systemUpdateErrorMessage({ code: "download_failed", message: "GitHub returned 403 for asset X" }), /ダウンロード.*GitHub returned 403/);
  assert.match(systemUpdateErrorMessage({ code: "system_update_target_active" }), /進行中/);
  assert.match(systemUpdateErrorMessage({ code: "system_update_not_cancellable" }), /キャンセルできません/);
  assert.match(systemUpdateErrorMessage({ status: 403 }), /権限/);
  assert.match(systemUpdateTargetBlockedReason("updater_not_configured"), /中央Updater.*設定されていません/);
  assert.equal(systemUpdateTargetBlockedReason("updater_missing"), "中央Updaterが設定されていません。");
  assert.equal(systemUpdateTargetBlockedReason("target_unreachable"), "中央Updaterから対象ホストへ接続できません。");
  assert.equal(systemUpdateTargetBlockedReason("target_reachability_unknown"), "対象ホストへの接続状態をまだ確認できません。");
  assert.match(systemUpdateTargetBlockedReason("updater_policy_pending"), /設定の反映を待っています/);
  assert.match(systemUpdateTargetBlockedReason("updater_policy_failed"), /反映できませんでした/);
  assert.match(systemUpdateTargetBlockedReason("updater_policy_mismatch"), /反映が完了していません/);
  assert.match(systemUpdateTargetBlockedReason("updater_policy_target_type_mismatch"), /サービス種別/);
  assert.match(systemUpdateTargetBlockedReason("updater_release_token_not_configured"), /GitHub Release Tokenが未設定/);
  assert.equal(systemUpdateErrorMessage({ code: "target_unreachable" }), "中央Updaterから対象ホストへ接続できません。");
  assert.equal(systemUpdateTargetBlockedReason("release_manifest_missing"), "更新用リリース情報が公開されていないため、適用できません。");
  assert.equal(systemUpdateTargetBlockedReason("release_manifest_invalid"), "更新用リリース情報を検証できないため、適用できません。");
  assert.equal(systemUpdateTargetBlockedReason("manifest_unverified"), "最新バージョンは確認できましたが、更新用リリース情報を検証できないため自動適用できません。");
  assert.equal(systemUpdateTargetBlockedReason("updater_version_incompatible"), "minimum_agent_versionを満たすように中央Updaterを更新してください。");
  assert.match(systemUpdatePolicyErrorMessage("policy_snapshot_failed"), /更新操作を停止して自動再試行/);
  assert.match(systemUpdatePolicyErrorMessage("ssh_connectivity_failed"), /SSH接続/);
});

test("deployment and progress presentation makes Docker bundle management explicit", () => {
  assert.equal(systemUpdateDeploymentLabel("docker_compose"), "Docker Compose（Bundle管理）");
  assert.equal(systemUpdateProgress({ progress: -10 }), 0);
  assert.equal(systemUpdateProgress({ progress: 57.6 }), 58);
  assert.equal(systemUpdateProgress({ progress: 180 }), 100);
});

test("system update audit actions have concrete Japanese labels", () => {
  assert.equal(auditActionLabel("system_updates.create"), "システム更新を依頼");
  assert.equal(auditActionLabel("system_updates.cancel"), "システム更新をキャンセル");
  assert.equal(auditActionLabel("system_updates.report"), "システム更新の進捗を報告");
  assert.equal(auditActionLabel("system_updates.updater_policy.save"), "中央Updater設定を保存");
  assert.equal(auditActionLabel("system_updates.succeeded"), "システム更新に成功");
});

test("updater token operations require update execution and secret permissions", () => {
  const base = {
    serviceType: "update_agent",
    canCreateTokens: true,
    canResolveManagedSecret: false,
    requiresManagedSecret: false,
    canExecuteSystemUpdates: false,
  };
  assert.equal(canIssueNodeConfiguration(base), false);
  assert.equal(canIssueNodeConfiguration({ ...base, canExecuteSystemUpdates: true }), false);
  assert.equal(canIssueNodeConfiguration({ ...base, canResolveManagedSecret: true }), false);
  assert.equal(canIssueNodeConfiguration({ ...base, canResolveManagedSecret: true, canExecuteSystemUpdates: true }), true);
  assert.equal(canRegenerateNodeConfigureToken({ ...base, canRevokeTokens: true }), false);
  assert.equal(canRegenerateNodeConfigureToken({ ...base, canRevokeTokens: true, canResolveManagedSecret: true, canExecuteSystemUpdates: true }), true);
  assert.equal(canRegenerateNodeConfigureToken({ ...base, canRevokeTokens: false, canResolveManagedSecret: true, canExecuteSystemUpdates: true }), false);
  assert.equal(canRotateNodeRuntimeToken({ ...base, canRevokeTokens: true }), false);
  assert.equal(canRotateNodeRuntimeToken({ ...base, canRevokeTokens: true, canResolveManagedSecret: true, canExecuteSystemUpdates: true }), true);
  assert.equal(canRotateNodeRuntimeToken({ ...base, canRevokeTokens: false, canResolveManagedSecret: true, canExecuteSystemUpdates: true }), false);
  assert.equal(canIssueNodeConfiguration({
    serviceType: "observability",
    canCreateTokens: true,
    canResolveManagedSecret: false,
    requiresManagedSecret: false,
    canExecuteSystemUpdates: false,
  }), true);
});

test("updater mock configure command keeps the one-time token out of argv", () => {
  const response = mockPost("/nodes/registration-tokens", {
    node_type: "update_agent",
    node_id: "central-updater",
    name: "Central Updater",
    host: "127.0.0.1",
    port: 8090,
    ssl_enabled: false,
  }) as { configure_token?: string; configure_command?: string; scopes?: string[] };

  assert.match(response.configure_token || "", /^ast_cfg_/);
  assert.match(response.configure_command || "", /sudo \/usr\/local\/bin\/autostream-updater configure/);
  assert.doesNotMatch(response.configure_command || "", /--token|ast_cfg_/);
  assert.equal(
    ["updates.claim", "updates.report", "updates.authorize"].every((scope) => response.scopes?.includes(scope)),
    true,
  );
});

test("updater configure failure guidance requires a fresh token before restart", () => {
  const source = readFileSync(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url), "utf8");

  assert.match(source, /設定処理が失敗または結果不確定の場合は、Updaterを再起動しないでください。/);
  assert.match(source, /新しいConfigure Tokenを発行し、同じtoken-free commandを新しいTokenで再実行/);
  assert.doesNotMatch(source, /失敗または結果不確定の場合も旧Runtime Tokenは維持/);
  assert.doesNotMatch(source, /同じコマンドで再開|再生成を求められた場合だけ/);
});

test("updater configure delegates managed policy to the system update screen", () => {
  const source = readFileSync(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url), "utf8");

  assert.match(source, /中央Updaterホストで1回実行/);
  assert.match(source, /「アプリケーション情報」の中央Updater設定から登録/);
  assert.match(source, /保存後はUpdaterが自動で反映/);
  assert.match(source, /ローカル設定ファイルを手作業で編集する必要はありません/);
  assert.doesNotMatch(source, /updater\.json|known_hosts|--init-from|JSON手動設定/);
});

test("updater node description identifies its central multi-host responsibility", () => {
  const source = readFileSync(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url), "utf8");

  assert.match(source, /value: "update_agent"[^{}\r\n]*description: "各管理対象ホストのサービス更新、検証、ロールバックを中央から担当するUpdater"/);
  assert.doesNotMatch(source, /value: "update_agent"[^{}\r\n]*description: "このホストのサービス更新、検証、ロールバックを担当するUpdater"/);
});
