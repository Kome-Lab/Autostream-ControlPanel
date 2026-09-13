import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { FetchRequestLifecycle } from "./helpers/browser-request-lifecycle.mts";
import { createBundle9Fixture } from "./helpers/bundle9-browser-fixtures.mts";
import { type APIObservation } from "./helpers/bundle9-browser-contract.mts";


export const fixtureOrigin = "http://127.0.0.1:4100";

// Compile the actual caller expressions from captureBundle9Source. This runs
// their await/control flow without launching a browser or modelling a new caller.
export const scenariosSource = ts.createSourceFile("bundle9-browser-scenarios.mts", readFileSync(new URL("./helpers/bundle9-browser-scenarios.mts", import.meta.url), "utf8"), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
export const captureSource = findSourceNode(scenariosSource, (node) => ts.isFunctionDeclaration(node) && node.name?.text === "captureBundle9Source");
const paintSource = findSourceNode(scenariosSource, (node) => ts.isFunctionDeclaration(node) && node.name?.text === "paintBarrier");
export const actualPaintBarrier = sourceFunction(paintSource.getText(scenariosSource), {});

export function deferred() {
  let resolve!: () => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<void>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}
export async function flushTasks() { await new Promise<void>((done) => setImmediate(done)); }

export function navigationBoundary() {
  const lifecycle = new FetchRequestLifecycle();
  const fixture = createBundle9Fixture(fixtureOrigin);
  fixture.resolver({ method: "GET", url: fixtureOrigin + "/auth/me" });
  const events: string[] = [];
  const reset = fixture.resetTrace;
  fixture.resetTrace = () => { events.push("fixture-reset"); reset(); };
  const paint = deferred(); const blank = deferred(); const document = deferred();
  const nextObservation = new Error("controlled next-document observation");
  const browser = {
    requestLifecycle: lifecycle,
    waitForRequestHandlersIdle: BrowserHarness.prototype.waitForRequestHandlersIdle,
    assertNoFatalError: BrowserHarness.prototype.assertNoFatalError,
    setViewport: BrowserHarness.prototype.setViewport,
    async send(method: string) { assert.equal(method, "Emulation.setDeviceMetricsOverride"); events.push("viewport"); return {}; },
    async evaluate(expression: string) { assert.ok(expression.includes("document.fonts.ready")); events.push("paint"); await paint.promise; return true; },
    async navigate(url: string) { const isBlank = url === "about:blank"; events.push(isBlank ? "blank" : "document"); await (isBlank ? blank.promise : document.promise); },
    clearRequestCounts() { events.push("response-reset"); },
    clearNavigationCount() { events.push("navigation-reset"); },
    clearConsoleErrors() { events.push("console-reset"); },
    async waitFor() { events.push("observe-next"); throw nextObservation; },
    async waitForResponseCount() { events.push("observe-next"); throw nextObservation; },
  } as unknown as BrowserHarness;
  return { browser, fixture, lifecycle, events, paint, blank, document, nextObservation,
    pendingResponse(requestId: string, pathname: string) {
      lifecycle.register({ requestId, pathname, method: "GET", requiredResponse: true });
      const attempt = lifecycle.beginSettlement(requestId, "Fetch.fulfillRequest");
      return () => lifecycle.completeSettlement(attempt);
    },
  };
}

export function findSourceNode(root: ts.Node, predicate: (node: ts.Node) => boolean): ts.Node {
  let found: ts.Node | undefined;
  const visit = (node: ts.Node) => { if (!found && predicate(node)) found = node; if (!found) ts.forEachChild(node, visit); };
  visit(root);
  assert.ok(found, "actual Bundle 9 caller/helper source must exist");
  return found;
}
export function sourceFunction(source: string, bindings: Record<string, unknown>) {
  return runInNewContext(ts.transpile(`(${source})`, { target: ts.ScriptTarget.ES2023, module: ts.ModuleKind.CommonJS }), bindings) as (...args: unknown[]) => Promise<void>;
}

export const sessionRefresh: APIObservation = { method: "POST", path: "/auth/session/refresh", body: null, status: 200 };
export const invalidCancelledMutations: APIObservation[][] = [
  [], [sessionRefresh, sessionRefresh],
  ...[{ method: "PUT" }, { path: "/auth/session/refresh?extra=1" }, { path: "/auth/other" }, { body: {} }, { status: 500 }].map((change) => [{ ...sessionRefresh, ...change }]),
  [sessionRefresh, { method: "POST", path: "/system-updates/updaters/host-agent-main/settings", body: { enabled: false }, status: 200 }],
];

export function scenarioSource(name: string) {
  const call = findSourceNode(captureSource, (node) => ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === "scenario" && ts.isStringLiteral(node.arguments[0]) && node.arguments[0].text === name) as ts.CallExpression;
  return call.arguments[1].getText(scenariosSource);
}
