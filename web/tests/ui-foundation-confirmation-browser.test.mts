import assert from "node:assert/strict";
import test from "node:test";

import type { BrowserHarness } from "./helpers/browser-harness.mts";
import { withConfirmationBrowserFixture } from "./helpers/ui-foundation-confirmation-browser-harness.mts";

test("high-risk and compatibility confirmations preserve browser focus, literal input, state, and intent boundaries", async (t) => {
  await withConfirmationBrowserFixture(async ({ baseUrl, browser }) => {
    await browser.setViewport(1100, 800);
    browser.clearConsoleErrors();

    await browser.navigate(`${baseUrl}/?scenario=consequence&locale=en`);
    await browser.clickSelector("#consequence-trigger");
    await browser.waitFor("document.querySelectorAll('[role=alertdialog]').length", (value: number) => value === 1, "consequence dialog open");
    assert.equal(await browser.evaluate("document.activeElement?.getAttribute('data-slot')"), "alert-dialog-cancel");
    assert.equal(await browser.evaluate("document.querySelector('[role=alertdialog]')?.getAttribute('aria-labelledby') !== null"), true);
    assert.equal(await browser.evaluate("document.querySelector('[role=alertdialog]')?.getAttribute('aria-describedby') !== null"), true);
    assert.equal(await browser.evaluate("document.querySelector('[data-slot=alert-dialog-title]')?.textContent"), "Restart");
    await browser.pressKey("Escape");
    await browser.waitFor("document.querySelectorAll('[role=alertdialog]').length", (value: number) => value === 0, "Escape closes consequence dialog");
    await browser.waitFor(
      "document.activeElement?.id || ''",
      (value: string) => value === "consequence-trigger",
      "Escape returns focus to consequence trigger",
    );
    assert.equal(await browser.evaluate("document.activeElement?.id"), "consequence-trigger");
    assert.equal(await intentCount(browser), 0, "close never confirms");
    await browser.clickSelector("#consequence-trigger");
    await browser.clickSelector("[data-slot=alert-dialog-cancel]");
    await browser.waitFor(
      "document.activeElement?.id || ''",
      (value: string) => value === "consequence-trigger",
      "cancel returns focus to consequence trigger",
    );
    assert.equal(await intentCount(browser), 0, "cancel never confirms");

    await browser.navigate(`${baseUrl}/?scenario=typed&locale=en`);
    await browser.clickSelector("#typed-trigger");
    await browser.waitFor("document.querySelector('[data-confirmation-token-input]') !== null", Boolean, "typed input visible");
    assert.equal(await browser.evaluate("document.activeElement?.hasAttribute('data-confirmation-token-input')"), true);
    await browser.fillSelector("[data-confirmation-token-input]", "worker alpha");
    assert.equal(await browser.evaluate("document.querySelector('[data-confirm-action]')?.hasAttribute('disabled')"), true);
    const mismatch = await browser.evaluate<{ describedBy: string | null; visible: boolean }>(`(() => {
      const input = document.querySelector('[data-confirmation-token-input]');
      const describedBy = input?.getAttribute('aria-describedby') || null;
      return { describedBy, visible: Boolean(describedBy && document.getElementById(describedBy)?.textContent) };
    })()`);
    assert.ok(mismatch.describedBy);
    assert.equal(mismatch.visible, true);
    await browser.fillSelector("[data-confirmation-token-input]", "Worker Alpha ");
    assert.equal(await browser.evaluate("document.querySelector('[data-confirm-action]')?.hasAttribute('disabled')"), true);
    const paste = await browser.evaluate<{ allowed: boolean; defaultPrevented: boolean }>(`(() => {
      const input = document.querySelector('[data-confirmation-token-input]');
      if (!(input instanceof HTMLInputElement)) return { allowed: false, defaultPrevented: true };
      const pasteEvent = new InputEvent('beforeinput', {
        bubbles: true,
        cancelable: true,
        inputType: 'insertFromPaste',
        data: 'Worker Alpha',
      });
      const allowed = input.dispatchEvent(pasteEvent);
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
      setter?.call(input, 'Worker Alpha');
      input.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertFromPaste', data: 'Worker Alpha' }));
      return { allowed, defaultPrevented: pasteEvent.defaultPrevented };
    })()`);
    assert.deepEqual(paste, { allowed: true, defaultPrevented: false });
    await browser.waitFor("!document.querySelector('[data-confirm-action]')?.hasAttribute('disabled')", Boolean, "exact pasted token enables confirm");
    await browser.evaluate(`(() => {
      const button = document.querySelector('[data-confirm-action]');
      if (!(button instanceof HTMLButtonElement)) return false;
      button.click();
      button.click();
      button.focus();
      return true;
    })()`);
    await browser.pressKey("Enter");
    await browser.pressKey("Enter");
    await browser.waitFor("Number(document.querySelector('[data-testid=intent-count]')?.textContent)", (value: number) => value === 1, "duplicate intent latch");
    assert.equal(await intentCount(browser), 1);
    assert.equal(await browser.evaluate("document.querySelector('[role=alertdialog]')?.getAttribute('aria-busy')"), "true");
    assert.equal(await browser.evaluate("document.querySelector('[data-confirm-action]')?.hasAttribute('disabled')"), true);
    await browser.pressKey("Escape");
    assert.equal(await browser.evaluate("document.querySelectorAll('[role=alertdialog]').length"), 1, "submitting cannot be abandoned by Escape");

    await browser.navigate(`${baseUrl}/?scenario=revalidating&locale=ja`);
    await browser.clickSelector("#revalidating-trigger");
    assert.equal(await browser.evaluate("document.querySelector('[data-slot=alert-dialog-title]')?.textContent"), "再起動");
    assert.equal(await browser.evaluate("document.querySelector('[role=alertdialog]')?.getAttribute('aria-busy')"), "true");
    assert.equal(await browser.evaluate("document.querySelector('[data-confirm-action]')?.hasAttribute('disabled')"), true);
    assert.equal(await browser.evaluate("document.querySelector('[data-slot=alert-dialog-cancel]')?.hasAttribute('disabled')"), false);
    assert.match(await browser.evaluate<string>("document.querySelector('[role=status]')?.textContent || ''"), /最新の権限と状態/);

    await browser.navigate(`${baseUrl}/?scenario=stale&locale=en`);
    await browser.clickSelector("#stale-trigger");
    assert.match(await browser.evaluate<string>("document.querySelector('[role=alert]')?.textContent || ''"), /action was not sent/);
    assert.equal(await intentCount(browser), 0);

    await browser.navigate(`${baseUrl}/?scenario=unknown&locale=en`);
    await browser.clickSelector("#unknown-trigger");
    assert.match(await browser.evaluate<string>("document.querySelector('[role=alert]')?.textContent || ''"), /Do not resend/);
    assert.equal(await browser.evaluate("document.querySelector('[data-confirm-action]') === null"), true);
    assert.equal(await intentCount(browser), 0);

    let conflictObservation: FailureStateObservation | undefined;
    let failedObservation: FailureStateObservation | undefined;
    let unavailableObservation: FailureStateObservation | undefined;

    await t.test("conflict renders safe error and hides diagnostic metadata", async () => {
      conflictObservation = await exerciseFailureState(browser, baseUrl, {
        scenario: "conflict",
        triggerId: "conflict-trigger",
      });
      assertFailureStateObservation(conflictObservation, conflictExpectation);
    });

    await t.test("failed renders safe error without retry", async () => {
      failedObservation = await exerciseFailureState(browser, baseUrl, {
        scenario: "failed",
        triggerId: "failed-trigger",
      });
      assertFailureStateObservation(failedObservation, failedExpectation);
    });

    await t.test("revalidation unavailable blocks confirmation and returns focus", async () => {
      unavailableObservation = await exerciseFailureState(browser, baseUrl, {
        scenario: "unavailable",
        triggerId: "unavailable-trigger",
      });
      assertFailureStateObservation(unavailableObservation, unavailableExpectation);
    });

    await t.test("failure-state browser oracle rejects in-memory false-positive variants", () => {
      assert.ok(conflictObservation);
      assert.ok(failedObservation);
      assert.ok(unavailableObservation);
      assert.throws(
        () => assertFailureStateObservation({ ...conflictObservation, dialogCount: 0 }, conflictExpectation),
        /dialog opens/,
      );
      assert.throws(
        () => assertFailureStateObservation({
          ...conflictObservation,
          documentText: `${conflictObservation.documentText} must-not-render`,
          dialogHtml: `${conflictObservation.dialogHtml} must-not-render`,
          accessibleName: `${conflictObservation.accessibleName} must-not-render`,
        }, conflictExpectation),
        /hides must-not-render/,
      );
      assert.throws(
        () => assertFailureStateObservation({
          ...failedObservation,
          buttonLabels: [...failedObservation.buttonLabels, "Resend"],
        }, failedExpectation),
        /automatic retry or resend control/,
      );
      assert.throws(
        () => assertFailureStateObservation({
          ...unavailableObservation,
          confirmPresent: true,
          confirmDisabled: false,
        }, unavailableExpectation),
        /confirm remains disabled or absent/,
      );
      assert.throws(
        () => assertFailureStateObservation({ ...conflictObservation, intentCount: 1 }, conflictExpectation),
        /confirm intent count/,
      );
      assert.throws(
        () => assertFailureStateObservation({ ...conflictObservation, escapeFocusId: "wrong-trigger" }, conflictExpectation),
        /Escape focus return/,
      );
    });

    await browser.navigate(`${baseUrl}/?scenario=invalid&locale=en`);
    assert.equal(await browser.evaluate("document.querySelector('#invalid-trigger')?.hasAttribute('disabled')"), true);
    await browser.evaluate("document.querySelector('#invalid-trigger')?.click()");
    assert.equal(await browser.evaluate("document.querySelectorAll('[role=alertdialog]').length"), 0);

    await browser.navigate(`${baseUrl}/?scenario=controller-close&locale=en`);
    await browser.clickSelector("#controller-close-trigger");
    await browser.clickSelector("[data-confirm-action]");
    await browser.waitFor(
      "document.activeElement?.id || ''",
      (value: string) => value === "controller-close-trigger",
      "controller-owned close returns focus to trigger",
    );
    assert.equal(await intentCount(browser), 1);

    await browser.setMediaFeatures([
      { name: "prefers-reduced-motion", value: "reduce" },
      { name: "forced-colors", value: "active" },
    ]);
    await browser.navigate(`${baseUrl}/?scenario=consequence&locale=en`);
    await browser.clickSelector("#consequence-trigger");
    assert.equal(await browser.evaluate("matchMedia('(prefers-reduced-motion: reduce)').matches"), true);
    assert.equal(await browser.evaluate("matchMedia('(forced-colors: active)').matches"), true);
    assert.equal(await browser.evaluate("document.activeElement?.getAttribute('data-slot')"), "alert-dialog-cancel");
    await browser.pressKey("Escape");
    await browser.waitFor(
      "document.querySelectorAll('[role=alertdialog]').length",
      (value: number) => value === 0,
      "reduced-motion Escape closes consequence dialog",
    );
    await browser.waitFor(
      "document.activeElement?.id || ''",
      (value: string) => value === "consequence-trigger",
      "reduced-motion focus returns to consequence trigger",
    );
    assert.equal(await browser.evaluate("document.activeElement?.id"), "consequence-trigger");

    await browser.setMediaFeatures([]);
    await browser.navigate(`${baseUrl}/?scenario=legacy&locale=ja`);
    await browser.clickSelector("#legacy-trigger");
    assert.equal(await browser.evaluate("document.querySelector('[data-slot=alert-dialog-title]')?.textContent"), "Legacy title");
    assert.match(await browser.evaluate<string>("document.querySelector('[data-slot=alert-dialog-description]')?.textContent || ''"), /本番配信に影響/);
    await browser.pressKey("Escape");
    await browser.waitFor(
      "document.activeElement?.id || ''",
      (value: string) => value === "legacy-trigger",
      "legacy focus returns to trigger",
    );
    assert.equal(await browser.evaluate("document.activeElement?.id"), "legacy-trigger");
    assert.equal(await intentCount(browser), 0);
    await browser.clickSelector("#legacy-trigger");
    await browser.clickSelector("[data-confirm-action]");
    await browser.waitFor("Number(document.querySelector('[data-testid=intent-count]')?.textContent)", (value: number) => value === 1, "legacy callback remains caller-owned");
    assert.equal(await intentCount(browser), 1);

    assert.equal(browser.consoleErrorCount, 0);
  });
});

