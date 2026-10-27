import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import test from "node:test";
import {
  assertStreamCreateCurrentContract, assertStreamCreateCurrentRegistration,
  createStreamCreateCurrentAdapter, inverseStreamCreateCurrentRecord,
  streamCreateCurrentBase, streamCreateCurrentFixturePath, streamCreateCurrentIO,
  streamCreateCurrentPaths, streamCreateCurrentTree,
  type StreamCreateCurrentContract, type StreamCreateCurrentIO,
} from "./stream-create-current-source-delta.mts";
import { createNormalizedReader } from "./source-normalization.mts";
import { historicalUI188Source } from "./ui188-current-source-deltas.mts";

const root = fileURLToPath(new URL("../../..", import.meta.url));
const actual = streamCreateCurrentIO(root);
const contract = () => assertStreamCreateCurrentContract(actual.raw(streamCreateCurrentFixturePath));
const hash = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");
const blob = (bytes: Buffer) => createHash("sha1").update(`blob ${bytes.length}\0`).update(bytes).digest("hex");
const encode = (value: unknown) => Buffer.from(JSON.stringify(value, null, 2) + "\n");

let captured: StreamCreateCurrentIO | undefined;
function fixed(): StreamCreateCurrentIO {
  if (captured) return captured;
  const rows = contract().records;
  const paths = [...streamCreateCurrentPaths, streamCreateCurrentFixturePath];
  const files = new Map(paths.map(path => [path, actual.raw(path)]));
  const modes = new Map(paths.map(path => [path, actual.mode(path)]));
  const entries = new Map(rows.map(row => [row.path, actual.entry(streamCreateCurrentBase, row.path)]));
  const objects = new Map(rows.map(row => [row.path, actual.object(streamCreateCurrentBase, row.path)]));
  const tree = actual.tree(streamCreateCurrentBase);
  captured = {
    raw: path => { const value = files.get(path); assert.ok(value, "explicit captured current path"); return value; },
    mode: path => { const value = modes.get(path); assert.ok(value); return value; },
    entry: (ref, path) => { assert.equal(ref, streamCreateCurrentBase); assert.ok(entries.has(path)); return entries.get(path); },
    object: (ref, path) => { assert.equal(ref, streamCreateCurrentBase); const value = objects.get(path); assert.ok(value); return value; },
    tree: ref => { assert.equal(ref, streamCreateCurrentBase); return tree; },
  };
  return captured;
}

test("UI-STREAM-CURRENT-001: exactly 39 actual changed sources invert to immutable f64 bytes through finite bidirectional contexts", () => {
  const adapter = createStreamCreateCurrentAdapter(root);
  assert.equal(streamCreateCurrentPaths.length, 39);
  assert.equal(actual.tree(streamCreateCurrentBase), streamCreateCurrentTree);
  for (const row of contract().records) {
    const raw = actual.raw(row.path), original = actual.object(streamCreateCurrentBase, row.path);
    assert.notDeepEqual(raw, original, "only an actual changed source belongs to this new layer");
    assert.deepEqual(adapter.inverse(row.path, raw), original);
  }
});

test("UI-STREAM-CURRENT-002: every listed current path rejects missing, empty, truncated, appended, reverted and wrong-mode input", () => {
  const io = fixed();
  const first = contract().records[0];
  assert.throws(() => inverseStreamCreateCurrentRecord({ ...first, path: "../unreviewed" }, io.raw(first.path), io), /stream-create unknown reviewed path/);
  for (const row of contract().records) {
    const current = io.raw(row.path), missing = Error("controlled actual source missing " + row.path);
    const absent = { ...io, raw: (path: string) => { if (path === row.path) throw missing; return io.raw(path); } };
    const adapter = createStreamCreateCurrentAdapter(root, absent);
    assert.throws(() => adapter.inverse(row.path, absent.raw(row.path)), error => error === missing);
    for (const mutant of [Buffer.alloc(0), current.subarray(0, current.length - 1), Buffer.concat([current, Buffer.from("\nunauthorized")]), io.object(streamCreateCurrentBase, row.path)]) {
      assert.throws(() => inverseStreamCreateCurrentRecord(row, mutant, io), /stream-create unreviewed current source bytes/);
    }
    assert.throws(() => inverseStreamCreateCurrentRecord(row, current, { ...io, mode: () => "100755" }), /actual current source mode/);
  }
});

