import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import test from "node:test";
import ts from "typescript";
import {
  assertPollingChangedPathInventory, assertPollingCurrentContract, createPollingCurrentAdapter,
  inversePollingCurrentRecord, pollingBlob, pollingCurrentIO, pollingHash,
  type PollingContract, type PollingRecord, type PollingSourceIO,
} from "./polling-current-source-delta.mts";
import {
  pollingCurrentBase, pollingCurrentTree, pollingCurrentFixturePath, pollingCurrentPaths,
  pollingLegacyFixturePath,
} from "./polling-current-source-contract.mts";
import { pollingOuterTestNames } from "./polling-current-source-test-names.mts";
import { assertLegacyParityInventory, assertParityExecution, type CompletedParityTest, type ParitySummary } from "./polling-fixed-parity.mts";
import { createStreamCreateCurrentAdapter } from "./stream-create-current-source-delta.mts";
import { createNormalizedReader } from "./source-normalization.mts";
import { historicalUI188Source } from "./ui188-current-source-deltas.mts";

const root = fileURLToPath(new URL("../../..", import.meta.url));
const actual = pollingCurrentIO(root);
const contract = () => assertPollingCurrentContract(actual.raw(pollingCurrentFixturePath));
const encode = (value: unknown) => Buffer.from(JSON.stringify(value, null, 2) + "\n");
function assertPublishedIndexRecord(record: PollingRecord, index: ReadonlyMap<string, string>) {
  assert.equal(index.get(record.path), `${record.after.mode} ${record.after.blob} 0`, "polling current bytes must bind the actual published-index blob/mode; no virtual clean-filter authority");
}
let captured: PollingSourceIO | undefined;
function fixed(): PollingSourceIO {
  if (captured) return captured;
  const rows = contract().records, paths = [...pollingCurrentPaths, pollingCurrentFixturePath];
  const files = new Map(paths.map(path => [path, actual.raw(path)]));
  const modes = new Map(paths.map(path => [path, actual.mode(path)]));
  const entries = new Map(rows.map(row => [row.path, actual.entry(pollingCurrentBase, row.path)]));
  const objects = new Map(rows.filter(row => row.before !== null).map(row => [row.path, actual.object(pollingCurrentBase, row.path)]));
  const tree = actual.tree(pollingCurrentBase);
  captured = {
    raw: path => { const value = files.get(path); assert.ok(value); return value; },
    mode: path => { const value = modes.get(path); assert.ok(value); return value; },
    entry: (ref, path) => { assert.equal(ref, pollingCurrentBase); assert.ok(entries.has(path)); return entries.get(path); },
    object: (ref, path) => { assert.equal(ref, pollingCurrentBase); const value = objects.get(path); assert.ok(value); return value; },
    tree: ref => { assert.equal(ref, pollingCurrentBase); return tree; },
  };
  return captured;
}

test(pollingOuterTestNames[0], () => {
  assertPollingChangedPathInventory(root);
  assert.equal(actual.tree(pollingCurrentBase), pollingCurrentTree);
  const index = new Map<string, string>();
  for (const row of execFileSync("git", ["ls-files", "--stage", "-z"], { cwd: root }).toString("utf8").split("\0").filter(Boolean)) {
    const tab = row.indexOf("\t"), path = row.slice(tab + 1);
    assert.ok(tab > 0 && !index.has(path), "polling published index must contain one unmerged-free entry per path");
    index.set(path, row.slice(0, tab));
  }
  for (const record of contract().records) {
    assertPublishedIndexRecord(record, index);
    const current = actual.raw(record.path), before = inversePollingCurrentRecord(record, current, actual);
    if (record.before === null) assert.equal(before, undefined);
    else assert.deepEqual(before, actual.object(pollingCurrentBase, record.path));
  }
});

test(pollingOuterTestNames[1], () => {
  const io = fixed();
  for (const record of contract().records) {
    const current = io.raw(record.path), missing = Error("polling controlled missing current file");
    const absent = { ...io, raw: (path: string) => { if (path === record.path) throw missing; return io.raw(path); } };
    assert.throws(() => inversePollingCurrentRecord(record, absent.raw(record.path), absent), error => error === missing);
    const reverted = record.before === null ? Buffer.alloc(0) : io.object(pollingCurrentBase, record.path);
    for (const mutant of [Buffer.alloc(0), current.subarray(0, current.length - 1), Buffer.concat([current, Buffer.from("\nunauthorized")]), reverted]) {
      assert.throws(() => inversePollingCurrentRecord(record, mutant, io), /unreviewed current source bytes/);
    }
    assert.throws(() => inversePollingCurrentRecord(record, current, { ...io, mode: () => "100755" }), /actual current source mode/);
    for (const metadata of [undefined, `100644 ${"0".repeat(40)} 0`, `100755 ${record.after.blob} 0`, `100644 ${record.after.blob} 2`]) {
      assert.throws(() => assertPublishedIndexRecord(record, new Map<string, string>(metadata === undefined ? [] : [[record.path, metadata]])), /actual published-index blob/);
    }
  }
});

