import assert from "node:assert/strict";
import { register } from "node:module";
import test from "node:test";
import type {
  SystemUpdateAgentStatus,
  SystemUpdateJob,
  SystemUpdateTarget,
  SystemUpdatesResponse,
  UpdaterSettings,
  UpdaterSettingsHost,
  UpdaterSettingsTarget,
  WorkerNode,
} from "../src/types/domain.ts";

// Execute the real bundler-resolved modules, including their TSX owners.
// This is the same native-test loader approach used by confirmation-fixture.
const resolverSource = [
  "let webRootURL;",
  "let typescriptURL;",
  "export function initialize(data) { webRootURL = data.webRootURL; typescriptURL = data.typescriptURL; }",
  "export async function resolve(specifier, context, nextResolve) {",
  "  if (specifier.startsWith('@/') || (specifier.startsWith('.') && context.parentURL?.startsWith(webRootURL))) {",
  "    const target = specifier.startsWith('@/') ? new URL('src/' + specifier.slice(2), webRootURL) : new URL(specifier, context.parentURL);",
  "    if (/\\.[cm]?[jt]sx?$/.test(target.pathname)) return nextResolve(target.href, context);",
  "    const typeScriptTarget = new URL(target);",
  "    typeScriptTarget.pathname += '.ts';",
  "    try { return await nextResolve(typeScriptTarget.href, context); } catch {",
  "      target.pathname += '.tsx';",
  "      return nextResolve(target.href, context);",
  "    }",
  "  }",
  "  return nextResolve(specifier, context);",
  "}",
  "export async function load(url, context, nextLoad) {",
  "  if (!url.endsWith('.tsx')) return nextLoad(url, context);",
  "  const { readFile } = await import('node:fs/promises');",
  "  const ts = (await import(typescriptURL)).default;",
  "  const source = await readFile(new URL(url), 'utf8');",
  "  const output = ts.transpileModule(source, { compilerOptions: {",
  "    jsx: ts.JsxEmit.ReactJSX, module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022,",
  "  }}).outputText;",
  "  return { format: 'module', shortCircuit: true, source: output };",
  "}",
].join("\n");

register(`data:text/javascript,${encodeURIComponent(resolverSource)}`, {
  parentURL: import.meta.url,
  data: {
    webRootURL: new URL("../", import.meta.url).href,
    typescriptURL: import.meta.resolve("typescript"),
  },
});

const {
  softwareUpdateAuthoritySnapshot,
  batchUpdateAuthoritySnapshot,
  cancelUpdateAuthoritySnapshot,
  portControlKey,
  portConfigurationAuthorityKey,
  portReconfigureAuthoritySnapshot,
} = await import("../src/features/application/port-reconfigure-authority.tsx");
const { ownershipActionAuthoritySnapshot, updaterSettingsFormFingerprint } = await import("../src/features/application/updater-settings-authority.tsx");
const { settingsToForm } = await import("../src/features/application/updater-settings-form-model.tsx");
const { bootstrapOperationContext } = await import("../src/features/application/updater-bootstrap-job-state.ts");
const {
  pullUpdaterOwnershipActivationEligibility,
  pullUpdaterOwnershipDeactivationEligibility,
} = await import("../src/lib/updater-ownership.ts");
const { createUpdaterActionController, updaterActionCanRevalidate } = await import("../src/features/application/updater-action-policy.ts");
const { createApplicationAuthorityReaders } = await import("../src/features/application/application-authority-readers.ts");
const { normalizeSystemUpdatesResponse } = await import("../src/lib/system-updates.ts");

