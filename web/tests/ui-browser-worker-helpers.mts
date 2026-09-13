import assert from "node:assert/strict";
import { BrowserHarness } from "./helpers/browser-harness.mts";
import { waitForAnimationFrames } from "./ui-browser-navigation-helpers.mts";


type WorkerActionSnapshot = {
  ariaControls: string | null;
  ariaExpanded: string | null;
  ariaHaspopup: string | null;
  count: number;
  dataState: string | null;
  disabled: boolean;
  interactiveButtonCount: number;
  nestedButtonCount: number;
  reason: string;
  tagName: string;
  targetCount: number;
};

type WorkerRestartDialogSnapshot = {
  activeInside: boolean;
  activeLabel: string;
  description: string;
  dialogCount: number;
  restartNotice: string;
  title: string;
  triggerAriaExpanded: string | null;
  triggerDataState: string | null;
};

type WorkerRestartOutcomeFocusSnapshot = {
  activeExists: boolean;
  activeHiddenOrInert: boolean;
  activeInside: boolean;
  activeIsBody: boolean;
  activeIsTrigger: boolean;
  activeVisible: boolean;
  dialogCount: number;
  safeOutcomeTextVisible: boolean;
};

type WorkerStatusSnapshot = {
  icon: string | null;
  known: string | null;
  rowText: string;
  text: string;
  tone: string | null;
  transitionDuration: string;
};

export async function waitForWorkerAction(
  browser: BrowserHarness,
  label: string,
  target: number | string,
  accept: (value: WorkerActionSnapshot) => boolean,
) {
  return browser.waitFor<WorkerActionSnapshot>(
    workerActionSnapshotExpression(label, target),
    (value) => value !== null && accept(value),
    `worker action ${label} for ${target} did not reach the expected state`,
    15_000,
  );
}

export async function waitForWorkerRestartReady(browser: BrowserHarness, target = "Worker One") {
  await browser.waitForRequestHandlersIdle({ pathname: "/workers", method: "GET" });
  await waitForWorkerAction(browser, "Restart worker", target, (value) => value.disabled === false);
}

export async function clickWorkerAction(browser: BrowserHarness, label: string, target: number | string = "Worker One") {
  const present = await browser.evaluate<boolean>(`(() => {
    const target = ${JSON.stringify(target)};
    const candidates = [...document.querySelectorAll('button[aria-label=${JSON.stringify(label)}]')];
    const button = typeof target === 'number'
      ? candidates[target]
      : candidates.find((candidate) => candidate.closest('tr')?.textContent?.includes(target));
    if (!(button instanceof HTMLButtonElement)) return false;
    button.scrollIntoView({ block: 'center', inline: 'nearest' });
    return true;
  })()`);
  assert.equal(present, true, `worker action ${label} for ${target} was not present`);
  await waitForAnimationFrames(browser);
  const point = await browser.evaluate<{ disabled: boolean; hit: boolean; x: number; y: number } | null>(`(() => {
    const target = ${JSON.stringify(target)};
    const candidates = [...document.querySelectorAll('button[aria-label=${JSON.stringify(label)}]')];
    const button = typeof target === 'number'
      ? candidates[target]
      : candidates.find((candidate) => candidate.closest('tr')?.textContent?.includes(target));
    if (!(button instanceof HTMLButtonElement) || button.getClientRects().length === 0) return null;
    const rect = button.getBoundingClientRect();
    const x = rect.left + rect.width / 2;
    const y = rect.top + rect.height / 2;
    return {
      disabled: button.disabled || button.getAttribute('aria-disabled') === 'true',
      hit: button.contains(document.elementFromPoint(x, y)),
      x,
      y,
    };
  })()`);
  if (!point) assert.fail(`worker action ${label} for ${target} was not present`);
  if (!point.disabled) {
    assert.equal(point.hit, true, `enabled worker action ${label} for ${target} did not own its click point`);
  }
  await browser.clickAt(point.x, point.y);
}

export async function clickButtonWithText(browser: BrowserHarness, text: string) {
  const clicked = await browser.evaluate<boolean>(`(() => {
    const button = [...document.querySelectorAll('button')].find((candidate) => candidate.textContent?.trim() === ${JSON.stringify(text)});
    if (!(button instanceof HTMLButtonElement)) return false;
    button.click();
    return true;
  })()`);
  assert.equal(clicked, true, `button ${text} was not present`);
}

