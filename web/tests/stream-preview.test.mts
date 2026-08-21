import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";
import {
  isStreamPreviewPlaybackReady,
  resolveStreamPreviewURL,
  selectStreamPreviewPlaybackEngine,
  signedStreamPreviewPlaybackURL,
  streamPreviewPlaybackDiagnosticMessage,
} from "../src/lib/stream-preview.ts";

const previewPlayerSource = fs.readFileSync(new URL("../src/features/streams/stream-preview-player-view.tsx", import.meta.url), "utf8");
const streamPreviewSource = fs.readFileSync(new URL("../src/features/streams/stream-preview.tsx", import.meta.url), "utf8");

test("relative preview links resolve against the current panel origin", () => {
  assert.equal(
    resolveStreamPreviewURL("/stream-previews/token/index.m3u8", "https://panel.example.jp"),
    "https://panel.example.jp/stream-previews/token/index.m3u8",
  );
});

test("configured HTTPS preview origins remain usable by external players", () => {
  assert.equal(
    resolveStreamPreviewURL("https://media.example.jp/stream-previews/token/index.m3u8", "https://panel.example.jp"),
    "https://media.example.jp/stream-previews/token/index.m3u8",
  );
});

test("scriptable and malformed preview links are rejected", () => {
  assert.equal(resolveStreamPreviewURL("javascript:alert(1)", "https://panel.example.jp"), "");
  assert.equal(resolveStreamPreviewURL("https://[invalid", "https://panel.example.jp"), "");
});

test("playback waits for a signed preview URL instead of selecting a fallback", () => {
  assert.equal(signedStreamPreviewPlaybackURL(null), null);
  assert.equal(signedStreamPreviewPlaybackURL(""), null);
  assert.equal(
    signedStreamPreviewPlaybackURL("https://panel.example.jp/stream-previews/signed-token/index.m3u8?expires=123&signature=abc"),
    "https://panel.example.jp/stream-previews/signed-token/index.m3u8?expires=123&signature=abc",
  );
});

test("hls.js takes precedence when a browser advertises both MSE and native HLS", () => {
  assert.equal(selectStreamPreviewPlaybackEngine(true, true), "hlsjs");
  assert.equal(selectStreamPreviewPlaybackEngine(true, false), "hlsjs");
});

test("native HLS remains the fallback only when hls.js is unavailable", () => {
  assert.equal(selectStreamPreviewPlaybackEngine(false, true), "native");
  assert.equal(selectStreamPreviewPlaybackEngine(false, false), "unsupported");
});

test("preview remains buffering until playing has advanced the media clock", () => {
  assert.equal(isStreamPreviewPlaybackReady({ canPlay: false, playing: false, hlsFragmentBuffered: false, timeProgressed: false }), false);
  assert.equal(isStreamPreviewPlaybackReady({ canPlay: true, playing: false, hlsFragmentBuffered: false, timeProgressed: false }), false);
  assert.equal(isStreamPreviewPlaybackReady({ canPlay: false, playing: true, hlsFragmentBuffered: false, timeProgressed: false }), false);
  assert.equal(isStreamPreviewPlaybackReady({ canPlay: false, playing: false, hlsFragmentBuffered: true, timeProgressed: true }), false);
  assert.equal(isStreamPreviewPlaybackReady({ canPlay: false, playing: true, hlsFragmentBuffered: true, timeProgressed: true }), true);
});

test("preview timeout diagnostics map known HLS failures and never expose raw values", () => {
  assert.equal(
    streamPreviewPlaybackDiagnosticMessage({ source: "hls", category: "network", detail: "manifestLoadTimeOut" }),
    "HLSプレイリストの取得がタイムアウトしました。",
  );
  const message = streamPreviewPlaybackDiagnosticMessage({
    source: "hls",
    category: "network",
    detail: "https://private.example.invalid/stream-key",
  });
  assert.equal(message, "HLSネットワーク取得でエラーを検出しました。");
  assert.equal(message.includes("private.example.invalid"), false);
  assert.equal(
    streamPreviewPlaybackDiagnosticMessage({ source: "browser", reason: "stalled" }),
    "プレビューの再生が停止しました。ネットワークまたはEncoderプレビューの状態を確認してください。",
  );
  assert.equal(
    streamPreviewPlaybackDiagnosticMessage({ source: "browser", reason: "play_rejected" }),
    "ブラウザーが再生開始を許可しませんでした。",
  );
});

test("external preview uses one custom control bar and capability-gates the legacy visual participant overlay", () => {
  assert.equal(/<video[^>]*\bcontrols\b/i.test(previewPlayerSource), false);
  assert.equal((previewPlayerSource.match(/<video\b/g) ?? []).length, 1);
  assert.match(previewPlayerSource, /stream-previews\/.*\/participants/);
  assert.match(previewPlayerSource, /video_overlay_burn_in/);
  assert.match(previewPlayerSource, /!videoOverlayBurnIn/);
  assert.match(previewPlayerSource, /sr-only/);
  assert.match(previewPlayerSource, /発言中/);
  assert.match(previewPlayerSource, /preview-playback/);
  assert.match(previewPlayerSource, /preview-skip-backward/);
  assert.match(previewPlayerSource, /preview-mute/);
  assert.match(previewPlayerSource, /aria-label="プレビュー音声をミュート"/);
  assert.match(previewPlayerSource, /aria-pressed=\{isMuted\}/);
});

test("in-panel preview retains an accessible participant feed and only uses the legacy visual overlay without burn-in", () => {
  assert.equal((streamPreviewSource.match(/<video\b/g) ?? []).length, 1);
  assert.match(streamPreviewSource, /resolvePreviewParticipantsURL/);
  assert.match(streamPreviewSource, /video_overlay_burn_in/);
  assert.match(streamPreviewSource, /!videoOverlayBurnIn/);
  assert.match(streamPreviewSource, /sr-only/);
  assert.match(streamPreviewSource, /発言中/);
});
