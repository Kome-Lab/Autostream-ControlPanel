import assert from "node:assert/strict";
import { clickVisible } from "../ui-regression/visible-trigger.mts";
import { withTimeout } from "./browser-process-timing.mts";
import type { RelatedBrowserContext } from "./polling-related-browser-context.mts";
import { confirmRelatedAction, waitRenderedMutation } from "./polling-workers-app-browser-cases.mts";

const updaterID = "updater-related";
const updaterPath = `/system-updates/updaters/${updaterID}/settings`;
const interval = '[role=dialog] input[id$="-poll-interval"]';
const nodePath = "/nodes/node-related";
const nodeName = '[role=dialog] fieldset > div:nth-child(2) input';
const stamp = "a".repeat(64);

export const relatedUpdaterNodesCaseNames = [
  "Updater draft survives three completed query cycles and slow bounded submit",
  "Updater true revision conflict preserves draft and prevents stale save",
  "Updater known validation rejection permits explicit corrected save",
  "Updater unknown save remains unresolved through a successful read",
  "Legacy Nodes draft survives three polls and one slow save intent",
  "Legacy Nodes actual metadata conflict retains input without PUT",
  "Legacy Nodes known rejection permits explicit correction",
  "Legacy Nodes unknown write stays latched after successful GET",
] as const;

async function updaterFixture(ctx: RelatedBrowserContext) {
  let settings = {
    updater_id: updaterID, revision: 9, projection_revision: 9, local_executor_policy_revision: 6,
    transport_mode: "pull_v2", execution_host_id: "host-related", local_executor_policy_sha256: `sha256:${"a".repeat(64)}`,
    execution_host_ownership: { transport_mode: "pull_v2", agent_service_id: updaterID, ownership_epoch: 5, policy_revision: 9 },
    api: { bind_host: "127.0.0.1", host: "127.0.0.1", port: 8090, ssl_enabled: false },
    poll_interval_seconds: 15, heartbeat_interval_seconds: 30, hosts: [],
    targets: [{ target_id: "worker-related", service_id: "worker-related", host_id: "host-related", service_type: "worker", deployment_mode: "systemd", local_listen_port: 8084 }],
    github_token_configured: false, updated_at: "2026-10-28T01:00:00Z",
  };
  let observations = 0;
  ctx.fixture.handle("GET", "/system-updates", () => ({ body: {
    updaters: [{ updater_id: updaterID, name: "Related Host Agent", status: "online", online: true, transport_mode: "pull_v2", execution_host_id: "host-related", ownership_epoch: 5, desired_revision: settings.revision, applied_revision: 9, policy_status: "applied", last_heartbeat_at: `2026-10-28T01:00:${String(observations++ % 60).padStart(2, "0")}Z` }],
    hosts: [{ host_id: "host-related", name: "Related host", updater_id: updaterID, reachability: "reachable" }],
    targets: [{ target_id: "worker-related", target_type: "worker", name: "Related worker", host_id: "host-related", deployment_mode: "systemd", updater_id: updaterID, updater_online: true, current_version: "v1.0.0", latest_version: "v1.0.0", eligible: false }], jobs: [],
  } }));
  ctx.fixture.handle("GET", updaterPath, () => ({ body: structuredClone(settings) }));
  ctx.fixture.set("GET", `/system-updates/updaters/${updaterID}/bootstrap-jobs`, { body: { updater_id: updaterID, jobs: [] } });
  const accept = (postData?: string) => {
    const body = JSON.parse(postData || "null");
    assert.equal(body.expected_revision, settings.revision);
    settings = { ...settings, ...body, revision: settings.revision + 1 };
    return { body: structuredClone(settings) };
  };
  ctx.fixture.handle("PUT", updaterPath, (request) => accept(request.postData));
  await ctx.navigate("/admin/application/", '[data-screen-family="application"]');
  await clickVisible(ctx.browser, 'button[aria-label="Settings for Related Host Agent"]');
  await ctx.browser.waitFor(`!!document.querySelector(${JSON.stringify(interval)})`, Boolean, "actual Updater runtime form missing");
  return {
    change: () => { settings = { ...settings, revision: 10, poll_interval_seconds: 35 }; },
    accept,
  };
}

