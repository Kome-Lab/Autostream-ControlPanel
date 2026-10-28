import assert from "node:assert/strict";
import { register } from "node:module";
import test from "node:test";
import { registerSourceResolution } from "./helpers/moved-source.mts";

register(`data:text/javascript,${encodeURIComponent(`
let root;
export function initialize(data) { root = data.root; }
export async function resolve(specifier, context, nextResolve) {
  if (specifier.startsWith('@/')) {
    const target = new URL('src/' + specifier.slice(2), root);
    if (!/\\.[cm]?[jt]sx?$/.test(target.pathname)) target.pathname += '.ts';
    return nextResolve(target.href, context);
  }
  return nextResolve(specifier, context);
}`)}`, { parentURL: import.meta.url, data: { root: new URL("../", import.meta.url).href } });
registerSourceResolution();

const client = await import("../src/lib/api/client.ts");

test("read and write transports preserve AbortSignal and never turn cancellation into mock success", async () => {
  const originalFetch = globalThis.fetch;
  const originalDemo = process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO;
  process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = "false";
  const received: string[] = [];
  let expectedSignal: AbortSignal;
  globalThis.fetch = async (_input, init) => {
    assert.equal(init?.signal, expectedSignal);
    received.push(init?.method || "GET");
    return new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener("abort", () => reject(init.signal?.reason), { once: true });
    });
  };
  try {
    const operations = [
      (signal: AbortSignal) => client.apiGet("/streams", signal),
      (signal: AbortSignal) => client.apiPost("/streams", { name: "draft" }, signal),
      (signal: AbortSignal) => client.apiPostForm("/media-assets", new FormData(), signal),
      (signal: AbortSignal) => client.apiPut("/streams/draft/settings", {}, signal),
      (signal: AbortSignal) => client.apiPutBinary("/media-assets/draft", new Blob(), signal),
      (signal: AbortSignal) => client.apiDelete("/media-assets/draft", signal),
      (signal: AbortSignal) => client.apiDeleteJSON("/media-assets/draft", {}, signal),
    ];
    for (const operation of operations) {
      const abort = new AbortController();
      expectedSignal = abort.signal;
      const pending = operation(abort.signal);
      abort.abort(new DOMException("Cancelled fixture request", "AbortError"));
      await assert.rejects(pending, { name: "AbortError" });
    }
    assert.deepEqual(received, ["GET", "POST", "POST", "PUT", "PUT", "DELETE", "DELETE"]);
  } finally {
    globalThis.fetch = originalFetch;
    if (originalDemo === undefined) delete process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO;
    else process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = originalDemo;
  }
});
