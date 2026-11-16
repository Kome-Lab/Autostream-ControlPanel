import assert from "node:assert/strict";
import { createServer } from "node:http";
import { register } from "node:module";
import test from "node:test";

register(`data:text/javascript,${encodeURIComponent([
  "let root; export function initialize(data) { root=data.root; }",
  "export async function resolve(specifier,context,next) {",
  "if(specifier.startsWith('@/')) return next(new URL('src/'+specifier.slice(2)+'.ts',root).href,context);",
  "if(specifier.startsWith('.') && context.parentURL?.startsWith(root+'src/') && !/\\.[cm]?[jt]sx?$/.test(specifier)) return next(new URL(specifier+'.ts',context.parentURL).href,context);",
  "return next(specifier,context); }",
].join("\n"))}`, { parentURL: import.meta.url, data: { root: new URL("../", import.meta.url).href } });

const { createStreamActionController } = await import("../src/features/streams/stream-action-controller.ts");
const { mutateStreamAction, readStreamStopOutcome } = await import("../src/features/streams/stream-action-runtime.ts");
const { QueryClient } = await import("@tanstack/react-query");
const stream = { id: "msr-original-stop", name: "Synthetic manual stop", status: "live", updated_at: "2026-11-16T00:00:00.000001Z" };

test("MSR-UI-07: actual Stop transport retains one bounded original request through ACK loss and pure lookup", async t => {
  let posts = 0, ids = 0, confirmed = false, postedID = "";
  const lookups: string[] = [];
  const server = createServer(async (req, res) => {
    const url = new URL(req.url || "/", "http://localhost");
    if (req.method === "POST" && url.pathname === `/streams/${stream.id}/stop`) {
      posts++;
      assert.equal(req.headers["x-csrf-token"], "synthetic-browser-csrf");
      postedID = String(req.headers["x-autostream-request-id"] || "");
      assert.equal(postedID, "00000000-0000-4000-8000-000000000071");
      let body = ""; for await (const chunk of req) body += chunk;
      assert.deepEqual(JSON.parse(body), { expected_updated_at: stream.updated_at });
      res.destroy(); return;
    }
    if (req.method === "GET" && url.pathname === `/streams/${stream.id}/stop-outcome`) {
      lookups.push(url.searchParams.get("request_id") || "");
      assert.equal(url.searchParams.get("expected_updated_at"), stream.updated_at);
      res.writeHead(confirmed ? 200 : 503, { "Content-Type": "application/json" });
      res.end(JSON.stringify(confirmed ? { stream: { ...stream, status: "completed" }, stop_confirmed: true, source_updated_at: stream.updated_at, original_attempt_id: postedID }
        : { code: "manual_hold_unknown" }));
      return;
    }
    res.writeHead(404); res.end();
  });
  await new Promise<void>(resolve => server.listen(0, "127.0.0.1", resolve));
  const address = server.address(); assert.ok(address && typeof address === "object");
  const origin = `http://127.0.0.1:${address.port}`;
  const fetch = globalThis.fetch, windowDescriptor = Object.getOwnPropertyDescriptor(globalThis, "window");
  Object.defineProperty(globalThis, "window", { configurable: true, value: { location: { port: String(address.port) }, sessionStorage: { getItem: (key: string) => key === "autostream.csrf_token" ? "synthetic-browser-csrf" : null } } });
  globalThis.fetch = (input, init) => {
    const target = new URL(String(input), origin);
    assert.equal(target.origin, origin, "this test cannot contact a real provider");
    return fetch(target, init);
  };
  t.after(async () => {
    globalThis.fetch = fetch;
    if (windowDescriptor) Object.defineProperty(globalThis, "window", windowDescriptor); else Reflect.deleteProperty(globalThis, "window");
    await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
  });
  const cache = new QueryClient(), intent = { id: "STR-05" as const, stream };
  const controller = createStreamActionController({ getPermissions: () => ({ kind: "ready", permissions: ["streams.stop"] }),
    getState: () => ({ kind: "ready", freshness: "fresh", fingerprint: "original-stream-revision" }),
    newStopRequestID: () => { ids++; return "00000000-0000-4000-8000-000000000071"; }, mutate: mutateStreamAction,
    readStopOutcome: (original, id) => readStreamStopOutcome(cache, original, id) });
  const opened = await controller.open(intent); assert.equal(opened.kind, "allowed"); if (opened.kind !== "allowed") throw Error("expected original Stop");
  assert.equal((await controller.submit(opened, { confirmed: true })).kind, "outcome_unknown");
  assert.equal((await controller.submit(opened, { confirmed: true })).kind, "blocked");
  assert.equal(posts, 1); assert.equal(ids, 1);
  confirmed = true;
  assert.equal((await controller.reconcileStop(intent)).kind, "succeeded");
  assert.equal(posts, 1); assert.equal(ids, 1); assert.deepEqual(lookups, [postedID, postedID]);
  assert.ok(postedID.length <= 80);
});

test("MSR-UI-08: force Stop retains its original UUID without borrowing a normal Stop receipt or retrying", async t => {
  let posts = 0, ids = 0, normalLookups = 0;
  const original = "00000000-0000-4000-8000-000000000081";
  const server = createServer(async (req, res) => {
    assert.equal(req.method, "POST"); assert.equal(req.url, `/streams/${stream.id}/force-stop`);
    assert.equal(req.headers["x-autostream-request-id"], original);
    posts++; for await (const chunk of req) void chunk;
    res.destroy();
  });
  await new Promise<void>(resolve => server.listen(0, "127.0.0.1", resolve));
  const address = server.address(); assert.ok(address && typeof address === "object");
  const origin = `http://127.0.0.1:${address.port}`, fetch = globalThis.fetch;
  globalThis.fetch = (input, init) => { const target = new URL(String(input), origin); assert.equal(target.origin, origin); return fetch(target, init); };
  t.after(async () => { globalThis.fetch = fetch; await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())); });
  const intent = { id: "STR-06" as const, stream };
  const controller = createStreamActionController({ getPermissions: () => ({ kind: "ready", permissions: ["streams.stop"] }), getState: () => ({ kind: "ready", freshness: "fresh", fingerprint: "original" }),
    newStopRequestID: () => { ids++; return original; }, mutate: mutateStreamAction,
    readStopOutcome: async () => { normalLookups++; return { stream: { ...stream, status: "completed" }, stop_confirmed: true, source_updated_at: stream.updated_at }; } });
  const opened = await controller.open(intent); assert.equal(opened.kind, "allowed"); if (opened.kind !== "allowed") throw Error("expected force Stop confirmation");
  const confirmation = { confirmed: true, typedValue: opened.typedToken };
  assert.equal((await controller.submit(opened, confirmation)).kind, "outcome_unknown");
  assert.equal(controller.getOriginalRequestID?.(intent), original);
  assert.equal((await controller.submit(opened, confirmation)).kind, "blocked");
  assert.equal((await controller.reconcileStop(intent)).kind, "blocked");
  assert.deepEqual([posts, ids, normalLookups], [1, 1, 0]);
});
