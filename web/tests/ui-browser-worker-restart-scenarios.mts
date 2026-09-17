import type { TestContext } from "node:test";
import assert from "node:assert/strict";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { assertNoBrowserConsoleErrors } from "./helpers/ui-foundation-assertions.mts";
import { type BrowserRouteFixture, permissionUser, workerPilotRows, currentUser } from "./ui-browser-fixture.mts";
import { deferred, setStoredDisplay, waitForAnimationFrames } from "./ui-browser-navigation-helpers.mts";
import { waitForShell } from "./ui-browser-query-auth-helpers.mts";
import { waitForWorkerAction, clickWorkerAction, workerRestartDialogCount, waitForWorkerRestartReady, workerRestartTriggerCount, waitForWorkerRestartDialog, assertWorkerRestartSingleOpenEvidence, waitForWorkerRestartDialogClosed, waitForWorkerRestartTriggerFocus, tabToWorkerAction, waitForWorkerRestartOutcomeFocus } from "./ui-browser-worker-helpers.mts";



export async function runWorkerRestartScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "workersResponse" | "nodesResponse" | "restartResponse" | "authResponse" | "workerRestartMethods">) {

  await t.test("Worker restart uses fresh canonical action policy and one POST per worker", async () => {
    const restartPath = "/workers/worker-one/restart";
    const permittedUser = permissionUser(["workers.read", "workers.restart"]);
    let authRefreshRelease: ReturnType<typeof deferred> | undefined;
    let restartRelease: ReturnType<typeof deferred> | undefined;
    try {
      await browser.setViewport(1440, 900);
      await browser.setMediaFeatures([]);
      browser.clearConsoleErrors();
      await setStoredDisplay(browser, "en", "light");
      fixture.workersResponse = { body: workerPilotRows };
      fixture.nodesResponse = { body: workerPilotRows };
      fixture.restartResponse = { status: 202, body: { status: "accepted" }, requiredResponse: true };
      fixture.authResponse = { body: permittedUser };
      await browser.navigate(`${server.baseUrl}/admin/workers/`);
      await waitForShell(browser, "Account menu");
      await waitForWorkerAction(browser, "Restart worker", "Worker One", (value) => value.disabled === false);

      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
      const actionOnlyAuthResponseCount = (browser.responses.get("/auth/me") || 0) + 1;
      fixture.authResponse = { body: permissionUser(["workers.restart"]) };
      await browser.waitForResponseCount("/auth/me", actionOnlyAuthResponseCount, 20_000);
      // A retained cache is not page read authority. WKR-01's independent
      // action-only policy is exercised against the real controller in UI-WORKER-READ-ACTION-017.
      await browser.waitFor(`(() => {const root=document.querySelector('[data-screen-family=workers]');
        const denied=[...(root?.querySelectorAll('[role=status]')||[])].some(e=>e.getClientRects().length&&getComputedStyle(e).visibility!=='hidden'&&e.textContent?.includes('Permission denied: no Worker information is available to read.'));
        return !!root&&denied&&!root.querySelector('tbody td[headers]')&&!root.querySelector('button[aria-label="Restart worker"]');})()`,
        (value: boolean) => value === true, "read permission loss must remove Worker rows and restart triggers");
      const readPaths = ["/workers", "/nodes", "/service-health"];
      for (const pathname of readPaths) await browser.waitForRequestHandlersIdle({ pathname, method: "GET" });
      const deniedReadCounts = readPaths.map(path => browser.requests.get(path) || 0);
      const deniedRestartCount = browser.requests.get(restartPath) || 0;
      await waitForAnimationFrames(browser);
      assert.deepEqual(readPaths.map(path => browser.requests.get(path) || 0), deniedReadCounts, "no read requests while read authority is denied");
      assert.equal(browser.requests.get(restartPath) || 0, deniedRestartCount, "read loss cannot initiate a restart");
      assert.equal(await workerRestartDialogCount(browser), 0);
      const readRestoredAuthResponseCount = (browser.responses.get("/auth/me") || 0) + 1;
      fixture.authResponse = { body: permittedUser };
      await browser.waitForResponseCount("/auth/me", readRestoredAuthResponseCount, 20_000);
      await waitForWorkerRestartReady(browser, "Worker One");

      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
      const refreshingAuthRequestCount = (browser.requests.get("/auth/me") || 0) + 1;
      authRefreshRelease = deferred();
      fixture.authResponse = {
        body: permittedUser,
        waitUntil: authRefreshRelease.promise,
      };
      await browser.waitForRequestCount("/auth/me", refreshingAuthRequestCount, 20_000);
      browser.clearRequestCounts(restartPath);
      await clickWorkerAction(browser, "Restart worker");
      const unknown = await waitForWorkerAction(browser, "Restart worker", "Worker One", (value) => value.disabled && /permission could not be verified/i.test(value.reason));
      assert.match(unknown.reason, /permission could not be verified/i);
      assert.equal(await workerRestartDialogCount(browser), 0, "an unknown restart permission must not open a confirmation");
      assert.equal(browser.requests.get(restartPath) || 0, 0, "an unknown restart permission must not send POST");
      authRefreshRelease.resolve();
      fixture.authResponse = { body: permittedUser };
      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
      authRefreshRelease = undefined;

      fixture.authResponse = { body: permissionUser(["workers.read"]) };
      await browser.reload();
      const denied = await waitForWorkerAction(browser, "Restart worker", "Worker One", (value) => value.disabled && /do not have permission to restart workers/i.test(value.reason));
      assert.match(denied.reason, /do not have permission to restart workers/i);
      browser.clearRequestCounts(restartPath);
      await clickWorkerAction(browser, "Restart worker");
      await waitForAnimationFrames(browser);
      assert.equal(await workerRestartDialogCount(browser), 0, "a visible denied restart must not open a confirmation");
      assert.equal(browser.requests.get(restartPath) || 0, 0, "a visible denied restart must not send POST");

      const permissionRestoredRows = workerPilotRows.map((row, index) => index === 0
        ? { ...row, service_name: "Worker One Permission Restored" }
        : row);
      fixture.workersResponse = { body: permissionRestoredRows };
      fixture.authResponse = { body: permittedUser };
      browser.clearRequestCounts("/workers");
      await browser.reload();
      await browser.waitForResponseCount("/workers", 1);
      await waitForWorkerRestartReady(browser, "Worker One Permission Restored");

      const trigger = await waitForWorkerAction(
        browser,
        "Restart worker",
        "Worker One Permission Restored",
        (value) => value.disabled === false && value.dataState === "closed",
      );
      assert.equal(trigger.targetCount, 1, "the allowed Worker row must expose exactly one restart trigger");
      assert.equal(trigger.tagName, "BUTTON", "the Radix trigger ref must target the actual Button element");
      assert.equal(trigger.interactiveButtonCount, 1, "the Worker restart trigger must expose exactly one button role");
      assert.equal(trigger.nestedButtonCount, 0, "the Worker restart trigger must not nest an interactive button");
      assert.equal(trigger.ariaHaspopup, "dialog", "the actual Button must receive Radix aria-haspopup");
      assert.equal(trigger.ariaExpanded, "false", "the closed actual Button must receive Radix aria-expanded");
      const inapplicableTriggerCount = await workerRestartTriggerCount(browser, "Encoder One");
      assert.equal(inapplicableTriggerCount, 0, "a non-Worker row must not render the restart trigger");

      browser.clearRequestCounts(restartPath);
      await clickWorkerAction(browser, "Restart worker", "Worker One Permission Restored");
      const pointerDialog = await waitForWorkerRestartDialog(browser, "Worker One Permission Restored");
      assertWorkerRestartSingleOpenEvidence(trigger, pointerDialog, "pointer");
      assert.ok(pointerDialog.title.length > 0, "the open Worker restart confirmation must expose its title");
      assert.ok(pointerDialog.description.length > 0, "the open Worker restart confirmation must expose its description");
      assert.equal(pointerDialog.activeInside, true, "pointer activation must move focus into the consequence dialog");
      assert.equal(pointerDialog.triggerDataState, "open", "the actual Button must receive Radix's open data-state");
      assert.equal(pointerDialog.triggerAriaExpanded, "true", "the actual Button must receive Radix's open aria-expanded state");
      assert.equal(pointerDialog.restartNotice, "", "opening the confirmation must not manufacture an outcome notice");
      assert.equal(browser.requests.get(restartPath) || 0, 0, "opening by pointer must not send POST before confirmation");
      await browser.pressNativeKey("Escape");
      await waitForWorkerRestartDialogClosed(browser);
      await waitForWorkerRestartTriggerFocus(browser, "Worker One Permission Restored", "Escape");

      await tabToWorkerAction(browser, "Restart worker", "Worker One Permission Restored");
      const enterTrigger = await waitForWorkerAction(
        browser,
        "Restart worker",
        "Worker One Permission Restored",
        (value) => value.dataState === "closed",
      );
      await browser.pressNativeKey("Enter");
      const enterDialog = await waitForWorkerRestartDialog(browser, "Worker One Permission Restored");
      assertWorkerRestartSingleOpenEvidence(enterTrigger, enterDialog, "Enter");
      assert.equal(enterDialog.activeInside, true, "Enter activation must move focus into the consequence dialog");
      assert.equal(browser.requests.get(restartPath) || 0, 0, "opening by Enter must not send POST before confirmation");
      await browser.pressNativeKey("Escape");
      await waitForWorkerRestartDialogClosed(browser);
      await waitForWorkerRestartTriggerFocus(browser, "Worker One Permission Restored", "Enter then Escape");

      const spaceTrigger = await waitForWorkerAction(
        browser,
        "Restart worker",
        "Worker One Permission Restored",
        (value) => value.dataState === "closed",
      );
      await browser.pressNativeKey("Space");
      const spaceDialog = await waitForWorkerRestartDialog(browser, "Worker One Permission Restored");
      assertWorkerRestartSingleOpenEvidence(spaceTrigger, spaceDialog, "Space");
      assert.equal(spaceDialog.activeInside, true, "Space activation must move focus into the consequence dialog");
      assert.equal(browser.requests.get(restartPath) || 0, 0, "opening by Space must not send POST before confirmation");
      await browser.clickSelector('[data-slot="alert-dialog-cancel"]');
      await waitForWorkerRestartDialogClosed(browser);
      await waitForWorkerRestartTriggerFocus(browser, "Worker One Permission Restored", "Cancel");

      await waitForWorkerRestartReady(browser);
      await clickWorkerAction(browser, "Restart worker");
      await waitForWorkerRestartDialog(browser);
      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
      const revokedAuthResponseCount = (browser.responses.get("/auth/me") || 0) + 1;
      fixture.authResponse = { body: permissionUser(["workers.read"]) };
      await browser.waitForResponseCount("/auth/me", revokedAuthResponseCount, 20_000);
      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
      const revoked = await waitForWorkerAction(browser, "Restart worker", "Worker One", (value) => value.disabled && /do not have permission to restart workers/i.test(value.reason));
      assert.match(revoked.reason, /do not have permission to restart workers/i);
      fixture.workerRestartMethods = [];
      browser.clearRequestCounts(restartPath);
      browser.clearRequestCounts("/workers");
      await browser.clickSelector('[data-confirm-action]');
      await browser.waitFor(
        "document.body.textContent || ''",
        (value: string) => value.includes("The action cannot be sent because the latest permissions or state could not be verified"),
        "a submit-time permission revoke was not blocked",
      );
      assert.equal(await workerRestartDialogCount(browser), 1, "submit-time permission revalidation must remain in the existing dialog");
      assert.equal(browser.requests.get(restartPath) || 0, 0, "a submit-time permission revoke must not send POST");
      assert.deepEqual(fixture.workerRestartMethods, []);
      await browser.pressNativeKey("Escape");
      await waitForWorkerRestartDialogClosed(browser);
      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
      const restoredAuthResponseCount = (browser.responses.get("/auth/me") || 0) + 1;
      fixture.authResponse = { body: permittedUser };
      await browser.waitForResponseCount("/auth/me", restoredAuthResponseCount, 20_000);
      fixture.workersResponse = { body: workerPilotRows };
      browser.clearRequestCounts("/workers");
      await browser.reload();
      await browser.waitForResponseCount("/workers", 1);
      await waitForWorkerRestartReady(browser);

      await clickWorkerAction(browser, "Restart worker");
      await waitForWorkerRestartDialog(browser);
      browser.clearRequestCounts("/workers");
      await browser.evaluate("window.dispatchEvent(new Event('focus')); true");
      await browser.waitForResponseCount("/workers", 1, 20_000);
      await browser.waitForRequestHandlersIdle({ pathname: "/workers", method: "GET" });
      fixture.workersResponse = { body: [] };
      fixture.workerRestartMethods = [];
      browser.clearRequestCounts(restartPath);
      browser.clearRequestCounts("/workers");
      await browser.clickSelector('[data-confirm-action]');
      await browser.waitForRequestCount("/workers", 1);
      await browser.waitFor(
        "document.body.textContent || ''",
        (value: string) => value.includes("The target changed, so the action was not sent"),
        "a removed target was not blocked at submit-time revalidation",
      );
      assert.equal(browser.requests.get(restartPath) || 0, 0, "a removed target must not send POST");
      assert.deepEqual(fixture.workerRestartMethods, []);
      fixture.workersResponse = { body: workerPilotRows };
      await browser.pressNativeKey("Escape");
      await waitForWorkerRestartDialogClosed(browser);
      browser.clearRequestCounts("/workers");
      await browser.reload();
      await browser.waitForResponseCount("/workers", 1);
      await waitForWorkerRestartReady(browser);

      await clickWorkerAction(browser, "Restart worker");
      await waitForWorkerRestartDialog(browser);
      fixture.workersResponse = { body: workerPilotRows.map((row, index) => index === 0 ? { ...row, service_type: "encoder_recorder" } : row) };
      fixture.workerRestartMethods = [];
      browser.clearRequestCounts(restartPath);
      browser.clearRequestCounts("/workers");
      await browser.clickSelector('[data-confirm-action]');
      await browser.waitForRequestCount("/workers", 1);
      await browser.waitFor(
        "document.body.textContent || ''",
        (value: string) => value.includes("The target changed, so the action was not sent"),
        "a changed service_type was not blocked at submit-time revalidation",
      );
      assert.equal(browser.requests.get(restartPath) || 0, 0, "a changed service_type must not send POST");
      assert.deepEqual(fixture.workerRestartMethods, []);
      fixture.workersResponse = { body: workerPilotRows };
      await browser.pressNativeKey("Escape");
      await waitForWorkerRestartDialogClosed(browser);
      browser.clearRequestCounts("/workers");
      await browser.reload();
      await browser.waitForResponseCount("/workers", 1);
      await waitForWorkerRestartReady(browser);

      restartRelease = deferred();
      fixture.restartResponse = {
        status: 202,
        body: { status: "accepted" },
        waitUntil: restartRelease.promise,
        requiredResponse: true,
      };
      fixture.workerRestartMethods = [];
      browser.clearRequestCounts(restartPath);
      await clickWorkerAction(browser, "Restart worker");
      await waitForWorkerRestartDialog(browser);
      await browser.clickSelector('[data-confirm-action]');
      await browser.waitForRequestCount(restartPath, 1);
      const pendingTrigger = await waitForWorkerAction(browser, "Restart worker", "Worker One", (value) => value.disabled);
      assert.equal(pendingTrigger.disabled, true, "the same Worker restart trigger must be unavailable while confirmation is pending");
      assert.equal(await workerRestartDialogCount(browser), 1, "pending duplicate activation must not create an additional dialog");
      await browser.clickSelector('[data-confirm-action]');
      await browser.pressNativeKey("Enter");
      await browser.pressNativeKey("Enter");
      await waitForAnimationFrames(browser);
      assert.equal(browser.requests.get(restartPath), 1, "double click and Enter repeat must share one latched POST");
      assert.deepEqual(fixture.workerRestartMethods, ["POST"]);
      const independentWorker = await waitForWorkerAction(browser, "Restart worker", "Future Worker", (value) => value.count >= 1);
      assert.equal(independentWorker.disabled, false, "a different worker must remain independently evaluated while the first worker is pending");
      restartRelease.resolve();
      await browser.waitForResponseCount(restartPath, 1);
      await browser.waitFor(
        "document.body.textContent || ''",
        (value: string) => value.includes("The worker restart request was accepted."),
        "restart success notice did not render",
      );
      await waitForWorkerRestartDialogClosed(browser);
      await waitForWorkerRestartTriggerFocus(browser, "Worker One", "completion");

      for (const failure of [
        {
          name: "403",
          response: { status: 403, body: { code: "permission_denied", message: "RAW-RESTART-403-MARKER" }, requiredResponse: true },
          publicText: "You do not have permission to perform this action.",
          expectedWorkerGets: 1,
        },
        {
          name: "409",
          response: { status: 409, body: { code: "worker_busy", message: "RAW-RESTART-409-MARKER" }, requiredResponse: true },
          publicText: "The resource has changed. Review the latest state.",
          expectedWorkerGets: 2,
        },
        {
          name: "outcome_unknown",
          response: { status: 503, body: { code: "worker_unavailable", message: "RAW-RESTART-503-MARKER" }, requiredResponse: true },
          publicText: "The result could not be confirmed. Do not resend the action",
          expectedWorkerGets: 1,
        },
      ] as const) {
        fixture.restartResponse = failure.response;
        fixture.workerRestartMethods = [];
        browser.clearRequestCounts(restartPath);
        browser.clearRequestCounts("/workers");
        await waitForWorkerRestartReady(browser);
        await clickWorkerAction(browser, "Restart worker");
        await waitForWorkerRestartDialog(browser);
        await browser.clickSelector('[data-confirm-action]');
        await browser.waitForResponseCount(restartPath, 1);
        await browser.waitForRequestCount("/workers", failure.expectedWorkerGets);
        await browser.waitFor(
          "document.body.textContent || ''",
          (value: string) => value.includes(failure.publicText),
          `${failure.name} did not reach its safe public state`,
        );
        const renderedOutcome = await browser.evaluate<string>("document.body.textContent || ''");
        assert.equal(renderedOutcome.includes(`RAW-RESTART-${failure.name === "outcome_unknown" ? "503" : failure.name}-MARKER`), false);
        if (failure.name === "outcome_unknown") {
          assert.equal(renderedOutcome.includes("The worker restart request was accepted."), false, "outcome_unknown must not claim success");
          assert.equal(renderedOutcome.includes("The worker restart request failed."), false, "outcome_unknown must not claim failure");
        }
        const outcomeFocus = await waitForWorkerRestartOutcomeFocus(
          browser,
          "Worker One",
          failure.publicText,
          failure.name,
        );
        assert.equal(outcomeFocus.dialogCount, 1, `${failure.name} must keep exactly one dialog open before Escape`);
        assert.equal(outcomeFocus.activeExists, true, `${failure.name} must retain an active element`);
        assert.equal(outcomeFocus.activeInside, true, `${failure.name} focus escaped the active dialog`);
        assert.equal(outcomeFocus.activeIsBody, false, `${failure.name} moved focus to body`);
        assert.equal(outcomeFocus.activeIsTrigger, false, `${failure.name} moved focus to the background restart trigger`);
        assert.equal(outcomeFocus.activeHiddenOrInert, false, `${failure.name} moved focus to a hidden or inert element`);
        assert.equal(outcomeFocus.activeVisible, true, `${failure.name} active element is not visibly focusable`);
        assert.equal(outcomeFocus.safeOutcomeTextVisible, true, `${failure.name} safe outcome text is not visible in the dialog`);
        const repeatActionPresent = await browser.evaluate<boolean>(
          "Boolean(document.querySelector('[data-confirm-action]'))",
        );
        if (repeatActionPresent) {
          await browser.clickSelector('[data-confirm-action]');
          await browser.pressNativeKey("Enter");
        } else {
          assert.equal(failure.name, "outcome_unknown", "only outcome_unknown may remove the repeat action");
        }
        await waitForAnimationFrames(browser);
        assert.equal(browser.requests.get(restartPath), 1, `${failure.name} must never be resent automatically or by repeated activation`);
        assert.deepEqual(fixture.workerRestartMethods, ["POST"], `${failure.name} request methods`);
        await browser.pressNativeKey("Escape");
        await waitForWorkerRestartDialogClosed(browser);
        await waitForWorkerRestartTriggerFocus(browser, "Worker One", `${failure.name} Escape`);
      }
      assertNoBrowserConsoleErrors(browser.consoleErrorCount);
    } finally {
      authRefreshRelease?.resolve();
      restartRelease?.resolve();
      fixture.authResponse = { body: currentUser };
      fixture.workersResponse = { body: workerPilotRows };
      fixture.nodesResponse = { body: workerPilotRows };
      fixture.restartResponse = { status: 202, body: { status: "accepted" } };
      fixture.workerRestartMethods = [];
      await browser.setMediaFeatures([]).catch(() => {});
    }
  });
}
