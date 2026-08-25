import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { translate, translations } from "../src/lib/i18n.ts";

const webRoot = fileURLToPath(new URL("..", import.meta.url));
const sourceRoot = join(webRoot, "src");
const globalsSource = readFileSync(join(sourceRoot, "app", "globals.css"), "utf8");
const statusNames = ["live", "running", "healthy", "info", "warning", "critical", "offline", "pending", "completed", "disabled"];

test("domain semantic status tokens are complete in light and dark themes", () => {
  const light = cssBlock(":root");
  const dark = cssBlock(".dark");

  for (const status of statusNames) {
    for (const suffix of ["", "-foreground", "-subtle", "-border", "-focus"]) {
      const token = `--status-${status}${suffix}:`;
      assert.match(light, new RegExp(escapeRegExp(token)), `light ${token}`);
      assert.match(dark, new RegExp(escapeRegExp(token)), `dark ${token}`);
    }
  }

  for (const token of ["--surface-raised:", "--surface-sunken:", "--surface-selected:", "--focus-critical:"]) {
    assert.match(light, new RegExp(escapeRegExp(token)), `light ${token}`);
    assert.match(dark, new RegExp(escapeRegExp(token)), `dark ${token}`);
  }
});

test("semantic status focus colors meet 3:1 against page and subtle backgrounds", () => {
  for (const [theme, selector] of [["light", ":root"], ["dark", ".dark"]] as const) {
    const block = cssBlock(selector);
    const page = customColor(block, "background");
    for (const status of statusNames) {
      const focus = customColor(block, `status-${status}-focus`);
      const subtle = customColor(block, `status-${status}-subtle`);
      const pageRatio = contrastRatio(focus, page);
      const subtleRatio = contrastRatio(focus, subtle);
      assert.ok(pageRatio >= 3, `${theme} ${status} focus/page contrast was ${pageRatio.toFixed(4)}`);
      assert.ok(subtleRatio >= 3, `${theme} ${status} focus/subtle contrast was ${subtleRatio.toFixed(4)}`);
    }
  }
});

test("contrast oracle rejects the reviewed 2.7 warning-focus fixture", () => {
  const light = cssBlock(":root");
  const oldWarningFocus = oklchToSrgb({ lightness: 0.68, chroma: 0.16, hue: 75 });
  const pageRatio = contrastRatio(oldWarningFocus, customColor(light, "background"));
  const subtleRatio = contrastRatio(oldWarningFocus, customColor(light, "status-warning-subtle"));
  assert.ok(pageRatio < 3, `negative fixture unexpectedly passed page contrast at ${pageRatio.toFixed(4)}`);
  assert.ok(subtleRatio < 3, `negative fixture unexpectedly passed subtle contrast at ${subtleRatio.toFixed(4)}`);
});

test("domain tokens are exposed through Tailwind theme aliases", () => {
  for (const status of statusNames) {
    for (const suffix of ["", "-foreground", "-subtle", "-border", "-focus"]) {
      const name = `status-${status}${suffix}`;
      assert.match(globalsSource, new RegExp(`--color-${escapeRegExp(name)}:\\s*var\\(--${escapeRegExp(name)}\\)`), name);
    }
  }
  for (const name of ["surface-raised", "surface-sunken", "surface-selected", "focus-critical"]) {
    assert.match(globalsSource, new RegExp(`--color-${name}:\\s*var\\(--${name}\\)`), name);
  }
});

