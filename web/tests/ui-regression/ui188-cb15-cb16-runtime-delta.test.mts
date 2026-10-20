import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { assertCB15CB16RuntimeDelta, assertCB15LeaseDelta, currentBindingLeaseDelta, currentBindingProtectedPaths, currentBindingRuntimeDelta, historicalCB15CB16RuntimeSource } from "./ui188-cb15-cb16-runtime-delta.mts";
import { createNormalizedReader } from "./source-normalization.mts";

const root = fileURLToPath(new URL("../../..", import.meta.url));
const row = currentBindingRuntimeDelta;
const raw = (path: string) => readFileSync(resolve(root, path));
const object = (commit: string, path: string) => execFileSync("git", ["show", commit + ":" + path], { cwd: root });
const hash = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");
const io = { raw, object, exists: (path: string) => existsSync(resolve(root, path)) };
const cb16Insertion = "\t\t\t\tif errors.Is(snapshotErr, store.ErrServiceAssignmentConflict) {\n\t\t\t\t\treturn serviceRuntimeConfigResponse{}, \"current_discord_binding_assignment_conflict\", snapshotErr\n\t\t\t\t}\n";

test("UI188-CB15-CB16-001: the exact adopted two-stage runtime delta restores the original 733-record authority", () => {
  assert.equal(row.path, "internal/httpapi/server_runtime_config.go");
  assert.equal(row.beforeCommit, "a9efbed3e058bf016b171e50c22650075f20ef50");
  assert.equal(row.acceptedCommit, "6419d0fb04386bb86dba4b8f2aeb05cc2c055bb7");
  const before = object(row.beforeCommit, row.path), current = raw(row.path);
  assert.notDeepEqual(before, current);
  assert.deepEqual(assertCB15CB16RuntimeDelta(before, current), before);
  assert.deepEqual(current, object(row.acceptedCommit, row.path), "the actual adopted Git source is an independent authority");
  assert.deepEqual(historicalCB15CB16RuntimeSource(row.path, current, object), before);
  const fixturePath = "web/tests/fixtures/ui-regression/protected.json";
  const fixtureBytes = raw(fixturePath);
  assert.equal(hash(fixtureBytes), "cd9a25bad28720244b8c9b9ec8c5ed5c08df1914800fd6b7831f5efcd3459ae8");
  assert.deepEqual(fixtureBytes, object(row.beforeCommit, fixturePath), "the frozen fixture bytes remain unchanged");
  const fixture = JSON.parse(fixtureBytes.toString("utf8"));
  assert.equal(fixture.protected.length, 733);
  assert.equal(fixture.protected.filter((record: { path: string }) => record.path === row.path).length, 1);
  assert.equal(fixture.protected.find((record: { path: string }) => record.path === row.path).sha256, row.beforeSha256);
  assert.deepEqual(before, object(fixture.base_commit, row.path), "the one-path inverse restores the original historical Git bytes");
  assert.deepEqual(historicalCB15CB16RuntimeSource(row.path, createNormalizedReader(root).read(row.path), object), before);
});

test("UI188-CB15-CB16-002: empty, truncated, extra, unauthorized API, tampered, reverted, CRLF and BOM runtime bytes fail closed", t => {
  const before = object(row.beforeCommit, row.path), current = raw(row.path), source = current.toString("utf8");
  assert.equal(source.split(cb16Insertion).length - 1, 1, "the adopted CB16 repair has one exact insertion");
  const mutants: [string, Buffer][] = [
    ["empty", Buffer.alloc(0)],
    ["truncated", current.subarray(0, current.length - 1)],
    ["extra newline", Buffer.concat([current, Buffer.from("\n")])],
    ["unauthorized API", Buffer.concat([current, Buffer.from("\nfunc unapprovedRuntimeAPI() {}\n")])],
    ["changed preview authority", Buffer.from(source.replace("!preview || !errors.Is(snapshotErr, store.ErrForbidden)", "!preview && !errors.Is(snapshotErr, store.ErrForbidden)"))],
    ["changed CB16 error", Buffer.from(source.replace('"current_discord_binding_assignment_conflict"', '"current_discord_binding_unavailable"'))],
    ["omitted CB16 repair", Buffer.from(source.replace(cb16Insertion, ""))],
    ["reverted CB15 and CB16", before],
    ["missing wire field", Buffer.from(source.replace('`json:"current_discord_bindings,omitempty"`', '`json:"unapproved,omitempty"`'))],
    ["CRLF", Buffer.from(source.replace(/\n/g, "\r\n"))],
    ["BOM", Buffer.concat([Buffer.from([0xef, 0xbb, 0xbf]), current])],
  ];
  assert.equal(hash(mutants.find(([name]) => name === "omitted CB16 repair")![1]), row.cb15Sha256, "the CB15-only negative uses the real intermediate bytes");
  for (const [name, mutant] of mutants) {
    assert.notDeepEqual(mutant, current, name + ": negative control must change the actual source");
    assert.throws(() => assertCB15CB16RuntimeDelta(before, mutant), /CB15\/CB16 unapproved runtime bytes/, name);
    assert.throws(() => historicalCB15CB16RuntimeSource(row.path, mutant, object), /CB15\/CB16 unapproved runtime bytes/, name);
  }
  t.diagnostic(JSON.stringify({ current_source_negative_cases: mutants.length, rejection_paths_per_case: 2 }));
});

