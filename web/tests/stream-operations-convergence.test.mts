import assert from "node:assert/strict";
import { register } from "node:module";
import test from "node:test";

register(`data:text/javascript,${encodeURIComponent([
  "let root; export function initialize(data) { root = data.root; }",
  "export async function resolve(specifier, context, next) {",
  "if (specifier.startsWith('@/')) { const target = new URL('src/' + specifier.slice(2) + '.ts', root); return next(target.href, context); }",
  "return next(specifier, context); }",
].join("\n"))}`, { parentURL: import.meta.url, data: { root: new URL("../", import.meta.url).href } });

const { createStreamActionController } = await import("../src/features/streams/stream-action-controller.ts");
const stream = { id: "ui188-stream", name: "UI188", status: "live", updated_at:"2026-10-12T00:00:00Z" };
const intent = { id: "STR-05" as const, stream };

function controller(mutate: () => Promise<unknown>, readStopOutcome?: () => Promise<unknown>) {
  return createStreamActionController({
    getPermissions: () => ({ kind: "ready", permissions: ["streams.stop"] }),
    getState: () => ({ kind: "ready", freshness: "fresh", fingerprint: "live-job-1" }),
    mutate,
    readStopOutcome,
  });
}

test("normal Stop accepts the canonical envelope, completed row and separate waiting successor", async () => {
  const value = { stream: { ...stream, status: "completed" }, dispatch: [{ accepted: true }], waiting_stream: { ...stream, id: "successor", status: "created" } };
  const action = controller(async () => value);
  const opened = await action.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  assert.deepEqual(await action.submit(opened, { confirmed: true }), { kind: "succeeded", value });
});

test("lost Stop response reads its exact completed stream and never resends Stop", async () => {
  let posts = 0, reads = 0;
  const completed = { stream:{ ...stream, status: "completed" }, stop_confirmed:true,source_updated_at:stream.updated_at };
  const action = controller(async () => { posts++; throw new TypeError("response lost"); }, async () => { reads++; return completed; });
  const opened = await action.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  assert.deepEqual(await action.submit(opened, { confirmed: true }), { kind: "succeeded", value: completed });
  assert.equal(posts, 1);
  assert.equal(reads, 1);
  assert.equal(action.evaluate(intent).availability.kind, "allowed");
});

for (const value of [undefined, {}, { ...stream, status: "live" }, { ...stream, status: "failed" }, { ...stream, id: "another-stream", status: "completed" }]) {
  test(`Stop uncertainty retains its latch for unconfirmed read ${JSON.stringify(value)}`, async () => {
    let posts = 0;
    const action = controller(async () => { posts++; throw new TypeError("response lost"); }, async () => value);
    const opened = await action.open(intent);
    assert.equal(opened.kind, "allowed");
    if (opened.kind !== "allowed") return;
    assert.equal((await action.submit(opened, { confirmed: true })).kind, "outcome_unknown");
    assert.equal((await action.submit(opened, { confirmed: true })).kind, "blocked");
    assert.equal(posts, 1);
  });
}

test("HTTP 200 without the Stop receipt cannot claim a successful stop", async () => {
  const action = controller(async () => ({ status: "ok" }), async () => ({ ...stream, status: "stopping" }));
  const opened = await action.open(intent);
  assert.equal(opened.kind, "allowed");
  if (opened.kind !== "allowed") return;
  assert.equal((await action.submit(opened, { confirmed: true })).kind, "outcome_unknown");
});