async function intentCount(browser: { evaluate<T>(expression: string): Promise<T> }) {
  return browser.evaluate<number>("Number(document.querySelector('[data-testid=intent-count]')?.textContent)");
}

type FailureStateExercise = Readonly<{
  scenario: "conflict" | "failed" | "unavailable";
  triggerId: string;
}>;

type FailureStateExpectation = Readonly<{
  scenario: "conflict" | "failed" | "revalidation-unavailable";
  triggerId: string;
  safeAlert: string;
  forbidden: readonly string[];
}>;

type FailureStateObservation = Readonly<{
  dialogCount: number;
  documentText: string;
  dialogHtml: string;
  accessibleName: string;
  accessibleDescription: string;
  alertText: string;
  confirmPresent: boolean;
  confirmDisabled: boolean | null;
  cancelPresent: boolean;
  cancelDisabled: boolean | null;
  buttonLabels: readonly string[];
  intentCount: number;
  requestCount: number;
  escapeDialogCount: number;
  escapeFocusId: string;
  cancelDialogCount: number;
  cancelFocusId: string;
}>;

const conflictExpectation: FailureStateExpectation = Object.freeze({
  scenario: "conflict",
  triggerId: "conflict-trigger",
  safeAlert: "The resource has changed. Review the latest state. Review the latest information before trying again.",
  forbidden: Object.freeze(["must-not-render", "endpoint", "payload", "safeReference", "workers.restart", "private-worker-id"]),
});

