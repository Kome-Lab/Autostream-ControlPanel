import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
const hash = (b: Buffer) => createHash("sha256").update(b).digest("hex");
const bytes = readFileSync(new URL("../fixtures/ui-regression/ui188-r5-readiness-deltas.json", import.meta.url));
assert.equal(hash(bytes), "bb6e7aa816184ae4503daab7c9c589927530d8d6273f8c380ce3ff75797378ed", "R5 independent finite copy contract changed");
type Row = { path: string; beforeCommit: string; beforeSha256: string; afterSha256: string; allowedReplacements: readonly { before: string; after: string; count: number }[] };
export const readinessCopyDeltas = JSON.parse(bytes.toString("utf8")).records as readonly Row[];
assert.equal(readinessCopyDeltas.length, 4);
assert.equal(new Set(readinessCopyDeltas.map(r => r.path)).size, 4);
export function historicalR5ReadinessSource(path: string, current: Buffer, object: (ref: string, path: string) => Buffer): Buffer {
  const row = readinessCopyDeltas.find(r => r.path === path);
  if (!row) return current;
  const before = object(row.beforeCommit, path);
  assert.equal(hash(before), row.beforeSha256, "R5 fixed readiness original mismatch");
  let forward = before.toString("utf8");
  for (const edit of row.allowedReplacements) {
    assert.equal(edit.count, 1);
    assert.equal(forward.split(edit.before).length - 1, 1, "R5 finite copy input count");
    forward = forward.replace(edit.before, edit.after);
  }
  assert.deepEqual(current, Buffer.from(forward), "R5 unapproved readiness source change");
  assert.equal(hash(current), row.afterSha256, "R5 fixed readiness after mismatch");
  return before;
}
