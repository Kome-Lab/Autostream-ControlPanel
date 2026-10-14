import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import test from "node:test";
import { historicalProfileUpdateSource, inverseProfileUpdate, profileUpdateDelta } from "./ui188-r3-profile-delta.mts";
const root = new URL("../../..", import.meta.url);
const object = (commit: string, path: string) => execFileSync("git", ["show", `${commit}:${path}`], { cwd: root });
const before = object(profileUpdateDelta.beforeCommit, profileUpdateDelta.path);
const current = readFileSync(new URL(profileUpdateDelta.path, root.href + "/"));

test("UI188-R3-PROFILE-001: one reviewed profile path preserves historical bytes and current behavior witnesses", () => {
  assert.notDeepEqual(before, current);
  assert.deepEqual(inverseProfileUpdate(before, current), before);
  assert.deepEqual(historicalProfileUpdateSource(profileUpdateDelta.path, current, object), before);
  for (const path of profileUpdateDelta.behaviorTests) assert.ok(readFileSync(new URL(path, root.href + "/")).length > 0);
  const unrelated = Buffer.from("unrelated current bytes");
  assert.equal(historicalProfileUpdateSource("internal/store/streams.go", unrelated, () => { throw Error("unknown path must not read history"); }), unrelated);
});
test("UI188-R3-PROFILE-002: changed originals and unreviewed current edits fail closed", () => {
  assert.throws(() => inverseProfileUpdate(Buffer.concat([before, Buffer.from("\n")]), current), /R3 profile fixed original mismatch/);
  for (const bytes of [before, Buffer.concat([current, Buffer.from("\n")]), Buffer.from(current.toString().replace("s.GetProfile(ctx, kind, id)", "s.GetProfile(ctx, kind, \"wrong-id\")"))]) {
    assert.throws(() => inverseProfileUpdate(before, bytes), /R3 profile permits only the reviewed zero-changed-row repair/);
  }
});