const failedExpectation: FailureStateExpectation = Object.freeze({
  scenario: "failed",
  triggerId: "failed-trigger",
  safeAlert: "Could not connect to the service. Check the latest state.",
  forbidden: Object.freeze([
    "must-not-render",
    "diagnosticCode",
    "raw native message",
    "endpoint",
    "payload",
    "safeReference",
    "workers.restart",
    "private-worker-id",
  ]),
});

const unavailableExpectation: FailureStateExpectation = Object.freeze({
  scenario: "revalidation-unavailable",
  triggerId: "unavailable-trigger",
  safeAlert: "The action cannot be sent because the latest permissions or state could not be verified.",
  forbidden: Object.freeze(["must-not-render", "endpoint", "payload", "safeReference", "workers.restart", "private-worker-id"]),
});

async function exerciseFailureState(
  browser: BrowserHarness,
  baseUrl: string,
  exercise: FailureStateExercise,
): Promise<FailureStateObservation> {
  await browser.navigate(`${baseUrl}/?scenario=${exercise.scenario}&locale=en`);
  await browser.waitForRequestHandlersIdle();
  browser.clearRequestCounts();
  await browser.clickSelector(`#${exercise.triggerId}`);
  await browser.waitFor(
    "document.querySelectorAll('[role=alertdialog]').length",
    (value: number) => value === 1,
    `${exercise.scenario} dialog open`,
  );
  const visible = await observeFailureDialog(browser);

  await browser.pressKey("Escape");
  await browser.waitFor(
    "document.querySelectorAll('[role=alertdialog]').length",
    (value: number) => value === 0,
    `${exercise.scenario} Escape closes dialog`,
  );
  await browser.waitFor(
    "document.activeElement?.id || ''",
    (value: string) => value === exercise.triggerId,
    `${exercise.scenario} Escape returns focus`,
  );
  const escapeDialogCount = await browser.evaluate<number>("document.querySelectorAll('[role=alertdialog]').length");
  const escapeFocusId = await browser.evaluate<string>("document.activeElement?.id || ''");

  await browser.clickSelector(`#${exercise.triggerId}`);
  await browser.waitFor(
    "document.querySelectorAll('[role=alertdialog]').length",
    (value: number) => value === 1,
    `${exercise.scenario} dialog reopens for cancel`,
  );
  await browser.clickSelector("[data-slot=alert-dialog-cancel]");
  await browser.waitFor(
    "document.querySelectorAll('[role=alertdialog]').length",
    (value: number) => value === 0,
    `${exercise.scenario} cancel closes dialog`,
  );
  await browser.waitFor(
    "document.activeElement?.id || ''",
    (value: string) => value === exercise.triggerId,
    `${exercise.scenario} cancel returns focus`,
  );
  await browser.waitForRequestHandlersIdle();
  return Object.freeze({
    ...visible,
    intentCount: await intentCount(browser),
    requestCount: [...browser.requests.values()].reduce((total, count) => total + count, 0),
    escapeDialogCount,
    escapeFocusId,
    cancelDialogCount: await browser.evaluate<number>("document.querySelectorAll('[role=alertdialog]').length"),
    cancelFocusId: await browser.evaluate<string>("document.activeElement?.id || ''"),
  });
}

