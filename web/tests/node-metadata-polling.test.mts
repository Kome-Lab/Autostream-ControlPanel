import assert from "node:assert/strict";
import test, { type TestContext } from "node:test";
import { readFileSync, existsSync } from "node:fs";
import { registerHooks } from "node:module";
import { fileURLToPath } from "node:url";
import ts from "typescript";
import { createElement } from "react";
import { renderToString } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const webRoot = new URL("../", import.meta.url);
registerHooks({
  resolve(specifier, context, next) {
    if (specifier.startsWith("@/") || (context.parentURL?.includes("/src/") && specifier.startsWith("."))) {
      const base = specifier.startsWith("@/") ? new URL("src/" + specifier.slice(2), webRoot) : new URL(specifier, context.parentURL);
      for (const suffix of ["", ".ts", ".tsx"]) {
        const target = new URL(base.href + suffix);
        if (existsSync(fileURLToPath(target))) return next(target.href, context);
      }
    }
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url.endsWith(".tsx")) return {
      format: "module", shortCircuit: true,
      source: ts.transpileModule(readFileSync(new URL(url), "utf8"), { compilerOptions: { module: ts.ModuleKind.ESNext, jsx: ts.JsxEmit.ReactJSX } }).outputText,
    };
    return next(url, context);
  },
});
const { useNodeRegistrationMutations } = await import("../src/features/nodes/use-node-registration-mutations.ts");
const revision = "a".repeat(64);
const baseline = () => ({ id: "node-1", service_id: "node-1", service_type: "worker", service_name: "Original", status: "online", host: "node.example.jp", port: 8084, ssl_enabled: true, metadata_revision: revision });
const values = () => ({ service_name: "Retained draft", description: "User input", host: "node.example.jp", port: "8084", ssl_enabled: true });

function fixture(t: TestContext) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const originalDemo = process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO;
  process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = "false";
  let current: ReturnType<typeof baseline> | null = baseline();
  let permissions = ["api_tokens.create"];
  let loseWriteResponse = false;
  const writes: Record<string, unknown>[] = [];
  const reads: string[] = [];
  const closed: unknown[] = [];
  let nodeReadGate: { started: () => void; wait: Promise<void> } | undefined;
  t.mock.method(globalThis, "fetch", async (input: string | URL | Request, init?: RequestInit) => {
    const path = String(input);
    if (init?.method === "GET") {
      assert.ok(init.signal, "save-time authority reads propagate their bounded signal");
      reads.push(path);
      if (path === "/auth/me") return Response.json({ permissions });
      if (path === "/nodes") {
        const snapshot = current;
        const gate = nodeReadGate;
        nodeReadGate = undefined;
        if (gate) { gate.started(); await gate.wait; }
        return Response.json(snapshot ? [snapshot] : []);
      }
    }
    if (init?.method === "PUT" && path === "/nodes/node-1") {
      writes.push(JSON.parse(String(init.body)));
      if (loseWriteResponse) { loseWriteResponse = false; throw new TypeError("controlled response loss"); }
      return Response.json({ ...current, service_name: "Retained draft" });
    }
    throw new Error("Unexpected controlled request");
  });
  let mutations!: ReturnType<typeof useNodeRegistrationMutations>;
  function Harness() {
    mutations = useNodeRegistrationMutations({
      registrationDraft: {} as Parameters<typeof useNodeRegistrationMutations>[0]["registrationDraft"],
      invalidateNodeQueries: async () => {}, setConfiguration: () => {}, setCreateOpen: () => {}, setEditingNode: (node) => closed.push(node),
    });
    return null;
  }
  renderToString(createElement(QueryClientProvider, { client }, createElement(Harness)));
  t.after(() => {
    client.clear();
    if (originalDemo === undefined) delete process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO;
    else process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = originalDemo;
  });
  return { client, writes, reads, closed, mutations, setCurrent: (row: ReturnType<typeof baseline> | null) => { current = row; }, revoke: () => { permissions = []; }, loseNextWriteResponse: () => { loseWriteResponse = true; },
    holdNodeRead: () => {
      let started!: () => void, release!: () => void;
      const readStarted = new Promise<void>((resolve) => { started = resolve; });
      const wait = new Promise<void>((resolve) => { release = resolve; });
      nodeReadGate = { started, wait };
      return { readStarted, release };
    },
  };
}

