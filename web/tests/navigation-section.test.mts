import assert from "node:assert/strict";
import test from "node:test";

import {
  createNavigationSectionState,
  navigationSectionStateKey,
  toggleNavigationSection,
} from "../src/lib/navigation-section-state.ts";

test("an active navigation section can be closed and reopened explicitly", () => {
  const active = createNavigationSectionState(false, true);
  assert.deepEqual(active, { open: true });

  const closed = toggleNavigationSection(active);
  assert.deepEqual(closed, { open: false });

  assert.deepEqual(toggleNavigationSection(closed), { open: true });
});

test("a section reopens only when it becomes active again", () => {
  const activeKey = navigationSectionStateKey("monitoring", true);
  const inactiveKey = navigationSectionStateKey("monitoring", false);
  assert.notEqual(activeKey, inactiveKey);

  const closedDuringVisit = toggleNavigationSection(createNavigationSectionState(false, true));
  assert.equal(closedDuringVisit.open, false);
  assert.equal(createNavigationSectionState(false, true).open, true);
});

test("the initially-open section remains open while inactive", () => {
  assert.deepEqual(createNavigationSectionState(true, false), { open: true });
});
