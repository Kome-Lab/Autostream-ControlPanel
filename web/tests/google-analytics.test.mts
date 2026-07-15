import assert from "node:assert/strict";
import test from "node:test";

import {
  googleAnalyticsPageLocation,
  isGoogleAnalyticsPathAllowed,
  normalizeGoogleAnalyticsMeasurementID,
} from "../src/lib/google-analytics.ts";

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