test("actual legacy Node save retains its draft-start metadata stamp through three monitoring refetches", async (t) => {
  const f = fixture(t), node = baseline(), draft = values();
  for (let cycle = 0; cycle < 3; cycle++) {
    f.setCurrent({ ...node, status: cycle % 2 ? "offline" : "online" });
    let finish!: () => void;
    const completion = new Promise<void>((resolve) => { finish = resolve; });
    const fetch = f.client.fetchQuery({ queryKey: ["nodes"], queryFn: async () => {
      await completion;
      return [{ ...node, metrics: { cpu: cycle }, last_heartbeat_at: `2026-10-28T00:00:0${cycle}Z` }];
    } });
    assert.equal(f.client.getQueryState(["nodes"])?.fetchStatus, "fetching");
    assert.deepEqual(draft, values());
    finish();
    await fetch;
  }
  await f.mutations.updateNode.mutateAsync({ nodeID: node.id, values: draft, endpointless: false, baseline: node });
  assert.deepEqual(f.writes, [{ service_name: draft.service_name, description: draft.description, host: draft.host, port: 8084, ssl_enabled: true, expected_metadata_revision: revision }]);
  assert.deepEqual(f.reads.sort(), ["/auth/me", "/nodes"]);
  assert.deepEqual(draft, values());
  assert.deepEqual(f.closed, [null]);
});

test("actual Node save never silently adopts another operator's metadata revision", async (t) => {
  const f = fixture(t), node = baseline(), draft = values();
  f.setCurrent({ ...node, service_name: "Other operator", metadata_revision: "b".repeat(64) });
  await assert.rejects(f.mutations.updateNode.mutateAsync({ nodeID: node.id, values: draft, endpointless: false, baseline: node }), { code: "node_metadata_conflict" });
  assert.deepEqual(f.writes, []);
  assert.deepEqual(f.closed, []);
  assert.deepEqual(draft, values());
});

test("actual Node save fails closed for missing edit stamp or revoked permission", async (t) => {
  const f = fixture(t), node = baseline();
  await assert.rejects(f.mutations.updateNode.mutateAsync({ nodeID: node.id, values: values(), endpointless: false, baseline: { ...node, metadata_revision: undefined } }), { code: "node_metadata_revision_required" });
  f.revoke();
  await assert.rejects(f.mutations.updateNode.mutateAsync({ nodeID: node.id, values: values(), endpointless: false, baseline: node }), { code: "permission_denied" });
  assert.deepEqual(f.writes, []);
});

test("one Node save intent waits for its slow authority read while another poll starts", async (t) => {
  const f = fixture(t), node = baseline(), gate = f.holdNodeRead();
  const save = f.mutations.updateNode.mutateAsync({ nodeID: node.id, values: values(), endpointless: false, baseline: node });
  assert.equal(await Promise.race([gate.readStarted.then(() => true), save.then(() => false)]), true, "the actual caller must revalidate before its first write");
  assert.deepEqual(f.writes, []);
  let finishPoll!: () => void;
  const poll = f.client.fetchQuery({ queryKey: ["nodes"], queryFn: () => new Promise<unknown[]>((resolve) => { finishPoll = () => resolve([node]); }) });
  const settledPoll = Promise.allSettled([poll]);
  try {
    assert.equal(f.client.getQueryState(["nodes"])?.fetchStatus, "fetching");
    gate.release();
    await save;
    assert.equal(f.writes.length, 1, "the first intent dispatches once without a second click or retry loop");
  } finally {
    finishPoll();
    await settledPoll;
  }
});

test("a deleted Node is rejected without clearing the editing session", async (t) => {
  const f = fixture(t), node = baseline();
  f.setCurrent(null);
  await assert.rejects(f.mutations.updateNode.mutateAsync({ nodeID: node.id, values: values(), endpointless: false, baseline: node }), { code: "not_found" });
  assert.deepEqual(f.writes, []);
  assert.deepEqual(f.closed, []);
});

test("a lost Node update response stays unresolved through a later successful GET", async (t) => {
  const f = fixture(t), node = baseline(), request = { nodeID: node.id, values: values(), endpointless: false, baseline: node };
  f.loseNextWriteResponse();
  await assert.rejects(f.mutations.updateNode.mutateAsync(request), { code: "node_update_outcome_unknown" });
  f.client.setQueryData(["nodes"], [node]);
  await assert.rejects(f.mutations.updateNode.mutateAsync(request), { code: "node_update_outcome_unknown" });
  assert.equal(f.writes.length, 1, "a successful list read cannot prove a lost update was not applied");
  assert.deepEqual(f.closed, []);
});