const firstObservation = "2026-10-28T00:00:00Z";
const nextObservation = "2026-10-28T00:00:30Z";
const updater: SystemUpdateAgentStatus = {
  updater_id: "updater-main", name: "Isolated Host Agent", status: "online", online: true,
  version: "v2.0.3", transport_mode: "pull_v2", execution_host_id: "host-main",
  ownership_epoch: 5, last_heartbeat_at: firstObservation,
  desired_revision: 9, applied_revision: 9, policy_status: "applied",
};
const target: SystemUpdateTarget = {
  target_id: "worker-main", target_type: "worker", name: "Isolated Worker", host_id: "host-main",
  current_version: "v1.0.0", latest_version: "v1.1.0", update_available: true,
  deployment_mode: "systemd", updater_id: updater.updater_id, updater_online: true,
  eligible: true, eligible_operations: ["software_update", "port_reconfigure"],
  port_contract_version: 2, port_policy_snapshot_id: `ps1:${"a".repeat(64)}`,
  local_listen_port: 8084, endpoint_revision: 7, applied_endpoint_revision: 7,
  applied_config_revision: 11, ownership_epoch: 5, port_modes: ["local_only", "local_and_advertised"],
};
const response = (selected: SystemUpdateTarget): SystemUpdatesResponse => ({
  updaters: [structuredClone(updater)],
  hosts: [{ host_id: "host-main", name: "Isolated host", updater_id: updater.updater_id,
    reachability: "reachable", reachability_checked_at: firstObservation }],
  targets: [selected], jobs: [],
});
const activeJob = (selected: SystemUpdateTarget): SystemUpdateJob => ({
  id: "new-active-job", target_id: selected.target_id, target_type: selected.target_type,
  updater_id: updater.updater_id, host_id: "host-main", status: "applying",
  created_at: firstObservation, updated_at: firstObservation,
  sequence: 3, report_sequence: 2, lease_generation: 1,
});

for (const actionID of ["UPD-01", "UPD-02"] as const) {
  const selected: SystemUpdateTarget = actionID === "UPD-02"
    ? { ...target, target_id: "control-panel", target_type: "control_panel", name: "Control Panel" }
    : target;
  for (const observation of ["heartbeat", "reachability-check"] as const) {
    test(`P1 ${actionID} same authority survives ${observation}-only polling`, () => {
      const initial = response(selected);
      const before = softwareUpdateAuthoritySnapshot(actionID, selected, initial);
      assert.equal(before.applicable, true, "fixture must represent an actionable update");

      // These real authority changes must remain distinct. No updated_at is
      // removed or advanced: its route-specific concurrency meaning is retained.
      for (const patch of [{ ownership_epoch: 6 }, { desired_revision: 10 }, { online: false }]) {
        const changed = structuredClone(initial);
        Object.assign(changed.updaters[0]!, patch);
        assert.notEqual(softwareUpdateAuthoritySnapshot(actionID, selected, changed).fingerprint, before.fingerprint);
      }
      const occupied = structuredClone(initial);
      occupied.jobs.push(activeJob(selected));
      assert.equal(softwareUpdateAuthoritySnapshot(actionID, selected, occupied).applicable, false);
      assert.notEqual(softwareUpdateAuthoritySnapshot(actionID, selected, occupied).fingerprint, before.fingerprint);

      const polled = structuredClone(initial);
      if (observation === "heartbeat") polled.updaters[0]!.last_heartbeat_at = nextObservation;
      else polled.hosts[0]!.reachability_checked_at = nextObservation;
      const after = softwareUpdateAuthoritySnapshot(actionID, selected, polled);
      assert.equal(after.applicable, true);
      assert.equal(after.fingerprint, before.fingerprint, "observation time alone must not invalidate confirmation");
    });
  }
}

test("P1 UPD-03 unchanged target set survives list-order-only polling", () => {
  const second = { ...target, target_id: "worker-secondary", name: "Second isolated Worker" };
  const initial = response(target);
  initial.targets = [target, second];
  const before = batchUpdateAuthoritySnapshot(initial.targets, initial);
  const changed = structuredClone(initial);
  changed.targets[1]!.latest_version = "v1.2.0";
  assert.notEqual(batchUpdateAuthoritySnapshot(changed.targets, changed).fingerprint, before.fingerprint);
  const occupied = structuredClone(initial);
  occupied.jobs.push(activeJob(second));
  assert.notEqual(batchUpdateAuthoritySnapshot(occupied.targets, occupied).fingerprint, before.fingerprint);

  const reordered = structuredClone(initial);
  reordered.targets.reverse();
  const originalOrder = reordered.targets.map((item) => item.target_id);
  assert.equal(batchUpdateAuthoritySnapshot(reordered.targets, reordered).fingerprint, before.fingerprint,
    "the same two non-Panel targets are one confirmed set regardless of response order");
  assert.deepEqual(reordered.targets.map((item) => item.target_id), originalOrder,
    "authority comparison must not reorder the live target list");
});

