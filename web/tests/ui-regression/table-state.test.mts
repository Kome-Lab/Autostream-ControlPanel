import assert from "node:assert/strict";
import test from "node:test";
import { boundedPageIndex, parseTableURL, tablePageSize, writeTableURL } from "../../src/lib/ui-v2/table-state.ts";

const policy = { key: "streams", sorts: ["name", "status"], filters: { status: ["live", "starting", "failed"] } };

test("UI-TABLE-004: empty data, shrinking data and invalid page sizes stay bounded", () => {
  assert.equal(boundedPageIndex(9, 17, 8), 2);
  assert.equal(boundedPageIndex(2, 0, 20), 0);
  assert.equal(boundedPageIndex(-1, 30, 8), 0);
  assert.equal(boundedPageIndex(Infinity, 30, 8), 0);
  assert.equal(tablePageSize(13), 8);
  assert.equal(tablePageSize(100), 100);
});

test("UI-TABLE-005: URL parsing admits only approved sort and enum filters", () => {
  assert.deepEqual(parseTableURL("streams.page=-8&streams.size=999&streams.sort=email&streams.filter.status=token&search=private", policy), {
    pageIndex: 0, pageSize: 8, sort: undefined, filters: [],
  });
  assert.deepEqual(parseTableURL("streams.page=2&streams.size=20&streams.sort=status&streams.order=desc&streams.filter.status=live", policy), {
    pageIndex: 2, pageSize: 20, sort: { id: "status", desc: true }, filters: [{ id: "status", value: "live" }],
  });
});

test("UI-TABLE-006: URL updates preserve detail/create parameters without persisting free input", () => {
  const search = writeTableURL("stream_id=existing&mode=edit&streams.sort=name", {
    pageIndex: 1, pageSize: 50, sort: { id: "secret", desc: true },
    filters: [{ id: "status", value: "live" }, { id: "search", value: "private@example.test" }],
  }, policy);
  const params = new URLSearchParams(search);
  assert.equal(params.get("stream_id"), "existing");
  assert.equal(params.get("mode"), "edit");
  assert.equal(params.get("streams.page"), "1");
  assert.equal(params.get("streams.size"), "50");
  assert.equal(params.get("streams.filter.status"), "live");
  assert.equal(params.has("streams.sort"), false);
  assert.doesNotMatch(search, /private|secret|search/);
});
