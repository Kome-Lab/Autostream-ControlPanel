import assert from "node:assert/strict";
import { createHash } from "node:crypto";

// Preserve the historical source/fixture contract by reversing only this fixed
// repair, whose versions and integrity were checked against npm and vendors.
export const cp176Base = "d0f76e522dabc3bb1619c6a134b0cde0a38a41bb";
export const cp176Paths = ["web/package.json", "web/package-lock.json"];
const hashes: Record<string, { before: string; after: string }> = {
  "web/package.json": { before: "156b43c64b43a0072bc134d52651b75f08d8d08ebcb23453ac6eb7c03668b64a", after: "c8e6568bd508eb3c1139584baad00d00bd37841aaf6a34b6343e1c56be691b21" },
  "web/package-lock.json": { before: "5803f4f2e1576f943730ccaa1c25744efeef89bf98602a5c917d25a13b8b4993", after: "2c4ab9c7aae07465c8fc3e1980cd49a8139bc8a7f39a757e0e53ec7e772b6db4" },
};
const digest = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");
const versions: Record<string, [string, string]> = {
  "node_modules/next": ["16.3.3", "16.3.6"],
  "node_modules/@next/env": ["16.3.3", "16.3.6"],
  ...Object.fromEntries(["darwin-arm64", "darwin-x64", "linux-arm64-gnu", "linux-arm64-musl", "linux-x64-gnu", "linux-x64-musl", "win32-arm64-msvc", "win32-x64-msvc"].map(arch => ["node_modules/@next/swc-" + arch, ["16.3.3", "16.3.6"] as [string, string]])),
  "node_modules/source-map-js": ["1.2.1", "1.2.2"],
  "node_modules/brace-expansion": ["1.1.18", "1.1.21"],
  "node_modules/@typescript-eslint/typescript-estree/node_modules/brace-expansion": ["5.0.9", "5.0.12"],
};

export function inverseCP176DependencyRepair(path: string, original: Buffer, current: Buffer): Buffer {
  const expected = hashes[path];
  assert.ok(expected, "unknown UI176 dependency path");
  assert.equal(digest(original), expected.before, "UI176 original dependency hash");
  // An unchanged snapshot is still used by historical negative-fixture tests.
  if (current.equals(original)) return original;
  assert.equal(digest(current), expected.after, "normalization permits only exact specified name edits or fixed UI176 bytes; exact repaired dependency hash required");
  const before = JSON.parse(original.toString()), after = JSON.parse(current.toString());
  if (path === "web/package.json") {
    assert.equal(before.dependencies.next, "16.3.3");
    assert.equal(after.dependencies.next, "16.3.6");
    after.dependencies.next = before.dependencies.next;
    for (const [name, version] of Object.entries({ "source-map-js": "1.2.2", "brace-expansion@1.x": "1.1.21", "brace-expansion@5.x": "5.0.12" })) {
      assert.equal(Object.hasOwn(before.overrides, name), false);
      assert.equal(after.overrides[name], version);
      delete after.overrides[name];
    }
  } else {
    assert.deepEqual(Object.keys(after.packages).sort(), Object.keys(before.packages).sort(), "UI176 no package addition or removal");
    assert.equal(after.packages[""].dependencies.next, "16.3.6");
    after.packages[""].dependencies.next = before.packages[""].dependencies.next;
    for (const [location, [oldVersion, newVersion]] of Object.entries(versions)) {
      assert.equal(before.packages[location].version, oldVersion);
      assert.equal(after.packages[location].version, newVersion);
      after.packages[location] = before.packages[location];
    }
  }
  assert.deepEqual(after, before, "UI176 preserves every unrelated package, script, override and lock field");
  return original;
}
