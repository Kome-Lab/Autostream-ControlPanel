import assert from "node:assert/strict";
import test from "node:test";
import { setTimeout as delay } from "node:timers/promises";

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
  for (const expected of stPortAcceptedGetCases) {
    await t.test(`ST-PORT actual CP GET ${expected.scenario_id} renders ${expected.result}`, async (scenario) => {
      await withStPortGetBrowser(async (browser, baseUrl) => {
        await browser.setViewport(1440, 1000);
        const entry = artifact.cases.find((value) => value.scenario_id === expected.scenario_id)!;
        await browser.waitForRequestHandlersIdle();
        browser.clearRequestCounts("/system-updates");
        browser.clearConsoleErrors();
        const versionRequestBaseline = browser.requests.get("/version") || 0;
        const versionResponseBaseline = browser.responses.get("/version") || 0;
        const versionStatusBaseline = browser.responseStatuses.get("/version")?.length || 0;
        const requests = installStPortGetRoutes(browser, entry);
        await browser.navigate(`${baseUrl}/admin/application/?st_port_case=${expected.scenario_id}`);
        const firstResponseDeadline = Date.now() + 45_000;
        while (((browser.responses.get("/system-updates") || 0) < 1
          || (browser.responses.get("/version") || 0) <= versionResponseBaseline) && Date.now() < firstResponseDeadline) {
          browser.assertNoFatalError();
          await delay(20);
        }
        browser.assertNoFatalError();
        assert.ok((browser.responses.get("/system-updates") || 0) >= 1, "actual CP GET response must settle within 45 seconds");
        assert.ok((browser.responses.get("/version") || 0) > versionResponseBaseline, "this case must acknowledge a new required VERSION response");
        await browser.waitFor<AcceptedJobDOM>(
          acceptedJobDOMExpression(entry),
          acceptedJobDOMMatches,
          `Production history did not render the actual CP ${expected.result} result`,
          45_000,
        );
        await browser.waitForRequestHandlersIdle({ pathname: "/system-updates", method: "GET" });
        await browser.waitFor(`(() => {
          const sections = [...document.querySelectorAll('#application-overview')];
          if (sections.length !== 1) return false;
          const values = label => [...sections[0].querySelectorAll('dl')]
            .filter(item => item.querySelector('dt')?.textContent === label)
            .map(item => item.querySelector('dd')?.textContent);
          const version = values('バージョン'), commit = values('コミット');
          return version.length === 1 && version[0] === '2.0.0'
            && commit.length === 1 && commit[0] === 'st-port-brow';
        })()`, Boolean, "production overview must render this fixture VERSION body", 45_000);
        await browser.waitForRequestHandlersIdle();
        const versionRequests = (browser.requests.get("/version") || 0) - versionRequestBaseline;
        const versionResponses = (browser.responses.get("/version") || 0) - versionResponseBaseline;
        const versionStatuses = (browser.responseStatuses.get("/version") || []).slice(versionStatusBaseline);
        assert.ok(versionRequests >= 1, "this case must issue a required VERSION GET");
        assert.equal(versionResponses, versionRequests, "all required VERSION requests must receive their acknowledgement before cleanup");
        assert.equal(versionStatuses.length, versionResponses, "VERSION acknowledgements must have observed statuses");
        assert.ok(versionStatuses.every(status => status === 200), "required VERSION response status changed");
        assert.ok((browser.requests.get("/system-updates") || 0) >= 1, "production query must consume the actual GET response");
        assert.ok((browser.responses.get("/system-updates") || 0) >= 1, "production query must complete an actual GET response");
        assert.ok((browser.responseStatuses.get("/system-updates")?.length || 0) >= 1, "actual GET response status must be observed");
        assert.ok(browser.responseStatuses.get("/system-updates")?.every((status) => status === 200), "GET response status changed");
        assert.equal(requests.portMutationCount(), 0, "viewing accepted results must not mutate jobs");
        assert.equal(browser.consoleErrorCount, 0, "production view must not emit console errors");
        browser.assertNoFatalError();
      }, (cleanup) => scenario.after(cleanup));
    });
  }
});
