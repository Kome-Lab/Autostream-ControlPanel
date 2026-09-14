import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync, existsSync } from "node:fs";
import { isAbsolute, relative, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
import { ConditionFailure, createConditionRunner } from "./condition-lifecycle.mts";
import { conditions, inventory, selectedConditions, assertExecution, nativeZoomEvidence, type Condition } from "./matrix.mts";
import { navigateDocument, paint } from "./navigation.mts";
import { assertObservation, exerciseAccessibility, type UIObservation } from "./observation.mts";
import { assertState, deniedPrimaryRequests, loadingPaths, failureTextExpression, hasFailureCopy, type RequestEvidence } from "./state-drivers.mts";
import { driveFormContent } from "./form-content-driver.mts";
import { assertContentReached, canonicalFixturePath } from "./fixture-inputs.mts";
import { clickNamed, clickVisible, clickStreamPrimary } from "./visible-trigger.mts";
import { settleRender, closeOverlay, assertStableCapture } from "./render-state.mts";
import { exerciseTable } from "./table-browser.mts";
import { layoutExpression, assertLayout, type LayoutObservation } from "./layout-observation.mts";
import { captureObservation } from "./capture.mts";
import { staticExportServer } from "./static-server.mts";

const web = fileURLToPath(new URL("../..", import.meta.url));
const repository = resolve(web, "..");
export function ownedOutput(parent: string, output: string) {
  const distance = relative(resolve(parent), resolve(output));
  assert.ok(isAbsolute(parent) && isAbsolute(output) && distance && distance !== ".." && !distance.startsWith(".." + sep) && !isAbsolute(distance));
  assert.equal(existsSync(output), false, "never overwrite prior evidence");
}
export async function waitForLivePreview(browser: BrowserHarness, stream: { id: string; status: string }) {
  if (stream.status !== "live") return;
  const path = "/streams/" + encodeURIComponent(stream.id) + "/preview-links";
  await browser.waitForRequestCount(path, 1);
  await browser.waitForResponseCount(path, 1);
}
export function assertPreviewIssue(mutations: { method: string; path: string; status: number }[], stream: { id: string }) {
  assert.equal(mutations.length, 1, "opening the live Preview owns exactly one existing ephemeral issue");
  assert.equal(mutations[0].path, canonicalFixturePath("/streams/" + encodeURIComponent(stream.id) + "/preview-links"));
  assert.equal(mutations[0].method, "POST"); assert.equal(mutations[0].status, 403);
}
export function assertCurrentObservation(value: UIObservation, layout: LayoutObservation, condition: Condition, evidence: RequestEvidence, primary: string) {
  assertLayout(layout);
  assertObservation(value, condition, evidence, primary);
  assertState(condition, value, evidence, primary);
}
export async function main() {
  assert.equal(process.env.CI, "true", "actual browser runs only in blocking CI");
  assert.equal(process.platform, "linux", "existing Linux Chrome runtime required");
  const sha = execFileSync("git", ["rev-parse", "HEAD"], { cwd: repository, encoding: "utf8" }).trim();
  assert.equal(sha, process.env.GITHUB_SHA, "current-source checkout identity");
  const family = process.argv[2] || "";
  const planned = selectedConditions(family);
  const surface = inventory.surfaces.find((item) => item.id === family);
  assert.ok(surface);
  const output = process.env.AUTOSTREAM_UI_REGRESSION_OUTPUT || "";
  ownedOutput(process.env.RUNNER_TEMP || "", output);
  mkdirSync(output);
  const write = (name: string, value: unknown) => writeFileSync(resolve(output, name), JSON.stringify(value, null, 2) + "\n", { flag: "wx" });
  const provenance = JSON.parse(readFileSync(resolve(web, "out/ui-regression-source.json"), "utf8"));
  assert.equal(provenance.commit, sha, "export producer/consumer source identity");
  const statuses = planned.map(item => ({ id: item.id, status: "NOT_REACHED", error: "" }));
  write("plan.json", { source: sha, provenance, family, expected: planned.length, globalExpected: conditions.length, planned, visual: "NOT_REVIEWED", clock: "2026-09-01T01:00:00Z", zoom: "css-magnification-200", nativeZoomEvidence });
  const server = await staticExportServer(resolve(web, "out"));
  const runCondition = createConditionRunner(server.baseURL, write);
  try {
    for (const [index, condition] of planned.entries()) {
      try {
        const accessibility = await runCondition(condition, surface, async (target, fixture) => {
        let restoreForm = async () => {}; let bodyFailed = false, renderFailure = 0;
        const reportRender = (value: unknown) => write(condition.id + ".render-failure-" + (++renderFailure) + ".json", value);
        try {
        await navigateDocument(target, server.baseURL + condition.route + (condition.exercise === "Table" ? "?view=retained&streams.page=2" : ""));
        await target.waitFor("document.querySelector('main h1')?.textContent", Boolean, "page h1");
        if (condition.state === "initial-loading") for (const path of loadingPaths(condition, surface.primary)) await target.waitForRequestCount(path, 1);
        if (condition.state !== "initial-loading" && condition.state !== "permission-denied") {
          await target.waitForResponseCount(surface.primary, 1);
        }
        if (condition.state === "permission-denied") {
          const expected = deniedPrimaryRequests[condition.family] || 0;
          if (expected) await target.waitForResponseCount(surface.primary, expected);
          else await target.waitForResponseCount("/auth/me", 1);
        }
        if (["background-refresh", "stale"].includes(condition.state)) {
          fixture.refresh();
          await clickNamed(target, condition.locale === "ja" ? (condition.family === "system-updates" ? /^再取得$/ : /^(更新|最新状態に更新)$/) : /^(Refresh|Reload|Update)$/ , 'main');
          await target.waitForRequestCount(surface.primary, 2);
          if (condition.state === "stale") await target.waitForResponseCount(surface.primary, 2);
        }
        if (surface.stage === "detail") {
          await clickStreamPrimary(target, fixture.detailStream());
          await target.waitFor("document.querySelectorAll('[role=dialog]').length", value => value === 1, "selected stream detail");
          await settleRender(target, "open", reportRender);
          await waitForLivePreview(target, fixture.detailStream());
        }
        if (surface.stage === "form") {
          await target.waitFor("document.querySelectorAll('[role=dialog]').length", value => value === 1, "deep-link stream creation form");
          await settleRender(target, "open", reportRender);
          await closeOverlay(target, reportRender);
          await target.waitFor("document.querySelectorAll('[role=dialog]').length", value => value === 0, "close deep-link form");
          await clickNamed(target, condition.locale === "ja" ? /^配信枠を作成$/ : /^Create stream$/, "main", true);
          await target.waitFor("document.querySelectorAll('[role=dialog]').length", value => value === 1, "creation form with real focus-return trigger");
        }
        if (condition.exercise === "Confirmation") {
          await clickVisible(target, 'main [data-slot="data-table"] tbody tr:first-child button', condition.locale === "ja" ? /^Worker を再起動$/ : /^Restart worker$/, true);
          await target.waitFor("document.querySelectorAll('[role=alertdialog]').length", value => value === 1, "one real confirmation owner");
        }
        if (condition.exercise === "css-magnification-200") await target.evaluate("document.documentElement.style.zoom='2';true");
        if (condition.family === "roles" && condition.state === "partial") {
          await clickNamed(target, condition.locale === "ja" ? /作成/ : /Create/, "main", true);
          await target.waitForRequestCount("/permissions", 1);
        }
        if (condition.family === "account" && ["stale", "background-refresh", "partial"].includes(condition.state)) {
          await clickVisible(target, 'main [role=tab]', condition.locale === "ja" ? /^セキュリティ$/ : /^Security$/);
        }
        restoreForm = await driveFormContent(target, condition);
        await paint(target);
        await settleRender(target, undefined, reportRender);
        const accessibility = await exerciseAccessibility(target, condition, value => write(condition.id + ".keyboard-trace.json", value));
        if (!["initial-loading", "background-refresh"].includes(condition.state)) await target.waitForRequestHandlersIdle();
        if (["blocking-error", "partial", "stale"].includes(condition.state)) {
          await target.waitFor(failureTextExpression, hasFailureCopy, "existing retry policy reaches visible failure");
        }
        await settleRender(target, undefined, reportRender);
        const layout = await target.evaluate<LayoutObservation>(layoutExpression);
        await assertStableCapture(target);
        const evidence = await captureObservation(target, condition, fixture.trace, write, (name, bytes) => writeFileSync(resolve(output, name), bytes, { flag: "wx" }));
        const observation = evidence.observation;
        write(condition.id + ".layout.json", layout);
        write(condition.id + ".accessibility.json", accessibility);
        assertCurrentObservation(observation, layout, condition, target, surface.primary);
        assertContentReached(condition, observation.text + observation.controls.map(control => control.name).join(" "), observation.controls.map(control => control.value || ""));
        assert.deepEqual(fixture.unexpected, [], "unknown API/external request");
        if (condition.exercise === "denied-read") assert.equal(target.requests.get("/observability/incidents") || 0, 0, "denied Dashboard incident GET");
        const mutations = fixture.trace.filter(call => !["GET", "HEAD"].includes(call.method) && call.path !== "/auth/session/refresh");
        const previewExpected = surface.stage === "detail" && fixture.detailStream().status === "live";
        if (previewExpected) {
          assertPreviewIssue(mutations, fixture.detailStream());
          assert.ok(await target.evaluate("!!document.querySelector('#stream-preview video')"), "actual live Preview component is mounted; synthetic permission failure is not playback proof");
        } else assert.deepEqual(mutations, [], "display/cancel must not mutate");
        if (condition.state === "ready") assert.equal(target.consoleErrorCount, 0, "ready console errors");
        await restoreForm(); restoreForm = async () => {};
        if (condition.exercise === "Confirmation" || surface.stage === "detail" || surface.stage === "form" || condition.family === "roles" && condition.state === "partial") {
          await closeOverlay(target, reportRender);
          await target.waitFor("document.querySelectorAll('[role=dialog],[role=alertdialog]').length", value => value === 0, "dialog closes without duplicate owner");
          assert.ok(await target.evaluate("document.activeElement === globalThis.__uiReturnTrigger && document.activeElement !== document.body && document.activeElement.getClientRects().length > 0"), "focus returns to the exact opening trigger");
          await target.evaluate("delete globalThis.__uiReturnTrigger;true");
        }
        if (condition.exercise === "Table") await exerciseTable(target);
        return accessibility;
        } catch (error) { bodyFailed = true; throw error; } finally { try { await restoreForm(); } catch (error) { if (!bodyFailed) throw error; } }
        });
        statuses[index].status = accessibility.pending.length ? "NOT_PROVEN" : "PASS";
        statuses[index].error = accessibility.pending.join("; ");
      } catch (error) {
        const cause = error instanceof ConditionFailure ? error.cause : error;
        statuses[index] = { id: condition.id, status: "FAIL", error: cause instanceof Error ? cause.message : String(cause) };
        write(condition.id + ".failure.json", statuses[index]);
        if (!(error instanceof ConditionFailure) || error.stop) throw error;
      }
    }
  } finally {
    write("execution.json", { source: sha, expected: planned.length, results: statuses, visual: "NOT_REVIEWED" });
    await server.close();
  }
  assertExecution(planned, statuses);
}
if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) await main();
