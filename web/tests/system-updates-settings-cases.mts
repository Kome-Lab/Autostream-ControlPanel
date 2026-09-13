import assert from "node:assert/strict";
import test from "node:test";
import type { SystemUpdateTarget, UpdaterSettings, UpdaterSettingsTarget } from "../src/types/domain.ts";
import { normalizeUpdaterSettingsResponse, updaterSettingsTargetRequiresDatabase, normalizeUpdaterSettingsTargetDatabaseName, applyUpdaterSettingsTargetPatch, updaterSettingsTargetRequiresLocalListenPort, normalizeUpdaterSettingsTargetLocalListenPort, baseTarget, updaterSettingsTargetOptions, firstUnusedUpdaterSettingsTarget, applyUpdaterSettingsTargetSelection } from "./system-updates-fixture.mts";


export function registerUpdaterSettingsCases() {


test("pull_v2 updater settings remain portless and keep server-owned host binding", () => {
  const digest = `sha256:${"a".repeat(64)}`;
  const settings = normalizeUpdaterSettingsResponse({
    updater_id: "host-agent-main",
    revision: 3,
    projection_revision: 4,
    local_executor_policy_revision: 6,
    transport_mode: "pull_v2",
    execution_host_id: "host-main",
    execution_host_ownership: {
      execution_host_id: "host-main",
      transport_mode: "pull_v2",
      agent_service_id: "",
      ownership_epoch: 12,
      policy_revision: 3,
    },
    pull_activation: {
      ready: true,
      status: "online",
      last_heartbeat_at: "2026-07-28T00:00:00Z",
      observe_only: true,
      update_executor: true,
      mutation_enabled: false,
      recovery_pending: false,
      reported_ownership_epoch: 0,
      reported_projection_revision: 4,
    },
    local_executor_policy_sha256: digest,
    poll_interval_seconds: 15,
    heartbeat_interval_seconds: 30,
    targets: [{
      target_id: "control-panel",
      service_id: "control-panel",
      host_id: "host-main",
      service_type: "control_panel",
      deployment_mode: "systemd",
      database_name: "autostream-kometubu_panel",
    }],
    api: {},
    hosts: [],
  });

  assert.equal(settings.transport_mode, "pull_v2");
  assert.equal(settings.execution_host_id, "host-main");
  assert.equal(settings.local_executor_policy_sha256, digest);
  assert.equal(settings.targets[0].service_id, "control-panel");
  assert.equal(settings.targets[0].database_name, "autostream-kometubu_panel");
  assert.equal(settings.hosts.length, 0);
  assert.deepEqual(settings.pull_activation, {
    ready: true,
    blocked_reason: "",
    status: "online",
    last_heartbeat_at: "2026-07-28T00:00:00Z",
    observe_only: true,
    update_executor: true,
    mutation_enabled: false,
    recovery_pending: false,
    reported_ownership_epoch: 0,
    reported_projection_revision: 4,
  });
  assert.equal(settings.github_token_configured, false);
});

test("pull_v2 database owner target validation is narrow and trims the saved database name", () => {
  const controlPanelTarget: UpdaterSettingsTarget = {
    target_id: "control-panel",
    service_id: "control-panel",
    host_id: "host-main",
    service_type: "control_panel",
    deployment_mode: "systemd",
    database_name: "  autostream-kometubu_panel  ",
  };
  assert.equal(updaterSettingsTargetRequiresDatabase("pull_v2", controlPanelTarget), true);
  assert.equal(
    normalizeUpdaterSettingsTargetDatabaseName("pull_v2", controlPanelTarget, "サービス 1"),
    "autostream-kometubu_panel",
  );
  assert.equal(updaterSettingsTargetRequiresDatabase("ssh_v1" as unknown as UpdaterSettings["transport_mode"], controlPanelTarget), false);
  assert.equal(
    updaterSettingsTargetRequiresDatabase("pull_v2", { ...controlPanelTarget, deployment_mode: "docker" }),
    false,
  );
  assert.equal(
    updaterSettingsTargetRequiresDatabase("pull_v2", { ...controlPanelTarget, service_type: "worker" }),
    false,
  );
  assert.throws(
    () => normalizeUpdaterSettingsTargetDatabaseName(
      "pull_v2",
      { ...controlPanelTarget, database_name: "" },
      "サービス 1",
    ),
    /MariaDBデータベース名を入力してください/,
  );
  assert.throws(
    () => normalizeUpdaterSettingsTargetDatabaseName(
      "pull_v2",
      { ...controlPanelTarget, database_name: "autostream.panel" },
      "サービス 1",
    ),
    /英数字・_・-/,
  );
  assert.throws(
    () => normalizeUpdaterSettingsTargetDatabaseName(
      "pull_v2",
      { ...controlPanelTarget, service_type: "worker" },
      "サービス 1",
    ),
    /MariaDBデータベース名を指定できません/,
  );
  assert.throws(
    () => normalizeUpdaterSettingsTargetDatabaseName("ssh_v1" as unknown as UpdaterSettings["transport_mode"], controlPanelTarget, "サービス 1"),
    /MariaDBデータベース名を指定できません/,
  );
});

test("changing a database owner target identity clears the previous database name", () => {
  const controlPanelTarget: UpdaterSettingsTarget = {
    target_id: "control-panel",
    service_id: "control-panel",
    host_id: "host-main",
    service_type: "control_panel",
    deployment_mode: "systemd",
    database_name: "autostream_control_panel",
  };
  assert.equal(
    applyUpdaterSettingsTargetPatch("pull_v2", controlPanelTarget, { service_type: "observability" }).database_name,
    undefined,
  );
  assert.equal(
    applyUpdaterSettingsTargetPatch("pull_v2", controlPanelTarget, {
      target_id: "control-panel-new",
      service_id: "control-panel-new",
    }).database_name,
    undefined,
  );
  assert.equal(
    applyUpdaterSettingsTargetPatch("pull_v2", controlPanelTarget, { host_id: "host-new" }).database_name,
    undefined,
  );
  assert.equal(
    applyUpdaterSettingsTargetPatch("pull_v2", controlPanelTarget, { deployment_mode: "docker" }).database_name,
    undefined,
  );
  assert.equal(
    applyUpdaterSettingsTargetPatch("pull_v2", controlPanelTarget, { database_name: "new_database" }).database_name,
    "new_database",
  );
});

test("pull_v2 systemd local listener stays separate from the public HTTPS port", () => {
  const observability: UpdaterSettingsTarget = {
    target_id: "observability-main",
    service_id: "observability-main",
    host_id: "host-main",
    service_type: "observability",
    deployment_mode: "systemd",
    local_listen_port: 8082,
  };
  assert.equal(updaterSettingsTargetRequiresLocalListenPort("pull_v2", observability), true);
  assert.equal(
    normalizeUpdaterSettingsTargetLocalListenPort("pull_v2", observability, "サービス 1"),
    8082,
  );
  assert.throws(
    () => normalizeUpdaterSettingsTargetLocalListenPort(
      "pull_v2",
      { ...observability, local_listen_port: 443 },
      "サービス 1",
    ),
    /1024〜65535/,
  );
  assert.equal(
    applyUpdaterSettingsTargetPatch("pull_v2", observability, { deployment_mode: "docker" }).local_listen_port,
    undefined,
  );
});

test("updater settings target options use registered supported services and preserve the current stale ID", () => {
  const registeredTargets: SystemUpdateTarget[] = [
    baseTarget,
    {
      ...baseTarget,
      target_id: "control-panel",
      target_type: "control_panel",
      name: "Control Panel",
    },
    {
      ...baseTarget,
      target_id: "updater-main",
      target_type: "update_agent",
      name: "Host Agent",
    },
    {
      ...baseTarget,
      target_id: "future-main",
      target_type: "future_service",
      name: "Future Service",
    },
  ];
  const configuredTargets: UpdaterSettingsTarget[] = [
    {
      target_id: "worker-main",
      service_id: "worker-main",
      host_id: "host-main",
      service_type: "worker",
      deployment_mode: "systemd",
    },
    {
      target_id: "stale-observer",
      service_id: "stale-observer",
      host_id: "host-main",
      service_type: "observability",
      deployment_mode: "systemd",
    },
  ];

  const staleOptions = updaterSettingsTargetOptions(registeredTargets, configuredTargets, 1);
  assert.deepEqual(staleOptions.map((option) => option.value), ["control-panel", "stale-observer"]);
  assert.match(staleOptions[0]?.label || "", /Control Panel.*control-panel/);
  assert.equal(staleOptions[1]?.current, true);
  assert.equal(staleOptions[1]?.stale, true);
  assert.match(staleOptions[1]?.label || "", /現在の設定/);

  const currentOptions = updaterSettingsTargetOptions(registeredTargets, configuredTargets, 0);
  const currentWorker = currentOptions.find((option) => option.value === "worker-main");
  assert.equal(currentWorker?.current, true);
  assert.equal(currentWorker?.stale, false);
  assert.match(currentWorker?.label || "", /Main Worker.*worker-main.*現在の設定/);
  assert.equal(currentOptions.some((option) => option.value === "updater-main"), false);
  assert.equal(currentOptions.some((option) => option.value === "future-main"), false);
});

test("first unused updater settings target skips duplicates and supports the synthetic control-panel target", () => {
  const registeredTargets: SystemUpdateTarget[] = [
    baseTarget,
    {
      ...baseTarget,
      target_id: "control-panel",
      target_type: "control_panel",
      name: "Control Panel",
    },
    {
      ...baseTarget,
      target_id: "updater-main",
      target_type: "update_agent",
      name: "Host Agent",
    },
  ];
  const worker: UpdaterSettingsTarget = {
    target_id: "worker-main",
    service_id: "worker-main",
    host_id: "host-main",
    service_type: "worker",
    deployment_mode: "systemd",
  };

  assert.deepEqual(firstUnusedUpdaterSettingsTarget("pull_v2", registeredTargets, [worker], "host-selected"), {
    target_id: "control-panel",
    service_id: "control-panel",
    host_id: "host-selected",
    service_type: "control_panel",
    deployment_mode: "systemd",
  });
  assert.equal(firstUnusedUpdaterSettingsTarget("pull_v2", registeredTargets, [
    worker,
    {
      target_id: "control-panel",
      service_id: "control-panel",
      host_id: "host-main",
      service_type: "control_panel",
      deployment_mode: "systemd",
    },
  ], "host-selected"), undefined);

  const rejectedLegacyTarget = firstUnusedUpdaterSettingsTarget(
    "ssh_v1" as unknown as UpdaterSettings["transport_mode"],
    [baseTarget],
    [],
    "host-selected",
  );
  assert.equal(rejectedLegacyTarget?.service_type, "worker");
  assert.equal(rejectedLegacyTarget?.local_listen_port, undefined);
});

test("selecting an updater settings target synchronizes both IDs and its derived service type", () => {
  const current: UpdaterSettingsTarget = {
    target_id: "control-panel",
    service_id: "control-panel",
    host_id: "host-main",
    service_type: "control_panel",
    deployment_mode: "systemd",
    database_name: "autostream_control_panel",
  };

  assert.deepEqual(applyUpdaterSettingsTargetSelection("pull_v2", current, {
    target_id: "worker-main",
    target_type: "worker",
  }), {
    target_id: "worker-main",
    service_id: "worker-main",
    host_id: "host-main",
    service_type: "worker",
    deployment_mode: "systemd",
    local_listen_port: 8084,
  });
  assert.equal(applyUpdaterSettingsTargetSelection("pull_v2", current, {
    target_id: "control-panel",
    target_type: "control_panel",
  }).database_name, "autostream_control_panel");
});
}
