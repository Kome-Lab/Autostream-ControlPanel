import assert from "node:assert/strict";
import test from "node:test";
import type { SystemUpdateAgentStatus, UpdaterSettings, UpdaterSettingsHost } from "../src/types/domain.ts";
import { mockGet, mockPut } from "./system-updates-fixture.mts";
import { normalizeSystemUpdatesResponse, normalizeUpdaterHostBootstrapJobsResponse, isUpdaterHostBootstrapJobActive, updaterHostBootstrapEligibility, updaterHostBootstrapEligibilityMessage, isUpdaterHostBootstrapBulkCandidate, updaterHostBootstrapConfirmationContext, activeUpdaterHostBootstrapStatus, isUpdaterPolicyHostID } from "./system-updates-fixture.mts";


export function registerBootstrapPolicyCases() {


test("system update response exposes only the updater bootstrap encryption public key metadata", () => {
  const response = normalizeSystemUpdatesResponse({
    updaters: [{
      updater_id: "updater-main",
      name: "Central Updater",
      status: "online",
      online: true,
      version: "v1.8.0",
      bootstrap_encryption_public_key: "BAc-public-key",
      bootstrap_encryption_key_fingerprint: "SHA256:bootstrap-key",
      bootstrap_encryption_private_key: "must-not-be-returned",
    }],
  });

  assert.equal(response.updaters[0].bootstrap_encryption_public_key, "BAc-public-key");
  assert.equal(response.updaters[0].bootstrap_encryption_key_fingerprint, "SHA256:bootstrap-key");
  assert.equal("bootstrap_encryption_private_key" in response.updaters[0], false);
});

test("bootstrap job response is whitelisted and active states fail closed", () => {
  const response = normalizeUpdaterHostBootstrapJobsResponse({
    jobs: [{
      id: "bootstrap-1",
      job_id: "legacy-alias",
      idempotency_key: "idempotency-1",
      updater_id: "updater-main",
      expected_revision: 7,
      status: "installing",
      host_ids: ["host-main", "host-standby"],
      hosts: [
        { host_id: "host-main", status: "installing", progress: 55, message: "Installing", updated_at: "2026-07-27T00:00:00Z", private_key: "secret" },
        { host_id: "host-standby", status: "queued", progress: 0 },
      ],
      created_at: "2026-07-27T00:00:00Z",
      envelope: { ciphertext: "must-not-be-returned" },
      administrator_user: "must-not-be-returned",
    }],
  }, "updater-fallback");

  assert.equal(response.jobs[0].id, "bootstrap-1");
  assert.equal(response.jobs[0].updater_id, "updater-main");
  assert.equal(response.jobs[0].hosts[0].progress, 55);
  assert.equal("envelope" in response.jobs[0], false);
  assert.equal("administrator_user" in response.jobs[0], false);
  assert.equal("private_key" in response.jobs[0].hosts[0], false);
  for (const status of ["awaiting_credentials", "queued", "claimed", "connecting", "uploading", "verifying", "installing", "probing"]) {
    assert.equal(isUpdaterHostBootstrapJobActive(status), true, status);
  }
  for (const status of ["succeeded", "failed", "credential_expired", "", "unknown"]) {
    assert.equal(isUpdaterHostBootstrapJobActive(status), false, status);
  }
});

test("bootstrap eligibility requires the applied policy projection, saved bootstrap host, generated keys, and no active job", () => {
  const savedHost: UpdaterSettingsHost = {
    host_id: "host-main",
    name: "Main",
    address: "10.0.0.10",
    port: 22,
    user: "autostream-update-host",
    arch: "amd64",
    host_public_key: "ssh-ed25519 AAAAHOST main",
    host_key_fingerprint: "SHA256:host-key",
    ssh_client_public_key: "ssh-ed25519 AAAACLIENT updater",
    ssh_client_key_fingerprint: "SHA256:client-key",
  };
  const updater: SystemUpdateAgentStatus = {
    updater_id: "updater-main",
    name: "Central Updater",
    status: "online",
    online: true,
    version: "v1.8.0",
    desired_revision: 4,
    applied_revision: 4,
    policy_status: "applied",
    bootstrap_encryption_public_key: "BAc-public-key",
    bootstrap_encryption_key_fingerprint: "SHA256:bootstrap-key",
    ssh_client_public_keys: { "host-main": "ssh-ed25519 AAAACLIENT updater" },
    ssh_client_key_fingerprints: { "host-main": "SHA256:client-key" },
  };
  const base = {
    updater,
    expectedAppliedRevision: 4,
    savedHost,
    currentHost: { ...savedHost },
    releaseTokenConfigured: true,
  };

  assert.deepEqual(updaterHostBootstrapEligibility(base), { ready: true, reason: "" });
  const missingReleaseToken = updaterHostBootstrapEligibility({ ...base, releaseTokenConfigured: false });
  assert.deepEqual(missingReleaseToken, { ready: false, reason: "release_token_pending" });
  assert.equal(
    updaterHostBootstrapEligibilityMessage(missingReleaseToken.reason, true),
    "GitHub Release Tokenを保存してからホストセットアップを開始してください。",
  );
  assert.equal(updaterHostBootstrapEligibility({ ...base, updater: { ...updater, online: false } }).reason, "updater_offline");
  assert.equal(updaterHostBootstrapEligibility({ ...base, updater: { ...updater, desired_revision: 5 } }).reason, "policy_pending");
  assert.equal(updaterHostBootstrapEligibility({ ...base, expectedAppliedRevision: 7 }).reason, "policy_pending");
  assert.equal(updaterHostBootstrapEligibility({ ...base, currentHost: { ...savedHost, address: "10.0.0.11" } }).reason, "host_unsaved");
  assert.equal(updaterHostBootstrapEligibility({
    ...base,
    updater: { ...updater, ssh_client_public_keys: {}, ssh_client_key_fingerprints: {} },
  }).reason, "client_key_pending");
  assert.equal(updaterHostBootstrapEligibility({
    ...base,
    savedHost: { ...savedHost, host_key_fingerprint: "", host_public_key_fingerprint: "" },
  }).reason, "host_key_pending");
  assert.equal(updaterHostBootstrapEligibility({ ...base, bootstrapStatus: "installing" }).reason, "bootstrap_active");
  const configured = updaterHostBootstrapEligibility({ ...base, bootstrapStatus: "succeeded" });
  assert.deepEqual(configured, { ready: true, reason: "already_configured" });
  assert.equal(isUpdaterHostBootstrapBulkCandidate(configured), false);
  const expired = updaterHostBootstrapEligibility({ ...base, bootstrapStatus: "credential_expired" });
  assert.deepEqual(expired, { ready: true, reason: "" });
  assert.equal(isUpdaterHostBootstrapBulkCandidate(expired), true);
  assert.equal(isUpdaterHostBootstrapBulkCandidate(updaterHostBootstrapEligibility(base)), true);
  assert.equal(updaterHostBootstrapEligibility({ ...base, updater: { ...updater, bootstrap_encryption_public_key: "" } }).reason, "encryption_key_pending");
  assert.equal(
    updaterHostBootstrapEligibility({
      ...base,
      savedHost: { ...savedHost, user: "custom-updater" },
      currentHost: { ...savedHost, user: "custom-updater" },
    }).reason,
    "unsupported_profile",
  );
  assert.equal(
    updaterHostBootstrapEligibility({
      ...base,
      savedHost: { ...savedHost, user: " autostream-update-host" },
      currentHost: { ...savedHost, user: " autostream-update-host" },
    }).reason,
    "unsupported_profile",
  );
});

test("bootstrap confirmation follows fresh updater client-key rotation instead of stale settings", () => {
  const savedHost: UpdaterSettingsHost = {
    host_id: "host-main",
    name: "Main",
    address: "10.0.0.10",
    port: 22,
    user: "autostream-update-host",
    arch: "amd64",
    host_public_key: "ssh-ed25519 AAAAHOST main",
    host_key_fingerprint: "SHA256:host-key",
    ssh_client_public_key: "ssh-ed25519 AAAAOLD updater",
    ssh_client_key_fingerprint: "SHA256:old-client-key",
  };
  const updater: SystemUpdateAgentStatus = {
    updater_id: "updater-main",
    name: "Central Updater",
    status: "online",
    online: true,
    version: "v1.8.0",
    desired_revision: 7,
    applied_revision: 7,
    policy_status: "applied",
    bootstrap_encryption_public_key: "BAc-public-key",
    bootstrap_encryption_key_fingerprint: "SHA256:bootstrap-key",
    ssh_client_public_keys: { "host-main": "ssh-ed25519 AAAAOLD updater" },
    ssh_client_key_fingerprints: { "host-main": "SHA256:old-client-key" },
  };
  const confirmed = updaterHostBootstrapConfirmationContext(updater, 7, ["host-main"], [savedHost]);
  const rotated = updaterHostBootstrapConfirmationContext({
    ...updater,
    ssh_client_public_keys: { "host-main": "ssh-ed25519 AAAANEW updater" },
    ssh_client_key_fingerprints: { "host-main": "SHA256:new-client-key" },
  }, 7, ["host-main"], [savedHost]);

  assert.notEqual(confirmed, rotated);
  const rotatedHost = (JSON.parse(rotated) as { hosts: Array<Record<string, string>> }).hosts[0];
  assert.equal(rotatedHost.ssh_client_public_key, "ssh-ed25519 AAAANEW updater");
  assert.equal(rotatedHost.ssh_client_key_fingerprint, "SHA256:new-client-key");

  const missing = {
    ...updater,
    ssh_client_public_keys: undefined,
    ssh_client_key_fingerprints: undefined,
  };
  const missingContext = updaterHostBootstrapConfirmationContext(missing, 7, ["host-main"], [savedHost]);
  assert.notEqual(confirmed, missingContext);
  const missingHost = (JSON.parse(missingContext) as { hosts: Array<Record<string, string>> }).hosts[0];
  assert.equal(missingHost.ssh_client_public_key, "");
  assert.equal(missingHost.ssh_client_key_fingerprint, "");
  assert.equal(updaterHostBootstrapEligibility({
    updater: missing,
    expectedAppliedRevision: 7,
    savedHost,
    currentHost: { ...savedHost },
    releaseTokenConfigured: true,
  }).reason, "client_key_pending");
});

test("an active bootstrap batch blocks every host on the updater", () => {
  const status = activeUpdaterHostBootstrapStatus([
    {
      id: "bootstrap-running",
      updater_id: "updater-main",
      expected_revision: 7,
      status: "running",
      host_ids: ["host-a"],
      hosts: [{ host_id: "host-a", status: "installing" }],
      created_at: "2026-07-27T00:00:00Z",
    },
    {
      id: "bootstrap-complete",
      updater_id: "updater-main",
      expected_revision: 7,
      status: "succeeded",
      host_ids: ["host-b"],
      hosts: [{ host_id: "host-b", status: "succeeded" }],
      created_at: "2026-07-26T00:00:00Z",
    },
  ]);

  assert.equal(status, "running");
});

test("a terminal bootstrap batch overrides stale active child state", () => {
  const response = normalizeUpdaterHostBootstrapJobsResponse({
    jobs: [{
      id: "bootstrap-expired",
      updater_id: "updater-main",
      expected_revision: 7,
      status: "credential_expired",
      host_ids: ["host-a"],
      hosts: [{ host_id: "host-a", status: "queued" }],
      created_at: "2026-07-27T00:00:00Z",
      completed_at: "2026-07-27T00:05:00Z",
    }],
  });

  assert.equal(response.jobs[0].hosts[0].status, "credential_expired");
  assert.equal(activeUpdaterHostBootstrapStatus(response.jobs), "");
  assert.equal(activeUpdaterHostBootstrapStatus([{
    ...response.jobs[0],
    hosts: [{ host_id: "host-a", status: "queued" }],
  }]), "");
  assert.equal(activeUpdaterHostBootstrapStatus([{
    ...response.jobs[0],
    status: "unknown",
    hosts: [{ host_id: "host-a", status: "queued" }],
  }]), "queued");
});

test("updater policy host IDs exclude colon while retaining safe separators", () => {
  assert.equal(isUpdaterPolicyHostID("host-main_01.example"), true);
  assert.equal(isUpdaterPolicyHostID("host:main"), false);
  assert.equal(isUpdaterPolicyHostID("-host-main"), false);
  assert.equal(isUpdaterPolicyHostID(`h${"a".repeat(127)}`), true);
  assert.equal(isUpdaterPolicyHostID(`h${"a".repeat(128)}`), false);
});

test("updater settings save preserves, deletes, and replaces the write-only GitHub token explicitly", () => {
  const path = "/system-updates/updaters/host-agent-control/settings";
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
}
