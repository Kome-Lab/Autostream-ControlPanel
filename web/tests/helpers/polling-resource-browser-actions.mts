import assert from "node:assert/strict";
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { clickVisible } from "../ui-regression/visible-trigger.mts";
import { withTimeout } from "./browser-process-timing.mts";
import type { RelatedBrowserContext } from "./polling-related-browser-context.mts";

export const discordPath = "/discord/configs";
export const securityPath = "/security/settings";
export const originalStamp = "2026-10-28T00:00:00.123457Z";
export const secondStamp = "2026-10-28T00:00:00.123458Z";
export const thirdStamp = "2026-10-28T00:00:00.123459Z";
export const resourceName = '[role="dialog"] form input[type="text"]';
export const resourceSubmit = '[role="dialog"] form button[type="submit"]';
export const securityInput = '[data-related-security-min]';
export const securitySubmit = 'main form button[type="submit"]';
const confirmation = '[role="alertdialog"] [data-confirm-action]';

export async function click(ctx: RelatedBrowserContext, selector: string) {
  await clickVisible(ctx.browser, selector);
}

export async function waitText(ctx: RelatedBrowserContext, scope: string, text: string) {
  await ctx.browser.waitFor(`document.querySelector(${JSON.stringify(scope)})?.textContent.includes(${JSON.stringify(text)})`, Boolean, "the real UI must display its terminal action outcome");
}

export async function openConfirmation(ctx: RelatedBrowserContext, submitSelector: string) {
  await click(ctx, submitSelector);
  await waitConfirmation(ctx);
}

export async function waitConfirmation(ctx: RelatedBrowserContext) {
  await ctx.browser.waitFor(`(() => {const e=document.querySelector(${JSON.stringify(confirmation)});return e instanceof HTMLButtonElement && !e.disabled && e.getClientRects().length>0;})()`, Boolean, "the actual confirmation must become enabled after fresh reads");
}

export async function confirmWrite(ctx: RelatedBrowserContext, method: string, path: string) {
  const gate = ctx.fixture.gateNext(method, path);
  try {
    await click(ctx, confirmation);
    await withTimeout(gate.entered, 8_000, "the one confirmed mutation must reach its actual HTTP boundary");
  } finally { gate.release(); }
  await ctx.browser.waitForRequestHandlersIdle({ pathname: path, method });
}

export async function confirmReadOnlyRejection(ctx: RelatedBrowserContext, outcome: string) {
  const gate = ctx.fixture.gateNext("GET", "/auth/me");
  try {
    await click(ctx, confirmation);
    await withTimeout(gate.entered, 8_000, "confirmation must perform its fresh permission read");
  } finally { gate.release(); }
  await ctx.browser.waitForRequestHandlersIdle({ pathname: "/auth/me", method: "GET" });
  await waitText(ctx, "main", outcome);
}

export async function noConfirmation(ctx: RelatedBrowserContext) {
  await ctx.browser.waitFor("document.querySelector('[role=alertdialog]') === null", Boolean, "the completed action must settle its confirmation");
}

export async function markSecurityInput(ctx: RelatedBrowserContext) {
  await ctx.browser.waitFor(`(() => {const e=document.querySelector('main form input[type=number]');if(!(e instanceof HTMLInputElement)||e.disabled)return false;e.setAttribute('data-related-security-min','');return e.value==='12';})()`, Boolean, "the real security editor must hydrate its original policy");
}

export async function watchInput(ctx: RelatedBrowserContext, selector: string, expected: string) {
  await click(ctx, selector);
  assert.equal(await ctx.readValue(selector), expected);
  await ctx.browser.evaluate(`globalThis.__relatedPollingInput=document.querySelector(${JSON.stringify(selector)});true`);
}

export async function assertRetainedFocus(ctx: RelatedBrowserContext, selector: string, expected: string) {
  assert.equal(await ctx.browser.evaluate(`(() => {const e=globalThis.__relatedPollingInput;return e instanceof HTMLInputElement&&e.isConnected&&e===document.querySelector(${JSON.stringify(selector)})&&document.activeElement===e&&e.value===${JSON.stringify(expected)};})()`), true, "the same real draft input, value and focus must survive query completion");
}

export async function threeSlowFocusCycles(ctx: RelatedBrowserContext, paths: readonly string[], selector: string, expected: string) {
  const gate = ctx.fixture.gateNext("GET", "/auth/me");
  const cycles = ctx.completeCycles(paths, 3);
  void cycles.catch(() => {});
  try {
    await withTimeout(gate.entered, 8_000, "the first controlled focus cycle must start its slow actual GET");
    await assertRetainedFocus(ctx, selector, expected);
  } finally { gate.release(); }
  await cycles;
  await assertRetainedFocus(ctx, selector, expected);
}

export async function captureProof(ctx: RelatedBrowserContext, label: string, selector?: string) {
  const operationWidth = await ctx.browser.evaluate<number>("innerWidth");
  await ctx.capture(label, selector);
  for (const width of [1440, 390, 320]) {
    const snapshot = JSON.parse(readFileSync(resolve(ctx.evidenceDirectory, `${label}-${width}.json`), "utf8")) as { width: number; overflow: number };
    assert.equal(snapshot.width, width);
    assert.ok(snapshot.overflow <= 1, `actual ${width}px document has no horizontal overflow`);
  }
  const requests = ctx.fixture.requests.filter((item) => [discordPath, securityPath, "/auth/me", "/nodes"].includes(item.path)).map((item) => ({
    method: item.method, path: item.path,
    ...(item.body ? { public_fields: Object.keys(item.body).filter((key) => ["name", "service_id", "expected_updated_at", "password_min_length"].includes(key)),
      ...(typeof item.body.expected_updated_at === "string" ? { expected_updated_at: item.body.expected_updated_at } : {}),
      ...(typeof item.body.service_id === "string" ? { service_id: item.body.service_id } : {}),
      ...(typeof item.body.password_min_length === "number" ? { password_min_length: item.body.password_min_length } : {}) } : {}),
  }));
  writeFileSync(resolve(ctx.evidenceDirectory, `${label}-proof.json`), JSON.stringify({
    boundary: "actual Next UI and query/controller path; controlled BrowserHarness HTTP responses",
    capture_entry_width: operationWidth, responsive_capture_widths: [1440, 390, 320],
    interaction_widths: "Recorded by the shared context's operateAt ledger; captures alone do not prove interactions",
    natural_timer_three_cycles: "NOT_RUN; controlled focus-manager query events are identified separately",
    completed_cycles: ctx.completedCycles.map((item) => ({ ...item })), requests,
  }, null, 2), { flag: "wx" });
}
