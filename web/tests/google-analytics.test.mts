import assert from "node:assert/strict";
import test from "node:test";

import {
  createGoogleTagCommandQueue,
  googleAnalyticsPageLocation,
  isGoogleAnalyticsPathAllowed,
  normalizeGoogleAnalyticsMeasurementID,
  shouldSendGoogleAnalyticsPageView,
} from "../src/lib/google-analytics.ts";

test("gtag queues Arguments objects instead of normal Arrays", () => {
  const dataLayer: unknown[] = [];
  const gtag = createGoogleTagCommandQueue(dataLayer);

  gtag("config", "G-ABCD1234", { send_page_view: false });

  assert.equal(dataLayer.length, 1);
  assert.equal(Array.isArray(dataLayer[0]), false);
  assert.deepEqual(Array.from(dataLayer[0] as ArrayLike<unknown>), [
    "config",
    "G-ABCD1234",
    { send_page_view: false },
  ]);
});

test("measurement IDs are normalized without accepting scriptable input", () => {
  assert.equal(normalizeGoogleAnalyticsMeasurementID(" g-abcd1234 "), "G-ABCD1234");
  assert.equal(normalizeGoogleAnalyticsMeasurementID("UA-1234"), "");
  assert.equal(normalizeGoogleAnalyticsMeasurementID("G-ABC<script>"), "");
  assert.equal(normalizeGoogleAnalyticsMeasurementID("G-ABC_123"), "");
});

test("page locations contain only origin and pathname", () => {
  assert.equal(
    googleAnalyticsPageLocation("https://panel.example.jp", "/admin/audit/"),
    "https://panel.example.jp/admin/audit/",
  );
  assert.equal(googleAnalyticsPageLocation("https://panel.example.jp", "?q=secret"), "https://panel.example.jp/");
});

test("server bootstrap suppresses only the duplicate initial page view", () => {
  const initialKey = "G-ABCD1234:https://panel.example.jp/login";
  const adminKey = "G-ABCD1234:https://panel.example.jp/admin/streams/";

  assert.equal(shouldSendGoogleAnalyticsPageView(initialKey, "", initialKey), false);
  assert.equal(shouldSendGoogleAnalyticsPageView(initialKey, initialKey, undefined), false);
  assert.equal(shouldSendGoogleAnalyticsPageView(adminKey, initialKey, initialKey), true);
});

test("analytics is limited to login and authenticated admin routes", () => {
  assert.equal(isGoogleAnalyticsPathAllowed("/login"), true);
  assert.equal(isGoogleAnalyticsPathAllowed("/login/"), true);
  assert.equal(isGoogleAnalyticsPathAllowed("/admin"), true);
  assert.equal(isGoogleAnalyticsPathAllowed("/admin/streams/"), true);

  assert.equal(isGoogleAnalyticsPathAllowed("/"), false);
  assert.equal(isGoogleAnalyticsPathAllowed("/setup"), false);
  assert.equal(isGoogleAnalyticsPathAllowed("/auth/email/confirm"), false);
  assert.equal(isGoogleAnalyticsPathAllowed("/archive/share"), false);
  assert.equal(isGoogleAnalyticsPathAllowed("/tokens"), false);
});
