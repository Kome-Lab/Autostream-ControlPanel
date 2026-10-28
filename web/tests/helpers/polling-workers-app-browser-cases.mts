import assert from "node:assert/strict";
import { clickVisible, clickDisabledVisible, clickWorkerRestart, VisibleTriggerCleanupFailure } from "../ui-regression/visible-trigger.mts";
import { withTimeout } from "./browser-process-timing.mts";
import type { RelatedBrowserContext } from "./polling-related-browser-context.mts";
import { waitForAnimationFrames } from "../ui-browser-navigation-helpers.mts";

export const relatedWorkersAppCaseNames = [
  "Workers configuration survives three completed reads and a slow same-target refresh",
  "Workers restart uses one intent despite a concurrent auth read",
  "Workers fresh permission revocation prevents restart",
  "APP draft and initial stamp survive three reads, focus and a slow save preflight",
  "APP true configuration change retains the draft and prevents stale write",
  "APP known non-applied rejection permits explicit corrected test-email operation",
  "APP unknown test-email response stays latched after successful GET cycles",
] as const;

export async function confirmRelatedAction(ctx: RelatedBrowserContext) {
  await ctx.browser.waitFor("document.querySelectorAll('[role=alertdialog] [data-confirm-action]').length", (count) => count === 1, "one actual confirmation action must be visible");
  await clickVisible(ctx.browser, "[role=alertdialog] [data-confirm-action]");
}

export async function waitRenderedMutation(ctx: RelatedBrowserContext, method: "POST" | "PUT", path: string, expression: string, description: string) {
  // The rendered terminal receipt follows the client's JSON read and action
  // result handling. The method filter excludes setup and post-save GETs at the
  // same path; the caller still asserts the exact number of mutation intents.
  await ctx.browser.waitFor(expression, Boolean, description);
  await ctx.browser.waitForRequestHandlersIdle({ pathname: path, method });
}

async function showWorkerOneConfiguration(ctx: RelatedBrowserContext) {
  await ctx.browser.waitFor(`(() => {
    if(document.querySelector('[data-related-worker-row]'))throw Error('related Worker row marker is already owned');
    const rows=[...document.querySelectorAll('main [data-screen-family=workers] tbody tr')].filter(row=>
      row.querySelector('td[headers$="-service_name"] .font-medium')?.textContent.trim()==='Worker One'&&
      row.querySelector('td[headers$="-service_type"] > div')?.textContent.trim()==='Worker');
    if(rows.length>1)throw Error('ambiguous related Worker One identity');
    if(rows.length!==1)return false;
    rows[0].setAttribute('data-related-worker-row','');globalThis.__relatedWorkerRow=rows[0];return true;
  })()`, Boolean, "the exact Worker One name and Worker type must identify one actual row");
  let bodyFailed = false, primary: unknown;
  try {
    await clickVisible(ctx.browser, '[data-related-worker-row] button[aria-label="Show configuration"]');
  } catch (error) { bodyFailed = true; primary = error; throw error; }
  finally {
    try { await ctx.browser.evaluate("globalThis.__relatedWorkerRow?.removeAttribute('data-related-worker-row');delete globalThis.__relatedWorkerRow;true"); }
    catch (cleanup) { throw bodyFailed ? new VisibleTriggerCleanupFailure("click-target", primary, cleanup) : cleanup; }
  }
}

async function appFixture(ctx: RelatedBrowserContext) {
  let settings: Record<string, unknown> = {
    app_name: "Related application", timezone: "Asia/Tokyo", updated_at: "2026-10-28T01:00:00Z",
    smtp_enabled: true, smtp_host: "smtp.example.test", smtp_port: 587, smtp_starttls: true,
    smtp_from: "operator@example.test", smtp_username: "", smtp_password_configured: true,
    turnstile_enabled: false, turnstile_site_key: "", turnstile_configured: false,
    google_analytics_enabled: false, google_analytics_measurement_id: "",
  };
  ctx.fixture.handle("GET", "/settings/app/manage", () => ({ body: structuredClone(settings) }));
  ctx.fixture.set("GET", "/settings/app", { body: { app_name: "Related application", timezone: "Asia/Tokyo" } });
  ctx.fixture.handle("PUT", "/settings/app", (request) => {
    const body = JSON.parse(request.postData || "null");
    assert.equal(body.expected_updated_at, settings.updated_at, "actual UI must carry its original draft stamp");
    settings = { ...settings, ...body, updated_at: "2026-10-28T01:00:01Z" };
    delete settings.expected_updated_at;
    return { body: structuredClone(settings) };
  });
  await ctx.navigate("/admin/settings/", "#app-name");
  return { change: () => { settings = { ...settings, app_name: "Another operator", updated_at: "2026-10-28T01:00:02Z" }; } };
}