test(pollingOuterTestNames[2], () => {
  const value = contract(), io = fixed();
  const mutations: ((copy: PollingContract) => void)[] = [
    copy => { Object.assign(copy, { schemaVersion: 2 }); },
    copy => { Object.assign(copy, { beforeCommit: "0".repeat(40), beforeTree: "0".repeat(40) }); },
    copy => { Object.assign(copy, { unapproved: true }); },
    copy => { (copy.records as unknown[]).pop(); },
    copy => { (copy.records as unknown[]).push(copy.records[0]); },
    copy => { Object.assign(copy.records[0], { path: "../spoof" }); },
    copy => { Object.assign(copy.records[1], { path: copy.records[0].path.toUpperCase() }); },
    copy => { Object.assign(copy.records[0].after, { mode: "100755", blob: "0".repeat(40), sha256: "0".repeat(64) }); },
  ];
  for (const mutate of mutations) {
    const copy = structuredClone(value); mutate(copy);
    assert.throws(() => assertPollingCurrentContract(encode(copy)), /independently pinned current fixture bytes changed/);
  }
  for (const record of value.records) {
    const copy = structuredClone(value), changed = copy.records.find(row => row.path === record.path)!;
    const current = Buffer.concat([io.raw(record.path), Buffer.from("\ncoordinated forge")]);
    Object.assign(changed.after, { sha256: pollingHash(current), blob: pollingBlob(current) });
    Object.assign(changed.hunks.at(-1)!, { after: changed.hunks.at(-1)!.after + "\ncoordinated forge" });
    const adapter = createPollingCurrentAdapter(root, { ...io, raw: path => path === pollingCurrentFixturePath ? encode(copy) : io.raw(path) });
    assert.throws(() => adapter.inverse(record.path, current), /independently pinned current fixture bytes changed/);
  }
});

test(pollingOuterTestNames[3], () => {
  const io = fixed();
  for (const record of contract().records) {
    const current = io.raw(record.path);
    const offset = structuredClone(record); Object.assign(offset.hunks[0], { offsetAfter: offset.hunks[0].offsetAfter + 1 });
    assert.throws(() => inversePollingCurrentRecord(offset, current, io), /exact contextual byte offset|exact addition offsets/);
    const absent = structuredClone(record); Object.assign(absent.hunks[0], { after: absent.hunks[0].after + "absent-context" });
    assert.throws(() => inversePollingCurrentRecord(absent, current, io), /literal must occur uniquely|complete addition/);
    const duplicate = structuredClone(record); Object.assign(duplicate.hunks[0], { after: "\n" });
    assert.throws(() => inversePollingCurrentRecord(duplicate, current, io), /literal must occur uniquely|complete addition/);
    const original = structuredClone(record); Object.assign(original.hunks[0], { before: original.hunks[0].before + "unapproved-base" });
    assert.throws(() => inversePollingCurrentRecord(original, current, io), /inverse restores complete fixed base bytes|exact addition offsets/);
  }
});

test(pollingOuterTestNames[4], () => {
  const io = fixed();
  for (const record of contract().records) {
    const current = io.raw(record.path), missing = Error("polling controlled immutable read failure");
    assert.throws(() => inversePollingCurrentRecord(record, current, { ...io, tree: () => "0".repeat(40) }), /fixed base tree/);
    if (record.before === null) {
      assert.throws(() => inversePollingCurrentRecord(record, current, { ...io, entry: () => record.after }), /absent at fixed base/);
      continue;
    }
    for (const entry of [undefined, { mode: "100755", blob: record.before.blob }, { mode: "100644", blob: "0".repeat(40) }]) {
      assert.throws(() => inversePollingCurrentRecord(record, current, { ...io, entry: () => entry }), /fixed base blob\/mode/);
    }
    assert.throws(() => inversePollingCurrentRecord(record, current, { ...io, object: () => Buffer.alloc(0) }), /fixed base source bytes/);
    assert.throws(() => inversePollingCurrentRecord(record, current, { ...io, object: () => { throw missing; } }), error => error === missing);
  }
});

