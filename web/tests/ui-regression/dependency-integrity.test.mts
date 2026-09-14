import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync, existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { assertCIManifest, assertCISourceDelta, assertLockDelta, assertPackageRegistration } from "./ci-source-deltas.mts";

const root = fileURLToPath(new URL("../../..", import.meta.url));
const read = (path: string) => readFileSync(new URL("../../../" + path, import.meta.url));
const base = "28a9ff71ea9925722000a2933c59c79b8cc8b9b2";
const before = (path: string) => execFileSync("git", ["show", base + ":" + path], { cwd: root });
const supplement = JSON.parse(read("web/tests/fixtures/ui-regression/ci-source-deltas.json").toString("utf8"));

test("UI-DEPENDENCY-001: actual package/lock has only two type dependencies, exact peers and one current registration", () => {
  assertCIManifest(supplement);
  assertPackageRegistration(JSON.parse(before("web/package.json").toString()), JSON.parse(read("web/package.json").toString()));
  assertLockDelta(before("web/package-lock.json"), read("web/package-lock.json"));
  assert.deepEqual(read("go.mod"), before("go.mod")); assert.deepEqual(read("go.sum"), before("go.sum"));
  assert.deepEqual(read("web/tsconfig.ui-regression.json"), before("web/tsconfig.ui-regression.json"), "all original strict roots/options stay intact");
  const old = JSON.parse(before("web/package-lock.json").toString());
  assert.equal(old.packages["node_modules/hls.js"].version, "1.7.0");
});

test("UI-DEPENDENCY-002: actual supplement rejects unknown paths, wrong original, removed workflow test and broad local exemptions", () => {
  assert.throws(() => assertCIManifest(null), /supplement/);
  assert.throws(() => assertCIManifest({ ...supplement, protectedDeltas: [...supplement.protectedDeltas, { path: "go.mod" }] }), /supplement/);
  for (const row of supplement.protectedDeltas as { path: string }[]) {
    const old = before(row.path), current = read(row.path);
    assertCISourceDelta(row.path, old, current, supplement);
    assert.throws(() => assertCISourceDelta("other", old, current, supplement), /unknown/);
    assert.throws(() => assertCISourceDelta(row.path, Buffer.concat([old, Buffer.from("changed")]), current, supplement), /original hash/);
  }
  const path = "internal/security/workflow_test.go", source = read(path).toString();
  for (const replacement of ["nil", 'nil; strings.HasPrefix(string(data), "./")']) {
    const mutant = source.replace("checkWorkflowSources(root, data)", replacement); assert.notEqual(mutant, source);
    assert.throws(() => assertCISourceDelta(path, before(path), Buffer.from(mutant), supplement), /structural policy/);
  }
  assert.throws(() => assertCISourceDelta(path, before(path), Buffer.from("package security\n"), supplement), /structural policy/);
});

test("UI-DEPENDENCY-003: lock negatives reach missing peers, altered old integrity/version and extra dependency failures", () => {
  const original = before("web/package-lock.json"), current = JSON.parse(read("web/package-lock.json").toString());
  for (const change of [
    (lock: typeof current) => { delete lock.packages["node_modules/@svta/cml-utils"]; },
    (lock: typeof current) => { lock.packages["node_modules/hls.js"].version = "1.6.17"; },
    (lock: typeof current) => { lock.packages["node_modules/next"].integrity = "changed"; },
    (lock: typeof current) => { lock.packages["node_modules/unrelated"] = { version: "1.0.0" }; },
    (lock: typeof current) => { lock.packages["node_modules/@svta/cml-cmcd"].peerDependencies["@svta/cml-utils"] = "*"; },
    (lock: typeof current) => { lock.packages[""].devDependencies.eventemitter3 = "^5.0.4"; },
  ]) { const mutant = structuredClone(current); change(mutant); assert.throws(() => assertLockDelta(original, Buffer.from(JSON.stringify(mutant))), /closure|unchanged|disagree/); }
  const pkg = JSON.parse(read("web/package.json").toString()); pkg.scripts["test:ui-regression:browser-contracts"] += " tests/ui-regression/render-state.test.mts";
  assert.throws(() => assertPackageRegistration(JSON.parse(before("web/package.json").toString()), pkg), /registered once/);
});

test("UI-DEPENDENCY-004: installed dependency versions must match the new lock; a stale local cache is a failure", () => {
  const lock = JSON.parse(read("web/package-lock.json").toString()) as { packages: Record<string, { version?: string; optional?: boolean }> };
  const mismatches: { path: string; expected: string; actual: string }[] = [];
  for (const [path, item] of Object.entries(lock.packages)) {
    if (!path || !item.version) continue;
    const file = new URL("../../" + path + "/package.json", import.meta.url);
    if (!existsSync(file) && item.optional) continue;
    const actual = existsSync(file) ? (JSON.parse(readFileSync(file, "utf8")) as { version: string }).version : "MISSING";
    if (actual !== item.version) mismatches.push({ path, expected: item.version, actual });
  }
  assert.deepEqual(mismatches, [], "exact new lock required; do not rewrite it to fit an old cache");
});
