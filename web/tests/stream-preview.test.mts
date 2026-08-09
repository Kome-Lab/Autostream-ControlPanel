import assert from "node:assert/strict";
import test from "node:test";
import { resolveStreamPreviewURL, signedStreamPreviewPlaybackURL } from "../src/lib/stream-preview.ts";

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
