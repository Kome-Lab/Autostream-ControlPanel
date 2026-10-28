import assert from "node:assert/strict";
import test from "node:test";

import { QueryClient } from "@tanstack/react-query";
import {
  apiError,
  auth,
  configurationHarness,
  deferred as legacyDeferred,
  restartHarness,
  worker as legacyWorker,
} from "./workers-pilot-fixture.mts";
import { registerSourceResolution } from "./helpers/moved-source.mts";
import type { WorkerNode } from "../src/types/domain.ts";

registerSourceResolution();

const { createAppSettingsActionController } = await import("../src/features/settings/app-settings-action-policy.ts");
const { appSettingsPermissionSnapshot, appSettingsStateSnapshot } = await import("../src/features/settings/app-settings-action-runtime.ts");
const { createWorkerRestartController } = await import("../src/features/workers/workers-action-controller.ts");

const nextTurn = () => new Promise<void>((resolve) => setImmediate(resolve));
const worker = (id: string): WorkerNode => ({ ...legacyWorker(id), id });
function deferred() {
  const value = legacyDeferred();
  assert.ok(value.resolve, "the controlled fixture must expose its response resolver");
  return { ...value, resolve: value.resolve };
}

test("P1 Worker restart keeps a successful authority snapshot usable during same-content background GETs", async () => {
  const harness = restartHarness();
  const authGate = deferred();
  const workersGate = deferred();
  const authFetch = harness.queryClient.fetchQuery({
    queryKey: ["auth", "me"],
    queryFn: () => authGate.promise,
    staleTime: 0,
  });
  const workersFetch = harness.queryClient.fetchQuery({
    queryKey: ["workers"],
    queryFn: () => workersGate.promise,
    staleTime: 0,
  });
  try {
    await nextTurn();
    assert.equal(harness.queryClient.getQueryState(["auth", "me"])?.fetchStatus, "fetching");
    assert.equal(harness.queryClient.getQueryState(["workers"])?.fetchStatus, "fetching");
    assert.equal(harness.controller.evaluate(worker("worker-1")).availability.kind, "allowed",
      "a background read is not evidence that the previously confirmed permission was revoked");
    assert.equal(harness.controller.open(worker("worker-1")).kind, "allowed",
      "one restart intent must reach submit-time revalidation instead of requiring another click");
    assert.equal(harness.calls().posts, 0, "opening confirmation must not restart a Worker");
  } finally {
    authGate.resolve(auth(["workers.restart"]));
    workersGate.resolve([worker("worker-1")]);
    await Promise.allSettled([authFetch, workersFetch]);
    harness.queryClient.clear();
  }
});

test("P1 Worker Configuration preserves the selected cached data while permission is being rechecked", async () => {
  const harness = configurationHarness();
  await harness.controller.select("node-1");
  const previous = harness.controller.getSnapshot();
  assert.equal(previous.state.kind, "ready");
  const authGate = deferred();
  const authFetch = harness.queryClient.fetchQuery({
    queryKey: ["auth", "me"],
    queryFn: () => authGate.promise,
    staleTime: 0,
  });
  let refresh: ReturnType<typeof harness.controller.refresh> | undefined;
  try {
    await nextTurn();
    refresh = harness.controller.refresh();
    await nextTurn();
    const pending = harness.controller.getSnapshot();
    assert.equal(pending.targetId, "node-1");
    assert.equal(pending.state.kind, "ready",
      "a cached Configuration must not become a blocking error merely because auth GET is in flight");
    if (previous.state.kind === "ready" && pending.state.kind === "ready") {
      assert.deepEqual(pending.state.data, previous.state.data);
    }
    authGate.resolve(auth(["service_health.read"]));
    await authFetch;
    await refresh;
    assert.equal(harness.controller.getSnapshot().state.kind, "ready");
  } finally {
    authGate.resolve(auth(["service_health.read"]));
    await Promise.allSettled([authFetch, refresh]);
    harness.queryClient.clear();
  }
});