test("page framing components preserve headings, breadcrumb semantics, and action separation", () => {
  const pageHeader = componentSource("page-header.tsx");
  const breadcrumbs = componentSource("breadcrumbs.tsx");
  const pageActions = componentSource("page-actions.tsx");

  assert.match(pageHeader, /<h1/);
  assert.match(pageHeader, /description/);
  assert.match(pageHeader, /breadcrumbs/);
  assert.match(pageHeader, /actions/);

  assert.match(breadcrumbs, /<nav[^>]*aria-label=/);
  assert.match(breadcrumbs, /<ol/);
  assert.match(breadcrumbs, /aria-current="page"/);

  assert.match(pageActions, /data-slot="page-actions-primary"/);
  assert.match(pageActions, /data-slot="page-actions-secondary"/);
  assert.match(pageActions, /data-slot="page-actions-overflow"/);
  assert.match(pageActions, /data-slot="page-actions-high-risk"/);
  assert.match(pageActions, /aria-label="その他の操作"/);

  const dashboard = readFileSync(join(sourceRoot, "features", "dashboard", "dashboard-view.tsx"), "utf8");
  assert.match(dashboard, /<PageHeader/);
  assert.match(dashboard, /<PageActions/);
  assert.match(dashboard, /breadcrumbs=\{/);
});

test("Shell messages resolve in Japanese and English without leaking keys or placeholders", () => {
  const shellKeys = [
    "navigationOpen",
    "navigationTitle",
    "navigationClose",
    "navigationMenu",
    "createStream",
    "accountMenu",
    "accountSettings",
    "logout",
    "authenticationPendingTitle",
    "authenticationPendingDescription",
    "goToLogin",
    "serviceHealthLoading",
    "serviceHealthEmpty",
    "serviceHealthError",
    "updateLoading",
    "updateCurrent",
    "updateRefreshing",
    "updateError",
    "updateUnknown",
  ] as const;

  for (const locale of ["ja", "en"] as const) {
    for (const key of shellKeys) {
      const message = translate(locale, key);
      assert.notEqual(message, key, `${locale} leaked ${key}`);
      assert.equal(message, translations[locale][key]);
    }
    assert.doesNotMatch(translate(locale, "serviceHealthReady", { healthy: 3, total: 4 }), /\{(?:healthy|total)\}/);
    assert.doesNotMatch(translate(locale, "serviceHealthStale", { healthy: 2, total: 4 }), /\{(?:healthy|total)\}/);
    assert.doesNotMatch(translate(locale, "updateAvailable", { version: "v1.3.0" }), /\{version\}/);
  }

  assert.equal(translate("en", "serviceHealthReady", { healthy: 3, total: 4 }), "3/4 services available");
  assert.equal(translate("ja", "serviceHealthStale", { healthy: 2, total: 4 }), "2/4 サービス稼働（更新失敗）");
});

function cssBlock(selector: string) {
  const match = globalsSource.match(new RegExp(`${escapeRegExp(selector)}\\s*\\{([\\s\\S]*?)\\n\\}`));
  assert.ok(match, `${selector} block is missing`);
  return match[1];
}

function componentSource(name: string) {
  const path = join(sourceRoot, "components", "shell", name);
  assert.equal(existsSync(path), true, `${name} is missing`);
  return readFileSync(path, "utf8");
}

type RGB = { red: number; green: number; blue: number };

function customColor(block: string, name: string) {
  const match = block.match(new RegExp(`--${escapeRegExp(name)}:\\s*oklch\\(([^)]+)\\)`));
  assert.ok(match, `--${name} is missing or is not an OKLCH color`);
  const channels = match[1].trim().split(/\s+/).map(Number);
  assert.equal(channels.length, 3, `--${name} must have three OKLCH channels`);
  assert.ok(channels.every(Number.isFinite), `--${name} contains a non-numeric OKLCH channel`);
  return oklchToSrgb({ lightness: channels[0], chroma: channels[1], hue: channels[2] });
}

function oklchToSrgb({ lightness, chroma, hue }: { lightness: number; chroma: number; hue: number }): RGB {
  const radians = hue * Math.PI / 180;
  const a = chroma * Math.cos(radians);
  const b = chroma * Math.sin(radians);
  const lPrime = lightness + 0.3963377774 * a + 0.2158037573 * b;
  const mPrime = lightness - 0.1055613458 * a - 0.0638541728 * b;
  const sPrime = lightness - 0.0894841775 * a - 1.291485548 * b;
  const l = lPrime ** 3;
  const m = mPrime ** 3;
  const s = sPrime ** 3;
  return {
    red: gammaEncode(clamp(4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s)),
    green: gammaEncode(clamp(-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s)),
    blue: gammaEncode(clamp(-0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s)),
  };
}

function contrastRatio(left: RGB, right: RGB) {
  const leftLuminance = relativeLuminance(left);
  const rightLuminance = relativeLuminance(right);
  const lighter = Math.max(leftLuminance, rightLuminance);
  const darker = Math.min(leftLuminance, rightLuminance);
  return (lighter + 0.05) / (darker + 0.05);
}

function relativeLuminance(color: RGB) {
  return 0.2126 * gammaDecode(color.red) + 0.7152 * gammaDecode(color.green) + 0.0722 * gammaDecode(color.blue);
}

function gammaEncode(channel: number) {
  return channel <= 0.0031308 ? 12.92 * channel : 1.055 * channel ** (1 / 2.4) - 0.055;
}

function gammaDecode(channel: number) {
  return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
}

function clamp(value: number) {
  return Math.min(1, Math.max(0, value));
}

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