test("P1 UPD-04 keeps queued job status and execution counters authoritative", () => {
  const queued = { ...activeJob(target), status: "queued" };
  const before = cancelUpdateAuthoritySnapshot(queued.id, queued);
  assert.equal(before.applicable, true);
  for (const patch of [{ sequence: 4 }, { report_sequence: 3 }, { lease_generation: 2 }, { recovery_required: true }]) {
    assert.notEqual(cancelUpdateAuthoritySnapshot(queued.id, { ...queued, ...patch }).fingerprint, before.fingerprint,
      "execution counters remain authoritative even with an identical updated_at");
  }
  const applying = cancelUpdateAuthoritySnapshot(queued.id, { ...queued, status: "applying" });
  assert.equal(applying.applicable, false);
  assert.notEqual(applying.fingerprint, before.fingerprint);
});

test("P1 UPD-05 same port proposal survives heartbeat-only polling and keeps port fences", () => {
  const node: WorkerNode = {
    id: target.target_id, service_id: target.target_id, service_type: "worker", service_name: "Isolated Worker",
    status: "online", endpoint_revision: 7, endpoint_status: "applied",
    applied_endpoint: { host: "worker.example.invalid", port: 8084, ssl_enabled: true,
      public_url: "https://worker.example.invalid:8084" },
  };
  const context = { target, updater, node, requestState: "idle" as const,
    proposal: { mode: "service" as const, portMode: "local_only" as const, newPort: 18084 } };
  const before = portReconfigureAuthoritySnapshot(context);
  assert.equal(before.applicable, true);
  for (const patch of [
    { port_policy_snapshot_id: `ps1:${"b".repeat(64)}` },
    { applied_config_revision: 12 },
    { ownership_epoch: 6 },
  ]) {
    const changed = { ...context, target: { ...target, ...patch } };
    assert.notEqual(portReconfigureAuthoritySnapshot(changed).fingerprint, before.fingerprint);
  }
  assert.notEqual(portReconfigureAuthoritySnapshot({ ...context, node: { ...node, endpoint_revision: 8 } }).fingerprint, before.fingerprint);
  assert.notEqual(portReconfigureAuthoritySnapshot({ ...context, latestJob: activeJob(target) }).fingerprint, before.fingerprint);
  const after = portReconfigureAuthoritySnapshot({ ...context, updater: { ...updater, last_heartbeat_at: nextObservation } });
  assert.equal(after.applicable, true);
  assert.equal(after.fingerprint, before.fingerprint, "a newer heartbeat does not change the proposed port operation");
});

const settings: UpdaterSettings = {
  updater_id: updater.updater_id, revision: 9, projection_revision: 9, local_executor_policy_revision: 6,
  transport_mode: "pull_v2", execution_host_id: "host-main", local_executor_policy_sha256: `sha256:${"a".repeat(64)}`,
  execution_host_ownership: { transport_mode: "pull_v2", agent_service_id: updater.updater_id,
    ownership_epoch: 5, policy_revision: 9 },
  pull_activation: { ready: true, status: "online", last_heartbeat_at: firstObservation,
    observe_only: false, update_executor: true, mutation_enabled: true, recovery_pending: false,
    reported_ownership_epoch: 5, reported_projection_revision: 9 },
  api: { bind_host: "127.0.0.1", host: "127.0.0.1", port: 8090, ssl_enabled: false },
  poll_interval_seconds: 15, heartbeat_interval_seconds: 30,
  hosts: [], targets: [], github_token_configured: false,
};

