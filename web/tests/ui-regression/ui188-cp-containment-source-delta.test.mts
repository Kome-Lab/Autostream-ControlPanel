import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import test from "node:test";
import {
  assertContainmentContract, assertContainmentGroup, assertContainmentOwnership,
  assertContainmentRegistration, assertContainmentSupportSources, containmentBeforeCommit,
  containmentBeforeTree, containmentFixturePath, containmentIO, containmentPaths,
  containmentSupportPaths, createContainmentAdapter, inverseContainmentRecord,
  type ContainmentContract, type ContainmentIO,
} from "./ui188-cp-containment-source-delta.mts";
import { createNormalizedReader } from "./source-normalization.mts";
import { historicalUI188Source } from "./ui188-current-source-deltas.mts";
import { historicalSessionRefreshSource } from "./ui188-r3-session-delta.mts";
import { createStreamCreateCurrentAdapter } from "./stream-create-current-source-delta.mts";

const root = fileURLToPath(new URL("../../..", import.meta.url));
const actual = containmentIO(root);
const currentSource = createStreamCreateCurrentAdapter(root, actual);
const real = { ...actual, raw: (path: string) => currentSource.inverse(path, actual.raw(path)) };
const hash = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");
const blob = (bytes: Buffer) => createHash("sha1").update(`blob ${bytes.length}\0`).update(bytes).digest("hex");
const contract = () => assertContainmentContract(real.raw(containmentFixturePath));
const encode = (value: unknown) => Buffer.from(JSON.stringify(value, null, 2) + "\n");

// Fetch immutable Git facts once, then exercise each negative through the real
// validator with controlled current I/O. This is not a replacement authority:
// the positive test below separately reads the actual worktree and Git objects.
function captured(): ContainmentIO {
  const rows = contract().records;
  const files = new Map([...containmentPaths, ...containmentSupportPaths(), containmentFixturePath,
    "web/tests/ui-regression/parity.test.mts"].map(path => [path, real.raw(path)]));
  const modes = new Map([...files.keys()].map(path => [path, real.mode(path)]));
  const entries = new Map(rows.map(row => [row.path, real.entry(containmentBeforeCommit, row.path)]));
  const objects = new Map(rows.filter(row => row.before !== null).map(row => [row.path, real.object(containmentBeforeCommit, row.path)]));
  const tree = real.tree(containmentBeforeCommit);
  return {
    raw: path => { const bytes = files.get(path); assert.ok(bytes, "captured current path must be explicit"); return bytes; },
    mode: path => { const value = modes.get(path); assert.ok(value); return value; },
    entry: (commit, path) => { assert.equal(commit, containmentBeforeCommit); assert.ok(entries.has(path)); return entries.get(path); },
    object: (commit, path) => { assert.equal(commit, containmentBeforeCommit); const value = objects.get(path); assert.ok(value); return value; },
    tree: commit => { assert.equal(commit, containmentBeforeCommit); return tree; },
  };
}
let snapshot: ContainmentIO | undefined;
const fixed = () => snapshot ??= captured();

test("UI188-CONTAINMENT-001: all nine actual current paths, additions, registration and four existing owners are mandatory", () => {
  assert.deepEqual(assertContainmentGroup(root, real), { records: 9, modified: 7, additions: 2, owners: 51, unchangedOwners: 47, supportSources: 6 });
  const value = contract();
  assert.equal(value.beforeCommit, containmentBeforeCommit);
  assert.equal(value.beforeTree, containmentBeforeTree);
  for (const row of value.records) {
    const result = inverseContainmentRecord(row, real.raw(row.path), real);
    if (row.before === null) {
      assert.equal(real.entry(containmentBeforeCommit, row.path), undefined);
      assert.deepEqual(result, real.raw(row.path), "an addition is verified current source, never fabricated empty history");
    } else assert.deepEqual(result, real.object(containmentBeforeCommit, row.path));
  }
});

test("UI188-CONTAINMENT-002: every behavior/test/metadata file rejects missing, empty, truncated, appended and reverted source", () => {
  const io = fixed();
  for (const row of contract().records) {
    const current = io.raw(row.path), missing = Error("controlled current I/O " + row.path);
    assert.throws(() => assertContainmentGroup(root, { ...io, raw: path => { if (path === row.path) throw missing; return io.raw(path); } }), error => error === missing);
    const mutants = [Buffer.alloc(0), current.subarray(0, current.length - 1), Buffer.concat([current, Buffer.from("\nunauthorized")])];
    if (row.before !== null) mutants.push(io.object(containmentBeforeCommit, row.path));
    for (const mutant of mutants) {
      assert.throws(() => assertContainmentGroup(root, { ...io, raw: path => path === row.path ? mutant : io.raw(path) }), /containment actual current/);
    }
    assert.throws(() => assertContainmentGroup(root, { ...io, mode: path => path === row.path ? "100755" : io.mode(path) }), /actual current mode/);
  }
});

