import "./component-loader.mts";
import assert from "node:assert/strict";
import test from "node:test";
const { currentTableSearch, subscribeTableLocation, updateTableLocation } = await import("../../src/lib/ui-v2/table-location.ts");
test("UI-TABLE-007: production URL subscription follows popstate and preserves framework history state", () => {
  const events = new EventTarget();
  let location = new URL("https://ui.test/admin/streams/?view=retained&streams.page=2#detail");
  const state = { framework: { tree: "retained" } };
  const writes: string[] = [];
  const previous = Object.getOwnPropertyDescriptor(globalThis, "window");
  Object.defineProperty(globalThis, "window", { configurable: true, value: {
    get location() { return location; },
    history: { state, replaceState(value: unknown, _title: string, url: string) { assert.equal(value, state); writes.push(url); location = new URL(url, location); } },
    addEventListener: events.addEventListener.bind(events),
    removeEventListener: events.removeEventListener.bind(events),
    dispatchEvent: events.dispatchEvent.bind(events),
  } });
  try {
    let notifications = 0;
    const release = subscribeTableLocation(() => { notifications++; });
    const policy = { key: "streams", sorts: ["name"], filters: { status: ["live"] } };
    updateTableLocation(policy, current => ({ ...current, pageIndex: 1 }));
    assert.equal(writes.length, 1);
    assert.match(currentTableSearch() || "", /view=retained/);
    assert.equal(location.hash, "#detail");
    assert.equal(notifications, 1);
    location = new URL("https://ui.test/admin/streams/?streams.page=2#detail");
    events.dispatchEvent(new Event("popstate"));
    assert.equal(currentTableSearch(), "?streams.page=2");
    assert.equal(notifications, 2);
    release();
    events.dispatchEvent(new Event("popstate"));
    assert.equal(notifications, 2);
  } finally {
    if (previous) Object.defineProperty(globalThis, "window", previous);
    else Reflect.deleteProperty(globalThis, "window");
  }
});