test(pollingOuterTestNames[5], () => {
  const path = ".github/workflows/ci.yml", adapter = createPollingCurrentAdapter(root), current = actual.raw(path);
  const prior = actual.object(pollingCurrentBase, path), older = createStreamCreateCurrentAdapter(root);
  const reader = createNormalizedReader(root);
  assert.deepEqual(reader.raw(path), current, "polling actual raw never becomes projected source");
  assert.deepEqual(adapter.inverse(path, current), prior);
  const oldest = older.inverse(path, prior);
  assert.deepEqual(reader.historicalRaw(path), historicalUI188Source(path, oldest, actual.object));
  assert.throws(() => older.inverse(path, current), /stream-create unreviewed current source bytes/);
  assert.throws(() => adapter.inverse(path, prior), /polling unreviewed current source bytes/);
  assert.throws(() => adapter.inverse(path, adapter.inverse(path, current)), /polling unreviewed current source bytes/);
  const parsed = ts.createSourceFile("source-normalization.mts", actual.raw("web/tests/ui-regression/source-normalization.mts").toString("utf8"), ts.ScriptTarget.Latest, true);
  let connections = 0;
  const visit = (node: ts.Node) => {
    if (ts.isCallExpression(node) && node.expression.getText(parsed) === "currentSource.inverse") {
      assert.equal(node.arguments[1]?.getText(parsed), "pollingSource.inverse(path, raw(path))", "polling new inverse precedes the unchanged previous inverse exactly once");
      connections++;
    }
    ts.forEachChild(node, visit);
  };
  visit(parsed);
  assert.equal(connections, 1);
});

test(pollingOuterTestNames[6], () => {
  const currentPackage = JSON.parse(actual.raw("web/package.json").toString("utf8"));
  assert.equal(currentPackage.scripts["test:ui-regression:parity"], "node --no-warnings tests/ui-regression/run-polling-current-parity.mts");
  const inventory = assertLegacyParityInventory(actual.raw(pollingLegacyFixturePath));
  for (const source of inventory.sources) {
    assert.deepEqual(actual.raw(source.path), actual.object(pollingCurrentBase, source.path), "polling earlier positive/negative assertion source bytes remain fixed");
  }
  assert.equal(inventory.executionCount, 59);
});

test(pollingOuterTestNames[7], () => {
  for (const path of [
    "web/tests/fixtures/ui-regression/protected.json", "web/tests/fixtures/ui-regression/source-normalization.json",
    "web/tests/fixtures/ui-regression/approved-source-deltas.json", "web/tests/fixtures/ui-regression/ci-source-deltas.json",
    "web/tests/fixtures/ui-regression/ui188-current-source-deltas.json", "web/tests/fixtures/ui-regression/ui188-cp-containment-source-delta.json",
    "web/tests/fixtures/ui-regression/ui188-r4-delete-deltas.json", "web/tests/fixtures/ui-regression/ui188-r5-readiness-deltas.json",
    "web/tests/fixtures/ui-regression/stream-create-current-source-delta.json",
    "web/tests/ui-regression/stream-create-current-source-delta.mts", "web/tests/ui-regression/stream-create-current-source-delta.test.mts",
    "web/tests/ui-regression/ui188-r9-finalization-deltas.mts", "web/tests/ui-regression/ui188-cp-containment-source-delta.mts",
    "web/tests/ui-regression/ui188-current-source-deltas.mts",
  ]) assert.deepEqual(actual.raw(path), actual.object(pollingCurrentBase, path), "polling earlier oracle remains byte unchanged: " + path);
});

test(pollingOuterTestNames[8], () => {
  const names = ["required positive", "required negative"], completed: CompletedParityTest[] = names.map(name => ({ name, passed: true, skipped: false, todo: false }));
  const summary: ParitySummary = { success: true, counts: { tests: 2, passed: 2, failed: 0, cancelled: 0, skipped: 0, todo: 0 } };
  assert.doesNotThrow(() => assertParityExecution(summary, completed, names));
  assert.throws(() => assertParityExecution({ success: true, counts: { tests: 0, passed: 0, failed: 0, cancelled: 0, skipped: 0, todo: 0 } }, [], []), /denominator is nonzero/);
  assert.throws(() => assertParityExecution(summary, completed, [names[0], names[0]]), /required identities are unique/);
  assert.throws(() => assertParityExecution(undefined, completed, names), /actual Node summary/);
  for (const field of ["tests", "passed", "failed", "cancelled", "skipped", "todo"]) {
    const copy = structuredClone(summary); Object.assign(copy.counts, { [field]: field === "tests" || field === "passed" ? 0 : 1 });
    assert.throws(() => assertParityExecution(copy, completed, names), /polling parity/);
  }
  for (const copy of [completed.slice(1), [...completed, completed[0]], [completed[0], completed[0]], [{ ...completed[0], skipped: true }, completed[1]], [{ ...completed[0], todo: true }, completed[1]], [{ ...completed[0], name: "unknown" }, completed[1]]]) {
    assert.throws(() => assertParityExecution(summary, copy, names), /polling parity/);
  }
});

test(pollingOuterTestNames[9], () => {
  let reads = 0;
  const forbidden = () => { reads++; throw Error("polling unrelated input cannot read source or Git"); };
  const adapter = createPollingCurrentAdapter(root, { raw: forbidden, mode: forbidden, object: forbidden, entry: forbidden, tree: forbidden });
  const current = Buffer.from("unlisted source remains actual");
  assert.equal(reads, 0);
  assert.equal(adapter.inverse("unlisted-current.ts", current), current);
  assert.equal(reads, 0);
});
