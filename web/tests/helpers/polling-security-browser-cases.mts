import assert from "node:assert/strict";
import { permissionUser } from "../ui-browser-fixture.mts";
import { withTimeout } from "./browser-process-timing.mts";
import type { RelatedBrowserContext } from "./polling-related-browser-context.mts";
import {
  assertRetainedFocus, captureProof, click, confirmReadOnlyRejection, confirmWrite, markSecurityInput,
  noConfirmation, openConfirmation, originalStamp, secondStamp, securityInput, securityPath, securitySubmit,
  thirdStamp, waitConfirmation, waitText, watchInput,
} from "./polling-resource-browser-actions.mts";

export const relatedSecurityCaseNames = [
  "Security: three real settings refreshes preserve the draft; submitted S is acknowledged while later T remains saveable with the new timestamp",
  "Security: a real same-setting revision conflict keeps input and sends zero PUTs",
  "Security: fresh permission revocation keeps input and sends zero PUTs",
  "Security: an ambiguous save remains latched after completed reads and never resends automatically",
] as const;

const policy = () => ({
  password_min_length: 12, password_hash: "argon2id", login_lockout_threshold: 5, session_idle_timeout_min: 30,
  session_absolute_lifetime_h: 12, remember_me_enabled: false, mfa_mode: "totp", mfa_required_roles: ["admin", "super_admin"],
  updated_at: originalStamp,
});

async function prepare(ctx: RelatedBrowserContext) {
  const model = { policy: policy(), observations: 0 };
  ctx.fixture.handle("GET", securityPath, () => ({ body: { ...model.policy, observed_at: ++model.observations } }));
  ctx.fixture.set("GET", "/secrets/status", { body: [] });
  ctx.fixture.handle("PUT", securityPath, (request) => {
    const body = JSON.parse(request.postData || "null") as Record<string, unknown>;
    if (body.expected_updated_at !== model.policy.updated_at) return { status: 409, body: { code: "revision_conflict" } };
    assert.equal(typeof body.password_min_length, "number");
    model.policy = { ...model.policy, password_min_length: body.password_min_length as number,
      updated_at: ctx.fixture.count("PUT", securityPath) === 1 ? secondStamp : thirdStamp };
    return { body: { ...model.policy } };
  });
  await ctx.navigate("/admin/security/");
  await markSecurityInput(ctx);
  return model;
}

async function threePolicyRefreshes(ctx: RelatedBrowserContext) {
  for (let cycle = 0; cycle < 3; cycle++) {
    const gate = ctx.fixture.gateNext("GET", securityPath), responses = ctx.browser.responses.get(securityPath) || 0;
    try {
      await ctx.click("Refresh", 'main [role="tabpanel"][data-state="active"]');
      await withTimeout(gate.entered, 8_000, "the actual policy Refresh button must start its query GET");
      await watchInput(ctx, securityInput, "16");
    } finally { gate.release(); }
    await ctx.browser.waitForResponseCount(securityPath, responses + 1);
    await ctx.browser.waitForRequestHandlersIdle({ pathname: securityPath, method: "GET" });
    await assertRetainedFocus(ctx, securityInput, "16");
  }
  ctx.completedCycles.push({ name: relatedSecurityCaseNames[0], paths: [securityPath], cycles: 3,
    trigger: "actual policy Refresh button; held GET, native field focus, response and handler-idle barriers" });
}

async function submittedThenLaterDraft(ctx: RelatedBrowserContext) {
  const gate = ctx.fixture.gateNext("GET", securityPath);
  try {
    await click(ctx, securitySubmit);
    await withTimeout(gate.entered, 8_000, "the Save intent must begin its actual confirmation-state read");
    assert.equal(await ctx.browser.evaluate("document.querySelector('[role=alertdialog]')===null"), true, "later input uses the real editable checking phase, never a modal's inert background");
    await ctx.fill(securityInput, "20");
    await watchInput(ctx, securityInput, "20");
  } finally { gate.release(); }
  await waitConfirmation(ctx);
  await confirmWrite(ctx, "PUT", securityPath);
  await noConfirmation(ctx);
  assert.equal(await ctx.readValue(securityInput), "20", "S success must not overwrite later input T");
}