test("UI188-CB15-CB16-003: wrong original, altered adopted Git objects and unavailable history cannot supply replacement authority", t => {
  const before = object(row.beforeCommit, row.path), current = raw(row.path);
  const wrongBefore = [Buffer.alloc(0), before.subarray(0, before.length - 1), Buffer.concat([before, Buffer.from("drift")]), Buffer.from(before.toString("utf8").replace(/\n/g, "\r\n")), Buffer.concat([Buffer.from([0xef, 0xbb, 0xbf]), before])];
  for (const mutant of wrongBefore) {
    assert.throws(() => assertCB15CB16RuntimeDelta(mutant, current), /fixed original hash mismatch/);
    assert.throws(() => historicalCB15CB16RuntimeSource(row.path, current, (commit, path) => commit === row.beforeCommit ? mutant : object(commit, path)), /fixed original hash mismatch/);
  }
  const wrongAccepted = Buffer.concat([current, Buffer.from("drift")]);
  assert.throws(() => historicalCB15CB16RuntimeSource(row.path, current, (commit, path) => commit === row.acceptedCommit ? wrongAccepted : object(commit, path)), /accepted Git runtime hash mismatch/);
  for (const absentCommit of [row.beforeCommit, row.acceptedCommit]) {
    const missing = new Error("controlled CB15/CB16 Git object unavailable");
    assert.throws(() => historicalCB15CB16RuntimeSource(row.path, current, (commit, path) => { if (commit === absentCommit) throw missing; return object(commit, path); }), error => error === missing);
  }
  const unknown = Buffer.from("unlisted current bytes");
  assert.equal(historicalCB15CB16RuntimeSource("unknown", unknown, () => { throw new Error("unlisted paths cannot read historical objects"); }), unknown);
  t.diagnostic(JSON.stringify({ wrong_original_cases: wrongBefore.length, rejection_paths_per_case: 2, altered_accepted_object_cases: 1, unavailable_object_cases: 2, unlisted_passthrough_cases: 1 }));
});

test("UI188-CB15-CB16-004: current-path absence and tampering cannot fall back to Git, and the required parity command registers the new checks", () => {
  const missing = new Error("controlled current runtime source absent");
  let deltaHistoryReads = 0;
  const history = (commit: string, path: string) => { deltaHistoryReads++; return object(commit, path); };
  const absent = createNormalizedReader(root, { ...io, raw: path => { if (path === row.path) throw missing; return raw(path); } });
  assert.throws(() => historicalCB15CB16RuntimeSource(row.path, absent.read(row.path), history), error => error === missing);
  assert.equal(deltaHistoryReads, 0, "current bytes must be read before any delta history is considered");
  const changed = createNormalizedReader(root, { ...io, raw: path => path === row.path ? Buffer.concat([raw(path), Buffer.from("drift")]) : raw(path) });
  assert.throws(() => historicalCB15CB16RuntimeSource(row.path, changed.read(row.path), history), /unapproved runtime bytes/);
  const unlisted = "internal/httpapi/server_runtime_config_test.go", unlistedBytes = Buffer.concat([raw(unlisted), Buffer.from("drift")]);
  assert.equal(historicalCB15CB16RuntimeSource(unlisted, unlistedBytes, history), unlistedBytes, "unlisted current test bytes are never silently substituted");
  const pkg = JSON.parse(raw("web/package.json").toString("utf8"));
  assert.equal(pkg.scripts["test:ui-regression:parity"], "node --no-warnings --test tests/ui-regression/parity.test.mts");
  assert.match(raw("web/tests/ui-regression/parity.test.mts").toString("utf8"), /^import "\.\/ui188-cb15-cb16-runtime-delta\.test\.mts";$/m, "the existing required command unconditionally imports the additional checks");
});

