import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { ciClosureBase, ciClosurePaths, inverseCIClosure } from "./ci-closure-deltas.mts";
import { createNormalizedReader } from "./source-normalization.mts";

const root = fileURLToPath(new URL("../../..", import.meta.url));
const object = (commit: string, path: string) => execFileSync("git", ["show", commit + ":" + path], { cwd: root, maxBuffer: 32 * 1024 * 1024 });
const raw = (path: string) => object("HEAD", path);
const io = { raw, object, exists: (path: string) => existsSync(resolve(root, path)) };
const expectedPaths = [
  "internal/httpapi/server_start_preparation_claim_test.go",
  "internal/store/memory_stream_start_claim.go",
  "internal/store/stream_start_claim_mariadb.go",
  "web/tests/helpers/browser-harness.mts",
  "web/tests/ui-browser-account-scenarios.mts"
];

test("UI-CI-CLOSURE-001: exactly five changed sources bind fixed before and actual after bytes", () => {
  assert.deepEqual(ciClosurePaths, expectedPaths);
  assert.equal(new Set(ciClosurePaths).size, 5);
  for (const path of ciClosurePaths) {
    const before = object(ciClosureBase, path), current = raw(path);
    assert.notDeepEqual(before, current);
    assert.deepEqual(inverseCIClosure(path, before, current), before);
  }
  for (const name of ["protected.json", "source-normalization.json", "approved-source-deltas.json", "ci-source-deltas.json"]) {
    const path = "web/tests/fixtures/ui-regression/" + name;
    assert.deepEqual(raw(path), object(ciClosureBase, path));
  }
});

test("UI-CI-CLOSURE-002: omitted, truncated, extra, reverted and wrong-original bytes fail closed", () => {
  for (const path of ciClosurePaths) {
    const before = object(ciClosureBase, path), current = raw(path);
    for (const mutant of [Buffer.alloc(0), current.subarray(0, current.length - 1), Buffer.concat([current, Buffer.from("drift")]), before]) {
      assert.throws(() => inverseCIClosure(path, before, mutant), /exact specified name edits/);
    }
    assert.throws(() => inverseCIClosure(path, Buffer.concat([before, Buffer.from("drift")]), current), /fixed original hash/);
    assert.throws(() => inverseCIClosure("unknown", before, current), /unknown CI closure path/);
  }
});

test("UI-CI-CLOSURE-003: current-source absence or mutation cannot be replaced with historical bytes", () => {
  for (const path of ciClosurePaths) {
    const reader = createNormalizedReader(root, io);
    const mapped = reader.manifest.currentMappings.find(row => row.newPath === path)?.oldPath ?? path;
    const missing = new Error("current CI closure source missing");
    const absent = createNormalizedReader(root, { ...io, raw: p => { if (p === path) throw missing; return raw(p); } });
    assert.throws(() => absent.read(mapped), error => error === missing);
    const changed = createNormalizedReader(root, { ...io, raw: p => p === path ? Buffer.concat([raw(p), Buffer.from("drift")]) : raw(p) });
    assert.throws(() => changed.read(mapped), /exact specified name edits/);
  }
});
