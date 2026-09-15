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
import { driveFormContent, prepareFreshNodeDraft, type FreshNodeDraft } from "./form-content-driver.mts";
import { assertContentReached, canonicalFixturePath } from "./fixture-inputs.mts";
import { clickNamed, clickVisible, clickStreamPrimary, clickWorkerRestart } from "./visible-trigger.mts";
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
export function previewExpectation(condition: Condition, stream: { status: string; name: string }) {
  if (condition.family === "stream-detail" && ["long-id", "long-text"].includes(condition.exercise || "")) {
    assert.equal(condition.state, "ready"); assert.ok(stream.name.length > 128, "negative Preview fixture must cross the immutable public-label boundary");
    return "guard-rejected";
  }
  return stream.status === "live" ? "issue" : "none";
}
export const previewUnavailableCopy = {
  ja: "最新の権限または状態を確認できないため、操作を実行できません。",
  en: "The action cannot be sent because the latest permissions or state could not be verified.",
};
export async function waitForLivePreview(browser: BrowserHarness, stream: { id: string; status: string; name: string }, condition: Condition) {
  const locale = condition.locale; assert.ok(locale === "ja" || locale === "en", "registered Preview locale");
  if (previewExpectation(condition, stream) === "guard-rejected") {
    await browser.waitFor("document.querySelector('#stream-preview')?.textContent || ''", value => typeof value === "string" && value.includes(previewUnavailableCopy[locale]), "actual Preview guard unavailable reason");
    assert.equal(browser.requests.get('/streams/' + encodeURIComponent(stream.id) + '/preview-links') || 0, 0, "guard-rejected Preview must not issue");
    return;
  }
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
export class DraftRestorationFailure extends Error {
  readonly primaryFailed: boolean; readonly primary: unknown; readonly restoration: unknown;
  constructor(primaryFailed: boolean, primary: unknown, restoration: unknown) {
    super("Exercise and draft restoration failed", { cause: primaryFailed ? primary : restoration });
    this.primaryFailed = primaryFailed; this.primary = primary; this.restoration = restoration;
  }
}
export class ConditionOutputFailure extends AggregateError {
  readonly original: unknown; readonly output: unknown;
  constructor(original: unknown, output: unknown) {
    super([original, output], "Condition evidence output failed", { cause: original });
    this.original = original; this.output = output;
  }
}
export function conditionFailureStatus(condition: Condition, error: unknown, restoration?: DraftRestorationFailure) {
  const cause = error instanceof ConditionFailure ? error.cause : error;
  const draft = cause instanceof DraftRestorationFailure ? cause : restoration;
  const primary = draft ? (draft.primaryFailed ? draft.primary : draft.restoration) : cause;
  return { id: condition.id, status: "FAIL", error: primary instanceof Error ? primary.message : String(primary) };
}
export function writeConditionFailure(condition: Condition, error: unknown, write: (name: string, value: unknown) => void, restoration?: DraftRestorationFailure) {
  const cause = error instanceof ConditionFailure ? error.cause : error;
  const draft = cause instanceof DraftRestorationFailure ? cause : restoration;
  try {
    write(condition.id + ".failure.json", conditionFailureStatus(condition, error, draft));
    if (draft) {
      assert.ok(conditions.some(item => item.id === condition.id), "registered diagnostic condition");
      const diagnostic = { schemaVersion: 1, conditionID: condition.id, primaryFailed: draft.primaryFailed, restorationFailed: true,
        phase: "draft-restoration", code: draft.restoration instanceof AggregateError ? "RESTORE_COMPOSITE" : draft.restoration instanceof Error ? "RESTORE_ERROR" : "RESTORE_UNKNOWN" };
      assert.ok(Buffer.byteLength(JSON.stringify(diagnostic), "utf8") <= 4096, "bounded restoration diagnostic");
      write(condition.id + ".draft-restoration.json", diagnostic);
    }
  } catch (output) { throw new ConditionOutputFailure(error, output); }
  if (!(error instanceof ConditionFailure) || error.stop) throw error;
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
  let runFailure: { error: unknown } | undefined;
  try {
    for (const [index, condition] of planned.entries()) {
      const draftRestoration: { failure?: DraftRestorationFailure } = {};
      try {
        const accessibility = await runCondition(condition, surface, async (target, fixture) => {
        let restoreForm: (() => Promise<void>) | undefined; let bodyFailed = false, bodyFailure: unknown, renderFailure = 0;
        const restoreOnce = async () => {
          const action = restoreForm; restoreForm = undefined;
          if (!action) return;
          try { await action(); }
          catch (restoration) {
            const failure = new DraftRestorationFailure(bodyFailed, bodyFailure, restoration);
            draftRestoration.failure = failure;
            // A sole failure keeps its exact thrown identity. The dual failure
            // carries both objects; JSON output is a separate closed diagnostic.
            throw bodyFailed ? failure : restoration;
          }
        };
        const nodeContent = condition.family === "nodes" && ["long-id", "long-text"].includes(condition.exercise || "");
        let freshNodeDraft: FreshNodeDraft | undefined;
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
          await clickNamed(target, condition.family === "system-updates" ? /^(更新情報を再確認|Check updates again)$/ : /^(更新|最新状態に更新|Refresh|Reload|Update)$/, 'main');
          await target.waitForRequestCount(surface.primary, 2);
          if (condition.state === "stale") await target.waitForResponseCount(surface.primary, 2);
        }
        if (surface.stage === "detail") {
          await clickStreamPrimary(target, fixture.detailStream());
          await target.waitFor("document.querySelectorAll('[role=dialog]').length", value => value === 1, "selected stream detail");
          await settleRender(target, "open", reportRender);
          await waitForLivePreview(target, fixture.detailStream(), condition);
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
          await clickWorkerRestart(target, fixture.workerRestartTarget());
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
        if (nodeContent) {
          freshNodeDraft = await prepareFreshNodeDraft(target, condition);
          await clickNamed(target, /^(Nodeを新規作成|Create node)$/, 'main [data-screen-family=nodes]', true);
          await target.waitFor("document.querySelectorAll('[role=dialog]').length", value => value === 1, "existing registration input owner");
          await settleRender(target, "open", reportRender);
        }
        restoreForm = await driveFormContent(target, condition, freshNodeDraft);
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
        const previewExpected = surface.stage === "detail" && previewExpectation(condition, fixture.detailStream()) === "issue";
        if (previewExpected) {
          assertPreviewIssue(mutations, fixture.detailStream());
          assert.ok(await target.evaluate("!!document.querySelector('#stream-preview video')"), "actual live Preview component is mounted; synthetic permission failure is not playback proof");
        } else assert.deepEqual(mutations, [], "display/cancel must not mutate");
        if (condition.state === "ready") assert.equal(target.consoleErrorCount, 0, "ready console errors");
        await restoreOnce();
        if (nodeContent || condition.exercise === "Confirmation" || surface.stage === "detail" || surface.stage === "form" || condition.family === "roles" && condition.state === "partial") {
          await closeOverlay(target, reportRender);
          await target.waitFor("document.querySelectorAll('[role=dialog],[role=alertdialog]').length", value => value === 0, "dialog closes without duplicate owner");
          assert.ok(await target.evaluate("document.activeElement === globalThis.__uiReturnTrigger && document.activeElement !== document.body && document.activeElement.getClientRects().length > 0"), "focus returns to the exact opening trigger");
          await target.evaluate("delete globalThis.__uiReturnTrigger;true");
        }
        if (condition.exercise === "Table") await exerciseTable(target);
        return accessibility;
        } catch (error) { bodyFailed = true; bodyFailure = error; throw error; } finally { await restoreOnce(); }
        });
        statuses[index].status = accessibility.pending.length ? "NOT_PROVEN" : "PASS";
        statuses[index].error = accessibility.pending.join("; ");
      } catch (error) {
        statuses[index] = conditionFailureStatus(condition, error, draftRestoration.failure);
        writeConditionFailure(condition, error, write, draftRestoration.failure);
      }
    }
  } catch (error) { runFailure = { error }; }
  finally {
    try { write("execution.json", { source: sha, expected: planned.length, results: statuses, visual: "NOT_REVIEWED" }); }
    catch (output) { runFailure = { error: new ConditionOutputFailure(runFailure?.error, output) }; }
    finally {
      try { await server.close(); }
      catch (cleanup) { runFailure = { error: runFailure ? new AggregateError([runFailure.error, cleanup], "Condition failure and server close failed", { cause: runFailure.error }) : cleanup }; }
    }
  }
  if (runFailure) throw runFailure.error;
  assertExecution(planned, statuses);
}
if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) await main();