async function nodesFixture(ctx: RelatedBrowserContext) {
  let node = { id: "node-related", service_id: "node-related", service_type: "worker", service_name: "Related Node", description: "Original description", status: "online", host: "node.example.test", port: 8084, ssl_enabled: true, metadata_revision: stamp, transport_mode: "direct_v1" };
  let observation = 0;
  ctx.fixture.handle("GET", "/nodes", () => ({ body: [{ ...node, last_heartbeat_at: `2026-10-28T01:00:${String(observation++ % 60).padStart(2, "0")}Z` }] }));
  const accept = (postData?: string) => {
    const body = JSON.parse(postData || "null");
    assert.equal(body.expected_metadata_revision, node.metadata_revision, "actual Legacy save must retain the original draft stamp");
    node = { ...node, ...body, metadata_revision: "c".repeat(64) };
    return { body: structuredClone(node) };
  };
  ctx.fixture.handle("PUT", nodePath, (request) => accept(request.postData));
  await ctx.navigate("/admin/registered-nodes/", '[data-screen-family="registered-nodes"]');
  await clickVisible(ctx.browser, 'button[aria-label="Edit node"]');
  await ctx.browser.waitFor(`!!document.querySelector(${JSON.stringify(nodeName)})`, Boolean, "actual Legacy Nodes edit dialog missing");
  return { change: () => { node = { ...node, service_name: "Other operator", metadata_revision: "b".repeat(64) }; }, accept };
}