for (const actionID of ["UPD-06", "UPD-07"] as const) {
  test(`P1 ${actionID} same ownership authority survives heartbeat-only polling`, () => {
    const selectedUpdater = actionID === "UPD-06" ? { ...updater, ownership_epoch: 0 } : updater;
    const selectedSettings: UpdaterSettings = actionID === "UPD-06" ? {
      ...settings,
      execution_host_ownership: { ...settings.execution_host_ownership!, agent_service_id: "", ownership_epoch: 12 },
      pull_activation: { ...settings.pull_activation!, observe_only: true, mutation_enabled: false, reported_ownership_epoch: 0 },
    } : settings;
    const snapshot = (candidateUpdater = selectedUpdater, candidateSettings = selectedSettings, jobs: SystemUpdateJob[] = []) => {
      const eligibility = (actionID === "UPD-06"
        ? pullUpdaterOwnershipActivationEligibility
        : pullUpdaterOwnershipDeactivationEligibility)({
        updater: candidateUpdater, settings: candidateSettings, jobs, requestState: "idle",
      });
      return ownershipActionAuthoritySnapshot(actionID, candidateUpdater, candidateSettings, jobs, eligibility.ready);
    };
    const before = snapshot();
    assert.equal(before.applicable, true, "fixture must satisfy the real ownership eligibility contract");
    for (const patch of [{ revision: 10 }, { projection_revision: 10 }, { local_executor_policy_revision: 7 }]) {
      assert.notEqual(snapshot(selectedUpdater, { ...selectedSettings, ...patch }).fingerprint, before.fingerprint);
    }
    assert.notEqual(snapshot({ ...selectedUpdater, ownership_epoch: 6 }).fingerprint, before.fingerprint);
    assert.notEqual(snapshot(selectedUpdater, { ...selectedSettings,
      execution_host_ownership: { ...selectedSettings.execution_host_ownership!, agent_service_id: "different-owner" } }).fingerprint, before.fingerprint);
    assert.notEqual(snapshot(selectedUpdater, { ...selectedSettings,
      pull_activation: { ...selectedSettings.pull_activation!, ready: false, status: "recovering", recovery_pending: true } }).fingerprint, before.fingerprint);
    assert.notEqual(snapshot(selectedUpdater, selectedSettings, [activeJob(target)]).fingerprint, before.fingerprint);

    const after = snapshot(
      { ...selectedUpdater, last_heartbeat_at: nextObservation },
      { ...selectedSettings, pull_activation: { ...selectedSettings.pull_activation!, last_heartbeat_at: nextObservation } });
    assert.equal(after.applicable, true);
    assert.equal(after.fingerprint, before.fingerprint, "unchanged ownership and readiness must survive a heartbeat");
  });
}

test("P1 UPD-08 form authority treats host and target order as sets without changing the draft", () => {
  const host = (id: string): UpdaterSettingsHost => ({ host_id: id, name: id, address: `${id}.example.invalid`,
    port: 22, user: "isolated-admin", arch: "amd64", host_public_key: `synthetic-public-key-${id}` });
  const settingsTarget = (id: string, hostID: string): UpdaterSettingsTarget => ({ target_id: id,
    service_id: id, host_id: hostID, service_type: "worker", deployment_mode: "systemd", local_listen_port: 8084 });
  const form = settingsToForm({ ...settings, hosts: [host("host-b"), host("host-a")],
    targets: [settingsTarget("worker-b", "host-b"), settingsTarget("worker-a", "host-a")] });
  const original = structuredClone(form);
  const before = updaterSettingsFormFingerprint(9, form, false, false);
  assert.deepEqual(form, original, "comparison must not sort the user's draft arrays in place");
  const reordered = { ...form, hosts: [...form.hosts].reverse(), targets: [...form.targets].reverse() };
  assert.equal(updaterSettingsFormFingerprint(9, reordered, false, false), before);
  assert.notEqual(updaterSettingsFormFingerprint(10, form, false, false), before);
  assert.notEqual(updaterSettingsFormFingerprint(9, form, true, false), before);
  assert.notEqual(updaterSettingsFormFingerprint(9, form, false, true), before);
  assert.notEqual(updaterSettingsFormFingerprint(9, { ...form,
    hosts: [{ ...form.hosts[0]!, host_public_key: "rotated-public-key" }, form.hosts[1]!] }, false, false), before);
  assert.notEqual(updaterSettingsFormFingerprint(9, { ...form,
    targets: [{ ...form.targets[0]!, local_listen_port: 18084 }, form.targets[1]!] }, false, false), before);
});

