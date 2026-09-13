import test from "node:test";
import { BrowserHarness, ensureWebServer } from "./helpers/browser-harness.mts";
import { webRoot, requestedBaseUrl, createBrowserRouteFixture, currentUser } from "./ui-browser-fixture.mts";
import { runQueryStatesScenario, runLogoutScenario, runSessionExpiryScenario, runAuthMeExpiryScenario, runRefreshGenerationScenario, runSetupUnmountScenario, runLoginReturnScenario } from "./ui-browser-query-auth-scenarios.mts";
import { runStartReadinessScenario } from "./ui-browser-start-readiness-scenarios.mts";
import { runWorkerRestartScenario } from "./ui-browser-worker-restart-scenarios.mts";
import { runWorkerConfigurationScenario } from "./ui-browser-worker-configuration-scenarios.mts";
import { runNavigationParityScenario, runNavigationCreateFocusScenario, runReducedMotionScenario, runDisplayControlsScenario } from "./ui-browser-navigation-scenarios.mts";
import { runAccountAppearanceScenario } from "./ui-browser-account-scenarios.mts";
import { runVisualSettingsScenario } from "./ui-browser-visual-settings-scenarios.mts";
import { runResponsiveSurfacesScenario, runStatusFocusScenario, runFalsePositiveGuardsScenario } from "./ui-browser-observation-scenarios.mts";

test("UI Foundation runtime behavior", { timeout: 420_000 }, async (t) => {
  const server = await ensureWebServer(webRoot, requestedBaseUrl);
  const browserPromise = BrowserHarness.launch();
  t.after(async () => {
    const browserForCleanup = await browserPromise.catch(() => undefined);
    await browserForCleanup?.close();
    await server.close();
  });
  const browser = await browserPromise;
  const fixture = createBrowserRouteFixture(browser);
  await runQueryStatesScenario(t, browser, server, fixture);
  await runLogoutScenario(t, browser, server, fixture);
  await runSessionExpiryScenario(t, browser, server, fixture);
  await runAuthMeExpiryScenario(t, browser, server, fixture);
  await runRefreshGenerationScenario(t, browser, server, fixture);
  await runSetupUnmountScenario(t, browser, server, fixture);
  await runLoginReturnScenario(t, browser, server, fixture);
  await runStartReadinessScenario(t, browser, server, fixture);
  await runWorkerRestartScenario(t, browser, server, fixture);
  await runWorkerConfigurationScenario(t, browser, server, fixture);
  await runNavigationParityScenario(t, browser, server, fixture);
  await runNavigationCreateFocusScenario(t, browser, server, fixture);
  await runReducedMotionScenario(t, browser, server, fixture);
  await runDisplayControlsScenario(t, browser, server, fixture);
  await runAccountAppearanceScenario(t, browser, server, fixture);
  await runVisualSettingsScenario(t, browser, server, fixture);
  await runResponsiveSurfacesScenario(t, browser, server, fixture);
  await runStatusFocusScenario(t, browser, fixture);
  await runFalsePositiveGuardsScenario(t, server);

  fixture.authResponse = { body: currentUser };
});
