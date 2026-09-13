import assert from "node:assert/strict";
import test from "node:test";
import { deferred, restartHarness, worker, allowedOpen, waitFor, apiError, workerConfigurationDescriptor, authority, configurationHarness, auth, configuration } from "./workers-pilot-fixture.mts";


export function registerWorkerActionLifecycleCases() {


test("same-worker duplicate activations latch before authority GET while different workers remain independent", async () => {
  const firstPost = deferred();
  const secondPost = deferred();
  const harness = restartHarness({
    post: (path) => path.includes("worker-1") ? firstPost.promise : secondPost.promise,
  });
  harness.setCachedWorkers([worker("worker-1"), worker("worker-2")]);
  harness.setFreshWorkers([worker("worker-1"), worker("worker-2")]);
  const openOne = allowedOpen(harness.controller, worker("worker-1"));
  const openTwo = allowedOpen(harness.controller, worker("worker-2"));

  const first = harness.controller.submit(openOne);
  await waitFor(() => harness.postInvocations.length === 1);
  const duplicate = await harness.controller.submit(openOne);
  assert.equal(duplicate.state.kind, "revalidation-unavailable");
  assert.deepEqual(harness.calls(), { gets: 1, posts: 1, invalidations: 0 });

  const second = harness.controller.submit(openTwo);
  await waitFor(() => harness.postInvocations.length === 2);
  assert.equal(harness.postInvocations[1].path, "/workers/worker-2/restart");
  firstPost.resolve({ status: "accepted" });
  secondPost.resolve({ status: "accepted" });
  await Promise.all([first, second]);
  assert.deepEqual(harness.calls(), { gets: 2, posts: 2, invalidations: 2 });
});

test("403, 409 and transport ambiguity never resend a non-idempotent restart", async () => {
  for (const scenario of [
    { name: "403", error: apiError(403, "permission_denied"), expectedState: "failed", expectedOutcome: "failed", expectedGets: 1 },
    { name: "409", error: apiError(409, "worker_busy"), expectedState: "conflict", expectedOutcome: "failed", expectedGets: 2 },
    { name: "network", error: new TypeError("RAW NETWORK MARKER"), expectedState: "outcome-unknown", expectedOutcome: "outcome_unknown", expectedGets: 1 },
    { name: "timeout", error: Object.assign(new Error("RAW TIMEOUT MARKER"), { name: "TimeoutError" }), expectedState: "outcome-unknown", expectedOutcome: "outcome_unknown", expectedGets: 1 },
    { name: "protocol", error: apiError(200, "non_json_response"), expectedState: "outcome-unknown", expectedOutcome: "outcome_unknown", expectedGets: 1 },
  ]) {
    const harness = restartHarness({ post: async () => { throw scenario.error; } });
    const result = await harness.controller.submit(allowedOpen(harness.controller, worker("worker-1")));
    assert.equal(result.state.kind, scenario.expectedState, scenario.name);
    assert.equal(result.outcome?.kind, scenario.expectedOutcome, scenario.name);
    assert.deepEqual(harness.calls(), { gets: scenario.expectedGets, posts: 1, invalidations: 0 }, scenario.name);
    if (scenario.name === "403") assert.equal("error" in result.state ? result.state.error.kind : undefined, "forbidden");
    if (scenario.name === "409") assert.equal("error" in result.state ? result.state.error.kind : undefined, "conflict");
    assert.equal(JSON.stringify(result).includes("RAW"), false, `${scenario.name} retained a raw error marker`);
  }
});

test("Configuration descriptor preserves exact server ANY permission independently of page access", () => {
  assert.deepEqual(workerConfigurationDescriptor, {
    labelKey: "workerConfigurationAction",
    permissions: { kind: "any", permissions: ["service_health.read", "api_tokens.create"] },
    disclosure: "visible-denied",
  });
  assert.deepEqual(authority.configuration, {
    method: "GET",
    pathTemplate: "/nodes/{encoded_id}/configuration",
    permission: { kind: "any", permissions: ["service_health.read", "api_tokens.create"] },
    pagePermission: "workers.read",
    queryKey: null,
  });
  const harness = configurationHarness();
  for (const [permissions, expected] of [
    [["service_health.read"], "allowed"],
    [["api_tokens.create"], "allowed"],
    [["service_health.read", "api_tokens.create"], "allowed"],
    [["*"], "allowed"],
    [[], "denied"],
    [["workers.read"], "denied"],
  ]) {
    harness.setAuth(permissions);
    assert.equal(harness.controller.evaluate().availability.kind, expected, permissions.join(","));
  }
});

test("Configuration handler re-evaluates latest auth and sends no GET when denied or unknown", async () => {
  const denied = configurationHarness();
  denied.setAuth(["workers.read"]);
  await denied.controller.select("node-1");
  assert.equal(denied.calls(), 0);
  assert.equal(denied.controller.getSnapshot().evaluation.availability.kind, "denied");

  const removed = configurationHarness();
  assert.equal(removed.controller.evaluate().availability.kind, "allowed");
  removed.setAuth([]);
  await removed.controller.select("node-1");
  assert.equal(removed.calls(), 0);

  const refreshing = configurationHarness();
  const authRefresh = deferred();
  const refreshPromise = refreshing.queryClient.fetchQuery({ queryKey: ["auth", "me"], queryFn: () => authRefresh.promise });
  await waitFor(() => refreshing.controller.evaluate().availability.kind === "unknown");
  await refreshing.controller.select("node-1");
  assert.equal(refreshing.calls(), 0);
  authRefresh.resolve(auth(["service_health.read"]));
  await refreshPromise;
});

test("Configuration target generation fence never renders old target data as the new target", async () => {
  const nodeA = deferred();
  const nodeB = deferred();
  const harness = configurationHarness({
    get: (path) => path.includes("node-a") ? nodeA.promise : nodeB.promise,
  });
  const first = harness.controller.select("node-a");
  await waitFor(() => harness.calls() === 1);
  const second = harness.controller.select("node-b");
  assert.equal(harness.controller.getSnapshot().targetId, "node-b");
  assert.equal(harness.controller.getSnapshot().state.kind, "initial-loading");
  nodeA.resolve(configuration("node-a", "CONFIG-A-MARKER"));
  await first;
  assert.equal(JSON.stringify(harness.controller.getSnapshot()).includes("CONFIG-A-MARKER"), false);
  nodeB.resolve(configuration("node-b", "CONFIG-B-MARKER"));
  await second;
  assert.equal(JSON.stringify(harness.controller.getSnapshot()).includes("CONFIG-B-MARKER"), true);
});

test("Configuration rejects a response whose node does not match the selected target", async () => {
  const harness = configurationHarness({
    get: async () => configuration("different-node", "WRONG-TARGET-CONFIG-MARKER"),
  });
  await harness.controller.select("node-1");
  const snapshot = harness.controller.getSnapshot();
  assert.equal(snapshot.targetId, "node-1");
  assert.equal(snapshot.state.kind, "blocking-error");
  assert.equal(JSON.stringify(snapshot).includes("WRONG-TARGET-CONFIG-MARKER"), false);
  assert.equal(harness.calls(), 1);
});

test("same-target refresh preserves cached configuration through pending and safe stale error", async () => {
  const refresh = deferred();
  let invocation = 0;
  const harness = configurationHarness({
    get: async () => {
      invocation += 1;
      if (invocation === 1) return configuration("node-1", "CACHED-CONFIG-MARKER");
      return refresh.promise;
    },
  });
  await harness.controller.select("node-1");
  const pending = harness.controller.refresh();
  assert.equal(harness.controller.getSnapshot().state.kind, "ready");
  assert.equal(harness.controller.getSnapshot().state.freshness.kind, "refreshing");
  assert.equal(JSON.stringify(harness.controller.getSnapshot()).includes("CACHED-CONFIG-MARKER"), true);
  refresh.reject(new TypeError("RAW CONFIGURATION ERROR MARKER"));
  await pending;
  const stale = harness.controller.getSnapshot();
  assert.equal(stale.state.kind, "ready");
  assert.equal(stale.state.freshness.kind, "stale");
  assert.equal(JSON.stringify(stale).includes("CACHED-CONFIG-MARKER"), true);
  assert.equal(JSON.stringify(stale).includes("RAW CONFIGURATION ERROR MARKER"), false);
});

test("Configuration distinguishes empty, malformed blocking error and safe manual retry", async () => {
  const empty = configurationHarness({ get: async () => ({ node: worker("node-1") }) });
  await empty.controller.select("node-1");
  assert.equal(empty.controller.getSnapshot().state.kind, "empty");

  const malformed = configurationHarness({ get: async () => ({ node: worker("node-1"), configuration_yaml: 42 }) });
  await malformed.controller.select("node-1");
  assert.equal(malformed.controller.getSnapshot().state.kind, "blocking-error");

  let attempt = 0;
  const retry = configurationHarness({ get: async () => {
    attempt += 1;
    if (attempt === 1) throw apiError(500, "get_node_failed");
    return configuration("node-1", "RETRY-CONFIG-MARKER");
  } });
  await retry.controller.select("node-1");
  assert.equal(retry.controller.getSnapshot().state.kind, "blocking-error");
  await retry.controller.refresh();
  assert.equal(retry.controller.getSnapshot().state.kind, "ready");
  assert.equal(retry.calls(), 2);
});

test("service_id-only Configuration becomes ready with a detached canonical node", async () => {
  const sourceNode = worker("worker-real-shape-1", "Worker A");
  const harness = configurationHarness({
    get: async () => ({
      node: sourceNode,
      node_api_url: "https://worker.invalid",
      configuration_yaml: "safe-fixture",
      configure_command: "safe-fixture",
      systemd_unit: "[Unit]",
    }),
  });
  await harness.controller.select("worker-real-shape-1");
  const snapshot = harness.controller.getSnapshot();
  assert.equal(harness.calls(), 1);
  assert.equal(snapshot.state.kind, "ready");
  assert.equal(snapshot.state.data.node.id, "worker-real-shape-1");
  assert.equal(snapshot.state.data.node.service_id, "worker-real-shape-1");
  assert.notEqual(snapshot.state.data.node, sourceNode);
  assert.equal(snapshot.state.data.configuration_yaml, "safe-fixture");
  assert.equal(snapshot.state.data.configure_command, "safe-fixture");
  assert.equal(snapshot.state.data.systemd_unit, "[Unit]");
});
}
