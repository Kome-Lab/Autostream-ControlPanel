import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { BrowserHarness, ensureWebServer } from "./helpers/browser-harness.mts";
import { createRelatedBrowserContext, type RelatedBrowserContext } from "./helpers/polling-related-browser-context.mts";
import { relatedWorkersAppCaseNames, runRelatedWorkersAppScenarios } from "./helpers/polling-workers-app-browser-cases.mts";
import { relatedUpdaterNodesCaseNames, runRelatedUpdaterNodesScenarios } from "./helpers/polling-updater-nodes-browser-cases.mts";
import { relatedResourceCaseNames, relatedSecurityCaseNames, runRelatedResourceScenarios, runRelatedSecurityScenarios } from "./helpers/polling-resource-security-browser-cases.mts";
import { webRoot } from "./ui-browser-fixture.mts";

// Standalone and deliberately outside the broad suite: one Next server and at
// most one owned browser process at a time. No production API is contacted.
process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = "false";
process.env.NEXT_TELEMETRY_DISABLED = "1";
process.env.NODE_OPTIONS ||= "--max-old-space-size=1536";
process.env.UV_THREADPOOL_SIZE = "1";
const evidenceDirectory = process.env.AUTOSTREAM_RELATED_UI_EVIDENCE_DIR
  ? resolve(process.env.AUTOSTREAM_RELATED_UI_EVIDENCE_DIR)
  : mkdtempSync(resolve(tmpdir(), "autostream-related-ui-"));
mkdirSync(evidenceDirectory, { recursive: true });
const names = [...relatedWorkersAppCaseNames, ...relatedUpdaterNodesCaseNames, ...relatedResourceCaseNames, ...relatedSecurityCaseNames];
assert.equal(new Set(names).size, names.length, "every required browser case has one distinct inventory name");

async function freeLoopbackURL() {
  const reservation = createServer();
  await new Promise<void>((done, reject) => { reservation.once("error", reject); reservation.listen(0, "127.0.0.1", done); });
  const address = reservation.address();
  assert.ok(address && typeof address === "object");
  const url = `http://127.0.0.1:${address.port}`;
  await new Promise<void>((done, reject) => reservation.close((error) => error ? reject(error) : done()));
  return url;
}