test("UI188-CONTAINMENT-003: independently pinned closed fixture rejects identity, path, hash, blob, mode and hunk mutations", () => {
  const input = contract();
  const mutations: ((value: ContainmentContract) => void)[] = [
    value => { Object.assign(value, { schemaVersion: 2 }); },
    value => { Object.assign(value, { beforeCommit: "0".repeat(40) }); },
    value => { Object.assign(value, { beforeTree: "0".repeat(40) }); },
    value => { Object.assign(value, { unexpected: true }); },
    value => { (value.records as unknown[]).pop(); },
    value => { (value.records as unknown[]).push(value.records[0]); },
    value => { Object.assign(value.records[1], { path: value.records[0].path.toUpperCase() }); },
    value => { Object.assign(value.records[0], { path: "../unapproved" }); },
    value => { Object.assign(value.records[0], { path: "/absolute" }); },
    value => { Object.assign(value.records[0], { path: "unapproved.ts" }); },
    value => { Object.assign(value.records[0], { extra: true }); },
    value => { Object.assign(value.records[0].before!, { sha256: "0".repeat(64) }); },
    value => { Object.assign(value.records[0].before!, { blob: "0".repeat(40) }); },
    value => { Object.assign(value.records[0].before!, { mode: "100755" }); },
    value => { Object.assign(value.records[0].after, { sha256: "0".repeat(64) }); },
    value => { Object.assign(value.records[0].after, { blob: "0".repeat(40) }); },
    value => { Object.assign(value.records[0].after, { mode: "100755" }); },
    value => { Object.assign(value.records[0].hunks[0], { after: value.records[0].hunks[0].after + "\n" }); },
    value => { Object.assign(value.records[0].hunks[0], { offsetAfter: 0 }); },
    value => { Object.assign(value.records[0].hunks[0], { extra: true }); },
    value => { Object.assign(value.records[2], { before: { path: value.records[2].path, mode: "100644", blob: blob(Buffer.alloc(0)), sha256: hash(Buffer.alloc(0)) } }); },
  ];
  for (const mutate of mutations) {
    const value = structuredClone(input); mutate(value);
    assert.throws(() => assertContainmentContract(encode(value)), /independently pinned fixture bytes changed/);
  }
});

test("UI188-CONTAINMENT-004: coordinated current-source, after-hash and inverse-hunk forgery cannot update the independent validator pin", () => {
  const io = fixed();
  for (let index = 0; index < contract().records.length; index++) {
    const forged = structuredClone(contract()), row = forged.records[index];
    const current = Buffer.concat([io.raw(row.path), Buffer.from("\nforged simultaneously\n")]);
    Object.assign(row.after, { sha256: hash(current), blob: blob(current) });
    if (row.before !== null) Object.assign(row.hunks.at(-1)!, { after: row.hunks.at(-1)!.after + "\nforged simultaneously\n" });
    const forgedBytes = encode(forged);
    assert.throws(() => assertContainmentGroup(root, { ...io, raw: path => path === containmentFixturePath ? forgedBytes : path === row.path ? current : io.raw(path) }), /independently pinned fixture bytes changed/);
  }
});

test("UI188-CONTAINMENT-005: immutable before objects, fixed tree and absence are real checks; read errors retain original identity", () => {
  const io = fixed();
  assert.throws(() => assertContainmentGroup(root, { ...io, tree: () => "0".repeat(40) }), /fixed before tree mismatch/);
  for (const row of contract().records) {
    if (row.before === null) {
      assert.throws(() => inverseContainmentRecord(row, io.raw(row.path), { ...io, entry: () => ({ mode: "100644", blob: blob(Buffer.alloc(0)) }) }), /must be absent.*not empty/);
      continue;
    }
    const missing = Error("controlled fixed before object unavailable");
    assert.throws(() => inverseContainmentRecord(row, io.raw(row.path), { ...io, object: () => { throw missing; } }), error => error === missing);
    assert.throws(() => inverseContainmentRecord(row, io.raw(row.path), { ...io, object: () => Buffer.alloc(0) }), /fixed before raw hash/);
    for (const entry of [{ mode: "100755", blob: row.before.blob }, { mode: "100644", blob: "0".repeat(40) }, undefined]) {
      assert.throws(() => inverseContainmentRecord(row, io.raw(row.path), { ...io, entry: () => entry }), /fixed before blob\/mode mismatch/);
    }
  }
});

