import assert from "node:assert/strict";
import { permissionUser } from "../ui-browser-fixture.mts";
import type { RelatedBrowserContext } from "./polling-related-browser-context.mts";
import {
  assertRetainedFocus, captureProof, click, confirmReadOnlyRejection, confirmWrite, discordPath,
  noConfirmation, openConfirmation, originalStamp, resourceName, resourceSubmit, secondStamp,
  threeSlowFocusCycles, waitText, watchInput,
} from "./polling-resource-browser-actions.mts";

export const relatedResourceCaseNames = [
  "Resource: three completed query cycles and a slow read preserve draft, Node selection and focus before one mobile Save",
  "Resource: a real edit conflict retains the original draft and sends zero PUTs",
  "Resource: fresh permission revocation retains the draft and sends zero POSTs",
  "Resource: a known nonapplied validation rejection permits only an explicit corrected retry",
  "Resource: an ambiguous POST failure remains latched after read completion and an explicit retry intent",
] as const;

const nodes = ["one", "two"].map((id) => ({ id: `poll-bot-${id}`, service_id: `poll-bot-${id}`, service_type: "discord_bot", service_name: `Polling BOT ${id === "one" ? "One" : "Two"}`, status: "online" }));
const existing = () => ({ id: "poll-existing-bot", name: "Existing BOT", config: { service_id: nodes[0].service_id }, updated_at: originalStamp });

async function prepare(ctx: RelatedBrowserContext) {
  const model = { rows: [existing()], reads: 0 };
  ctx.fixture.handle("GET", discordPath, () => ({ body: [...model.rows] }));
  ctx.fixture.set("GET", "/discord/target-presets", { body: [] });
  ctx.fixture.handle("GET", "/nodes", () => ({ body: ++model.reads % 2 ? [...nodes] : [...nodes].reverse() }));
  ctx.fixture.handle("POST", discordPath, (request) => {
    const payload = JSON.parse(request.postData || "null") as { name: string; service_id: string };
    assert.equal(payload.service_id, nodes[1].service_id);
    const saved = { ...existing(), id: "poll-created-bot", name: payload.name, config: { service_id: payload.service_id }, updated_at: secondStamp };
    model.rows.push(saved);
    return { status: 201, body: saved };
  });
  await ctx.navigate("/admin/discord/");
  await ctx.browser.waitFor("[...document.querySelectorAll('main button')].some(e=>e.textContent.trim()==='Create'&&!e.disabled)", Boolean, "actual Discord resource creation becomes available");
  return model;
}

async function createDraft(ctx: RelatedBrowserContext, name: string) {
  await ctx.click("Create");
  await ctx.browser.waitFor(`!!document.querySelector(${JSON.stringify(resourceName)})`, Boolean, "actual Discord create dialog mounts");
  await click(ctx, '[role="dialog"] [role="combobox"]');
  await ctx.browser.clickRoleWithText("option", "Polling BOT Two");
  await ctx.fill(resourceName, name);
  await watchInput(ctx, resourceName, name);
}

async function selectionRetained(ctx: RelatedBrowserContext) {
  assert.equal(await ctx.browser.evaluate("document.querySelector('[role=dialog] [role=combobox]')?.textContent.includes('Polling BOT Two')"), true, "the selected Node never follows reordered defaults");
}

async function draftReadyAfterFailure(ctx: RelatedBrowserContext) {
  await ctx.browser.waitFor(`(() => {const e=document.querySelector(${JSON.stringify(resourceSubmit)});return e instanceof HTMLButtonElement&&!e.disabled;})()`, Boolean, "the failed create returns control to the retained form");
}

