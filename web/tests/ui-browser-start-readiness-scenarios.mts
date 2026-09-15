import type { TestContext } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { assertStreamsStartReadinessHandlerGuard, mutateStreamsStartReadinessHandlerGuard, type StreamsStartReadinessGuardSources, type StreamsStartReadinessGuardMutation } from "./helpers/streams-start-readiness-handler-guard.mts";
import { assertNoBrowserConsoleErrors } from "./helpers/ui-foundation-assertions.mts";
import { type BrowserRouteFixture, startReadinessStream, healthyRows, currentVersion, permissionUser, startReadinessPath, currentUser } from "./ui-browser-fixture.mts";
import { setStoredDisplay, waitForAnimationFrames, deferred } from "./ui-browser-navigation-helpers.mts";
import { waitForStartReadinessHandlersIdle, waitForShell } from "./ui-browser-query-auth-helpers.mts";
import { clickVisible, clickDisabledVisible } from "./ui-regression/visible-trigger.mts";



export async function runStartReadinessScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "streamsResponse" | "healthResponse" | "versionFixture" | "authResponse" | "startReadinessResponse" | "startReadinessMethods">) {

  await t.test("Streams start-readiness follows streams.start at render and confirm time", async (t) => {
    const rowSelector = `[data-slot="data-table"] tr:has([data-slot="stream-primary-trigger"][data-stream-id=${JSON.stringify(startReadinessStream.id)}])`;
    const readinessSelector = `${rowSelector} button[aria-label=${JSON.stringify(`${startReadinessStream.name} の開始準備を再確認`)}]`;
    const editSelector = `${rowSelector} button[aria-label=${JSON.stringify(`${startReadinessStream.name} を編集`)}]`;
    const actionSnapshotExpression = `(() => {
      const visibleButton = (selector) => { const matches=[...document.querySelectorAll(selector)].filter(element => element instanceof HTMLButtonElement && element.getClientRects().length > 0 && getComputedStyle(element).visibility!=='hidden'); if(matches.length>1)throw Error('ambiguous readiness row control');return matches[0]; };
      const readiness = visibleButton(${JSON.stringify(readinessSelector)});
      const edit = visibleButton(${JSON.stringify(editSelector)});
      const reasonId = readiness?.getAttribute("aria-describedby");
      return {
        readinessPresent: Boolean(readiness),
        readinessAvailable: readiness instanceof HTMLButtonElement && !readiness.disabled,
        editAvailable: edit instanceof HTMLButtonElement && !edit.disabled,
        readinessAvailabilityKind: readiness?.closest("[data-action-availability]")?.getAttribute("data-action-availability") || "allowed",
        readinessReason: reasonId ? document.getElementById(reasonId)?.textContent || "" : "",
      };
    })()`;
    const successNotice = "開始準備の確認結果を受信しました。";
    const successResponse = {
      body: { stream_id: startReadinessStream.id, ready: true, missing_service_types: [], issues: [], assigned_service_count: 2 },
      requiredResponse: true,
    };
    const matrix = [
      { name: "start only", permissions: ["streams.read", "streams.start"], readinessAvailable: true, editAvailable: false, requestCount: 1 },
      { name: "update only", permissions: ["streams.read", "streams.update"], readinessAvailable: false, editAvailable: true, requestCount: 0 },
      { name: "both", permissions: ["streams.read", "streams.start", "streams.update"], readinessAvailable: true, editAvailable: true, requestCount: 1 },
      { name: "neither", permissions: ["streams.read"], readinessAvailable: false, editAvailable: false, requestCount: 0 },
      { name: "wildcard", permissions: ["*"], readinessAvailable: true, editAvailable: true, requestCount: 1 },
    ] as const;

    await t.test("handler guard structural oracle rejects in-memory regressions", () => {
      const sources: StreamsStartReadinessGuardSources = {
        view: readFileSync(new URL("../src/features/streams/streams-view.tsx", import.meta.url), "utf8"),
        cells: readFileSync(new URL("../src/features/streams/stream-table-cells.tsx", import.meta.url), "utf8"),
        detailDialog: readFileSync(new URL("../src/features/streams/stream-details-dialog.tsx", import.meta.url), "utf8"),
        details: readFileSync(new URL("../src/features/streams/stream-detail-operations.tsx", import.meta.url), "utf8"),
        controller: readFileSync(new URL("../src/features/streams/stream-action-controller.ts", import.meta.url), "utf8"),
        descriptors: readFileSync(new URL("../src/features/streams/stream-action-descriptors.ts", import.meta.url), "utf8"),
      };
      assert.doesNotThrow(
        () => assertStreamsStartReadinessHandlerGuard(sources),
        "actual start-readiness submit handler must retain its fresh permission guard",
      );
      const negativeFixtures: {
        name: string;
        mutation: StreamsStartReadinessGuardMutation;
        expectedError: RegExp;
      }[] = [
        { name: "current permission snapshot removed", mutation: "remove-current-permission-snapshot", expectedError: /current permission snapshot/ },
        { name: "streams.start replaced by streams.update", mutation: "use-streams-update-authority", expectedError: /descriptor authority must remain streams\.start/ },
        { name: "pre-submit evaluation removed", mutation: "remove-pre-submit-evaluation", expectedError: /pre-submit evaluator/ },
        { name: "mutation moved before guard", mutation: "move-mutation-before-guard", expectedError: /mutation must follow every pre-submit guard/ },
        { name: "alternate unguarded mutation added", mutation: "add-alternate-unguarded-mutation", expectedError: /exactly one guarded mutation path/ },
      ];
      for (const fixture of negativeFixtures) {
        const mutatedSources = mutateStreamsStartReadinessHandlerGuard(sources, fixture.mutation);
        assert.throws(
          () => assertStreamsStartReadinessHandlerGuard(mutatedSources),
          fixture.expectedError,
          `${fixture.name} must make the structural oracle Red`,
        );
      }
    });

    try {
      fixture.streamsResponse = { body: [startReadinessStream] };
      fixture.healthResponse = { body: healthyRows };
      fixture.versionFixture = { body: currentVersion };
      await browser.setViewport(1440, 900);
      await setStoredDisplay(browser, "ja", "light");

      for (const matrixCase of matrix) {
        await t.test(matrixCase.name, async () => {
          await waitForStartReadinessHandlersIdle(browser);
          fixture.authResponse = { body: permissionUser([...matrixCase.permissions]) };
          fixture.startReadinessResponse = successResponse;
          fixture.startReadinessMethods = [];
          browser.clearRequestCounts(startReadinessPath);
          browser.clearConsoleErrors();
          await browser.navigate(`${server.baseUrl}/admin/streams/`);
          await waitForShell(browser, "アカウントメニュー");
          const initial = await browser.waitFor<{ readinessPresent: boolean; readinessAvailable: boolean; editAvailable: boolean }>(
            actionSnapshotExpression,
            (value) => value.readinessPresent,
            `${matrixCase.name}: start-readiness action did not render`,
          );

          if (initial.readinessAvailable) {
            await clickVisible(browser, readinessSelector);
            await browser.waitFor(
              "Boolean(document.querySelector('[data-slot=\"alert-dialog-content\"][data-state=\"open\"]'))",
              Boolean,
              `${matrixCase.name}: start-readiness confirmation did not open`,
            );
            await clickVisible(browser, '[data-slot="alert-dialog-content"][data-state="open"] [data-confirm-action]');
            await browser.waitForRequestCount(startReadinessPath, 1);
            await browser.waitForResponseCount(startReadinessPath, 1);
            await browser.waitFor(
              "document.body.textContent || ''",
              (value: string) => value.includes(successNotice),
              `${matrixCase.name}: start-readiness mutation did not reach its public success boundary`,
            );
          } else {
            await clickDisabledVisible(browser, readinessSelector);
            await waitForAnimationFrames(browser);
          }
          await waitForStartReadinessHandlersIdle(browser);

          assert.deepEqual(
            {
              readinessAvailable: initial.readinessAvailable,
              editAvailable: initial.editAvailable,
              requestCount: browser.requests.get(startReadinessPath) || 0,
              responseCount: browser.responses.get(startReadinessPath) || 0,
              methods: fixture.startReadinessMethods,
            },
            {
              readinessAvailable: matrixCase.readinessAvailable,
              editAvailable: matrixCase.editAvailable,
              requestCount: matrixCase.requestCount,
              responseCount: matrixCase.requestCount,
              methods: matrixCase.requestCount === 1 ? ["POST"] : [],
            },
            `${matrixCase.name}: start-readiness permission authority`,
          );
          assertNoBrowserConsoleErrors(browser.consoleErrorCount);
        });
      }

      await t.test("permission changes before confirm", async () => {
        await waitForStartReadinessHandlersIdle(browser);
        fixture.authResponse = { body: permissionUser(["streams.read", "streams.start"]) };
        fixture.startReadinessResponse = successResponse;
        await browser.navigate(`${server.baseUrl}/admin/streams/`);
        await waitForShell(browser, "アカウントメニュー");
        await browser.waitFor(
          actionSnapshotExpression,
          (value: { readinessAvailable: boolean }) => value.readinessAvailable,
          "start-readiness action was not initially available",
        );
        await clickVisible(browser, readinessSelector);
        await browser.waitFor(
          "Boolean(document.querySelector('[data-slot=\"alert-dialog-content\"][data-state=\"open\"]'))",
          Boolean,
          "start-readiness confirmation did not open before permission change",
        );

        fixture.authResponse = { body: permissionUser(["streams.read"]) };
        await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
        browser.clearRequestCounts("/auth/me");
        await browser.evaluate("document.dispatchEvent(new Event('visibilitychange', { bubbles: true })); true");
        await browser.waitForResponseCount("/auth/me", 1);
        await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
        await browser.waitFor(
          actionSnapshotExpression,
          (value: { readinessAvailable: boolean }) => !value.readinessAvailable,
          "refetched permissions did not disable start-readiness",
        );

        await waitForStartReadinessHandlersIdle(browser);
        fixture.startReadinessMethods = [];
        browser.clearRequestCounts(startReadinessPath);
        assert.equal(
          await browser.evaluate("Boolean(document.querySelector('[data-slot=\"alert-dialog-content\"][data-state=\"open\"]'))"),
          false,
          "permission refresh must dismiss the stale start-readiness confirmation",
        );
        await clickDisabledVisible(browser, readinessSelector);
        await waitForAnimationFrames(browser);
        await waitForStartReadinessHandlersIdle(browser);
        assert.deepEqual(
          {
            requestCount: browser.requests.get(startReadinessPath) || 0,
            responseCount: browser.responses.get(startReadinessPath) || 0,
            methods: fixture.startReadinessMethods,
          },
          { requestCount: 0, responseCount: 0, methods: [] },
          "a permission change before confirmation must not send a mutation",
        );
      });

      await t.test("backend 403 retains the existing action error mapping", async () => {
        await waitForStartReadinessHandlersIdle(browser);
        fixture.authResponse = { body: permissionUser(["streams.read", "streams.start"]) };
        fixture.startReadinessResponse = { status: 403, body: { code: "permission_denied" }, requiredResponse: true };
        fixture.startReadinessMethods = [];
        browser.clearRequestCounts(startReadinessPath);
        await browser.navigate(`${server.baseUrl}/admin/streams/`);
        await waitForShell(browser, "アカウントメニュー");
        await browser.waitFor(
          actionSnapshotExpression,
          (value: { readinessAvailable: boolean }) => value.readinessAvailable,
          "403 fixture start-readiness action was not available",
        );
        await clickVisible(browser, readinessSelector);
        await browser.waitFor(
          "Boolean(document.querySelector('[data-slot=\"alert-dialog-content\"][data-state=\"open\"]'))",
          Boolean,
          "403 fixture confirmation did not open",
        );
        await clickVisible(browser, '[data-slot="alert-dialog-content"][data-state="open"] [data-confirm-action]');
        await browser.waitForRequestCount(startReadinessPath, 1);
        await browser.waitForResponseCount(startReadinessPath, 1);
        await browser.waitFor(
          "document.body.textContent || ''",
          (value: string) => value.includes("この操作を実行する権限がありません。") && !value.includes("permission_denied"),
          "backend 403 did not use the safe shared API error mapping",
        );
        await browser.waitFor(
          actionSnapshotExpression,
          (value: { readinessAvailable: boolean }) => value.readinessAvailable,
          "backend 403 mutation did not return to its terminal state",
        );
        await waitForStartReadinessHandlersIdle(browser);
        assert.equal(browser.requests.get(startReadinessPath), 1, "backend 403 must not be resent automatically");
        assert.equal(browser.responses.get(startReadinessPath), 1, "backend 403 must have exactly one response");
        assert.deepEqual(fixture.startReadinessMethods, ["POST"]);
      });

      await t.test("pending mutation keeps duplicate start-readiness blocked", async () => {
        const release = deferred();
        try {
          await waitForStartReadinessHandlersIdle(browser);
          fixture.authResponse = { body: permissionUser(["streams.read", "streams.start"]) };
          fixture.startReadinessResponse = { ...successResponse, waitUntil: release.promise };
          fixture.startReadinessMethods = [];
          browser.clearRequestCounts(startReadinessPath);
          await browser.navigate(`${server.baseUrl}/admin/streams/`);
          await waitForShell(browser, "アカウントメニュー");
          await browser.waitFor(
            actionSnapshotExpression,
            (value: { readinessAvailable: boolean }) => value.readinessAvailable,
            "pending fixture start-readiness action was not available",
          );
          await clickVisible(browser, readinessSelector);
          await browser.waitFor(
            "Boolean(document.querySelector('[data-slot=\"alert-dialog-content\"][data-state=\"open\"]'))",
            Boolean,
            "pending fixture confirmation did not open",
          );
          await clickVisible(browser, '[data-slot="alert-dialog-content"][data-state="open"] [data-confirm-action]');
          await browser.waitForRequestCount(startReadinessPath, 1);
          await browser.waitFor(
            actionSnapshotExpression,
            (value: { readinessAvailable: boolean }) => !value.readinessAvailable,
            "pending start-readiness mutation did not disable its trigger",
          );
          await clickDisabledVisible(browser, readinessSelector);
          await waitForAnimationFrames(browser);
          assert.equal(browser.requests.get(startReadinessPath), 1, "pending start-readiness must not send a duplicate request");
          assert.equal(browser.responses.get(startReadinessPath) || 0, 0, "deferred start-readiness must remain pending before release");
          assert.deepEqual(fixture.startReadinessMethods, ["POST"]);
        } finally {
          release.resolve();
          if ((browser.requests.get(startReadinessPath) || 0) > 0) {
            await browser.waitForResponseCount(startReadinessPath, 1);
            await browser.waitFor(
              "document.body.textContent || ''",
              (value: string) => value.includes(successNotice),
              "released start-readiness mutation did not reach its public success boundary",
            );
          }
          await waitForStartReadinessHandlersIdle(browser);
        }
      });
    } finally {
      fixture.authResponse = { body: currentUser };
      fixture.streamsResponse = { body: [] };
      fixture.startReadinessResponse = successResponse;
      fixture.startReadinessMethods = [];
    }
  });
}
