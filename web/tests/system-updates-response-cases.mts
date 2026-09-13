import assert from "node:assert/strict";
import test from "node:test";
import { isControlPanelUpdateTarget, isSystemUpdateJobActive, isSystemUpdateJobCancellable, systemUpdateJobStatusLabel, systemUpdateMayDisconnectPanel, compareSystemUpdateVersions, normalizeSystemUpdatesResponse, systemUpdateJobFromResponse, systemUpdateUpdaterPolicyState, systemUpdatePolicyErrorMessage, normalizeUpdaterSettingsResponse } from "./system-updates-fixture.mts";


export function registerUpdateResponseCases() {


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
    targets: [{
      target_id: "worker-main",
      service_type: "worker",
      name: "Worker",
      host_id: "host-main",
      update_agent_id: "updater-1",
      updater_online: true,
      eligible: true,
      eligible_operations: ["software_update", "port_reconfigure"],
      operation_blocked_reasons: { software_update: "release_manifest_invalid" },
      update_available: true,
      update_check_source: "github_release",
      update_check_error: "rate_limited",
      port_mapping: {
        mode: "docker",
        advertised_port: 443,
        published_host_ip: "127.0.0.1",
        published_port: 18084,
        container_port: 8080,
        health_port: 18084,
        config_revision: 12,
        state: "applied",
        reported_at: "2026-07-18T00:00:00Z",
      },
    }],
    jobs: [{
      id: "job-1",
      idempotency_key: "request-1",
      target_id: "worker-main",
      target_service_type: "worker",
      requested_by_username: "ops",
      operation: "port_reconfigure",
      port_reconfigure: {
        old_port: 8084,
        new_port: 443,
        expected_endpoint_revision: 7,
        docker: {
          published_host_ip: "127.0.0.1",
          old_published_port: 8084,
          new_published_port: 18084,
          old_container_port: 8080,
          new_container_port: 18080,
          old_health_port: 8084,
          new_health_port: 18084,
          approved_compose_config_sha256: "a".repeat(64),
          approved_compose_revision: 12,
          expected_version_env_sha256: `sha256:${"b".repeat(64)}`,
          expected_container_id: "c".repeat(64),
          expected_image_id: `sha256:${"d".repeat(64)}`,
          expected_repository_digest: `sha256:${"e".repeat(64)}`,
        },
      },
      status: "queued",
      progress: 0,
      sequence: 3,
      lease_generation: 2,
      created_at: "2026-07-18T00:00:00Z",
      updated_at: "2026-07-18T00:00:00Z",
    }],
  });
  assert.equal(response.targets[0].target_type, "worker");
  assert.equal(response.targets[0].host_id, "host-main");
  assert.equal(response.targets[0].updater_id, "updater-1");
  assert.equal(response.targets[0].updater_online, true);
  assert.deepEqual(response.targets[0].eligible_operations, ["software_update", "port_reconfigure"]);
  assert.deepEqual(response.targets[0].operation_blocked_reasons, { software_update: "release_manifest_invalid" });
  assert.deepEqual(response.targets[0].port_mapping, {
    mode: "docker",
    advertised_port: 443,
    published_host_ip: "127.0.0.1",
    published_port: 18084,
    container_port: 8080,
    health_port: 18084,
    config_revision: 12,
    state: "applied",
    reported_at: "2026-07-18T00:00:00Z",
  });
  assert.deepEqual(response.updaters[0], { updater_id: "updater-1", name: "Central Updater", status: "online", online: true, version: "v1.7.0", last_heartbeat_at: "2026-07-18T00:00:00Z" });
  assert.deepEqual(response.hosts[0], { host_id: "host-main", name: "Main Host", updater_id: "updater-1", reachability: "reachable", reachability_checked_at: "2026-07-18T00:00:00Z", reachability_code: "" });
  assert.equal(response.targets[0].update_check_source, "github_release");
  assert.equal(response.targets[0].update_check_error, "rate_limited");
  assert.equal(response.jobs[0].target_type, "worker");
  assert.equal(response.jobs[0].idempotency_key, "request-1");
  assert.equal(response.jobs[0].requested_by, "ops");
  assert.equal(response.jobs[0].sequence, 3);
  assert.equal(response.jobs[0].lease_generation, 2);
  assert.equal(response.jobs[0].port_reconfigure?.docker?.new_published_port, 18084);
  assert.equal(response.jobs[0].port_reconfigure?.docker?.new_container_port, 18080);
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

test("Host Agent policy status is normalized fail closed", () => {
  const response = normalizeSystemUpdatesResponse({
    updaters: [{
      updater_id: "host-agent-main",
      name: "Host Agent",
      status: "online",
      online: true,
      transport_mode: "pull_v2",
      execution_host_id: "host-main",
      ownership_epoch: 1,
      desired_revision: 4,
      applied_revision: 3,
      policy_status: "pending",
      policy_error_code: "",
    }],
  });
  assert.equal(response.updaters[0].desired_revision, 4);
  assert.equal(response.updaters[0].applied_revision, 3);
  assert.deepEqual(systemUpdateUpdaterPolicyState(response.updaters[0]), { label: "反映待ち", tone: "secondary", ready: false });
  assert.deepEqual(systemUpdateUpdaterPolicyState({ ...response.updaters[0], applied_revision: 4, policy_status: "applied" }), { label: "反映済み", tone: "default", ready: true });
  assert.deepEqual(systemUpdateUpdaterPolicyState({ ...response.updaters[0], policy_status: "failed" }), { label: "反映失敗", tone: "destructive", ready: false });
  assert.deepEqual(systemUpdateUpdaterPolicyState({ ...response.updaters[0], online: false }), { label: "オフライン", tone: "destructive", ready: false });
  assert.deepEqual(systemUpdateUpdaterPolicyState({ updater_id: "new", name: "New", status: "online", online: true, version: "" }), { label: "未設定", tone: "outline", ready: false });
  assert.match(systemUpdatePolicyErrorMessage("active_job_pending"), /処理完了後に自動で反映/);
});

test("updater settings response rejects removed central policy fields", () => {
  assert.throws(() => normalizeUpdaterSettingsResponse({
    updater_id: "updater-main",
    revision: 7,
    transport_mode: "ssh_v1",
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
  }), /invalid_updater_settings_transport/);
});
}
