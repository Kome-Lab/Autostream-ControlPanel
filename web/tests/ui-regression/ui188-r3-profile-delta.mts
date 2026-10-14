import assert from "node:assert/strict";
import { createHash } from "node:crypto";

// A reviewed one-path extension. The frozen UI188 48-path contract is intact.
export const profileUpdateDelta = Object.freeze({
  path: "internal/store/profiles.go",
  beforeCommit: "de8ef12171e069c5dc93610df9d2b58dd3481b8b",
  beforeSha256: "c1d85fabfaf7f9d34dc45ab828f83636f59bf90ccf0ba2e01654d8161f40a054",
  afterSha256: "ecf4bb36c790593ebb62f043ed689d7af2072d03b3f00c177908d0605ca5e991",
  specification: "UI188-R3 section 3: zero changed rows require the same ctx/id/kind DB read; preserve errors and YouTube fences",
  allowedChanges: "private fixed-time helper; preserve UPDATE and RowsAffected errors; replace zero-row absence inference with existing GetProfile",
  behaviorTests: ["internal/store/profiles_update_test.go", "internal/store/profiles_update_mariadb_test.go"],
});
const hash = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");

export function inverseProfileUpdate(before: Buffer, current: Buffer): Buffer {
  assert.equal(hash(before), profileUpdateDelta.beforeSha256, "R3 profile fixed original mismatch");
  assert.equal(hash(current), profileUpdateDelta.afterSha256, "R3 profile permits only the reviewed zero-changed-row repair");
  return before;
}

export function historicalProfileUpdateSource(path: string, current: Buffer, object: (commit: string, path: string) => Buffer): Buffer {
  return path === profileUpdateDelta.path
    ? inverseProfileUpdate(object(profileUpdateDelta.beforeCommit, path), current)
    : current;
}
