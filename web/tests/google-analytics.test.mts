import assert from "node:assert/strict";
import test from "node:test";

import {
  googleAnalyticsPageLocation,
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
