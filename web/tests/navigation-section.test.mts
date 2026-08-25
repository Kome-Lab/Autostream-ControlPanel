import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import test from "node:test";

import {
  createNavigationSectionsState,
  isNavigationSectionOpen,
  navigationSectionsStorageKey,
  navigationSectionStateKey,
  restoreNavigationSectionsState,
  serializeNavigationSectionsState,
  synchronizeNavigationSectionsState,
  toggleNavigationSection,
} from "../src/lib/navigation-section-state.ts";

const navigationPersistenceSource = readFileSync(new URL("../src/components/shell/use-navigation-sections.ts", import.meta.url), "utf8");

test("an active navigation section can be closed and reopened explicitly", () => {
  const active = createNavigationSectionsState(["operations", "monitoring"], "operations", "monitoring");
  assert.equal(isNavigationSectionOpen(active, "monitoring"), true);

  const closed = toggleNavigationSection(active, "monitoring");
  assert.equal(isNavigationSectionOpen(closed, "monitoring"), false);
  assert.equal(synchronizeNavigationSectionsState(closed, "monitoring"), closed);

  assert.equal(isNavigationSectionOpen(toggleNavigationSection(closed, "monitoring"), "monitoring"), true);
});

test("an automatically opened section stays open after its route becomes inactive", () => {
  const active = createNavigationSectionsState(["operations", "monitoring"], "operations", "monitoring");
  const inactive = synchronizeNavigationSectionsState(active, null);
  assert.equal(isNavigationSectionOpen(inactive, "monitoring"), true);
});

test("a manually closed section stays closed within the same section and reopens on a later visit", () => {
  const active = createNavigationSectionsState(["operations", "monitoring"], "operations", "monitoring");
  const closed = toggleNavigationSection(active, "monitoring");
  assert.equal(isNavigationSectionOpen(synchronizeNavigationSectionsState(closed, "monitoring"), "monitoring"), false);

  const inactive = synchronizeNavigationSectionsState(closed, "operations");
  assert.equal(isNavigationSectionOpen(inactive, "monitoring"), false);
  const revisited = synchronizeNavigationSectionsState(inactive, "monitoring");
  assert.equal(isNavigationSectionOpen(revisited, "monitoring"), true);
});

test("switching sections preserves previously opened sections", () => {
  const monitoring = createNavigationSectionsState(["operations", "monitoring", "administration"], "operations", "monitoring");
  const administration = synchronizeNavigationSectionsState(monitoring, "administration");
  assert.equal(isNavigationSectionOpen(administration, "operations"), true);
  assert.equal(isNavigationSectionOpen(administration, "monitoring"), true);
  assert.equal(isNavigationSectionOpen(administration, "administration"), true);
});

test("mobile navigation remounts can reuse the lifted section state", () => {
  const initial = createNavigationSectionsState(["operations", "monitoring"], "operations", null);
  const opened = toggleNavigationSection(initial, "monitoring");
  assert.equal(isNavigationSectionOpen(opened, "monitoring"), true);
  assert.equal(isNavigationSectionOpen(synchronizeNavigationSectionsState(opened, null), "monitoring"), true);
});

test("a section keeps the same React key when its active route changes", () => {
  assert.equal(navigationSectionStateKey("monitoring"), "monitoring");
});

test("admin navigation restores and persists section state across reloads", () => {
  assert.match(navigationPersistenceSource, /navigationSectionsStorageKey/);
  assert.match(navigationPersistenceSource, /localStorage\.getItem/);
  assert.match(navigationPersistenceSource, /localStorage\.setItem/);
  assert.match(navigationSectionsStorageKey, /:v1$/);

  const initial = createNavigationSectionsState(["operations", "monitoring", "administration"], "operations", "operations");
  const opened = toggleNavigationSection(initial, "monitoring");
  const restored = restoreNavigationSectionsState(
    ["operations", "monitoring", "administration"],
    serializeNavigationSectionsState(opened, ["operations", "monitoring", "administration"]),
    "operations",
    "operations",
  );
  assert.equal(isNavigationSectionOpen(restored, "monitoring"), true);
});

test("invalid or stale navigation storage falls back without adding unknown sections", () => {
  const malformed = restoreNavigationSectionsState(["operations", "monitoring"], "not-json", "operations", "monitoring");
  assert.equal(isNavigationSectionOpen(malformed, "operations"), true);
  assert.equal(isNavigationSectionOpen(malformed, "monitoring"), true);

  const restored = restoreNavigationSectionsState(
    ["operations", "monitoring"],
    JSON.stringify({ openByKey: { operations: false, unknown: true } }),
    "operations",
    "monitoring",
  );
  assert.deepEqual(restored.openByKey, { operations: false, monitoring: true });
});

test("responsive admin surfaces do not depend on a bare hidden utility", () => {
  const shellDirectory = new URL("../src/components/shell/", import.meta.url);
  const sources = [
    ...readdirSync(shellDirectory, { withFileTypes: true })
      .filter((entry) => entry.isFile() && entry.name.endsWith(".tsx"))
      .map((entry) => readFileSync(new URL(entry.name, shellDirectory), "utf8")),
    readFileSync(new URL("../src/features/dashboard/dashboard-view.tsx", import.meta.url), "utf8"),
  ];
  const responsiveDisplay = /^(?:sm|md|lg|xl|2xl):(block|flex|grid|inline-flex)$/;
  const conflictingClassLists = sources.flatMap((source) => [...source.matchAll(/"([^"\r\n]*)"/g)])
    .map(([, value]) => value.trim().split(/\s+/))
    .filter((tokens) => tokens.includes("hidden") && tokens.some((token) => responsiveDisplay.test(token)));

  assert.deepEqual(
    conflictingClassLists,
    [],
    "bare .hidden can be forced with !important by injected styles and must not gate desktop navigation",
  );
});