test("UI-STREAM-CURRENT-003: the independently pinned closed contract rejects path, identity, snapshot, hunk and coordinated source forgery", () => {
  const input = contract(), io = fixed();
  const mutations: ((value: StreamCreateCurrentContract) => void)[] = [
    value => { Object.assign(value, { schemaVersion: 2 }); },
    value => { Object.assign(value, { beforeCommit: "0".repeat(40) }); },
    value => { Object.assign(value, { beforeTree: "0".repeat(40) }); },
    value => { Object.assign(value, { extra: true }); },
    value => { (value.records as unknown[]).pop(); },
    value => { (value.records as unknown[]).push(value.records[0]); },
    value => { Object.assign(value.records[1], { path: value.records[0].path.toUpperCase() }); },
    value => { Object.assign(value.records[0], { path: "../outside" }); },
    value => { Object.assign(value.records[0].before, { sha256: "0".repeat(64) }); },
    value => { Object.assign(value.records[0].before, { blob: "0".repeat(40) }); },
    value => { Object.assign(value.records[0].after, { mode: "100755" }); },
    value => { Object.assign(value.records[0].hunks[0], { offsetAfter: 0 }); },
    value => { Object.assign(value.records[0].hunks[0], { after: "forged literal" }); },
  ];
  for (const mutate of mutations) {
    const value = structuredClone(input); mutate(value);
    assert.throws(() => assertStreamCreateCurrentContract(encode(value)), /independently pinned current fixture bytes changed/);
  }
  for (const row of input.records) {
    const forged = structuredClone(input), changed = forged.records.find(record => record.path === row.path)!;
    const current = Buffer.concat([io.raw(row.path), Buffer.from("\ncoordinated forge\n")]);
    Object.assign(changed.after, { sha256: hash(current), blob: blob(current) });
    Object.assign(changed.hunks.at(-1)!, { after: changed.hunks.at(-1)!.after + "\ncoordinated forge\n" });
    const adapter = createStreamCreateCurrentAdapter(root, { ...io, raw: path => path === streamCreateCurrentFixturePath ? encode(forged) : path === row.path ? current : io.raw(path) });
    assert.throws(() => adapter.inverse(row.path, current), /independently pinned current fixture bytes changed/);
  }
});

test("UI-STREAM-CURRENT-004: wrong immutable tree, entry, original bytes and original read failure cannot substitute source", () => {
  const io = fixed();
  for (const row of contract().records) {
    const current = io.raw(row.path), missing = Error("controlled immutable base read missing");
    assert.throws(() => inverseStreamCreateCurrentRecord(row, current, { ...io, tree: () => "0".repeat(40) }), /fixed base tree/);
    for (const entry of [undefined, { mode: "100755", blob: row.before.blob }, { mode: "100644", blob: "0".repeat(40) }]) {
      assert.throws(() => inverseStreamCreateCurrentRecord(row, current, { ...io, entry: () => entry }), /fixed base blob\/mode/);
    }
    assert.throws(() => inverseStreamCreateCurrentRecord(row, current, { ...io, object: () => Buffer.alloc(0) }), /fixed base source bytes/);
    assert.throws(() => inverseStreamCreateCurrentRecord(row, current, { ...io, object: () => { throw missing; } }), error => error === missing);
  }
});

test("UI-STREAM-CURRENT-005: the real inverse checks unique contexts, byte offsets and complete source reconstruction below the fixture pin", () => {
  const io = fixed();
  for (const row of contract().records) {
    const current = io.raw(row.path);
    const offset = structuredClone(row); Object.assign(offset.hunks[0], { offsetAfter: offset.hunks[0].offsetAfter + 1 });
    assert.throws(() => inverseStreamCreateCurrentRecord(offset, current, io), /exact contextual byte offset/);
    const literal = structuredClone(row); Object.assign(literal.hunks[0], { after: literal.hunks[0].after + "absent-context" });
    assert.throws(() => inverseStreamCreateCurrentRecord(literal, current, io), /contextual literal must occur uniquely/);
    const duplicate = structuredClone(row); Object.assign(duplicate.hunks[0], { after: "\n" });
    assert.throws(() => inverseStreamCreateCurrentRecord(duplicate, current, io), /contextual literal must occur uniquely/);
    const original = structuredClone(row); Object.assign(original.hunks[0], { before: original.hunks[0].before + "unapproved-base" });
    assert.throws(() => inverseStreamCreateCurrentRecord(original, current, io), /inverse restores complete fixed base bytes/);
  }
});