async function observeFailureDialog(browser: BrowserHarness) {
  return browser.evaluate<Omit<
    FailureStateObservation,
    "intentCount" | "requestCount" | "escapeDialogCount" | "escapeFocusId" | "cancelDialogCount" | "cancelFocusId"
  >>(`(() => {
    const normalize = (value) => (value || '').replace(/\\s+/g, ' ').trim();
    const dialog = document.querySelector('[role=alertdialog]');
    const referencedText = (attribute) => {
      if (!(dialog instanceof HTMLElement)) return '';
      return normalize((dialog.getAttribute(attribute) || '')
        .split(/\\s+/)
        .filter(Boolean)
        .map((id) => document.getElementById(id)?.textContent || '')
        .join(' '));
    };
    const confirm = dialog?.querySelector('[data-confirm-action]');
    const cancel = dialog?.querySelector('[data-slot=alert-dialog-cancel]');
    return {
      dialogCount: document.querySelectorAll('[role=alertdialog]').length,
      documentText: normalize(document.body.textContent),
      dialogHtml: dialog?.outerHTML || '',
      accessibleName: referencedText('aria-labelledby'),
      accessibleDescription: referencedText('aria-describedby'),
      alertText: normalize(dialog?.querySelector('[role=alert]')?.textContent),
      confirmPresent: confirm instanceof HTMLButtonElement,
      confirmDisabled: confirm instanceof HTMLButtonElement ? confirm.disabled : null,
      cancelPresent: cancel instanceof HTMLButtonElement,
      cancelDisabled: cancel instanceof HTMLButtonElement ? cancel.disabled : null,
      buttonLabels: [...(dialog?.querySelectorAll('button') || [])].map((button) => normalize(button.textContent)),
    };
  })()`);
}

