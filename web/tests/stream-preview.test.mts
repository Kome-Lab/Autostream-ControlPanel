import assert from "node:assert/strict";
import test from "node:test";
import { resolveStreamPreviewURL } from "../src/lib/stream-preview.ts";

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
