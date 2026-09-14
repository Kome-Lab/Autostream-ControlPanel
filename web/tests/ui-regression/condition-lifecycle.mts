import assert from "node:assert/strict";
import { BrowserHarness } from "../helpers/browser-harness.mts";
import { createUIFixture } from "./route-fixture.mts";
import { preserveFetchDiagnostic } from "./capture.mts";
import type { Condition, Surface } from "./matrix.mts";

type Writer = (name: string, value: unknown) => void;
type Fixture = ReturnType<typeof createUIFixture>;
type Stage = "launch" | "fixture" | "document-settings" | "exercise" | "release" | "settlement" | "close";
export class ConditionFailure extends Error {
  readonly stage: Stage; readonly stop: boolean; readonly cleanup: unknown[];
  constructor(stage: Stage, stop: boolean, cause: unknown, cleanup: unknown[]) {
    super("Condition failed at " + stage, { cause });
    this.stage=stage; this.stop=stop; this.cleanup=cleanup;
  }
}
export function createConditionRunner(baseURL: string, write: Writer, launch = () => BrowserHarness.launch(), makeFixture = createUIFixture) {
  const completed = new Set<string>(), browsers = new WeakSet<BrowserHarness>(), fixtures = new WeakSet<Fixture>();
  let active = false, stopped = false;
  return async <T,>(condition: Condition, surface: Surface, exercise: (browser: BrowserHarness, fixture: Fixture) => Promise<T>): Promise<T> => {
    assert.equal(stopped, false, "condition owner stopped after cleanup/output failure");
    assert.equal(active, false, "conditions must run sequentially");
    assert.equal(completed.has(condition.id), false, "condition retry/duplicate execution forbidden");
    completed.add(condition.id); active = true;
    let browser: BrowserHarness | undefined, fixture: Fixture | undefined, stage: Stage = "launch";
    let failure: unknown, failed = false, failedStage: Stage = stage, result: T | undefined;
    const cleanup: unknown[] = [];
    const save = (name: string, value: unknown) => { try { write(condition.id + "." + name, value); } catch (error) { stopped = true; throw error; } };
    try {
      browser = await launch();
      assert.equal(browsers.has(browser), false, "old browser instance reuse forbidden"); browsers.add(browser);
      assert.equal(browser.requests.size, 0, "condition has requests before fixture setup");
      stage = "fixture";
      fixture = makeFixture(baseURL);
      assert.equal(fixtures.has(fixture), false, "old fixture instance reuse forbidden"); fixtures.add(fixture);
      fixture.reset(condition, surface.primary); browser.setRouteResolver(fixture.resolver);
      stage = "document-settings";
      await browser.setViewport(condition.width, 900);
      await browser.setMediaFeatures([
        { name: "prefers-color-scheme", value: condition.mode },
        ...(condition.exercise === "reduced-motion" ? [{ name: "prefers-reduced-motion", value: "reduce" }] : []),
        ...(condition.exercise === "forced-colors" ? [{ name: "forced-colors", value: "active" }] : []),
      ]);
      await browser.configureDeterministicDocument({ timezone: "Asia/Tokyo", locale: condition.locale === "ja" ? "ja-JP" : "en-US",
        source: "const UIClock=Date;const uiNow=UIClock.parse('2026-09-01T01:00:00Z');globalThis.Date=class extends UIClock{constructor(...args){super(...(args.length?args:[uiNow]));}static now(){return uiNow;}};" +
          "localStorage.setItem('autostream.controlPanel.locale'," + JSON.stringify(condition.locale) + ");localStorage.setItem('autostream.ui_preference'," + JSON.stringify(JSON.stringify({ theme_id: condition.theme, color_mode: condition.exercise === "system-mode" ? "system" : condition.mode })) + ");",
      });
      save("browser-version.json", await browser.browserVersion());
      browser.assertNoFatalError(); stage = "exercise";
      result = await exercise(browser, fixture);
      browser.assertNoFatalError();
    } catch (error) { failure = error; failed = true; failedStage = stage; }
    finally {
      // Release held required responses before draining or closing their sole owner.
      for (const [step, action] of [
        ["release", async () => fixture?.release()],
        ["settlement", async () => { if (browser) await browser.waitForRequestHandlersIdle(); }],
        ["close", async () => { if (browser) await browser.close(); }],
      ] as const) {
        try { stage = step; await action(); }
        catch (error) { cleanup.push(error); stopped = true; if (!failed) { failure = error; failed = true; failedStage = stage; } }
      }
      if (browser && failed) preserveFetchDiagnostic(browser, (name, value) => save(name, value));
      active = false;
    }
    try { save("lifecycle.json", { id: condition.id, completed: !failed, failedStage: failed ? failedStage : null, cleanupFailures: cleanup.length, retryCount: 0 }); }
    catch (error) { if (!failed) { failure = error; failed = true; failedStage = stage; } else cleanup.push(error); }
    if (failed) throw new ConditionFailure(failedStage, stopped, failure, cleanup);
    return result as T;
  };
}