export async function runRelatedSecurityScenarios(ctx: RelatedBrowserContext) {
  await ctx.check(relatedSecurityCaseNames[0], async () => {
    await prepare(ctx);
    await ctx.fill(securityInput, "16");
    await threePolicyRefreshes(ctx);
    await ctx.completeCycles(["/auth/me"], 3);
    await assertRetainedFocus(ctx, securityInput, "16");
    await captureProof(ctx, "security-polls-retained", securityInput);
    await ctx.operateAt(390, async () => submittedThenLaterDraft(ctx));
    const first = ctx.fixture.requests.find((item) => item.method === "PUT" && item.path === securityPath);
    assert.equal(first?.body?.password_min_length, 16);
    assert.equal(first?.body?.expected_updated_at, originalStamp);
    assert.equal(ctx.fixture.count("PUT", securityPath), 1);
    await captureProof(ctx, "security-later-draft-retained", securityInput);
    await ctx.operateAt(320, async () => {
      await openConfirmation(ctx, securitySubmit);
      await confirmWrite(ctx, "PUT", securityPath);
      await noConfirmation(ctx);
    });
    const writes = ctx.fixture.requests.filter((item) => item.method === "PUT" && item.path === securityPath);
    assert.equal(writes.length, 2, "one mutation for each explicit Save intent, with no automatic retry");
    assert.equal(writes[1].body?.password_min_length, 20);
    assert.equal(writes[1].body?.expected_updated_at, secondStamp);
    assert.equal(await ctx.readValue(securityInput), "20");
    await captureProof(ctx, "security-next-explicit-save", securityInput);
  });

  await ctx.check(relatedSecurityCaseNames[1], async () => {
    const model = await prepare(ctx);
    await ctx.fill(securityInput, "16");
    model.policy = { ...model.policy, password_min_length: 14, updated_at: secondStamp };
    await click(ctx, securitySubmit);
    await waitText(ctx, "main", "The target changed, so the action was not sent.");
    await ctx.browser.waitForRequestHandlersIdle({ pathname: securityPath, method: "GET" });
    assert.equal(await ctx.readValue(securityInput), "16");
    assert.equal(ctx.fixture.count("PUT", securityPath), 0);
    await captureProof(ctx, "security-real-conflict", securityInput);
  });

  await ctx.check(relatedSecurityCaseNames[2], async () => {
    await prepare(ctx);
    await ctx.fill(securityInput, "16");
    await openConfirmation(ctx, securitySubmit);
    ctx.fixture.set("GET", "/auth/me", { body: permissionUser(["system_settings.read"]) });
    await confirmReadOnlyRejection(ctx, "You do not have permission to perform this action.");
    assert.equal(await ctx.readValue(securityInput), "16");
    assert.equal(ctx.fixture.count("PUT", securityPath), 0);
    await captureProof(ctx, "security-permission-revoked", securityInput);
  });

  await ctx.check(relatedSecurityCaseNames[3], async () => {
    await prepare(ctx);
    ctx.fixture.set("PUT", securityPath, { status: 500, body: { code: "temporarily_unavailable" } });
    await ctx.fill(securityInput, "16");
    await openConfirmation(ctx, securitySubmit);
    await confirmWrite(ctx, "PUT", securityPath);
    await waitText(ctx, "main", "The result could not be confirmed.");
    await ctx.completeCycles(["/auth/me"], 3);
    assert.equal(await ctx.readValue(securityInput), "16");
    assert.equal(ctx.fixture.count("PUT", securityPath), 1);
    assert.equal(await ctx.browser.evaluate(`document.querySelector(${JSON.stringify(securitySubmit)})?.disabled`), true, "the uncertain save cannot silently enable a resend");
    await captureProof(ctx, "security-unknown-outcome-retained", securityInput);
  });
}