function workerActionSnapshotExpression(label: string, target: number | string) {
  return `(() => {
    const target = ${JSON.stringify(target)};
    const buttons = [...document.querySelectorAll('button[aria-label=${JSON.stringify(label)}]')];
    const targetButtons = typeof target === 'number'
      ? (buttons[target] ? [buttons[target]] : [])
      : buttons.filter((candidate) => candidate.closest('tr')?.textContent?.includes(target));
    const button = targetButtons[0];
    if (!(button instanceof HTMLButtonElement)) return null;
    const reasonId = button.getAttribute('aria-describedby');
    return {
      ariaControls: button.getAttribute('aria-controls'),
      ariaExpanded: button.getAttribute('aria-expanded'),
      ariaHaspopup: button.getAttribute('aria-haspopup'),
      count: buttons.length,
      dataState: button.getAttribute('data-state'),
      disabled: button.disabled || button.getAttribute('aria-disabled') === 'true',
      interactiveButtonCount: 1 + button.querySelectorAll('button, [role="button"]').length,
      nestedButtonCount: button.querySelectorAll('button').length,
      reason: reasonId ? document.getElementById(reasonId)?.textContent || '' : '',
      tagName: button.tagName,
      targetCount: targetButtons.length,
    };
  })()`;
}

export async function waitForWorkerRestartDialog(browser: BrowserHarness, target = "Worker One") {
  return browser.waitFor<WorkerRestartDialogSnapshot>(
    `(() => {
      const content = document.querySelector('[data-slot="alert-dialog-content"][data-state="open"]');
      const buttons = [...document.querySelectorAll('button[aria-label="Restart worker"]')];
      const trigger = buttons.find((candidate) => candidate.closest('tr')?.textContent?.includes(${JSON.stringify(target)}));
      return {
        activeInside: content instanceof HTMLElement && content.contains(document.activeElement),
        activeLabel: document.activeElement?.getAttribute('aria-label') || '',
        description: content?.querySelector('[data-slot="alert-dialog-description"]')?.textContent?.trim() || '',
        dialogCount: document.querySelectorAll('[data-slot="alert-dialog-content"][data-state="open"]').length,
        restartNotice: [...document.querySelectorAll('[role="status"]')]
          .map((element) => element.textContent?.trim() || '')
          .find((text) => text.includes('worker') || text.includes('Worker')) || '',
        title: content?.querySelector('[data-slot="alert-dialog-title"]')?.textContent?.trim() || '',
        triggerAriaExpanded: trigger?.getAttribute('aria-expanded') || null,
        triggerDataState: trigger?.getAttribute('data-state') || null,
      };
    })()`,
    (value) => value.dialogCount === 1
      && value.title.length > 0
      && value.description.length > 0
      && value.activeInside
      && value.triggerDataState === "open",
    "Worker restart confirmation did not open",
  );
}

export function assertWorkerRestartSingleOpenEvidence(
  before: WorkerActionSnapshot,
  after: WorkerRestartDialogSnapshot,
  activation: string,
) {
  assert.equal(before.targetCount, 1, `${activation} must begin with one public restart trigger`);
  assert.equal(before.dataState, "closed", `${activation} must begin from the public closed trigger state`);
  assert.equal(before.ariaExpanded, "false", `${activation} must begin with aria-expanded=false`);
  assert.equal(after.dialogCount, 1, `${activation} must produce one visible Worker restart dialog`);
  assert.equal(after.triggerDataState, "open", `${activation} must produce one public closed-to-open state transition`);
  assert.equal(after.triggerAriaExpanded, "true", `${activation} must produce aria-expanded=true`);
}

export async function waitForWorkerRestartOutcomeFocus(
  browser: BrowserHarness,
  target: string,
  safeOutcomeText: string,
  outcomeName: string,
) {
  return browser.waitFor<WorkerRestartOutcomeFocusSnapshot>(
    `(() => {
      const content = document.querySelector('[data-slot="alert-dialog-content"][data-state="open"]');
      const active = document.activeElement;
      const trigger = [...document.querySelectorAll('button[aria-label="Restart worker"]')]
        .find((candidate) => candidate.closest('tr')?.textContent?.includes(${JSON.stringify(target)}));
      const style = active instanceof HTMLElement ? getComputedStyle(active) : null;
      const activeHiddenOrInert = active instanceof HTMLElement && (
        Boolean(active.closest('[hidden], [aria-hidden="true"], [inert]'))
        || style?.display === 'none'
        || style?.visibility === 'hidden'
      );
      return {
        activeExists: active instanceof Element,
        activeHiddenOrInert,
        activeInside: content instanceof HTMLElement && active instanceof Element && content.contains(active),
        activeIsBody: active === document.body,
        activeIsTrigger: active === trigger,
        activeVisible: active instanceof HTMLElement && active.getClientRects().length > 0 && !activeHiddenOrInert,
        dialogCount: document.querySelectorAll('[data-slot="alert-dialog-content"][data-state="open"]').length,
        safeOutcomeTextVisible: content instanceof HTMLElement
          && content.getClientRects().length > 0
          && (content.textContent || '').includes(${JSON.stringify(safeOutcomeText)}),
      };
    })()`,
    (value) => value.dialogCount === 1
      && value.activeExists
      && value.activeInside
      && !value.activeIsBody
      && !value.activeIsTrigger
      && !value.activeHiddenOrInert
      && value.activeVisible
      && value.safeOutcomeTextVisible,
    `${outcomeName} did not retain safe focus inside the active Worker restart dialog`,
  );
}

