import assert from "node:assert/strict";
import test from "node:test";
import {
  isStreamPreviewPlaybackReady,
  resolveStreamPreviewURL,
  selectStreamPreviewPlaybackEngine,
  signedStreamPreviewPlaybackURL,
  streamPreviewPlaybackDiagnosticMessage,
} from "../src/lib/stream-preview.ts";

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