function assertFailureStateObservation(
  actual: FailureStateObservation,
  expected: FailureStateExpectation,
) {
  assert.equal(actual.dialogCount, 1, `${expected.scenario} dialog opens`);
  assert.equal(actual.alertText, expected.safeAlert, `${expected.scenario} safe localized alert`);
  assert.equal(
    actual.confirmPresent && actual.confirmDisabled !== true,
    false,
    `${expected.scenario} confirm remains disabled or absent`,
  );
  assert.equal(actual.cancelPresent, true, `${expected.scenario} close control exists`);
  assert.equal(actual.cancelDisabled, false, `${expected.scenario} close control remains available`);
  assert.equal(actual.intentCount, 0, `${expected.scenario} confirm intent count`);
  assert.equal(actual.requestCount, 0, `${expected.scenario} mutation request count`);
  assert.equal(
    actual.buttonLabels.some((label) => /retry|resend|try again/i.test(label)),
    false,
    `${expected.scenario} automatic retry or resend control absent`,
  );
  for (const forbidden of expected.forbidden) {
    for (const [surface, value] of [
      ["document text", actual.documentText],
      ["dialog HTML", actual.dialogHtml],
      ["accessible name", actual.accessibleName],
      ["accessible description", actual.accessibleDescription],
    ] as const) {
      assert.equal(
        value.toLocaleLowerCase("en-US").includes(forbidden.toLocaleLowerCase("en-US")),
        false,
        `${expected.scenario} hides ${forbidden} from ${surface}`,
      );
    }
  }
  assert.equal(actual.escapeDialogCount, 0, `${expected.scenario} Escape close`);
  assert.equal(actual.escapeFocusId, expected.triggerId, `${expected.scenario} Escape focus return`);
  assert.equal(actual.cancelDialogCount, 0, `${expected.scenario} cancel close`);
  assert.equal(actual.cancelFocusId, expected.triggerId, `${expected.scenario} cancel focus return`);
}