async function threeAppRefreshes(ctx: RelatedBrowserContext, name: string, expected: string) {
  await ctx.browser.evaluate("globalThis.__relatedAppInput=document.querySelector('#app-name');true");
  for (let cycle = 0; cycle < 3; cycle++) {
    await ctx.browser.waitForRequestHandlersIdle({ pathname: "/settings/app/manage", method: "GET" });
    const count = ctx.browser.responses.get("/settings/app/manage") || 0;
    const held = ctx.fixture.gateNext("GET", "/settings/app/manage");
    try {
      await clickVisible(ctx.browser, 'button[aria-describedby="app-settings-remote-state"]');
      await withTimeout(held.entered, 10_000, "actual APP Refresh button did not start its settings query");
      await clickVisible(ctx.browser, "#app-name");
      assert.equal(await ctx.readValue("#app-name"), expected);
    } finally { held.release(); }
    await ctx.browser.waitForResponseCount("/settings/app/manage", count + 1);
    await ctx.browser.waitForRequestHandlersIdle({ pathname: "/settings/app/manage", method: "GET" });
    assert.equal(await ctx.browser.evaluate(`globalThis.__relatedAppInput===document.querySelector('#app-name') && document.activeElement===globalThis.__relatedAppInput && globalThis.__relatedAppInput.value===${JSON.stringify(expected)}`), true);
  }
  ctx.completedCycles.push({ name, paths: ["/settings/app/manage"], cycles: 3, trigger: "actual APP Refresh button; held GET, native field focus, completed response and handler-idle barriers" });
}

