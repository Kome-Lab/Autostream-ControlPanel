import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import type { BrowserHarness } from "./browser-harness.mts";
import { scrollSelectorIntoView, waitForAnimationFrames } from "../ui-browser-navigation-helpers.mts";
import { clickDisabledVisible, clickVisible } from "../ui-regression/visible-trigger.mts";
import { labels, unsafeErrorProbe, type ImageKind } from "./p3-media-browser-fixture.mts";

export const operationSelector = "#create-stream-visual [data-media-upload]";
export const submitSelector = '#create-stream form button[type="submit"]';
export type Observation = { width: number; overflow: number; candidates: number; title: string; theme: string; locale: string; cssZoom: string; operation: null | { id: string; phase: string; kind: string; text: string; progress: boolean; percent: boolean; filename: string; separate: boolean; buttonsFit: boolean }; summary: boolean; summaryVisible: boolean; uploadVisible: boolean; saveDisabled: boolean; ready: number };

export function createP3Proof(browser: BrowserHarness, directory: string | undefined, prefix: string, locale: "ja" | "en" = "ja") {
  const rows: { label: string; observation: Observation }[] = [];
  let serial = 0;
  const observe = () => browser.evaluate<Observation>(`(() => {
    const op=document.querySelector('${operationSelector}'),name=op?.querySelector('details'),actions=op?.lastElementChild;
    const rect=el=>el?.getBoundingClientRect();
    const visible=el=>{const r=rect(el);return !!r&&r.width>0&&r.height>0&&r.top>=0&&r.bottom<=innerHeight;};
    const upload=[...document.querySelectorAll('#create-stream-visual input[type=file]')].find(el=>op?.getAttribute('data-media-upload-kind')==='cover'?/蓋画像|cover image/i.test(el.closest('label')?.textContent||''):/背景画像|background image/i.test(el.closest('label')?.textContent||''));
    return {width:innerWidth,overflow:Math.max(0,document.documentElement.scrollWidth-document.documentElement.clientWidth),candidates:document.querySelectorAll('[data-cover-candidate]').length,
      title:document.querySelector('#create-stream-basic input')?.value||'',theme:document.documentElement.classList.contains('dark')?'dark':'light',locale:document.documentElement.lang,cssZoom:document.documentElement.style.zoom||'1',
      operation:op?{id:op.getAttribute('data-media-upload'),phase:op.getAttribute('data-media-upload-phase'),kind:op.getAttribute('data-media-upload-kind'),text:op.textContent||'',progress:!!op.querySelector('progress'),percent:!!op.querySelector('progress[value]'),filename:name?.querySelector('p')?.textContent||'',separate:!!name&&!!actions&&rect(actions).top>=rect(name).bottom,buttonsFit:[...op.querySelectorAll('button')].every(button=>rect(button).width>=80&&rect(button).height>=30&&rect(button).right<=rect(op).right+1)}:null,
      summary:!!document.querySelector('[data-media-upload-summary]'),summaryVisible:visible(document.querySelector('[data-media-upload-summary]')),uploadVisible:visible(upload?.closest('label')),saveDisabled:document.querySelector('${submitSelector}')?.disabled===true,ready:document.querySelectorAll('[data-media-ready-unsaved]').length};
  })()`);
  const capture = async (label: string, diagnostic = false) => {
    const anchor = await browser.evaluate<boolean>(`(() => {const el=document.querySelector('${operationSelector}')||document.querySelector('[data-cover-candidate]')||document.querySelector('#create-stream-basic')||(${diagnostic}&&document.body);if(!el)return false;el.scrollIntoView({block:'center',inline:'nearest',behavior:'instant'});return true;})()`);
    assert.equal(anchor, true); await waitForAnimationFrames(browser);
    if (!diagnostic) await browser.waitFor(`(() => {const images=[...document.querySelectorAll('#create-stream-visual [data-media-upload] img,[data-cover-candidate] img')];return images.every(image=>image.complete&&image.naturalWidth>0);})()`, Boolean, "actual pending and ready thumbnails must decode before capture");
    // Clone the DOM observation before awaiting PNG capture; later response
    // releases cannot rewrite a previous phase's evidence.
    const observation = structuredClone(await observe());
    if (!diagnostic) assert.equal(observation.overflow, 0, `${prefix}/${label} must fit the measured CSS viewport`);
    if (observation.operation && !diagnostic) { assert.equal(observation.summaryVisible, true, "the count summary stays readable after scrolling to the active card"); assert.equal(observation.operation.separate, true); assert.equal(observation.operation.buttonsFit, true); assert.equal(observation.operation.text.includes(unsafeErrorProbe), false); }
    rows.push({ label, observation });
    if (directory) {
      mkdirSync(directory, { recursive: true }); const filename = `${prefix}-${String(++serial).padStart(3, "0")}-${label}`;
      const image = await browser.captureViewportEvidence();
      writeFileSync(resolve(directory, `${filename}.png`), image.png, { flag: "wx" });
      writeFileSync(resolve(directory, `${filename}.json`), JSON.stringify({ observation, viewport: image.viewport, evidence_kind: "real_browser_CSS_viewport", css_200_is_native_zoom: false }, null, 2), { flag: "wx" });
    }
    return observation;
  };
  const phase = async (kind: ImageKind, value: string, candidates: number, filename?: string) => {
    await browser.waitFor(`document.querySelector('${operationSelector}')?.getAttribute('data-media-upload-kind')===${JSON.stringify(kind)}&&document.querySelector('${operationSelector}')?.getAttribute('data-media-upload-phase')===${JSON.stringify(value)}`, Boolean, `${kind} actual ${value} phase missing`);
    const current = await observe();
    const phaseText: Record<string, string> = locale === "ja"
      ? { preparing: "準備中", uploading: "アップロード中", converting: "画像を変換中", checking: "画像の状態を確認中", cleaning: "一時画像を取り消し中", failed: "画像の処理に失敗しました。", outcome_unknown: "画像の処理結果が不明です。" }
      : { preparing: "Preparing", uploading: "Uploading", converting: "Converting image", checking: "Checking image status", cleaning: "Discarding temporary image", failed: "The image could not be processed.", outcome_unknown: "The image result is unknown." };
    assert.ok(phaseText[value]); assert.equal(current.operation?.text.includes(phaseText[value]), true, "the visible phase must use the selected locale");
    assert.equal(current.candidates, candidates, "pending, failed and unknown images must never enter ready candidates");
    assert.equal(current.saveDisabled, true); assert.equal(current.summary, true);
    assert.equal(current.operation?.percent, false, "actual work must use indeterminate progress, never an invented percentage");
    assert.equal(current.operation?.progress, ["preparing", "uploading", "converting", "checking", "cleaning"].includes(value));
    if (filename) assert.equal(current.operation?.filename, filename);
    await clickDisabledVisible(browser, submitSelector);
  };
  const action = async (locale: "ja" | "en", name: "check" | "resume" | "retry" | "discard") => clickVisible(browser, `${operationSelector} button`, new RegExp(`^${labels[locale][name]}$`));
  const ready = async (count: number, locale: "ja" | "en", minimumBadges: number) => {
    await browser.waitFor(`!document.querySelector('${operationSelector}')&&document.querySelectorAll('[data-cover-candidate]').length===${count}&&document.querySelectorAll('[data-media-ready-unsaved]').length>=${minimumBadges}`, Boolean, "only a finished response may become ready-unsaved");
    assert.equal(await browser.evaluate(`document.querySelector('#create-stream-visual')?.textContent?.includes(${JSON.stringify(labels[locale].ready)})`), true);
  };
  const layouts = async (label: string, originalWidth: number) => {
    for (const width of [320, 390, 1440]) {
      await browser.setViewport(width, 1000); const row = await capture(`${label}-css-${width}`); assert.equal(row.width, width);
    }
    for (const width of [390, 1440]) {
      await browser.setViewport(width, 1000);
      try { await browser.evaluate("document.documentElement.style.zoom='2';true"); const row = await capture(`${label}-css-${width}-magnification-200`); assert.equal(row.cssZoom, "2"); }
      finally { await browser.evaluate("document.documentElement.style.zoom='';true"); }
    }
    await browser.setViewport(originalWidth, 1000);
  };
  return { rows, observe, phase, capture, action, ready, layouts,
    revealFilename: async () => {
      // A retry can retain the same native details element in its open state.
      // Establish the closed starting state with real input before testing reveal.
      if (await browser.evaluate(`document.querySelector('${operationSelector} details')?.hasAttribute('open')`)) {
        await clickVisible(browser, `${operationSelector} details summary`);
        await browser.waitFor(`!document.querySelector('${operationSelector} details')?.hasAttribute('open')`, Boolean, "native click must close an already expanded filename");
      }
      await clickVisible(browser, `${operationSelector} details summary`);
      await browser.waitFor(`document.querySelector('${operationSelector} details')?.hasAttribute('open')`, Boolean, "native click must reveal the full filename");
      await scrollSelectorIntoView(browser, `${operationSelector} details p`);
      assert.equal(await browser.evaluate(`document.querySelector('${operationSelector} details')?.hasAttribute('open')`), true);
    },
  };
}
