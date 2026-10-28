import assert from "node:assert/strict";
import type { BrowserHarness } from "./browser-harness.mts";
import { withTimeout } from "./browser-process-timing.mts";
import { createP3MediaFixture, labels, selectImage, type ImageKind } from "./p3-media-browser-fixture.mts";
import { createP3Proof, operationSelector } from "./p3-media-browser-proof.mts";

type Context = { browser: BrowserHarness; fixture: ReturnType<typeof createP3MediaFixture>; proof: ReturnType<typeof createP3Proof>; locale: "ja" | "en"; width: number };
const snapshotPath = /^\/media-assets\/upload-sessions\/[^/]+\/assets$/;
const originalPath = /^\/media-assets$/;
const conversionPath = /^\/media-assets\/asset-\d+\/variants$/;
const deletePath = /^\/media-assets\/asset-\d+$/;
const entered = (value: { entered: { promise: Promise<void> } }, label: string) => withTimeout(value.entered.promise, 10_000, `${label} actual request did not enter`);
const candidates = (browser: BrowserHarness) => browser.evaluate(`([...document.querySelectorAll('[data-cover-candidate]')].map(el=>({variant:el.getAttribute('data-cover-candidate'),name:el.querySelector('[data-cover-candidate-name] p')?.textContent,src:el.querySelector('img')?.getAttribute('src'),start:el.querySelector('[data-cover-candidate-actions] button')?.textContent})))`);

async function checkUnknown(context: Context, kind: ImageKind, name: string, count: number, unobserved: boolean) {
  const { browser, fixture, proof, locale } = context;
  const uploads = fixture.count("POST", originalPath), deletes = fixture.count("DELETE", /^\/media-assets\/[^/]+$/);
  const read = fixture.gate("GET", snapshotPath, unobserved ? "unobserved" : "normal");
  try {
    await proof.action(locale, "check"); await entered(read, "read-only operation inspection");
    await proof.phase(kind, "checking", count, name); await proof.capture(`${kind}-checking-${unobserved ? "not-observed" : "committed"}`);
    read.release.resolve();
    await proof.phase(kind, "outcome_unknown", count, name);
    const current = await proof.observe();
    assert.equal(current.operation?.text.includes(labels[locale].resume), !unobserved);
    assert.equal(fixture.count("POST", originalPath), uploads, "GET inspection must not resend an original");
    assert.equal(fixture.count("DELETE", /^\/media-assets\/[^/]+$/), deletes, "GET inspection must not clean any uncertain or shared original");
  } finally { read.release.resolve(); }
}

