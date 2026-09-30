import test from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { withBootstrapInput, assertBootstrapWitness, type BootstrapWitness } from "./ui-browser-bootstrap-input.mts";

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
      await withBootstrapInput(browser, url, entry.input, async () => {
        await browser.navigate(url);
        const witness = await browser.evaluate<BootstrapWitness>("globalThis.__accountBootstrapWitness");
        assertBootstrapWitness(witness, url, entry.input, entry.theme, entry.colorMode);
      });
    });
  }
  for (const mutation of ["omitted", "unreachable"] as const) {
    await t.test(`reject actual ${mutation} bootstrap`, async () => {
      mode = mutation;
      await withBootstrapInput(browser, url, cyan, async () => {
        await browser.navigate(url);
        const witness = await browser.evaluate<BootstrapWitness>("globalThis.__accountBootstrapWitness");
        assert.equal(witness.input, cyan);
        assert.equal(witness.status, mutation === "omitted" ? "waiting" : "error");
        assert.throws(() => assertBootstrapWitness(witness, url, cyan, "cyan", "light"), /actual external bootstrap/);
      });
    });
  }
  await t.test("registration removed when its observation fails", async () => {
    mode = "normal";
    await assert.rejects(withBootstrapInput(browser, url, cyan, async () => {
      await browser.navigate(url);
      throw new Error("deliberate bootstrap observation failure");
    }), /deliberate bootstrap observation failure/);
    await browser.navigate(url);
    assert.equal(await browser.evaluate("typeof globalThis.__accountBootstrapWitness"), "undefined");
  });
  await t.test("one-shot registration removed by its original ID", async () => {
    mode = "normal";
    await browser.navigate(url);
    assert.equal(await browser.evaluate("typeof globalThis.__accountBootstrapWitness"), "undefined");
  });
});


test("Account bootstrap cleanup preserves callback cause and the original registration", async t => {
  for (const fault of ["none", "callback", "remove", "callback-and-remove", "registration"] as const) {
    await t.test(fault, async () => {
      const primary = new Error("deliberate observation failure"), secondary = new Error("deliberate CDP failure");
      const commands: Array<{ method: string; params: Record<string, unknown> }> = [];
      let callbacks = 0;
      const browser = {
        installNewDocumentScript: BrowserHarness.prototype.installNewDocumentScript,
        send: async (method: string, params: Record<string, unknown>) => {
          commands.push({ method, params });
          if (method === "Page.addScriptToEvaluateOnNewDocument") {
            if (fault === "registration") throw secondary;
            return { identifier: "original-bootstrap-observer" };
          }
          assert.equal(method, "Page.removeScriptToEvaluateOnNewDocument");
          assert.deepEqual(params, { identifier: "original-bootstrap-observer" });
          if (fault === "remove" || fault === "callback-and-remove") throw secondary;
          return {};
        },
      } as unknown as BrowserHarness;
      let caught: unknown, result: unknown;
      try {
        result = await withBootstrapInput(browser, "http://fixture.test/account/", "synthetic mirror", async () => {
          callbacks++;
          if (fault === "callback" || fault === "callback-and-remove") throw primary;
          return "observed";
        });
      } catch (error) { caught = error; }
      if (fault === "none") { assert.equal(caught, undefined); assert.equal(result, "observed"); }
      else if (fault === "callback-and-remove") {
        assert.ok(caught instanceof AggregateError);
        assert.equal(caught.cause, primary);
        assert.deepEqual(caught.errors, [primary, secondary]);
      } else assert.equal(caught, fault === "callback" ? primary : secondary);
      assert.equal(callbacks, fault === "registration" ? 0 : 1);
      assert.equal(commands.filter(c => c.method === "Page.addScriptToEvaluateOnNewDocument").length, 1);
      assert.equal(commands.filter(c => c.method === "Page.removeScriptToEvaluateOnNewDocument").length, fault === "registration" ? 0 : 1);
    });
  }
});