export async function runRelatedResourceScenarios(ctx: RelatedBrowserContext) {
  await ctx.check(relatedResourceCaseNames[0], async () => {
    const model = await prepare(ctx), name = "Polling retained resource draft";
    await createDraft(ctx, name);
    await threeSlowFocusCycles(ctx, ["/auth/me", "/nodes"], resourceName, name);
    await selectionRetained(ctx);
    await assertRetainedFocus(ctx, resourceName, name);
    model.rows[0] = { ...existing(), name: "An unrelated existing BOT changed", updated_at: secondStamp };
    await captureProof(ctx, "resource-poll-retained", resourceName);
    await ctx.operateAt(320, async () => {
      await openConfirmation(ctx, resourceSubmit);
      await confirmWrite(ctx, "POST", discordPath);
      await ctx.browser.waitFor("document.querySelector('[role=dialog]')===null", Boolean, "one successful create closes its actual dialog");
    });
    assert.equal(ctx.fixture.count("POST", discordPath), 1);
    assert.equal(ctx.fixture.count("PUT", `${discordPath}/${existing().id}`), 0);
    const sent = ctx.fixture.requests.find((item) => item.method === "POST" && item.path === discordPath);
    assert.equal(sent?.body?.name, name);
    assert.equal(sent?.body?.service_id, nodes[1].service_id);
    await captureProof(ctx, "resource-poll-saved");
  });

  await ctx.check(relatedResourceCaseNames[1], async () => {
    const model = await prepare(ctx);
    await click(ctx, 'main button[aria-label="Edit Existing BOT"]');
    await ctx.browser.waitFor(`!!document.querySelector(${JSON.stringify(resourceName)})`, Boolean, "actual row edit dialog mounts");
    await ctx.fill(resourceName, "My retained edit");
    model.rows[0] = { ...existing(), name: "Another operator changed this BOT", updated_at: secondStamp };
    await click(ctx, resourceSubmit);
    await waitText(ctx, '[role="dialog"]', "The target changed, so the action was not sent.");
    await ctx.browser.waitForRequestHandlersIdle({ pathname: discordPath, method: "GET" });
    assert.equal(await ctx.readValue(resourceName), "My retained edit");
    assert.equal(ctx.fixture.count("PUT", `${discordPath}/${existing().id}`), 0);
    await captureProof(ctx, "resource-true-conflict", resourceName);
  });

  await ctx.check(relatedResourceCaseNames[2], async () => {
    await prepare(ctx);
    await createDraft(ctx, "Permission-retained draft");
    await openConfirmation(ctx, resourceSubmit);
    ctx.fixture.set("GET", "/auth/me", { body: permissionUser(["discord_configs.read", "nodes.read"]) });
    await confirmReadOnlyRejection(ctx, "You do not have permission to perform this action.");
    assert.equal(await ctx.readValue(resourceName), "Permission-retained draft");
    assert.equal(ctx.fixture.count("POST", discordPath), 0);
    await captureProof(ctx, "resource-permission-revoked", resourceName);
  });

  await ctx.check(relatedResourceCaseNames[3], async () => {
    await prepare(ctx);
    const success = { id: "poll-corrected-bot", name: "Corrected explicit draft", updated_at: secondStamp };
    ctx.fixture.handle("POST", discordPath, () => ctx.fixture.count("POST", discordPath) === 1
      ? { status: 400, body: { code: "invalid_discord_config" } } : { status: 201, body: success });
    await createDraft(ctx, "Rejected draft");
    await openConfirmation(ctx, resourceSubmit);
    await confirmWrite(ctx, "POST", discordPath);
    await draftReadyAfterFailure(ctx);
    assert.equal(ctx.fixture.count("POST", discordPath), 1, "no automatic resend after the early rejection");
    assert.equal(await ctx.readValue(resourceName), "Rejected draft");
    await ctx.fill(resourceName, success.name);
    await ctx.operateAt(390, async () => {
      await openConfirmation(ctx, resourceSubmit);
      await confirmWrite(ctx, "POST", discordPath);
      await ctx.browser.waitFor("document.querySelector('[role=dialog]')===null", Boolean, "the explicit corrected retry settles successfully");
    });
    assert.equal(ctx.fixture.count("POST", discordPath), 2);
    const retry = ctx.fixture.requests.filter((item) => item.method === "POST" && item.path === discordPath)[1];
    assert.equal(retry.body?.name, success.name);
    assert.equal(retry.body?.service_id, nodes[1].service_id);
    await captureProof(ctx, "resource-known-rejection-corrected");
  });

  await ctx.check(relatedResourceCaseNames[4], async () => {
    await prepare(ctx);
    ctx.fixture.set("POST", discordPath, { status: 500, body: { code: "temporarily_unavailable" } });
    await createDraft(ctx, "Unknown outcome retained draft");
    await openConfirmation(ctx, resourceSubmit);
    await confirmWrite(ctx, "POST", discordPath);
    await draftReadyAfterFailure(ctx);
    await waitText(ctx, '[role="dialog"]', "The result could not be confirmed.");
    await ctx.completeCycles(["/auth/me", "/nodes"], 3);
    assert.equal(ctx.fixture.count("POST", discordPath), 1);
    await click(ctx, resourceSubmit);
    await waitText(ctx, '[role="dialog"]', "Review the latest information before trying again.");
    await noConfirmation(ctx);
    assert.equal(await ctx.readValue(resourceName), "Unknown outcome retained draft");
    assert.equal(ctx.fixture.count("POST", discordPath), 1, "an explicit retry intent still cannot resend an uncertain write");
    await captureProof(ctx, "resource-unknown-outcome-retained", resourceName);
  });
}
