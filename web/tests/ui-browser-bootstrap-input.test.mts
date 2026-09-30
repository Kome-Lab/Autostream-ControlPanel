import test from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { bootstrapInputObserver, assertBootstrapWitness, type BootstrapWitness } from "./ui-browser-bootstrap-input.mts";

test("Account bootstrap input, actual external application and omission negative", { timeout: 120_000 }, async t => {
  const source = readFileSync(new URL("../public/theme-bootstrap.js", import.meta.url));
  let mode: "normal" | "omitted" | "unreachable" = "normal";
  const server = createServer((request, response) => {
    if (request.url === "/theme-bootstrap.js") {
      response.writeHead(mode === "unreachable" ? 404 : 200, { "content-type": "text/javascript", "cache-control": "no-store" });
      response.end(mode === "unreachable" ? "" : source);
      return;
    }
    response.writeHead(200, { "content-type": "text/html", "cache-control": "no-store" });
    response.end(`<!doctype html><html><head>${mode === "omitted" ? "" : '<script src="/theme-bootstrap.js"></script>'}</head><body>inert bootstrap fixture</body></html>`);
  });
  await new Promise<void>(resolve => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  assert.ok(address && typeof address !== "string");
  const url = `http://127.0.0.1:${address.port}/account-fixture`;
  const browser = await BrowserHarness.launch();
  t.after(async () => { await browser.close(); await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())); });
  const cyan = JSON.stringify({ theme_id: "cyan", color_mode: "light" });
  for (const entry of [
    { name: "valid mirror", input: cyan, theme: "cyan", colorMode: "light" },
    { name: "broken mirror fallback", input: "{broken", theme: "autostream", colorMode: "system" },
    { name: "invalid values fallback", input: JSON.stringify({ theme_id: "unknown", color_mode: "infrared" }), theme: "autostream", colorMode: "system" },
  ]) {
    await t.test(entry.name, async () => {
      await browser.withNewDocumentScript(bootstrapInputObserver(url, entry.input), async () => {
        await browser.navigate(url);
        const witness = await browser.evaluate<BootstrapWitness>("globalThis.__accountBootstrapWitness");
        assertBootstrapWitness(witness, url, entry.input, entry.theme, entry.colorMode);
      });
    });
  }
  for (const mutation of ["omitted", "unreachable"] as const) {
    await t.test(`reject actual ${mutation} bootstrap`, async () => {
      mode = mutation;
      await browser.withNewDocumentScript(bootstrapInputObserver(url, cyan), async () => {
        await browser.navigate(url);
        const witness = await browser.evaluate<BootstrapWitness>("globalThis.__accountBootstrapWitness");
        assert.equal(witness.input, cyan);
        assert.equal(witness.status, mutation === "omitted" ? "waiting" : "error");
        assert.throws(() => assertBootstrapWitness(witness, url, cyan, "cyan", "light"), /actual external bootstrap/);
      });
    });
  }
  await t.test("one-shot registration removed by its original ID", async () => {
    mode = "normal";
    await browser.navigate(url);
    assert.equal(await browser.evaluate("typeof globalThis.__accountBootstrapWitness"), "undefined");
  });
});
