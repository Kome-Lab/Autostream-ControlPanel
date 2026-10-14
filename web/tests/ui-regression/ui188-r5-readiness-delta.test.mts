import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import test from "node:test";
import { readinessCopyDeltas, historicalR5ReadinessSource } from "./ui188-r5-readiness-delta.mts";
const root = new URL("../../..", import.meta.url);
const object = (ref: string, path: string) => execFileSync("git", ["show", ref + ":" + path], { cwd: root });
test("UI188-R5-COPY-001: four fixed Japanese copy differences preserve all history and reject unrelated edits", () => {
  for (const row of readinessCopyDeltas) {
    const before = object(row.beforeCommit, row.path), current = readFileSync(new URL(row.path, root.href + "/"));
    assert.deepEqual(historicalR5ReadinessSource(row.path, current, object), before);
    assert.throws(() => historicalR5ReadinessSource(row.path, Buffer.concat([current, Buffer.from("\n")]), object), /unapproved readiness source change/);
    assert.throws(() => historicalR5ReadinessSource(row.path, current, () => Buffer.concat([before, Buffer.from("wrong")])), /fixed readiness original mismatch/);
  }
  const unknown = Buffer.from("unrelated actual source");
  assert.equal(historicalR5ReadinessSource("unknown", unknown, () => { throw Error("unknown cannot read history"); }), unknown);
});