test("P1 application settings keeps cached successful settings and permissions usable during background reads", async () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const settings = { app_name: "Draft baseline", timezone: "Asia/Tokyo", updated_at: "2026-10-28T00:00:00Z" };
  queryClient.setQueryData(["auth", "me"], auth(["system_settings.update"]));
  queryClient.setQueryData(["settings", "app", "manage"], settings);
  const before = appSettingsStateSnapshot(queryClient);
  const authGate = deferred();
  const settingsGate = deferred();
  const reads = [
    queryClient.fetchQuery({ queryKey: ["auth", "me"], queryFn: () => authGate.promise, staleTime: 0 }),
    queryClient.fetchQuery({ queryKey: ["settings", "app", "manage"], queryFn: () => settingsGate.promise, staleTime: 0 }),
  ];
  try {
    await nextTurn();
    assert.equal(queryClient.getQueryState(["auth", "me"])?.fetchStatus, "fetching");
    assert.equal(appSettingsPermissionSnapshot(queryClient).kind, "ready");
    const current = appSettingsStateSnapshot(queryClient);
    assert.equal(current.kind, "ready");
    assert.equal(current.freshness, "fresh",
      "fetch activity alone must not be treated as a different application configuration");
    assert.equal(current.fingerprint, before.fingerprint);
  } finally {
    authGate.resolve(auth(["system_settings.update"]));
    settingsGate.resolve(settings);
    await Promise.allSettled(reads);
    queryClient.clear();
  }
});

test("P1 a definite pre-send APP-02 recipient rejection allows one corrected explicit submission", async () => {
  let requests = 0;
  const permissions = Object.freeze({ kind: "ready" as const, permissions: Object.freeze(["system_settings.update"]) });
  const state = Object.freeze({ kind: "ready" as const, freshness: "fresh" as const, fingerprint: "same-settings" });
  const controller = createAppSettingsActionController({
    getPermissions: () => permissions,
    getState: () => state,
    refresh: async () => ({ permissions, state }),
    mutate: async () => {
      requests += 1;
      // server_settings.go rejects this code before any Mailer.Send call.
      if (requests === 1) throw apiError(400, "invalid_email_recipient");
      return { status: "sent" };
    },
  });
  const firstIntent = Object.freeze({ id: "APP-02" as const, payload: Object.freeze({ to: "invalid recipient" }) });
  const first = await controller.open(firstIntent);
  assert.equal(first.kind, "allowed");
  if (first.kind !== "allowed") return;
  assert.equal((await controller.submit(first, { confirmed: true })).kind, "failed");
  assert.equal(requests, 1, "a deterministic rejection must not trigger automatic resend");
  const correctedIntent = Object.freeze({ id: "APP-02" as const, payload: Object.freeze({ to: "corrected@example.invalid" }) });
  const corrected = await controller.open(correctedIntent);
  assert.equal(corrected.kind, "allowed",
    "the known pre-send recipient rejection must not latch the whole settings session as outcome unknown");
  if (corrected.kind !== "allowed") return;
  assert.equal((await controller.submit(corrected, { confirmed: true })).kind, "succeeded");
  assert.equal(requests, 2);
});

test("P1 an ambiguous APP-02 result still forbids a second mail request without reconciliation", async () => {
  let requests = 0;
  const permissions = Object.freeze({ kind: "ready" as const, permissions: Object.freeze(["system_settings.update"]) });
  const state = Object.freeze({ kind: "ready" as const, freshness: "fresh" as const, fingerprint: "same-settings" });
  const controller = createAppSettingsActionController({
    getPermissions: () => permissions,
    getState: () => state,
    refresh: async () => ({ permissions, state }),
    mutate: async () => { requests += 1; throw new TypeError("isolated response-loss fixture"); },
  });
  const intent = Object.freeze({ id: "APP-02" as const, payload: Object.freeze({ to: "operator@example.invalid" }) });
  const opened = await controller.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  assert.equal((await controller.submit(opened, { confirmed: true })).kind, "outcome_unknown");
  assert.deepEqual(await controller.open(intent), { kind: "blocked", reason: "reconciliation-required" });
  assert.equal(requests, 1);
});

test("P1 Worker submit uses its fresh permission proof while a newer background read remains in flight", async () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const current = worker("worker-1");
  const permissions = Object.freeze({ kind: "ready" as const, permissions: Object.freeze(["workers.restart"]) });
  queryClient.setQueryData(["auth", "me"], auth(["workers.restart"]));
  queryClient.setQueryData(["workers"], [current]);
  const proof = deferred();
  const authGate = deferred();
  let posts = 0, reads = 0;
  const controller = createWorkerRestartController({
    queryClient,
    revalidate: async (signal) => {
      assert.equal(signal.aborted, false);
      reads += 1;
      await proof.promise;
      return { permissions, workers: [current] };
    },
    postRestart: async () => { posts += 1; return { status: "accepted" }; },
  });
  const opened = controller.open(current);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  const execution = controller.submit(opened);
  const authFetch = queryClient.fetchQuery({ queryKey: ["auth", "me"], queryFn: () => authGate.promise, staleTime: 0 });
  try {
    await nextTurn();
    assert.equal(posts, 0);
    proof.resolve(undefined);
    const result = await execution;
    assert.equal(queryClient.getQueryState(["auth", "me"])?.fetchStatus, "fetching");
    assert.equal(result.outcome?.kind, "succeeded");
    assert.equal(reads, 1);
    assert.equal(posts, 1);
  } finally {
    proof.resolve(undefined);
    authGate.resolve(auth(["workers.restart"]));
    await Promise.allSettled([authFetch, execution]);
    queryClient.clear();
  }
});