test("UI188-CONTAINMENT-006: actual top-level test registration rejects absent, duplicate, commented, dynamic and skipped variants", () => {
  const io = fixed();
  for (const [path, module] of [
    ["web/tests/ui-regression/parity.test.mts", "./ui188-cp-containment-source-delta.test.mts"],
    ["web/tests/ui-regression/browser-contracts.test.mts", "./command-deadline.test.mts"],
  ]) {
    const source = io.raw(path).toString("utf8"), registration = `import "${module}";`;
    assertContainmentRegistration(source, module);
    assert.ok(source.includes(registration));
    for (const mutant of [source.replace(registration, ""), source + "\n" + registration,
      source.replace(registration, "/* " + registration + " */"),
      source.replace(registration, `if (true) await import("${module}");`),
      source + `\nif (false) await import("${module}");`]) {
      assert.throws(() => assertContainmentRegistration(mutant, module), /exactly once.*top-level side-effect import|cannot be dynamic or skipped/);
    }
  }
  const path = "web/tests/ui-regression/command-deadline.test.mts", source = io.raw(path).toString("utf8");
  for (const witness of ["original assertion identity", "cleanup", "deadlineWrites"]) {
    const line = source.split("\n").find(item => item.includes(witness));
    assert.ok(line, "the actual registered test retains its body/cleanup/deadline witness");
    const mutant = Buffer.from(source.replace(line + "\n", ""));
    assert.notDeepEqual(mutant, io.raw(path));
    assert.throws(() => assertContainmentGroup(root, { ...io, raw: name => name === path ? mutant : io.raw(name) }), /containment actual current/);
  }
  const skipped = source.replace(/\btest\(/, "test.skip(");
  assert.notEqual(skipped, source);
  assert.throws(() => assertContainmentGroup(root, { ...io, raw: name => name === path ? Buffer.from(skipped) : io.raw(name) }), /containment actual current/);
});

test("UI188-CONTAINMENT-007: owner denominator, all other 47 targets, identity, actual symbol and physical lines remain enforced", () => {
  const io = fixed(), path = containmentPaths[0], before = io.object(containmentBeforeCommit, path), bytes = io.raw(path);
  const input = JSON.parse(bytes.toString("utf8")) as { target_id: string; destinations: { path: string; sha256: string; symbols: string[]; physical_lines: number }[]; [key: string]: unknown }[];
  assertContainmentOwnership(before, bytes, io.raw);
  const allowed = input.findIndex(row => row.target_id === "B9-M009"), unrelated = input.findIndex(row => row.target_id !== "B9-M009" && row.target_id !== "B9-M016" && row.target_id !== "B9-M051" && row.target_id !== "B9-M052");
  for (const mutate of [
    (value: typeof input) => { value.pop(); },
    (value: typeof input) => { [value[0], value[1]] = [value[1], value[0]]; },
    (value: typeof input) => { value[allowed].unapproved = true; },
    (value: typeof input) => { value[unrelated].unapproved = true; },
    (value: typeof input) => { value[allowed].destinations.push(value[allowed].destinations[0]); },
    (value: typeof input) => { const destination = value[allowed].destinations.find(item => item.path === containmentPaths[2])!; destination.symbols = ["symbolThatDoesNotExist"]; },
    (value: typeof input) => { const destination = value[allowed].destinations.find(item => item.path === containmentPaths[2])!; destination.physical_lines++; },
    (value: typeof input) => { const destination = value[allowed].destinations.find(item => item.path === containmentPaths[2])!; destination.sha256 = "0".repeat(64); },
    (value: typeof input) => { value[allowed].destinations = value[allowed].destinations.filter(item => item.path !== containmentPaths[2]); },
  ]) {
    const value = structuredClone(input); mutate(value);
    assert.throws(() => assertContainmentOwnership(before, encode(value), io.raw), /containment/);
  }
});

test("UI188-CONTAINMENT-008: raw stays current, one inverse precedes the old chain, bypass and double inverse are rejected", () => {
  const path = containmentPaths[1], ownerPath = containmentPaths[0], current = real.raw(path);
  const adapter = createContainmentAdapter(root), reader = createNormalizedReader(root);
  const before = real.object(containmentBeforeCommit, path);
  assert.deepEqual(reader.raw(path), actual.raw(path), "raw never becomes historical source");
  assert.deepEqual(adapter.inverse(path, current), before);
  assert.deepEqual(reader.historicalRaw(path), historicalUI188Source(path, before, real.object), "new inverse enters the old UI188 chain exactly once");
  const beforeOwner = real.object(containmentBeforeCommit, ownerPath);
  assert.deepEqual(reader.historicalRaw(ownerPath), historicalSessionRefreshSource(ownerPath, beforeOwner, real.object), "owner inverse precedes the old R9 map and session inverse");
  assert.throws(() => adapter.inverse(path, before), /containment actual current/);
  assert.throws(() => adapter.inverse(path, adapter.inverse(path, current)), /containment actual current/);
  assert.throws(() => historicalUI188Source(path, current, real.object), /reviewed finite UI188 source bytes/, "bypassing the new layer still reaches the original unchanged hash guard");
  const supportIO = fixed(), normalizerPath = "web/tests/ui-regression/source-normalization.mts";
  const source = supportIO.raw(normalizerPath).toString("utf8");
  const bypassed = source.replace("containment.inverse(path, raw(path))", "raw(path)");
  assert.notEqual(bypassed, source);
  assert.throws(() => assertContainmentSupportSources({ ...supportIO, raw: name => name === normalizerPath ? Buffer.from(bypassed) : supportIO.raw(name) }), /supporting authority source/);
});

test("UI188-CONTAINMENT-009: reader construction and unrelated current I/O remain lazy and never substitute history", () => {
  let fixtureReads = 0;
  const marker = Buffer.from("unlisted actual current bytes"), missing = Error("controlled current read failure");
  const raw = (path: string) => {
    if (path === containmentFixturePath) { fixtureReads++; throw Error("new group must not be read eagerly"); }
    if (path === "unlisted-current.ts") return marker;
    if (path === containmentPaths[1]) throw missing;
    return real.raw(path);
  };
  const reader = createNormalizedReader(root, { raw, exists: () => false, object: () => { throw Error("unlisted path must not read history"); } });
  assert.equal(fixtureReads, 0);
  assert.equal(reader.read("unlisted-current.ts"), marker);
  assert.throws(() => reader.read(containmentPaths[1]), error => error === missing);
  assert.equal(fixtureReads, 0, "actual current read failure precedes new fixture or old Git lookups");
});

test("UI188-CONTAINMENT-010: all six authority support sources reject mutation independently of the behavior fixture", () => {
  const io = fixed();
  assert.equal(containmentSupportPaths().length, 6);
  assertContainmentSupportSources(io);
  for (const path of containmentSupportPaths()) {
    const missing = Error("controlled authority support missing " + path);
    assert.throws(() => assertContainmentSupportSources({ ...io, raw: name => { if (name === path) throw missing; return io.raw(name); } }), error => error === missing);
    for (const mutant of [Buffer.alloc(0), io.raw(path).subarray(0, io.raw(path).length - 1), Buffer.concat([io.raw(path), Buffer.from("\nunauthorized")])]) {
      assert.throws(() => assertContainmentSupportSources({ ...io, raw: name => name === path ? mutant : io.raw(name) }), /supporting authority source/);
    }
  }
});

test("UI188-CONTAINMENT-011: the actual inverse checks unique literals, offsets and complete before reconstruction below the independent fixture pin", () => {
  const io = fixed(), input = contract().records.find(row => row.path === containmentPaths[1])!;
  const current = io.raw(input.path);
  const wrongPosition = structuredClone(input);
  Object.assign(wrongPosition.hunks[0], { offsetAfter: wrongPosition.hunks[0].offsetAfter + 1 });
  assert.throws(() => inverseContainmentRecord(wrongPosition, current, io), /exact inverse position/);
  const missingLiteral = structuredClone(input);
  Object.assign(missingLiteral.hunks[0], { after: missingLiteral.hunks[0].after + "not-an-actual-literal" });
  assert.throws(() => inverseContainmentRecord(missingLiteral, current, io), /inverse hunk must occur uniquely/);
  const falseBefore = structuredClone(input);
  Object.assign(falseBefore.hunks[0], { before: falseBefore.hunks[0].before + "unapproved-before" });
  assert.throws(() => inverseContainmentRecord(falseBefore, current, io), /restore complete fixed before bytes/);
});
