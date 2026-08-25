import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

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

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