export async function workerRestartDialogCount(browser: BrowserHarness) {
  return browser.evaluate<number>(
    "document.querySelectorAll('[data-slot=\"alert-dialog-content\"][data-state=\"open\"]').length",
  );
}

export async function workerRestartTriggerCount(browser: BrowserHarness, target: string) {
  return browser.evaluate<number>(`(() => {
    const row = [...document.querySelectorAll('tr')].find((candidate) => candidate.textContent?.includes(${JSON.stringify(target)}));
    return row?.querySelectorAll('button[aria-label="Restart worker"]').length || 0;
  })()`);
}

export async function waitForWorkerRestartTriggerFocus(browser: BrowserHarness, target: string, action: string) {
  await browser.waitFor<{
    activeLabel: string;
    activeRow: string;
    activeTag: string;
    focused: boolean;
    targetCount: number;
  }>(
    `(() => {
      const buttons = [...document.querySelectorAll('button[aria-label="Restart worker"]')]
        .filter((candidate) => candidate.closest('tr')?.textContent?.includes(${JSON.stringify(target)}));
      const button = buttons[0];
      return {
        activeLabel: document.activeElement?.getAttribute('aria-label') || '',
        activeRow: document.activeElement?.closest('tr')?.textContent?.trim() || '',
        activeTag: document.activeElement?.tagName || '',
        focused: button instanceof HTMLButtonElement && document.activeElement === button,
        targetCount: buttons.length,
      };
    })()`,
    (value) => value.focused,
    `${action} did not return focus to the exact restart trigger`,
  );
}

export async function tabToWorkerAction(browser: BrowserHarness, label: string, target: string) {
  const prepared = await browser.evaluate<boolean>(`(() => {
    const button = [...document.querySelectorAll('button[aria-label=${JSON.stringify(label)}]')]
      .find((candidate) => candidate.closest('tr')?.textContent?.includes(${JSON.stringify(target)}));
    if (!(button instanceof HTMLButtonElement)) return false;
    const tabbable = [...new Set(document.querySelectorAll('a[href], button, input, select, textarea, [tabindex]'))]
      .filter((candidate) => candidate instanceof HTMLElement
        && candidate.tabIndex >= 0
        && candidate.getClientRects().length > 0
        && !candidate.hasAttribute('disabled'));
    const index = tabbable.indexOf(button);
    const previous = index > 0 ? tabbable[index - 1] : null;
    if (!(previous instanceof HTMLElement)) return false;
    previous.focus();
    return document.activeElement === previous;
  })()`);
  assert.equal(prepared, true, `could not prepare Tab navigation to Worker action ${label} for ${target}`);
  await browser.pressKey("Tab");
  await waitForWorkerRestartTriggerFocus(browser, target, "Tab");
}

export async function waitForWorkerRestartDialogClosed(browser: BrowserHarness) {
  await browser.waitFor(
    `(() => !document.querySelector('[data-slot="alert-dialog-content"]')
      && !document.querySelector('[data-slot="alert-dialog-overlay"]'))()`,
    Boolean,
    "Worker restart confirmation did not close and release its portal",
  );
}

export async function waitForWorkerStatus(
  browser: BrowserHarness,
  workerName: string,
  accept: (value: WorkerStatusSnapshot) => boolean,
) {
  return browser.waitFor<WorkerStatusSnapshot>(
    `(() => {
      const row = [...document.querySelectorAll('tr')].find((candidate) => candidate.textContent?.includes(${JSON.stringify(workerName)}));
      const badge = row?.querySelector('[data-status-known]');
      if (!(row instanceof HTMLElement) || !(badge instanceof HTMLElement)) return null;
      return {
        icon: badge.querySelector('[data-status-icon]')?.getAttribute('data-status-icon') || null,
        known: badge.getAttribute('data-status-known'),
        rowText: row.textContent || '',
        text: badge.textContent || '',
        tone: badge.getAttribute('data-status-tone'),
        transitionDuration: getComputedStyle(badge).transitionDuration,
      };
    })()`,
    (value) => value !== null && accept(value),
    `Worker status for ${workerName} did not reach the expected state`,
    15_000,
  );
}

export async function browserContainsMarker(browser: BrowserHarness, marker: string) {
  return browser.evaluate<boolean>(`(() => {
    const marker = ${JSON.stringify(marker)};
    if ((document.body.textContent || '').includes(marker)) return true;
    return [...document.querySelectorAll('[aria-label], [aria-description], [aria-describedby], [title]')].some((element) =>
      ['aria-label', 'aria-description', 'title'].some((attribute) => (element.getAttribute(attribute) || '').includes(marker))
      || (element.getAttribute('aria-describedby') || '').split(/\\s+/).some((id) => (document.getElementById(id)?.textContent || '').includes(marker))
    );
  })()`);
}