test("P1 fresh Worker permission revocation blocks restart even when the cache still grants it", async () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const current = worker("worker-1");
  queryClient.setQueryData(["auth", "me"], auth(["workers.restart"]));
  queryClient.setQueryData(["workers"], [current]);
  let posts = 0;
  const controller = createWorkerRestartController({
    queryClient,
    revalidate: async () => ({ permissions: { kind: "ready", permissions: [] }, workers: [current] }),
    postRestart: async () => { posts += 1; return {}; },
  });
  try {
    const opened = controller.open(current);
    assert.equal(opened.kind, "allowed");
    if (opened.kind !== "allowed") return;
    const result = await controller.submit(opened);
    assert.equal(result.outcome, undefined);
    assert.equal(result.state.kind, "revalidation-unavailable");
    assert.equal(posts, 0);
  } finally {
    queryClient.clear();
  }
});

test("P1 APP-01 compares with the edit-session baseline before adopting the latest GET", async () => {
  let mutations = 0;
  const permissions = Object.freeze({ kind: "ready" as const, permissions: Object.freeze(["system_settings.update"]) });
  const oldState = Object.freeze({ kind: "ready" as const, freshness: "fresh" as const, fingerprint: "original-settings" });
  const newState = Object.freeze({ kind: "ready" as const, freshness: "fresh" as const, fingerprint: "changed-by-another-user" });
  const controller = createAppSettingsActionController({
    getPermissions: () => permissions,
    getState: () => oldState,
    revalidate: async () => ({ permissions, state: newState }),
    mutate: async () => { mutations += 1; return {}; },
  });
  const intent = Object.freeze({ id: "APP-01" as const, payload: Object.freeze({ app_name: "Unsaved draft" }), baselineFingerprint: oldState.fingerprint });
  assert.deepEqual(await controller.open(intent), { kind: "blocked", reason: "authority-changed" });
  assert.equal(mutations, 0);
});

test("P1 cancelling an APP confirmation-opening read aborts it and releases only that operation", async () => {
  const permission = Object.freeze({ kind: "ready" as const, permissions: Object.freeze(["system_settings.update"]) });
  const state = Object.freeze({ kind: "ready" as const, freshness: "fresh" as const, fingerprint: "original-settings" });
  const response = deferred();
  let observedSignal: AbortSignal | undefined;
  let reads = 0, mutations = 0;
  const controller = createAppSettingsActionController({
    getPermissions: () => permission,
    getState: () => state,
    revalidate: async (_intent, signal) => {
      reads += 1;
      if (reads === 1) {
        observedSignal = signal;
        await response.promise;
      }
      if (signal.aborted) throw signal.reason;
      return { permissions: permission, state };
    },
    mutate: async () => { mutations += 1; return {}; },
  });
  const intent = Object.freeze({ id: "APP-01" as const, payload: Object.freeze({ app_name: "Unsaved draft" }), baselineFingerprint: state.fingerprint });
  const opening = controller.open(intent);
  try {
    await nextTurn();
    assert.equal(reads, 1);
    assert.deepEqual(await controller.open(intent), { kind: "blocked", reason: "duplicate" });
    controller.cancel(intent);
    assert.deepEqual(await opening, { kind: "blocked", reason: "cancelled" });
    assert.equal(observedSignal?.aborted, true, "cancellation must reach the actual read callback");
    assert.equal(mutations, 0);
    response.resolve(undefined);
    await nextTurn();
    assert.equal((await controller.open(intent)).kind, "allowed", "the cancelled operation must release its duplicate scope");
    assert.equal(reads, 2);
    assert.equal(mutations, 0);
  } finally {
    response.resolve(undefined);
    controller.cancel();
    await opening;
  }
});
