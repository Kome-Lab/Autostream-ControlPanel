import assert from "node:assert/strict";
import { createHash } from "node:crypto";

// Preserve the historical source/fixture contract by reversing only this fixed
// repair, whose versions and integrity were checked against npm and vendors.
export const cp176Base = "d0f76e522dabc3bb1619c6a134b0cde0a38a41bb";
export const cp176Paths = ["web/package.json", "web/package-lock.json"];
const hashes: Record<string, { before: string; after: string }> = {
  "web/package.json": { before: "156b43c64b43a0072bc134d52651b75f08d8d08ebcb23453ac6eb7c03668b64a", after: "cee9ab572e4384dd52ccd78d31209f0a0a1f4cc9439fa8f14928aa7c52447d04" },
  "web/package-lock.json": { before: "5803f4f2e1576f943730ccaa1c25744efeef89bf98602a5c917d25a13b8b4993", after: "27e911cc1ec4d2643b75b2f77506eafba58587775771995e6807558396ad80aa" },
};
const digest = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");
const versions: Record<string, [string, string]> = {
  "node_modules/next": ["16.3.3", "16.3.6"],
  "node_modules/@next/env": ["16.3.3", "16.3.6"],
  ...Object.fromEntries(["darwin-arm64", "darwin-x64", "linux-arm64-gnu", "linux-arm64-musl", "linux-x64-gnu", "linux-x64-musl", "win32-arm64-msvc", "win32-x64-msvc"].map(arch => ["node_modules/@next/swc-" + arch, ["16.3.3", "16.3.6"] as [string, string]])),
  "node_modules/source-map-js": ["1.2.1", "1.2.2"],
  "node_modules/brace-expansion": ["1.1.18", "1.1.21"],
  "node_modules/@typescript-eslint/typescript-estree/node_modules/brace-expansion": ["5.0.9", "5.0.12"],
  "node_modules/sharp": ["0.35.4", "0.35.5"],
  ...Object.fromEntries(["darwin-arm64", "darwin-x64", "freebsd-wasm32", "linux-arm", "linux-arm64", "linux-ppc64", "linux-riscv64", "linux-s390x", "linux-x64", "linuxmusl-arm64", "linuxmusl-x64", "wasm32", "webcontainers-wasm32", "win32-arm64", "win32-ia32", "win32-x64"].map(arch => ["node_modules/@img/sharp-" + arch, ["0.35.4", "0.35.5"] as [string, string]])),
  ...Object.fromEntries(["darwin-arm64", "darwin-x64", "linux-arm", "linux-arm64", "linux-ppc64", "linux-riscv64", "linux-s390x", "linux-x64", "linuxmusl-arm64", "linuxmusl-x64"].map(arch => ["node_modules/@img/sharp-libvips-" + arch, ["1.3.3", "1.3.4"] as [string, string]])),
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
    assert.equal(before.overrides.sharp, "0.35.4");
    assert.equal(after.overrides.sharp, "0.35.5");
    after.overrides.sharp = before.overrides.sharp;
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