test("UI-STREAM-CURRENT-006: actual raw is preserved, one current inverse precedes history and bypass, reversion and double application fail", () => {
  const path = ".github/workflows/ci.yml", adapter = createStreamCreateCurrentAdapter(root), current = actual.raw(path);
  const reader = createNormalizedReader(root), before = actual.object(streamCreateCurrentBase, path);
  assert.deepEqual(reader.raw(path), current);
  assert.deepEqual(adapter.inverse(path, current), before);
  assert.deepEqual(reader.historicalRaw(path), historicalUI188Source(path, before, actual.object));
  assert.throws(() => historicalUI188Source(path, current, actual.object), /R9 unreviewed current finalization bytes/);
  assert.throws(() => adapter.inverse(path, before), /stream-create unreviewed current source bytes/);
  assert.throws(() => adapter.inverse(path, adapter.inverse(path, current)), /stream-create unreviewed current source bytes/);
});

test("UI-STREAM-CURRENT-007: registration is exactly one unconditional top-level side-effect import", () => {
  const source = actual.raw("web/tests/ui-regression/parity.test.mts").toString("utf8");
  const registration = 'import "./stream-create-current-source-delta.test.mts";';
  assertStreamCreateCurrentRegistration(source);
  for (const mutant of [source.replace(registration, ""), source + "\n" + registration,
    source.replace(registration, "/* " + registration + " */"),
    source.replace(registration, `{ ${registration} }`),
    source.replace(registration, 'import * as ignored from "./stream-create-current-source-delta.test.mts";'),
    source.replace(registration, 'if (true) await import("./stream-create-current-source-delta.test.mts");'),
    source + '\nif (false) await import("./stream-create-current-source-delta.test.mts");']) {
    assert.throws(() => assertStreamCreateCurrentRegistration(mutant), /exactly once at top level|cannot be dynamic or skipped/);
  }
});

test("UI-STREAM-CURRENT-008: unlisted input remains identical and construction does not read the fixture, source or Git", () => {
  let reads = 0;
  const forbidden = () => { reads++; throw Error("unlisted source must not read metadata or history"); };
  const adapter = createStreamCreateCurrentAdapter(root, { raw: forbidden, mode: forbidden, object: forbidden, entry: forbidden, tree: forbidden });
  const bytes = Buffer.from("unlisted actual source drift");
  assert.equal(reads, 0);
  assert.equal(adapter.inverse("unlisted-current.ts", bytes), bytes);
  assert.equal(reads, 0);
});

test("UI-STREAM-CURRENT-009: all frozen fixture bytes and prior historical validators remain unchanged", () => {
  for (const path of [
    "web/tests/fixtures/ui-regression/protected.json", "web/tests/fixtures/ui-regression/source-normalization.json",
    "web/tests/fixtures/ui-regression/approved-source-deltas.json", "web/tests/fixtures/ui-regression/ci-source-deltas.json",
    "web/tests/fixtures/ui-regression/ui188-current-source-deltas.json", "web/tests/fixtures/ui-regression/ui188-cp-containment-source-delta.json",
    "web/tests/fixtures/ui-regression/ui188-r4-delete-deltas.json", "web/tests/fixtures/ui-regression/ui188-r5-readiness-deltas.json",
    "web/tests/ui-regression/ui188-r9-finalization-deltas.mts", "web/tests/ui-regression/ui188-cp-containment-source-delta.mts",
    "web/tests/ui-regression/ui188-current-source-deltas.mts",
  ]) {
    assert.equal(streamCreateCurrentPaths.some(listed => listed === path), false, "new lineage does not impersonate its prior authorities");
    assert.deepEqual(actual.raw(path), actual.object(streamCreateCurrentBase, path), path);
  }
});
