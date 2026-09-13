import type { TestContext } from "node:test";
import assert from "node:assert/strict";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { assertNoBrowserConsoleErrors } from "./helpers/ui-foundation-assertions.mts";
import { type BrowserRouteFixture, workerPilotRows, permissionUser, workerConfiguration, currentUser, healthyRows } from "./ui-browser-fixture.mts";
import { deferred, setStoredDisplay, waitForAnimationFrames } from "./ui-browser-navigation-helpers.mts";
import { waitForShell } from "./ui-browser-query-auth-helpers.mts";
import { waitForWorkerAction, clickWorkerAction, clickButtonWithText, browserContainsMarker, waitForWorkerStatus } from "./ui-browser-worker-helpers.mts";



export async function runWorkerConfigurationScenario(t: TestContext, browser: BrowserHarness, server: { baseUrl: string }, fixture: Pick<BrowserRouteFixture, "workersResponse" | "nodesResponse" | "healthResponse" | "authResponse" | "configurationResponse" | "workerConfigurationMethods">) {

  await t.test("Workers Configuration uses the server ANY permission and safe remote state", async () => {
    const configurationPath = "/nodes/worker-one/configuration";
    let authRefreshRelease: ReturnType<typeof deferred> | undefined;
    let configurationRefreshRelease: ReturnType<typeof deferred> | undefined;
    try {
      await browser.setViewport(1440, 900);
      await browser.setMediaFeatures([]);
      browser.clearConsoleErrors();
      await setStoredDisplay(browser, "en", "light");
      fixture.workersResponse = { body: workerPilotRows };
      fixture.nodesResponse = { body: workerPilotRows };
      fixture.healthResponse = { body: workerPilotRows };

      for (const permissionCase of [
        { name: "service_health.read only", permissions: ["workers.read", "service_health.read"], allowed: true },
        { name: "api_tokens.create only", permissions: ["workers.read", "api_tokens.create"], allowed: true },
        { name: "both", permissions: ["workers.read", "service_health.read", "api_tokens.create"], allowed: true },
        { name: "neither", permissions: ["workers.read"], allowed: false },
        { name: "wildcard", permissions: ["*"], allowed: true },
      ] as const) {
        fixture.authResponse = { body: permissionUser([...permissionCase.permissions]) };
        fixture.configurationResponse = { body: workerConfiguration("worker-one", `CONFIG-${permissionCase.name}-MARKER`) };
        fixture.workerConfigurationMethods = [];
        await browser.navigate(`${server.baseUrl}/admin/workers/`);
        await waitForShell(browser, "Account menu");
        const action = await waitForWorkerAction(
          browser,
          "Show configuration",
          "Worker One",
          (value) => permissionCase.allowed ? value.disabled === false : value.disabled && value.reason.length > 0,
        );
        browser.clearRequestCounts(configurationPath);
        await clickWorkerAction(browser, "Show configuration");
        if (permissionCase.allowed) {
          await browser.waitForResponseCount(configurationPath, 1);
          await browser.waitFor(
            "document.body.textContent || ''",
            (value: string) => value.includes(`CONFIG-${permissionCase.name}-MARKER`),
            `${permissionCase.name} did not render Configuration content`,
          );
          assert.equal(browser.requests.get(configurationPath), 1, `${permissionCase.name} GET count`);
          assert.deepEqual(fixture.workerConfigurationMethods, ["GET"]);
        } else {
          await waitForAnimationFrames(browser);
          assert.match(action.reason, /do not have permission to view this configuration/i);
          assert.equal(browser.requests.get(configurationPath) || 0, 0, "workers.read alone must not authorize Configuration GET");
          assert.deepEqual(fixture.workerConfigurationMethods, []);
        }
      }

      fixture.authResponse = { body: permissionUser(["workers.read", "service_health.read"]) };
      fixture.configurationResponse = { body: workerConfiguration("worker-one", "CACHED-CONFIGURATION-MARKER") };
      await browser.navigate(`${server.baseUrl}/admin/workers/`);
      await waitForShell(browser, "Account menu");
      await waitForWorkerAction(browser, "Show configuration", "Worker One", (value) => value.disabled === false);

      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
      const refreshingAuthRequestCount = (browser.requests.get("/auth/me") || 0) + 1;
      authRefreshRelease = deferred();
      fixture.authResponse = {
        body: permissionUser(["workers.read", "service_health.read"]),
        waitUntil: authRefreshRelease.promise,
      };
      await browser.waitForRequestCount("/auth/me", refreshingAuthRequestCount, 20_000);
      await waitForWorkerAction(browser, "Show configuration", "Worker One", (value) => value.count >= 1);
      browser.clearRequestCounts(configurationPath);
      await clickWorkerAction(browser, "Show configuration");
      const unknown = await waitForWorkerAction(browser, "Show configuration", "Worker One", (value) => value.disabled && value.reason.length > 0);
      assert.match(unknown.reason, /permission could not be verified/i);
      assert.equal(browser.requests.get(configurationPath) || 0, 0, "a refreshing permission snapshot must suppress Configuration GET");
      authRefreshRelease.resolve();
      fixture.authResponse = { body: permissionUser(["workers.read", "service_health.read"]) };
      await browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
      await browser.reload();
      await waitForShell(browser, "Account menu");
      await waitForWorkerAction(browser, "Show configuration", "Worker One", (value) => value.disabled === false);

      fixture.workerConfigurationMethods = [];
      browser.clearRequestCounts(configurationPath);
      await clickWorkerAction(browser, "Show configuration");
      await browser.waitForResponseCount(configurationPath, 1);
      await browser.waitFor(
        "document.body.textContent || ''",
        (value: string) => value.includes("CACHED-CONFIGURATION-MARKER")
          && value.includes("Node Agent API URL")
          && value.includes("Auto Configure command")
          && value.includes("systemd unit"),
        "successful Configuration content was incomplete",
      );
      assert.deepEqual(fixture.workerConfigurationMethods, ["GET"]);

      configurationRefreshRelease = deferred();
      fixture.configurationResponse = {
        status: 503,
        body: { code: "configuration_unavailable", message: "RAW-CONFIGURATION-REFRESH-MARKER" },
        waitUntil: configurationRefreshRelease.promise,
      };
      fixture.workerConfigurationMethods = [];
      browser.clearRequestCounts(configurationPath);
      await clickButtonWithText(browser, "Reload configuration");
      await browser.waitForRequestCount(configurationPath, 1);
      await browser.waitFor(
        "document.body.textContent || ''",
        (value: string) => value.includes("CACHED-CONFIGURATION-MARKER") && value.includes("Refreshing configuration"),
        "same-target pending refresh hid the cached Configuration",
      );
      configurationRefreshRelease.resolve();
      await browser.waitForResponseCount(configurationPath, 1);
      await browser.waitFor(
        "document.body.textContent || ''",
        (value: string) => value.includes("CACHED-CONFIGURATION-MARKER") && value.includes("The refresh failed"),
        "same-target refresh failure did not preserve stale Configuration",
      );
      assert.deepEqual(fixture.workerConfigurationMethods, ["GET"]);
      assert.equal(await browserContainsMarker(browser, "RAW-CONFIGURATION-REFRESH-MARKER"), false);

      fixture.configurationResponse = {
        status: 500,
        body: { code: "get_node_failed", message: "RAW-CONFIGURATION-BLOCKING-MARKER" },
      };
      fixture.workerConfigurationMethods = [];
      await browser.navigate(`${server.baseUrl}/admin/workers/`);
      await waitForShell(browser, "Account menu");
      await waitForWorkerAction(browser, "Show configuration", "Worker One", (value) => value.disabled === false);
      browser.clearRequestCounts(configurationPath);
      await clickWorkerAction(browser, "Show configuration");
      await browser.waitForResponseCount(configurationPath, 1);
      await browser.waitFor(
        "document.body.textContent || ''",
        (value: string) => value.includes("The data could not be loaded.") && value.includes("The service is temporarily unavailable."),
        "a no-data Configuration error did not render as a safe blocking state",
      );
      assert.equal(await browserContainsMarker(browser, "RAW-CONFIGURATION-BLOCKING-MARKER"), false, "raw error marker leaked into DOM or accessibility attributes");
      assert.deepEqual(fixture.workerConfigurationMethods, ["GET"]);

      fixture.configurationResponse = { body: workerConfiguration("worker-one", "STATUS-CONFIGURATION-MARKER") };
      fixture.authResponse = { body: currentUser };
      fixture.healthResponse = { body: [
        ...healthyRows.filter((row) => row.service_type === "worker"),
        { id: "worker-malformed", service_type: "worker", service_name: "Malformed Status Worker", status: "future_connectivity", health_status: { known: true, labelKey: "statusNodeHealthy" } },
      ] };
      await browser.navigate(`${server.baseUrl}/admin/workers/`);
      await waitForShell(browser, "Account menu");
      const healthyStatus = await waitForWorkerStatus(browser, "Worker One", (value) => value.text.includes("Healthy"));
      assert.deepEqual(
        { known: healthyStatus.known, tone: healthyStatus.tone, icon: healthyStatus.icon },
        { known: "true", tone: "success", icon: "heart-pulse" },
      );
      const unknownStatus = await waitForWorkerStatus(browser, "Future Worker", (value) => value.text.includes("Unknown status"));
      assert.deepEqual(
        { known: unknownStatus.known, tone: unknownStatus.tone, icon: unknownStatus.icon },
        { known: "false", tone: "unknown", icon: "circle-help" },
      );
      assert.equal(unknownStatus.rowText.includes("future_online_v2"), false);
      assert.equal(unknownStatus.rowText.includes("future_healthy_v2"), false);
      const assignedStatus = await waitForWorkerStatus(browser, "Assigned Worker", (value) => value.text.includes("Assigned"));
      assert.equal(assignedStatus.text.includes("Healthy"), false, "assignment must not be presented as health");
      assert.equal(assignedStatus.tone, "info");
      const formerAlias = await waitForWorkerStatus(browser, "Former Alias Worker", (value) => value.text.includes("Unknown status"));
      assert.equal(formerAlias.rowText.includes("ok"), false, "removed node-health alias must remain unknown and hidden");
      const restoredCanonical = await waitForWorkerStatus(browser, "Degraded Worker", (value) => value.text.includes("Degraded"));
      assert.deepEqual(
        { known: restoredCanonical.known, tone: restoredCanonical.tone, icon: restoredCanonical.icon },
        { known: "true", tone: "warning", icon: "triangle-alert" },
      );
      const malformedStatus = await waitForWorkerStatus(browser, "Malformed Status Worker", (value) => value.text.includes("Unknown status"));
      assert.equal(malformedStatus.text.includes("Healthy"), false, "a partial presentation-like object must contribute no positive status");
      assert.equal(
        await browser.evaluate("[...document.querySelectorAll('section')].some((section) => section.textContent?.includes('Online nodes') && section.textContent?.includes('1/6'))"),
        true,
        "unknown, removed alias, malformed, degraded and assigned rows must be excluded from the healthy numerator",
      );

      await browser.setMediaFeatures([{ name: "forced-colors", value: "active" }]);
      const forcedUnknown = await waitForWorkerStatus(browser, "Future Worker", (value) => value.text.includes("Unknown status"));
      assert.deepEqual(
        { known: forcedUnknown.known, tone: forcedUnknown.tone, icon: forcedUnknown.icon },
        { known: "false", tone: "unknown", icon: "circle-help" },
        "forced colors must preserve text, icon and semantic tone",
      );

      await browser.setMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
      await browser.evaluate("document.querySelector('button[aria-label=\"Show configuration\"]')?.focus(); true");
      const reduced = await waitForWorkerStatus(browser, "Future Worker", (value) => value.text.includes("Unknown status"));
      assert.equal(reduced.transitionDuration, "0s");
      assert.equal(await browser.evaluate("document.activeElement?.getAttribute('aria-label')"), "Show configuration");

      await setStoredDisplay(browser, "ja", "light");
      await browser.reload();
      await waitForShell(browser, "アカウントメニュー");
      await waitForWorkerAction(browser, "Configuration を表示", "Worker One", (value) => value.disabled === false);
      const japaneseUnknown = await waitForWorkerStatus(browser, "Future Worker", (value) => value.text.includes("不明な状態"));
      assert.equal(japaneseUnknown.rowText.includes("future_online_v2"), false);
      const japaneseAssigned = await waitForWorkerStatus(browser, "Assigned Worker", (value) => value.text.includes("割り当て済み"));
      assert.equal(japaneseAssigned.text.includes("正常"), false);
      assertNoBrowserConsoleErrors(browser.consoleErrorCount);
    } finally {
      authRefreshRelease?.resolve();
      configurationRefreshRelease?.resolve();
      fixture.authResponse = { body: currentUser };
      fixture.healthResponse = { body: healthyRows };
      fixture.workersResponse = { body: workerPilotRows };
      fixture.nodesResponse = { body: workerPilotRows };
      fixture.configurationResponse = { body: workerConfiguration("worker-one", "BROWSER-CONFIG-MARKER") };
      fixture.workerConfigurationMethods = [];
      await browser.setMediaFeatures([]).catch(() => {});
    }
  });
}