// Every phase is held by its corresponding actual Fetch response. Ready is
// asserted only after the actual conversion response, not after a timer.
export async function exerciseImageAttempt(context: Context, kind: ImageKind, name: string, count: number, badges: number,
  mode: "normal" | "failed" | "unknown" = "normal", options: { trigger?: () => Promise<unknown>; layouts?: boolean; unknownCleanup?: boolean } = {}) {
  const { browser, fixture, proof, locale } = context;
  const before = await candidates(browser), title = (await proof.observe()).title;
  const prepare = fixture.app.state.sessionSerial === 0
    ? fixture.gate("POST", /^\/media-assets\/upload-sessions$/) : fixture.gate("GET", snapshotPath);
  const upload = fixture.gate("POST", originalPath, mode === "unknown" ? "committed-unknown" : "normal");
  const convert = fixture.gate("POST", conversionPath, mode === "failed" ? "failed-variant" : "normal");
  const cleanup = mode === "failed" ? fixture.gate("DELETE", deletePath, options.unknownCleanup ? "unknown-delete" : "normal") : undefined;
  let operationID = "", assetID = "";
  const held = async (phase: string) => {
    await proof.phase(kind, phase, count, name);
    assert.deepEqual(await candidates(browser), before, "existing candidates, previews and the one start selection remain unchanged");
    assert.equal((await proof.observe()).title, title, "a stage transition cannot erase another input");
    assert.equal(fixture.app.state.creates.length, 0, "disabled-save negative must never create a stream");
    await proof.capture(`${kind}-${name.slice(0, 12).replace(/[^A-Za-z0-9_-]/g, "x")}-${phase}`);
  };
  try {
    await (options.trigger ? options.trigger() : selectImage(browser, kind, name, count));
    await entered(prepare, "preparation"); await held("preparing");
    operationID = (await proof.observe()).operation!.id;
    assert.match(operationID, /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
    if (name.length > 60) {
      await proof.revealFilename(); await browser.pressNativeKey("Space");
      await browser.waitFor(`!document.querySelector('${operationSelector} details')?.hasAttribute('open')`, Boolean, "keyboard must close the full filename");
      await browser.pressNativeKey("Space"); await browser.waitFor(`document.querySelector('${operationSelector} details')?.hasAttribute('open')`, Boolean, "keyboard must reveal the full filename");
      await proof.capture(`${kind}-full-filename-keyboard`);
    }
    prepare.release.resolve(); await entered(upload, "original upload"); await held("uploading");
    assetID = `asset-${fixture.app.state.uploads}`;
    const original = fixture.app.state.assets.get(assetID); assert.ok(original);
    assert.equal(original.client_operation_id, operationID, "the displayed operation must be the nonce actually sent in multipart metadata");
    assert.equal(original.usage_type, kind === "cover" ? "video_cover" : "scene_background");
    if (kind === "cover" && count === 3) {
      assert.match(await browser.evaluate<string>(`document.querySelector('[data-cover-upload-count]')?.textContent||''`), /4\/4/);
      const sent = fixture.app.state.uploads;
      await assert.rejects(selectImage(browser, "cover", "blocked-fifth.png"), /image upload disabled/);
      assert.equal(fixture.app.state.uploads, sent, "the fourth pending original reserves its slot before readiness");
    }
    if (options.layouts) await proof.layouts(`${kind}-pending`, context.width);
    upload.release.resolve();
    if (mode === "unknown") {
      await held("outcome_unknown");
      await checkUnknown(context, kind, name, count, true);
      await checkUnknown(context, kind, name, count, false);
      assert.equal((await proof.observe()).operation?.id, operationID);
      const originals = fixture.app.state.uploads;
      await proof.action(locale, "resume"); await entered(convert, "explicit conversion resume");
      assert.equal(fixture.app.state.uploads, originals, "explicit conversion resumes the same original");
    } else await entered(convert, "conversion");
    await held("converting"); convert.release.resolve();
    if (cleanup) {
      await entered(cleanup, "exact-session cleanup"); await held("cleaning");
      assert.equal(fixture.app.state.deletes.at(-1), `/media-assets/${assetID}`);
      assert.equal(fixture.app.state.deleteSessions.at(-1), original.upload_session_id);
      assert.equal(fixture.app.state.claimedAssets.has(assetID), false);
      cleanup.release.resolve();
      if (options.unknownCleanup) {
        await held("outcome_unknown");
        const beforeDeletes = fixture.app.state.deletes.length, read = fixture.gate("GET", snapshotPath);
        try {
          await proof.action(locale, "check"); await entered(read, "cleanup tombstone inspection");
          await held("checking"); read.release.resolve();
          await browser.waitFor(`document.querySelector('${operationSelector}')?.getAttribute('data-media-upload-phase')==='failed'`, Boolean, "only the read tombstone may confirm cleanup");
          assert.equal(fixture.app.state.deletes.length, beforeDeletes, "a lost DELETE receipt must not cause another DELETE");
        } finally { read.release.resolve(); }
      }
      await held("failed");
      await assert.rejects(selectImage(browser, kind, name), /image upload disabled/);
      return { assetID, operationID, failed: true };
    }
    await proof.ready(count + (kind === "cover" ? 1 : 0), locale, badges + 1);
    await proof.capture(`${kind}-ready-unsaved`);
    assert.equal(fixture.app.state.assets.get(assetID)?.client_operation_id, operationID);
    return { assetID, operationID, failed: false };
  } finally { prepare.release.resolve(); upload.release.resolve(); convert.release.resolve(); cleanup?.release.resolve(); }
}