export async function runRelatedUpdaterNodesScenarios(ctx: RelatedBrowserContext) {
  await ctx.check(relatedUpdaterNodesCaseNames[0], async () => {
    await updaterFixture(ctx);
    await ctx.fill(interval, "25");
    await ctx.browser.evaluate(`document.querySelector(${JSON.stringify(interval)}).focus();true`);
    await ctx.completeCycles(["/auth/me", "/system-updates"], 3);
    assert.equal(await ctx.readValue(interval), "25");
    assert.equal(await ctx.browser.evaluate(`document.activeElement===document.querySelector(${JSON.stringify(interval)})`), true);
    await ctx.operateAt(390, () => ctx.click("Save settings", "[role=dialog]"));
    const held = ctx.fixture.gateNext("GET", updaterPath);
    try {
      await ctx.operateAt(320, () => confirmRelatedAction(ctx));
      await withTimeout(held.entered, 10_000, "Updater submit did not execute bounded real settings GET");
      assert.equal(ctx.fixture.count("PUT", updaterPath), 0);
      assert.equal(await ctx.readValue(interval), "25");
      await ctx.capture("updater-slow-preflight-draft", "[role=alertdialog]");
    } finally { held.release(); }
    await waitRenderedMutation(ctx, "PUT", updaterPath, "!document.querySelector('[role=alertdialog]') && document.querySelector('[role=dialog]')?.textContent.includes('Settings saved.')", "the actual Updater settings save must render its successful receipt");
    assert.equal(ctx.fixture.count("PUT", updaterPath), 1);
    assert.equal(await ctx.readValue(interval), "25");
    await ctx.capture("updater-saved-draft", interval);
  });
  await ctx.check(relatedUpdaterNodesCaseNames[1], async () => {
    const state = await updaterFixture(ctx);
    await ctx.fill(interval, "25");
    await ctx.click("Save settings", "[role=dialog]");
    state.change();
    await confirmRelatedAction(ctx);
    await ctx.browser.waitFor("!!document.querySelector('[role=alertdialog] [role=alert]')", Boolean, "Updater fresh revision conflict was not shown");
    assert.equal(ctx.fixture.count("PUT", updaterPath), 0);
    assert.equal(await ctx.readValue(interval), "25");
    await ctx.capture("updater-real-revision-conflict", "[role=alertdialog]");
  });
  await ctx.check(relatedUpdaterNodesCaseNames[2], async () => {
    const state = await updaterFixture(ctx);
    await ctx.fill(interval, "25");
    ctx.fixture.set("PUT", updaterPath, { status: 400, body: { code: "invalid_updater_settings", message: "Synthetic validation before write" } });
    await ctx.click("Save settings", "[role=dialog]");
    await confirmRelatedAction(ctx);
    await waitRenderedMutation(ctx, "PUT", updaterPath, "!!document.querySelector('[role=alertdialog] [role=alert]')", "Updater validation result did not reach the actual dialog");
    await ctx.click("Cancel", "[role=alertdialog]");
    await ctx.fill(interval, "30");
    ctx.fixture.handle("PUT", updaterPath, (request) => state.accept(request.postData));
    await ctx.click("Save settings", "[role=dialog]");
    await confirmRelatedAction(ctx);
    await waitRenderedMutation(ctx, "PUT", updaterPath, "!document.querySelector('[role=alertdialog]') && document.querySelector('[role=dialog]')?.textContent.includes('Settings saved.')", "the explicit corrected Updater save must render its successful receipt");
    assert.equal(ctx.fixture.count("PUT", updaterPath), 2);
    await ctx.capture("updater-known-validation-corrected", interval);
  });
  await ctx.check(relatedUpdaterNodesCaseNames[3], async () => {
    await updaterFixture(ctx);
    await ctx.fill(interval, "25");
    ctx.fixture.set("PUT", updaterPath, { status: 503, body: { code: "save_outcome_unavailable", message: "Synthetic ambiguous result" } });
    await ctx.click("Save settings", "[role=dialog]");
    await confirmRelatedAction(ctx);
    await waitRenderedMutation(ctx, "PUT", updaterPath, "!!document.querySelector('[role=alertdialog]') && !document.querySelector('[role=alertdialog] [data-confirm-action]')", "unknown Updater outcome must stop resend");
    await ctx.click("Cancel", "[role=alertdialog]");
    await ctx.completeCycles(["/auth/me", "/system-updates"], 3);
    await ctx.click("Save settings", "[role=dialog]");
    await confirmRelatedAction(ctx);
    await ctx.browser.waitFor("document.querySelector('[role=alertdialog]')?.textContent || ''", (text: string) => /could not|refresh|verify|check/i.test(text), "unresolved Updater save must be presented");
    assert.equal(ctx.fixture.count("PUT", updaterPath), 1);
    assert.equal(await ctx.readValue(interval), "25");
    await ctx.capture("updater-unknown-put-no-retry", "[role=alertdialog]");
  });
  await ctx.check(relatedUpdaterNodesCaseNames[4], async () => {
    await nodesFixture(ctx);
    await ctx.fill(nodeName, "Retained Legacy Node draft");
    await ctx.browser.evaluate(`document.querySelector(${JSON.stringify(nodeName)}).focus();true`);
    await ctx.completeCycles(["/auth/me", "/nodes"], 3);
    assert.equal(await ctx.readValue(nodeName), "Retained Legacy Node draft");
    assert.equal(await ctx.browser.evaluate(`document.activeElement===document.querySelector(${JSON.stringify(nodeName)})`), true);
    const held = ctx.fixture.gateNext("GET", "/nodes");
    try {
      await ctx.operateAt(390, () => ctx.click("Save", "[role=dialog]"));
      await withTimeout(held.entered, 10_000, "Legacy Node preflight did not read actual nodes");
      assert.equal(ctx.fixture.count("PUT", nodePath), 0);
      assert.equal(await ctx.readValue(nodeName), "Retained Legacy Node draft");
      assert.equal(await ctx.browser.evaluate("document.querySelector('[role=dialog] fieldset')?.disabled"), true);
      await ctx.capture("nodes-slow-save-retained-draft", "[role=dialog]");
    } finally { held.release(); }
    await waitRenderedMutation(ctx, "PUT", nodePath, "!document.querySelector('[role=dialog]') && document.querySelector('[data-screen-family=registered-nodes]')?.textContent.includes('Retained Legacy Node draft')", "the actual Legacy Node save must close the editor and render its saved metadata");
    assert.equal(ctx.fixture.count("PUT", nodePath), 1);
    await ctx.capture("nodes-saved-metadata");
  });
  await ctx.check(relatedUpdaterNodesCaseNames[5], async () => {
    const state = await nodesFixture(ctx);
    await ctx.fill(nodeName, "Keep conflicting Node input");
    state.change();
    await ctx.operateAt(320, () => ctx.click("Save", "[role=dialog]"));
    await ctx.browser.waitFor("document.querySelector('[role=dialog] [role=alert]')?.textContent || ''", (text: string) => /changed/i.test(text), "Node true metadata conflict not shown");
    assert.equal(ctx.fixture.count("PUT", nodePath), 0);
    assert.equal(await ctx.readValue(nodeName), "Keep conflicting Node input");
    await ctx.capture("nodes-real-metadata-conflict", "[role=dialog]");
  });
  await ctx.check(relatedUpdaterNodesCaseNames[6], async () => {
    const state = await nodesFixture(ctx);
    await ctx.fill(nodeName, "Initially rejected Node input");
    ctx.fixture.set("PUT", nodePath, { status: 400, body: { code: "invalid_node_registration", message: "Synthetic validation before metadata update" } });
    await ctx.click("Save", "[role=dialog]");
    await waitRenderedMutation(ctx, "PUT", nodePath, "!!document.querySelector('[role=dialog] [role=alert]') && !document.querySelector('[role=dialog] fieldset')?.disabled", "Node known validation rejection did not retain editable draft");
    await ctx.fill(nodeName, "Explicit corrected Node input");
    ctx.fixture.handle("PUT", nodePath, (request) => state.accept(request.postData));
    await ctx.click("Save", "[role=dialog]");
    await waitRenderedMutation(ctx, "PUT", nodePath, "!document.querySelector('[role=dialog]') && document.querySelector('[data-screen-family=registered-nodes]')?.textContent.includes('Explicit corrected Node input')", "the corrected Node save must render its saved metadata");
    assert.equal(ctx.fixture.count("PUT", nodePath), 2);
    await ctx.capture("nodes-known-rejection-corrected");
  });
  await ctx.check(relatedUpdaterNodesCaseNames[7], async () => {
    await nodesFixture(ctx);
    await ctx.fill(nodeName, "Unknown Node write input");
    ctx.fixture.set("PUT", nodePath, { status: 503, body: { code: "node_update_unavailable", message: "Synthetic ambiguous result" } });
    await ctx.click("Save", "[role=dialog]");
    await waitRenderedMutation(ctx, "PUT", nodePath, "/unknown|resubmission/i.test(document.querySelector('[role=dialog] [role=alert]')?.textContent || '')", "Node unknown outcome not retained");
    await ctx.completeCycles(["/auth/me", "/nodes"], 3);
    await ctx.click("Save", "[role=dialog]");
    await ctx.browser.evaluate("new Promise(done=>requestAnimationFrame(()=>requestAnimationFrame(()=>done(true))))");
    await ctx.browser.waitForRequestHandlersIdle({ pathname: "/nodes", method: "GET" });
    await ctx.browser.waitFor("document.querySelector('[role=dialog] [role=alert]')?.textContent || ''", (text: string) => /unknown|resubmission/i.test(text), "Node unknown latch must survive GET");
    assert.equal(ctx.fixture.count("PUT", nodePath), 1);
    assert.equal(await ctx.readValue(nodeName), "Unknown Node write input");
    await ctx.capture("nodes-unknown-put-no-retry", "[role=dialog]");
  });
}
