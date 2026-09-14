import assert from "node:assert/strict";
import type { BrowserHarness } from "../helpers/browser-harness.mts";
import { clickVisible, selectVisible } from "./visible-trigger.mts";

export function assertPageText(value: unknown, pageIndex: number) {
  const expected = pageIndex + 1;
  assert.match(String(value), new RegExp(`(?:^|\\s)(?:${expected}\\s*\\/|Page ${expected} of)`), "zero-based URL index must match the displayed page");
}
export const rowNamesExpression = "[...document.querySelectorAll('[data-slot=data-table] tbody tr td:first-child button:first-child')].map(e=>e.textContent.trim())";
export function assertSortedRows(names: string[], descending: boolean) {
  assert.ok(names.length > 1, "sort needs at least two displayed rows");
  const expected = [...names].sort((a,b)=>a.localeCompare(b, "en", {numeric:true}));
  if (descending) expected.reverse();
  assert.deepEqual(names, expected, "sort must change actual displayed row order");
}
export async function exerciseTableSort(browser: BrowserHarness) {
  const header = '[data-slot=data-table] th:first-child button';
  const headerVisible = await browser.evaluate(`(() => {const e=document.querySelector('${header}');return !!e && e.getClientRects().length>0 && getComputedStyle(e).visibility!=='hidden';})()`);
  if (headerVisible) await clickVisible(browser, header);
  else await selectVisible(browser, '[data-slot=table-toolbar] select:has(option[value="name"])', "name");
  await browser.waitFor("document.querySelector('[data-slot=data-table] th:first-child')?.getAttribute('aria-sort')", value => value === "ascending", "visible sort must change actual table order to ascending");
  assert.equal(await browser.evaluate("new URLSearchParams(location.search).get('streams.sort')"), "name");
  assertSortedRows(await browser.evaluate<string[]>(rowNamesExpression), false);
  if (headerVisible) await clickVisible(browser, header);
  else await clickVisible(browser, '[data-slot=table-toolbar] button', /^(昇順|Ascending)$/);
  await browser.waitFor("document.querySelector('[data-slot=data-table] th:first-child')?.getAttribute('aria-sort')", value => value === "descending", "visible sort must change actual table order to descending");
  assertSortedRows(await browser.evaluate<string[]>(rowNamesExpression), true);
}
export async function exerciseTable(browser: BrowserHarness) {
  const root = '[data-slot="data-table"]';
  const rows = root + " tbody tr";
  assert.equal(await browser.evaluate("document.querySelectorAll('" + rows + "').length"), 8, "initial page size");
  assert.equal(await browser.evaluate("new URLSearchParams(location.search).get('streams.page')"), "2", "initial URL page survives asynchronous data");
  await browser.evaluate("history.replaceState({uiProbe:1},'',location.pathname+'?view=retained&streams.page=1#retained');dispatchEvent(new PopStateEvent('popstate'));true");
  await browser.waitFor("document.querySelector('[data-slot=table-pagination]')?.textContent || ''", value => { try { assertPageText(value, 1); return true; } catch { return false; } }, "popstate drives real table page 2");
  await browser.evaluate("history.pushState({uiProbe:2},'',location.pathname+'?view=retained&streams.page=2#retained');dispatchEvent(new PopStateEvent('popstate'));history.back();true");
  await browser.waitFor("new URLSearchParams(location.search).get('streams.page')", value => value === "1", "real history back");
  await browser.evaluate("history.forward();true");
  await browser.waitFor("new URLSearchParams(location.search).get('streams.page')", value => value === "2", "real history forward");
  await browser.clickSelector('[data-slot=column-visibility] summary');
  const visibleColumns = await browser.evaluate<number>("document.querySelectorAll('[data-slot=data-table] thead th').length");
  assert.ok(await browser.evaluate("document.querySelectorAll('[data-slot=column-visibility] input:disabled').length >= 3"), "name/status/actions remain required");
  await browser.clickSelector('[data-slot=column-visibility] input:not(:disabled)');
  await browser.waitFor("document.querySelectorAll('[data-slot=data-table] thead th').length", value => value === visibleColumns - 1, "real optional column hides");
  await browser.clickSelector('[data-slot=column-visibility] input:not(:disabled)');
  await browser.waitFor("document.querySelectorAll('[data-slot=data-table] thead th').length", value => value === visibleColumns, "real optional column restores");
  await browser.clickSelector('[data-slot=column-visibility] summary');
  const length = await browser.evaluate("history.length");
  await browser.fillSelector(root + ' input[type="search"]', "private@example.test");
  await browser.waitFor("document.querySelector('" + root + "')?.textContent || ''", value => typeof value === "string" && /No results|該当する/.test(value), "real search filters rows");
  assert.doesNotMatch(await browser.evaluate<string>("location.href"), /private|example/);
  assert.equal(await browser.evaluate("history.length"), length);
  await browser.fillSelector(root + ' input[type="search"]', "");
  await browser.waitFor("document.querySelectorAll('" + rows + "').length", value => value === 8, "search recovery");
  await selectVisible(browser, '[data-slot=table-toolbar] select:has(option[value="live"])', "live");
  await browser.waitFor("new URLSearchParams(location.search).get('streams.filter.status')", value => value === "live", "real structured filter writes allowlisted enum");
  assert.equal(await browser.evaluate("new URLSearchParams(location.search).get('view')"), "retained");
  assert.equal(await browser.evaluate("location.hash"), "#retained");
  await selectVisible(browser, '[data-slot=table-toolbar] select:has(option[value="live"])', "");
  await exerciseTableSort(browser);
  await selectVisible(browser, "[data-slot=table-pagination] select", "50");
  await browser.waitFor("document.querySelectorAll('" + rows + "').length", value => value === 25, "real page-size change");
  await browser.evaluate("(() => { const e=document.querySelector('[data-slot=data-table] tbody button'); globalThis.__uiOwner=e; e.focus();return true;})()");
  for (const width of [390, 1440]) {
    await browser.setViewport(width, 900);
    assert.equal(await browser.evaluate("document.activeElement === globalThis.__uiOwner && document.activeElement.getClientRects().length > 0"), true, "breakpoint preserves one visible focus owner");
  }
  await browser.evaluate("delete globalThis.__uiOwner;true");
}
