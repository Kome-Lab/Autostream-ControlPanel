import assert from "node:assert/strict";
import test from "node:test";

import {
  acceptedJobDOMExpression,
  acceptedJobDOMMatches,
  installStPortGetRoutes,
  loadStPortGetBrowserArtifact,
  stPortAcceptedGetCases,
  stPortGetBrowserParentName,
  withStPortGetBrowser,
  type AcceptedJobDOM,
} from "./helpers/st-port-get-browser-harness.mts";

test(stPortGetBrowserParentName, { timeout: 300_000 }, async (t) => {
  // Missing actual CP output fails before launching any browser or web server.
  const artifact = loadStPortGetBrowserArtifact();
  await withStPortGetBrowser(async (browser, baseUrl) => {
    await browser.setViewport(1440, 1000);
    for (const expected of stPortAcceptedGetCases) {
      await t.test(`ST-PORT actual CP GET ${expected.scenario_id} renders ${expected.result}`, async () => {
        const entry = artifact.cases.find((value) => value.scenario_id === expected.scenario_id)!;
        await browser.waitForRequestHandlersIdle();
        browser.clearRequestCounts("/system-updates");
        browser.clearConsoleErrors();
        const requests = installStPortGetRoutes(browser, entry);
        await browser.navigate(`${baseUrl}/admin/application/?st_port_case=${expected.scenario_id}`);
        await browser.waitForResponseCount("/system-updates", 1, 45_000);
        await browser.waitFor<AcceptedJobDOM>(
          acceptedJobDOMExpression(entry),
          acceptedJobDOMMatches,
          `Production history did not render the actual CP ${expected.result} result`,
          45_000,
        );
        await browser.waitForRequestHandlersIdle({ pathname: "/system-updates", method: "GET" });
        assert.ok((browser.requests.get("/system-updates") || 0) >= 1, "production query must consume the actual GET response");
        assert.ok(browser.responseStatuses.get("/system-updates")?.every((status) => status === 200), "GET response status changed");
        assert.equal(requests.portMutationCount(), 0, "viewing accepted results must not mutate jobs");
        assert.equal(browser.consoleErrorCount, 0, "production view must not emit console errors");
        browser.assertNoFatalError();
      });
    }
  }, (cleanup) => t.after(cleanup));
});