const sourcePaths = [
  "src/features/workers/workers-action-controller.ts", "src/features/workers/workers-configuration-controller.ts",
  "src/features/workers/workers-action-runtime.ts", "src/features/workers/workers-view.tsx",
  "src/features/settings/settings-view.tsx", "src/features/settings/app-settings-action-policy.ts",
  "src/features/settings/app-settings-action-runtime.ts", "src/features/application/updater-settings-form.tsx",
  "src/features/application/updater-action-policy.ts", "src/features/application/updater-action-confirmation.tsx",
  "src/features/application/port-reconfigure-authority.tsx", "src/features/nodes/node-registration-view.tsx",
  "src/features/nodes/node-metadata-edit.ts", "src/features/nodes/use-node-registration-mutations.ts",
  "src/features/resources/resource-security-editor.tsx", "src/features/resources/generic-resource-panel.tsx",
  "src/features/resources/resource-action-controller.ts", "src/features/queries.ts", "src/lib/api/client.ts",
  "src/lib/foundation/actions/bounded-revalidation.ts",
];
const authority = () => ({
  head: execFileSync("git", ["rev-parse", "HEAD"], { cwd: webRoot, encoding: "utf8", windowsHide: true }).trim(),
  files: Object.fromEntries(sourcePaths.map((path) => [path, createHash("sha256").update(readFileSync(resolve(webRoot, path))).digest("hex")])),
});
let before: ReturnType<typeof authority> | undefined;
let web: Awaited<ReturnType<typeof ensureWebServer>> | undefined;
let ctx: RelatedBrowserContext | undefined;
let originalBrowser: BrowserHarness | undefined;
let browserVersion: unknown;
const runnerFailures: unknown[] = [];
let after: ReturnType<typeof authority> | undefined;
try {
  before = authority();
  const requestedURL = process.env.AUTOSTREAM_RELATED_UI_NEXT_URL || await freeLoopbackURL();
  const target = new URL(requestedURL);
  assert.equal(target.hostname, "127.0.0.1", "related UI proof requires its isolated loopback Next fixture");
  web = await ensureWebServer(webRoot, requestedURL);
  // Compile routes sequentially before the browser's bounded navigation window.
  for (const path of ["/admin/workers/", "/admin/settings/", "/admin/application/", "/admin/registered-nodes/", "/admin/discord/", "/admin/security/"]) {
    const response: Response = await fetch(new URL(path, web.baseUrl), { signal: AbortSignal.timeout(60_000) });
    assert.equal(response.status, 200, `actual Next route ${path} must be available`);
    await response.arrayBuffer();
  }
  originalBrowser = await BrowserHarness.launch();
  browserVersion = await originalBrowser.browserVersion();
  ctx = createRelatedBrowserContext(originalBrowser, web.baseUrl, evidenceDirectory);
  ctx.plan(names);
  await runRelatedWorkersAppScenarios(ctx);
  await runRelatedUpdaterNodesScenarios(ctx);
  await runRelatedResourceScenarios(ctx);
  await runRelatedSecurityScenarios(ctx);
  assert.equal(ctx.cases.length, names.length);
  assert.equal(ctx.cases.filter((item) => item.status === "PASS").length, names.length);
  assert.deepEqual(authority(), before, "product source must remain unchanged while actual UI evidence is collected");
} catch (error) {
  runnerFailures.push(error);
  if (ctx && ctx.failures.length === 0) {
    try { await ctx.capture("runner-failure"); }
    catch (captureError) { runnerFailures.push(captureError); }
  }
} finally {
  try { ctx?.fixture.releaseAll(); }
  catch (error) { runnerFailures.push(error); }
  const cleanups = await Promise.allSettled([ctx?.closeBrowser() ?? originalBrowser?.close()]);
  for (const item of cleanups) if (item.status === "rejected") runnerFailures.push(item.reason);
  try { await web?.close(); }
  catch (error) { runnerFailures.push(error); }
  try {
    after = authority();
    assert.deepEqual(after, before, "product source must remain unchanged even when a browser case fails");
  } catch (error) { runnerFailures.push(error); }
}
const results = ctx?.cases ?? names.map((name) => ({ name, status: "NOT_RUN" as const }));
try {
  assert.equal(results.length, names.length, "the full required case inventory remains present");
  assert.equal(results.filter((item) => item.status === "PASS").length, names.length, "all required cases must pass; FAIL and NOT_RUN are never accepted");
} catch (error) { runnerFailures.push(error); }
const allFailures = [...new Set([...(ctx?.failures ?? []), ...runnerFailures])];
const receipt = {
    status: allFailures.length ? "FAIL_RELATED_ACTUAL_NEXT_UI_FIXTURE" : "PASS_RELATED_ACTUAL_NEXT_UI_FIXTURE",
    expected_cases: names.length,
    counts: { passed: results.filter((item) => item.status === "PASS").length, failed: results.filter((item) => item.status === "FAIL").length, not_run: results.filter((item) => item.status === "NOT_RUN").length, skipped: 0, cancelled: 0 },
    cases: results, captures: ctx?.captures ?? [], completed_query_cycles: ctx?.completedCycles ?? [],
    browser: browserVersion, source_before: before ?? null, source_after: after ?? null,
    default_operation_width: 1440, additional_operations: ctx?.mobileOperations ?? [], capture_widths: [1440, 390, 320],
    constraints: { concurrent_browser_processes: 1, shared_next_servers: 1, node_heap_default_mib: 1536, uv_threadpool_size: 1 },
    limits: [
      "Actual Next/React UI, query owners, API client and real DOM controls; controlled API responses through BrowserHarness. These are fixture UI results, not real Go handler/DB results.",
      "Three completed real query cycles use the per-case recorded visibilitychange focus-manager events or actual Refresh buttons. Natural timer cycles, physical device focus and physical mobile hardware are not claimed.",
      "Default input/click width is 1440 pixels; explicit 390/320 operations are listed by case in additional_operations. Responsive screenshots alone are not interaction proof for that width.",
      "No production settings, production BOT, Discord, VC entry, real update/install or release download is performed. Total 8 GiB memory consumption requires the sole runner's external observation.",
    ],
    ...(allFailures.length ? { error_class: "AggregateError", errors: allFailures.map((error) => ({ error_class: error instanceof Error ? error.name : "UnknownError" })) } : {}),
  };
try {
  writeFileSync(resolve(evidenceDirectory, "receipt.json"), JSON.stringify(receipt, null, 2), { flag: "wx" });
} catch (error) { allFailures.push(error); }
try {
  process.stdout.write(JSON.stringify({ status: allFailures.length ? "FAIL_RELATED_ACTUAL_NEXT_UI_FIXTURE" : receipt.status, expected_cases: names.length, counts: receipt.counts, evidence_directory: evidenceDirectory }) + "\n");
} catch (error) { allFailures.push(error); }
if (allFailures.length) throw new AggregateError(allFailures, "Related actual UI acceptance failed; every original case, capture, source and cleanup failure is retained");