test("UI188-CB15-CB16-005: the second protected CB15 lease path uses its exact transaction edits and the same immutable historical authority", () => {
  const lease = currentBindingLeaseDelta;
  assert.deepEqual(currentBindingProtectedPaths, ["internal/httpapi/server_runtime_config.go", "internal/store/runtime_secret_leases.go"]);
  const before = object(lease.beforeCommit, lease.path), current = raw(lease.path);
  assert.notDeepEqual(current, before);
  assert.deepEqual(assertCB15LeaseDelta(before, current), before);
  assert.deepEqual(current, object(lease.acceptedCommit, lease.path));
  assert.deepEqual(historicalCB15CB16RuntimeSource(lease.path, createNormalizedReader(root).read(lease.path), object), before);
  const fixture = JSON.parse(raw("web/tests/fixtures/ui-regression/protected.json").toString("utf8"));
  assert.equal(fixture.protected.length, 733);
  assert.equal(fixture.protected.filter((record: { path: string }) => record.path === lease.path).length, 1);
  assert.equal(fixture.protected.find((record: { path: string }) => record.path === lease.path).sha256, lease.beforeSha256);
  assert.deepEqual(before, object(fixture.base_commit, lease.path));
});

test("UI188-CB15-CB16-006: the lease delta rejects mutation, extra API, revert, encoding drift, wrong or missing history and missing current bytes", t => {
  const lease = currentBindingLeaseDelta, before = object(lease.beforeCommit, lease.path), current = raw(lease.path), source = current.toString("utf8");
  const mutants: [string, Buffer][] = [
    ["empty", Buffer.alloc(0)], ["truncated", current.subarray(0, current.length - 1)],
    ["extra bytes", Buffer.concat([current, Buffer.from("drift")])],
    ["unauthorized API", Buffer.concat([current, Buffer.from("\nfunc unapprovedLeaseAPI() {}\n")])],
    ["reverted transaction sharing", before],
    ["weakened lease guard", Buffer.from(source.replace("err == nil && now.Before(existingExpiresAt)", "err == nil"))],
    ["changed lease identity", Buffer.from(source.replace("lease.ID = newUUID()", 'lease.ID = "unapproved"'))],
    ["CRLF", Buffer.from(source.replace(/\n/g, "\r\n"))],
    ["BOM", Buffer.concat([Buffer.from([0xef, 0xbb, 0xbf]), current])],
  ];
  for (const [name, mutant] of mutants) {
    assert.notDeepEqual(mutant, current, name + ": negative control changes the actual lease bytes");
    assert.throws(() => assertCB15LeaseDelta(before, mutant), /CB15 lease unapproved runtime bytes/, name);
    assert.throws(() => historicalCB15CB16RuntimeSource(lease.path, mutant, object), /CB15 lease unapproved runtime bytes/, name);
  }
  for (const wrong of [Buffer.alloc(0), Buffer.concat([before, Buffer.from("drift")]), Buffer.from(before.toString("utf8").replace(/\n/g, "\r\n")), Buffer.concat([Buffer.from([0xef, 0xbb, 0xbf]), before])]) {
    assert.throws(() => assertCB15LeaseDelta(wrong, current), /fixed original hash mismatch/);
  }
  for (const absentCommit of [lease.beforeCommit, lease.acceptedCommit]) {
    const missing = new Error("controlled lease historical source unavailable");
    assert.throws(() => historicalCB15CB16RuntimeSource(lease.path, current, (commit, path) => { if (commit === absentCommit) throw missing; return object(commit, path); }), error => error === missing);
  }
  const missing = new Error("controlled current lease source absent");
  const absent = createNormalizedReader(root, { ...io, raw: path => { if (path === lease.path) throw missing; return raw(path); } });
  assert.throws(() => historicalCB15CB16RuntimeSource(lease.path, absent.read(lease.path), object), error => error === missing);
  assert.throws(() => historicalCB15CB16RuntimeSource(lease.path, current, (commit, path) => commit === lease.acceptedCommit ? Buffer.alloc(0) : object(commit, path)), /accepted Git runtime hash mismatch/);
  t.diagnostic(JSON.stringify({ lease_current_negative_cases: mutants.length, rejection_paths_per_case: 2, wrong_original_cases: 4, unavailable_history_cases: 2, missing_current_cases: 1, altered_accepted_history_cases: 1 }));
});