test("P1 bootstrap operation context survives selected-host and target list reordering", () => {
  const host = (id: string): UpdaterSettingsHost => ({ host_id: id, name: id, address: `${id}.example.invalid`,
    port: 22, user: "isolated-admin", arch: "amd64", host_public_key: `synthetic-public-key-${id}` });
  const bootstrapTarget = (id: string, hostID: string): UpdaterSettingsTarget => ({ target_id: id,
    service_id: id, host_id: hostID, service_type: "worker", deployment_mode: "systemd", local_listen_port: 8084 });
  const hosts = [host("host-a"), host("host-b")];
  const targets = [bootstrapTarget("worker-a", "host-a"), bootstrapTarget("worker-b", "host-b")];
  const context = {
    confirmationContext: "unchanged-confirmed-host-keys", confirmedContext: "unchanged-confirmed-host-keys", updater,
    expectedRevision: 9, expectedAppliedRevision: 9, savedHosts: hosts, currentHosts: structuredClone(hosts),
    savedTargets: targets, currentTargets: structuredClone(targets), selectedHostIDs: ["host-a", "host-b"],
    selectionMode: "bulk" as const, releaseTokenConfigured: true, canEdit: true,
  };
  const before = bootstrapOperationContext(context);
  assert.notEqual(bootstrapOperationContext({ ...context, expectedRevision: 10 }), before);
  assert.notEqual(bootstrapOperationContext({ ...context, canEdit: false }), before);
  assert.notEqual(bootstrapOperationContext({ ...context, currentHosts: [{ ...hosts[0]!, host_public_key: "rotated-public-key" }, hosts[1]!] }), before);
  assert.notEqual(bootstrapOperationContext({ ...context, currentTargets: [{ ...targets[0]!, local_listen_port: 18084 }, targets[1]!] }), before);

  const reordered = {
    ...context, savedHosts: [...hosts].reverse(), currentHosts: [...hosts].reverse(),
    savedTargets: [...targets].reverse(), currentTargets: [...targets].reverse(),
    selectedHostIDs: [...context.selectedHostIDs].reverse(),
  };
  assert.equal(bootstrapOperationContext(reordered), before,
    "identical selected operation inputs must not cancel asynchronous preparation because a list was reordered");
});

test("P1 Updater cached permission can start revalidation during a background fetch", () => {
  const cached = { permission: "allowed" as const, freshness: "refreshing" as const,
    applicability: "applicable" as const, authorityFingerprint: "cached-semantic-authority" };
  assert.equal(updaterActionCanRevalidate(cached), true);
  assert.equal(updaterActionCanRevalidate({ ...cached, permission: "denied" }), false);
  assert.equal(updaterActionCanRevalidate({ ...cached, permission: "unknown" }), false);
  assert.equal(updaterActionCanRevalidate({ ...cached, freshness: "unavailable" }), false);
  assert.equal(updaterActionCanRevalidate({ ...cached, applicability: "not-applicable" }), false);
});

