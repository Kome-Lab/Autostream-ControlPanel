import assert from "node:assert/strict";
import test from "node:test";

import {
  createNavigationSectionsState,
  isNavigationSectionOpen,
  navigationSectionStateKey,
  synchronizeNavigationSectionsState,
  toggleNavigationSection,
} from "../src/lib/navigation-section-state.ts";

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