export async function runRelatedWorkersAppScenarios(ctx: RelatedBrowserContext) {
  await ctx.check(relatedWorkersAppCaseNames[0], async () => {
    const configPath = "/nodes/worker-one/configuration";
    ctx.fixture.set("GET", configPath, { body: { node: { id: "worker-one", service_id: "worker-one", service_type: "worker", service_name: "Worker One", status: "online" }, configuration_yaml: "synthetic_nonsecret_configuration: retained", node_api_url: "https://worker.example.test", configure_command: "synthetic configure", systemd_unit: "[Unit]" } });
    await ctx.navigate("/admin/workers/", '[data-screen-family="workers"]');
    await showWorkerOneConfiguration(ctx);
    await ctx.browser.waitFor("document.body.textContent.includes('synthetic_nonsecret_configuration: retained')", Boolean, "actual Configuration content missing");
    await ctx.completeCycles(["/auth/me", "/workers"], 3);
    const held = ctx.fixture.gateNext("GET", configPath);
    try {
      await ctx.operateAt(320, () => ctx.click("Reload configuration"));
      await withTimeout(held.entered, 10_000, "real Configuration refresh did not start");
      assert.equal(await ctx.browser.evaluate("document.body.textContent.includes('synthetic_nonsecret_configuration: retained')"), true);
      await ctx.focusCycle(["/auth/me", "/workers"]);
      await ctx.capture("workers-cached-configuration-slow", "main pre");
    } finally { held.release(); }
    await ctx.browser.waitForRequestHandlersIdle({ pathname: configPath, method: "GET" });
    assert.equal(ctx.fixture.count("POST", "/workers/worker-one/restart"), 0);
  });
  await ctx.check(relatedWorkersAppCaseNames[1], async () => {
    await ctx.navigate("/admin/workers/", '[data-screen-family="workers"]');
    await clickWorkerRestart(ctx.browser, { id: "worker-one", type: "worker", name: "Worker One" });
    await ctx.completeCycles(["/auth/me", "/workers"], 3);
    const held = ctx.fixture.gateNext("GET", "/auth/me");
    await ctx.browser.evaluate("document.dispatchEvent(new Event('visibilitychange',{bubbles:true}));true");
    try {
      await withTimeout(held.entered, 10_000, "background auth read did not enter");
      await ctx.operateAt(390, () => confirmRelatedAction(ctx));
      await waitRenderedMutation(ctx, "POST", "/workers/worker-one/restart", "!document.querySelector('[role=alertdialog]') && document.body.textContent.includes('The worker restart request was accepted.')", "the real Worker accepted-restart receipt must render");
      assert.equal(ctx.fixture.count("POST", "/workers/worker-one/restart"), 1);
      await ctx.capture("workers-restart-one-intent");
    } finally { held.release(); }
  });
  await ctx.check(relatedWorkersAppCaseNames[2], async () => {
    await ctx.navigate("/admin/workers/", '[data-screen-family="workers"]');
    await clickWorkerRestart(ctx.browser, { id: "worker-one", type: "worker", name: "Worker One" });
    ctx.fixture.set("GET", "/auth/me", { body: { user: { id: "related-user", username: "operator", roles: [] }, permissions: ["workers.read"] } });
    const authBefore = ctx.fixture.count("GET", "/auth/me");
    await confirmRelatedAction(ctx);
    await ctx.browser.waitFor("!!document.querySelector('[role=alertdialog] [role=alert]')", Boolean, "fresh denial was not presented");
    await ctx.browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
    assert.ok(ctx.fixture.count("GET", "/auth/me") >= authBefore + 1, "the denial follows an actual fresh permission read");
    assert.equal(ctx.fixture.count("POST", "/workers/worker-one/restart"), 0);
    await ctx.capture("workers-permission-revoked");
  });
  await ctx.check(relatedWorkersAppCaseNames[3], async () => {
    await appFixture(ctx);
    await ctx.fill("#app-name", "Draft retained through actual refetches");
    await ctx.browser.evaluate("document.querySelector('#app-name').focus();true");
    await threeAppRefreshes(ctx, relatedWorkersAppCaseNames[3], "Draft retained through actual refetches");
    await ctx.completeCycles(["/auth/me"], 3);
    assert.equal(await ctx.readValue("#app-name"), "Draft retained through actual refetches");
    assert.equal(await ctx.browser.evaluate("document.activeElement===document.querySelector('#app-name')"), true);
    const held = ctx.fixture.gateNext("GET", "/settings/app/manage");
    try {
      await ctx.operateAt(390, () => ctx.click("Save"));
      await withTimeout(held.entered, 10_000, "APP actual save preflight was not read");
      assert.equal(ctx.fixture.count("PUT", "/settings/app"), 0);
      assert.equal(await ctx.readValue("#app-name"), "Draft retained through actual refetches");
      await ctx.capture("app-slow-preflight-draft", "#app-name");
    } finally { held.release(); }
    await ctx.operateAt(320, () => confirmRelatedAction(ctx));
    await waitRenderedMutation(ctx, "PUT", "/settings/app", "!document.querySelector('[role=alertdialog]') && document.body.textContent.includes('Saved.')", "the actual APP save must render its successful receipt");
    assert.equal(ctx.fixture.count("PUT", "/settings/app"), 1);
    assert.equal(await ctx.readValue("#app-name"), "Draft retained through actual refetches");
    await ctx.capture("app-saved-draft", "#app-name");
  });
  await ctx.check(relatedWorkersAppCaseNames[4], async () => {
    const saved = await appFixture(ctx);
    await ctx.fill("#app-name", "Keep my conflicting draft");
    saved.change();
    const before = ctx.fixture.count("GET", "/settings/app/manage");
    await ctx.click("Save");
    await ctx.browser.waitFor("document.body.textContent.includes('Settings have changed. Your draft has been kept.')", Boolean, "APP stale draft did not show conflict");
    await ctx.browser.waitForRequestHandlersIdle({ pathname: "/settings/app/manage", method: "GET" });
    assert.ok(ctx.fixture.count("GET", "/settings/app/manage") >= before + 1, "the conflict follows an actual fresh settings read");
    assert.equal(await ctx.readValue("#app-name"), "Keep my conflicting draft");
    assert.equal(ctx.fixture.count("PUT", "/settings/app"), 0);
    await ctx.capture("app-real-config-conflict", "#app-name");
  });
  await ctx.check(relatedWorkersAppCaseNames[5], async () => {
    await appFixture(ctx);
    ctx.fixture.set("POST", "/settings/app/test-email", { status: 400, body: { code: "invalid_email_recipient", message: "Rejected before Mailer.Send" } });
    await ctx.fill('main input[type="email"]', "invalid recipient");
    await ctx.click("Send test");
    await confirmRelatedAction(ctx);
    await waitRenderedMutation(ctx, "POST", "/settings/app/test-email", "!document.querySelector('[role=alertdialog]') && document.querySelector('main [data-slot=form-footer] [role=status]')?.textContent.trim()==='Review the entered values.'", "the known rejected email operation must render its actual main-form validation feedback");
    assert.equal(await ctx.readValue('main input[type="email"]'), "invalid recipient", "the definite rejection preserves the entered recipient");
    assert.equal(ctx.fixture.count("POST", "/settings/app/test-email"), 1, "the known rejection does not automatically retry");
    await ctx.capture("app-known-rejection-retained", 'main [data-slot="form-footer"]');
    await ctx.fill('main input[type="email"]', "corrected@example.test");
    ctx.fixture.set("POST", "/settings/app/test-email", { body: { status: "sent" } });
    await ctx.click("Send test");
    await confirmRelatedAction(ctx);
    await waitRenderedMutation(ctx, "POST", "/settings/app/test-email", "!document.querySelector('[role=alertdialog]') && document.body.textContent.includes('Test email sent.')", "the explicit corrected email operation must render its successful receipt");
    assert.equal(ctx.fixture.count("POST", "/settings/app/test-email"), 2);
    assert.deepEqual(ctx.fixture.requests.filter((request) => request.method === "POST" && request.path === "/settings/app/test-email").map((request) => request.body?.to), ["invalid recipient", "corrected@example.test"]);
    await ctx.capture("app-known-rejection-explicit-correction", 'main input[type="email"]');
  });
  await ctx.check(relatedWorkersAppCaseNames[6], async () => {
    await appFixture(ctx);
    const originalRecipient = await ctx.readValue('main input[type="email"]');
    assert.ok(originalRecipient);
    const unknownState = `(() => {
      const message=document.querySelector('main [data-slot=form-footer] [role=status]')?.textContent || '';
      const send=[...document.querySelectorAll('main button')].filter(button=>button.textContent.trim()==='Send test');
      return !document.querySelector('[role=alertdialog]')
        && message.includes('The result could not be confirmed.') && message.includes('Do not resend the action;')
        && send.length===1 && send[0] instanceof HTMLButtonElement && send[0].disabled;
    })()`;
    ctx.fixture.set("POST", "/settings/app/test-email", { status: 503, body: { code: "mailer_outcome_unavailable", message: "Synthetic ambiguous result" } });
    await ctx.click("Send test");
    await confirmRelatedAction(ctx);
    await waitRenderedMutation(ctx, "POST", "/settings/app/test-email", unknownState, "unknown APP response must render its no-resend warning and disable the actual Send test control");
    assert.equal(ctx.fixture.count("POST", "/settings/app/test-email"), 1);
    assert.equal(await ctx.readValue('main input[type="email"]'), originalRecipient);
    await ctx.capture("app-unknown-response-retained", 'main [data-slot="form-footer"]');
    await threeAppRefreshes(ctx, relatedWorkersAppCaseNames[6], "Related application");
    await ctx.completeCycles(["/auth/me"], 3);
    await ctx.browser.waitFor(unknownState, Boolean, "successful settings and permission GETs must retain the unknown-outcome warning and disabled Send test control");
    assert.equal(ctx.fixture.count("POST", "/settings/app/test-email"), 1, "the successful GET cycles must not resend the uncertain mail operation");
    await clickDisabledVisible(ctx.browser, 'main button:has(svg.lucide-send)');
    await waitForAnimationFrames(ctx.browser);
    await ctx.browser.waitForRequestHandlersIdle({ pathname: "/settings/app/test-email", method: "POST" });
    assert.equal(ctx.fixture.count("POST", "/settings/app/test-email"), 1, "a native pointer attempt on the disabled send control must issue zero additional POSTs");
    assert.equal(await ctx.readValue('main input[type="email"]'), originalRecipient);
    await ctx.capture("app-unknown-post-no-retry", 'main input[type="email"]');
  });
}