test("P1 Updater actual authority reader uses isolated GET results while the cache keeps fetching", async () => {
  const previousFetch = globalThis.fetch;
  const previousDemo = process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO;
  process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = "false";
  const initial = normalizeSystemUpdatesResponse(response(target));
  const intent = { id: "UPD-01" as const, resourceId: target.target_id,
    authorityFingerprint: softwareUpdateAuthoritySnapshot("UPD-01", initial.targets[0]!, initial).fingerprint };
  const dependencies = { systemUpdates: { isFetching: true, data: initial }, currentUser: {
    isFetching: true, data: { username: "operator", permissions: ["system_updates.execute"] },
  } } as unknown as Parameters<typeof createApplicationAuthorityReaders>[0];
  const readers = createApplicationAuthorityReaders(dependencies);
  const signals: AbortSignal[] = [];
  let permission = true;
  let mutations = 0;
  globalThis.fetch = async (input, init) => {
    assert.ok(init?.signal instanceof AbortSignal, "the real apiGet must receive the operation's bounded signal");
    signals.push(init.signal);
    const path = String(input);
    assert.ok(path === "/auth/me" || path === "/system-updates", "authority revalidation is GET-only and narrowly scoped");
    assert.equal(init.method, "GET");
    const body = path === "/auth/me"
      ? { username: "operator", permissions: permission ? ["system_updates.execute"] : [] }
      : response(target);
    return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
  };
  try {
    const controller = createUpdaterActionController();
    const execute = () => controller.execute(intent, { confirmed: true },
      (signal) => readers.refreshTargetAuthority("UPD-01", target.target_id, signal), async () => { mutations += 1; });
    assert.equal((await execute()).kind, "succeeded");
    assert.equal(mutations, 1);
    assert.equal(dependencies.systemUpdates.isFetching, true, "cache polling is independent from the completed save intent");
    permission = false;
    assert.deepEqual(await execute(), { kind: "blocked", reason: "not-allowed" });
    assert.equal(mutations, 1, "fresh permission revocation must prevent a second mutation");
    assert.equal(signals.length, 4);
  } finally {
    globalThis.fetch = previousFetch;
    if (previousDemo === undefined) delete process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO;
    else process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = previousDemo;
  }
});

test("P1 Updater abort reaches actual authority GETs and never enters the mutation", { timeout: 3_000 }, async () => {
  const previousFetch = globalThis.fetch;
  const previousDemo = process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO;
  process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = "false";
  const initial = normalizeSystemUpdatesResponse(response(target));
  const intent = { id: "UPD-01" as const, resourceId: target.target_id,
    authorityFingerprint: softwareUpdateAuthoritySnapshot("UPD-01", initial.targets[0]!, initial).fingerprint };
  const readers = createApplicationAuthorityReaders({ systemUpdates: {}, currentUser: {} } as unknown as Parameters<typeof createApplicationAuthorityReaders>[0]);
  const signals: AbortSignal[] = [];
  let started!: () => void;
  const readsStarted = new Promise<void>((resolve) => { started = resolve; });
  let mutations = 0;
  globalThis.fetch = (_input, init) => new Promise<Response>((_resolve, reject) => {
    assert.ok(init?.signal instanceof AbortSignal);
    const signal = init.signal;
    signals.push(signal);
    signal.addEventListener("abort", () => reject(signal.reason), { once: true });
    if (signals.length === 2) started();
  });
  try {
    const abort = new AbortController();
    const result = createUpdaterActionController().execute(intent, { confirmed: true },
      (signal) => readers.refreshTargetAuthority("UPD-01", target.target_id, signal), async () => { mutations += 1; }, abort.signal);
    await readsStarted;
    abort.abort(new DOMException("Editor left", "AbortError"));
    assert.deepEqual(await result, { kind: "blocked", reason: "freshness-unavailable" });
    assert.equal(mutations, 0);
    assert.equal(signals.length, 2);
    assert.ok(signals.every((signal) => signal.aborted));
  } finally {
    globalThis.fetch = previousFetch;
    if (previousDemo === undefined) delete process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO;
    else process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = previousDemo;
  }
});

test("P1 port editor identity stays mounted when configuration authority changes", () => {
  const node: WorkerNode = { id: target.target_id, service_id: target.target_id, service_type: "worker",
    service_name: "Isolated Worker", status: "online", endpoint_revision: 7 };
  const changed = { ...target, applied_config_revision: 12, ownership_epoch: 6 };
  assert.equal(portControlKey(node, changed), portControlKey(node, target), "configuration changes must not remount and discard the draft");
  assert.notEqual(portConfigurationAuthorityKey(node, changed), portConfigurationAuthorityKey(node, target),
    "the retained editor must still detect a real configuration or ownership conflict");
});
