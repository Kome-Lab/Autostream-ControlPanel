import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { lstatSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import ts from "typescript";

export const containmentBeforeCommit = "c2e8b25ac8b96d985a13f7a3a457365adacb8a83";
export const containmentBeforeTree = "e9cb92b26c29254f783534b2a3eb6649c0fcd496";
export const containmentFixturePath = "web/tests/fixtures/ui-regression/ui188-cp-containment-source-delta.json";
// The fixture binds behavior sources only. This validator is independently
// reviewed and bound by the final raw inventory and signed Git tree.
const fixtureSha256 = "52c33be9e3bbe2ebb051464c764fb9b43fd082f55f4994221af914f0be6d15ee";
const supportBindings: Readonly<Record<string, Snapshot>> = {
  "web/tests/ui-regression/source-normalization.mts": {
    "path": "web/tests/ui-regression/source-normalization.mts",
    "mode": "100644",
    "blob": "273abc5cf3783789af6a4de2aa6b77bdbc3b4b65",
    "sha256": "08ce320d557424ce5787243ea2e70d1194460b24a8b487ba0d8fda8ddb1a4ea1"
  },
  "web/tests/ui-regression/ci-closure-deltas.test.mts": {
    "path": "web/tests/ui-regression/ci-closure-deltas.test.mts",
    "mode": "100644",
    "blob": "fd07193bc6d89b52a13cef4a680da0502932c5b3",
    "sha256": "37a846a838de455b2c3c58ccfcb07593272335e5107ef121a0b55a015b31e33c"
  },
  "web/tests/ui-regression/ui188-current-source-deltas.test.mts": {
    "path": "web/tests/ui-regression/ui188-current-source-deltas.test.mts",
    "mode": "100644",
    "blob": "4d184bb1a2cbfe491489e7cc7c6a1d35c302dc90",
    "sha256": "b4d5d47c868fdc51c63b0bc5fb3c64c2b7db2f0ee4d5608e1531dad68ac84d68"
  },
  "web/tests/ui-regression/ui188-r3-session-delta.test.mts": {
    "path": "web/tests/ui-regression/ui188-r3-session-delta.test.mts",
    "mode": "100644",
    "blob": "2badccebb25ad4738ec3fa966257437d9a262509",
    "sha256": "a13fde23e66220be641b80f0900e9cb3c97080a39f3613c352aa2dc94e189f40"
  },
  "web/tests/ui-regression/parity.test.mts": {
    "path": "web/tests/ui-regression/parity.test.mts",
    "mode": "100644",
    "blob": "16f93da6de2f164043c6791aeb014ef05799ff95",
    "sha256": "31464d1c5ac74fcee87ab3130a33a82b746bc1cea0bc5474cdb923c6663e3c34"
  },
  "web/tests/ui-regression/ui188-cp-containment-source-delta.test.mts": {
    "path": "web/tests/ui-regression/ui188-cp-containment-source-delta.test.mts",
    "mode": "100644",
    "blob": "85f1e177243987376abe1539954fedfc5c34b4f1",
    "sha256": "87b0f6c4926f79ce45a6639ff0dd21801f869145fdff23be5c7e47bc27f8efda"
  }
}; // AUTHORITY_SUPPORT_BINDINGS
export const containmentPaths: readonly string[] = Object.freeze([
  "scripts/ci/source-owner-map.json",
  "web/tests/helpers/browser-harness.mts",
  "web/tests/helpers/browser-command-deadline.mts",
  "web/tests/helpers/browser-next-readiness-fixture.mts",
  "web/tests/ui-regression/browser-contracts.test.mts",
  "web/tests/ui-regression/condition-lifecycle.mts",
  "web/tests/ui-regression/command-deadline.test.mts",
  "web/tests/ui-regression/visible-trigger.mts",
  "web/tests/ui-regression/run-browser.mts",
]);
const additions = [containmentPaths[2], containmentPaths[6]];
const ownerIds = ["B9-M009", "B9-M016", "B9-M051", "B9-M052"];
type Snapshot = Readonly<{ path: string; mode: string; blob: string; sha256: string }>;
type Hunk = Readonly<{ offsetBefore: number; offsetAfter: number; before: string; after: string }>;
type DeltaRecord = Readonly<{ path: string; before: Snapshot | null; after: Snapshot; hunks: readonly Hunk[] }>;
export type ContainmentContract = Readonly<{
  schemaVersion: number; beforeCommit: string; beforeTree: string; records: readonly DeltaRecord[];
}>;
export type ContainmentIO = {
  raw: (path: string) => Buffer;
  mode: (path: string) => string;
  object: (commit: string, path: string) => Buffer;
  entry: (commit: string, path: string) => { mode: string; blob: string } | undefined;
  tree: (commit: string) => string;
};
const hash = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");
const blob = (bytes: Buffer) => createHash("sha1").update(`blob ${bytes.length}\0`).update(bytes).digest("hex");
const safePath = (path: string) => typeof path === "string" && /^[A-Za-z0-9_./-]+$/.test(path)
  && !path.startsWith("/") && path.split("/").every(part => part !== "." && part !== ".." && part !== "");
const keys = (value: object, expected: readonly string[], reason: string) =>
  assert.deepEqual(Object.keys(value).sort(), [...expected].sort(), reason);

function assertSnapshot(value: Snapshot, path: string) {
  assert.ok(value && typeof value === "object", "containment snapshot object");
  keys(value, ["path", "mode", "blob", "sha256"], "containment snapshot closed schema");
  assert.equal(value.path, path, "containment snapshot path identity");
  assert.equal(value.mode, "100644", "containment permits no chmod or non-file mode");
  assert.match(value.blob, /^[a-f0-9]{40}$/);
  assert.match(value.sha256, /^[a-f0-9]{64}$/);
}

export function assertContainmentContract(raw: Buffer): ContainmentContract {
  assert.equal(hash(raw), fixtureSha256, "containment independently pinned fixture bytes changed");
  const value = JSON.parse(raw.toString("utf8")) as ContainmentContract;
  keys(value, ["schemaVersion", "beforeCommit", "beforeTree", "records"], "containment fixture closed schema");
  assert.equal(value.schemaVersion, 1);
  assert.equal(value.beforeCommit, containmentBeforeCommit, "containment fixed before commit");
  assert.equal(value.beforeTree, containmentBeforeTree, "containment fixed before tree");
  assert.ok(Array.isArray(value.records));
  assert.deepEqual(value.records.map(row => row.path), containmentPaths, "containment exact ordered nine-path group");
  assert.equal(new Set(value.records.map(row => row.path.toLowerCase())).size, value.records.length, "containment path collision");
  for (const row of value.records) {
    keys(row, ["path", "before", "after", "hunks"], "containment record closed schema");
    assert.ok(safePath(row.path), "containment bounded relative path");
    assertSnapshot(row.after, row.path);
    assert.ok(Array.isArray(row.hunks));
    if (additions.includes(row.path)) {
      assert.equal(row.before, null, "containment addition means absent, not an empty before file");
      assert.equal(row.hunks.length, 0, "containment additions have no historical inverse hunks");
    } else {
      assert.ok(row.before, "containment modified file needs a before snapshot");
      assertSnapshot(row.before, row.path);
      assert.notEqual(row.before.sha256, row.after.sha256, "containment modified source must differ");
      assert.ok(row.hunks.length > 0 && row.hunks.length <= 64, "containment finite hunk count");
    }
    let beforeEnd = 0, afterEnd = 0;
    for (const hunk of row.hunks) {
      keys(hunk, ["offsetBefore", "offsetAfter", "before", "after"], "containment hunk closed schema");
      assert.ok(Number.isSafeInteger(hunk.offsetBefore) && hunk.offsetBefore >= beforeEnd, "containment hunk before order");
      assert.ok(Number.isSafeInteger(hunk.offsetAfter) && hunk.offsetAfter >= afterEnd, "containment hunk after order");
      assert.ok(typeof hunk.before === "string" && hunk.before.length && typeof hunk.after === "string" && hunk.after.length, "containment contextual nonempty hunk");
      assert.notEqual(hunk.before, hunk.after, "containment hunk must specify a real edit");
      beforeEnd = hunk.offsetBefore + hunk.before.length;
      afterEnd = hunk.offsetAfter + hunk.after.length;
    }
  }
  return value;
}

export function containmentIO(root: string, overrides: Partial<ContainmentIO> = {}): ContainmentIO {
  const git = (argv: string[]) => execFileSync("git", argv, { cwd: root, maxBuffer: 32 * 1024 * 1024 });
  return {
    raw: path => readFileSync(resolve(root, path)),
    mode: path => {
      const stat = lstatSync(resolve(root, path));
      assert.ok(stat.isFile() && !stat.isSymbolicLink(), "containment actual current must be a regular file");
      return stat.mode & 0o111 ? "100755" : "100644";
    },
    object: (commit, path) => git(["show", commit + ":" + path]),
    entry: (commit, path) => {
      const bytes = git(["ls-tree", "-z", commit, "--", path]).toString("utf8");
      if (bytes === "") return undefined;
      const match = /^([0-7]{6}) blob ([a-f0-9]{40})\t([^\0]+)\0$/.exec(bytes);
      assert.ok(match && match[3] === path, "containment exact fixed Git tree entry");
      return { mode: match[1], blob: match[2] };
    },
    tree: commit => git(["rev-parse", commit + "^{tree}"]).toString("ascii").trim(),
    ...overrides,
  };
}

function assertBytes(bytes: Buffer, snapshot: Snapshot, reason: string) {
  assert.equal(hash(bytes), snapshot.sha256, reason + " raw hash; normalization permits only exact specified name edits");
  assert.equal(blob(bytes), snapshot.blob, reason + " Git blob");
}

export function inverseContainmentRecord(row: DeltaRecord, current: Buffer, io: ContainmentIO): Buffer {
  assert.equal(io.tree(containmentBeforeCommit), containmentBeforeTree, "containment fixed before tree mismatch");
  assertBytes(current, row.after, "containment actual current");
  assert.equal(io.mode(row.path), row.after.mode, "containment actual current mode");
  const entry = io.entry(containmentBeforeCommit, row.path);
  if (row.before === null) {
    assert.equal(entry, undefined, "containment addition must be absent in the fixed base, not empty");
    return current;
  }
  assert.deepEqual(entry, { mode: row.before.mode, blob: row.before.blob }, "containment fixed before blob/mode mismatch");
  const before = io.object(containmentBeforeCommit, row.path);
  assertBytes(before, row.before, "containment fixed before");
  let inverse = current.toString("utf8");
  assert.deepEqual(Buffer.from(inverse), current, "containment UTF-8 source round trip");
  for (const hunk of [...row.hunks].reverse()) {
    assert.equal(inverse.split(hunk.after).length, 2, "containment inverse hunk must occur uniquely");
    assert.equal(inverse.slice(hunk.offsetAfter, hunk.offsetAfter + hunk.after.length), hunk.after, "containment exact inverse position");
    inverse = inverse.slice(0, hunk.offsetAfter) + hunk.before + inverse.slice(hunk.offsetAfter + hunk.after.length);
  }
  const restored = Buffer.from(inverse);
  assert.deepEqual(restored, before, "containment inverse must restore complete fixed before bytes");
  let forward = before.toString("utf8");
  for (const hunk of [...row.hunks].reverse()) {
    assert.equal(forward.split(hunk.before).length, 2, "containment forward hunk must occur uniquely");
    assert.equal(forward.slice(hunk.offsetBefore, hunk.offsetBefore + hunk.before.length), hunk.before, "containment exact forward position");
    forward = forward.slice(0, hunk.offsetBefore) + hunk.after + forward.slice(hunk.offsetBefore + hunk.before.length);
  }
  assert.deepEqual(Buffer.from(forward), current, "containment only the finite contextual edits are accepted");
  return restored;
}

// Construction performs no group I/O. Read current raw first, then invoke this
// entry once, before any old R5/R4/session/profile/UI188 lineage transform.
export function createContainmentAdapter(root: string, overrides: Partial<ContainmentIO> = {}) {
  const io = containmentIO(root, overrides);
  return {
    inverse(path: string, current: Buffer): Buffer {
      if (!containmentPaths.includes(path)) return current;
      const contract = assertContainmentContract(io.raw(containmentFixturePath));
      const row = contract.records.find(item => item.path === path)!;
      return inverseContainmentRecord(row, current, io);
    },
    io,
  };
}

export function assertContainmentRegistration(source: string, module: string) {
  const parsed = ts.createSourceFile("registration.mts", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const direct = parsed.statements.filter(node => ts.isImportDeclaration(node) && ts.isStringLiteral(node.moduleSpecifier)
    && node.moduleSpecifier.text === module && node.importClause === undefined);
  let dynamic = 0;
  const visit = (node: ts.Node) => {
    if (ts.isCallExpression(node) && node.arguments.some(argument => ts.isStringLiteral(argument) && argument.text === module)) dynamic++;
    ts.forEachChild(node, visit);
  };
  visit(parsed);
  assert.equal(direct.length, 1, "containment test must be registered exactly once as a top-level side-effect import");
  assert.equal(dynamic, 0, "containment test registration cannot be dynamic or skipped");
}

type Destination = { path: string; sha256: string; symbols: string[]; physical_lines: number };
type Owner = { target_id: string; destinations: Destination[]; [key: string]: unknown };
function symbols(source: string): Set<string> {
  const parsed = ts.createSourceFile("owner.mts", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const result = new Set<string>();
  const visit = (node: ts.Node) => {
    if ((ts.isFunctionDeclaration(node) || ts.isClassDeclaration(node) || ts.isMethodDeclaration(node)
      || ts.isGetAccessorDeclaration(node) || ts.isSetAccessorDeclaration(node)) && node.name && ts.isIdentifier(node.name)) result.add(node.name.text);
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.initializer
      && (ts.isArrowFunction(node.initializer) || ts.isFunctionExpression(node.initializer))) result.add(node.name.text);
    if ((ts.isPropertyAssignment(node) || ts.isPropertyDeclaration(node)) && ts.isIdentifier(node.name) && node.initializer
      && (ts.isArrowFunction(node.initializer) || ts.isFunctionExpression(node.initializer))) result.add(node.name.text);
    ts.forEachChild(node, visit);
  };
  visit(parsed);
  return result;
}
const physicalLines = (source: string) => source.split(/\r\n|\r|\n/).length - (/\r?\n$/.test(source) ? 1 : 0);

export function assertContainmentOwnership(beforeBytes: Buffer, currentBytes: Buffer, raw: (path: string) => Buffer) {
  const before = JSON.parse(beforeBytes.toString("utf8")) as Owner[];
  const current = JSON.parse(currentBytes.toString("utf8")) as Owner[];
  assert.equal(before.length, 51, "containment original owner denominator");
  assert.equal(current.length, 51, "containment owner denominator remains 51");
  assert.deepEqual(current.map(row => row.target_id), before.map(row => row.target_id), "containment owner identity and order");
  assert.equal(new Set(current.map(row => row.target_id)).size, 51);
  let unchanged = 0;
  for (let index = 0; index < current.length; index++) {
    const row = current[index], old = before[index];
    if (!ownerIds.includes(row.target_id)) {
      assert.deepEqual(row, old, "containment other 47 owner targets remain unchanged");
      unchanged++;
      continue;
    }
    const { destinations: _currentDestinations, ...identity } = row;
    const { destinations: _oldDestinations, ...oldIdentity } = old;
    assert.deepEqual(identity, oldIdentity, "containment unapproved owner field change");
    assert.equal(new Set(row.destinations.map(item => item.path)).size, row.destinations.length, "containment owner destination uniqueness");
    assert.deepEqual(row.destinations.map(item => item.path).filter(path => path !== additions[0]), old.destinations.map(item => item.path), "containment owner permits only the explicit deadline-module addition");
    for (const destination of row.destinations) {
      if (!containmentPaths.includes(destination.path)) {
        assert.deepEqual(destination, old.destinations.find(item => item.path === destination.path), "containment unrelated destination unchanged");
        continue;
      }
      keys(destination, ["path", "sha256", "symbols", "physical_lines"], "containment owner destination closed schema");
      const bytes = raw(destination.path), source = bytes.toString("utf8"), actual = symbols(source);
      assert.equal(destination.sha256, hash(bytes), "containment actual owner source hash");
      assert.equal(destination.physical_lines, physicalLines(source), "containment actual owner physical lines");
      assert.equal(new Set(destination.symbols).size, destination.symbols.length, "containment owner symbol uniqueness");
      assert.ok(destination.symbols.every(symbol => actual.has(symbol)), "containment actual owner symbol: " + destination.path + ": " + destination.symbols.filter(symbol => !actual.has(symbol)).join(","));
      if (destination.path === containmentPaths[1]) assert.ok(destination.physical_lines <= 800, "containment keeps the original harness 800-line limit");
    }
  }
  assert.equal(unchanged, 47);
  const moduleOwners = current.filter(row => row.destinations.some(item => item.path === additions[0])).map(row => row.target_id);
  assert.deepEqual(moduleOwners, ownerIds, "containment deadline module belongs exactly to the four original targets");
}

export function assertContainmentSupportSources(io: ContainmentIO) {
  assert.equal(Object.keys(supportBindings).length, 6, "containment six authority support sources are independently bound");
  for (const [path, snapshot] of Object.entries(supportBindings)) {
    assertSnapshot(snapshot, path);
    assertBytes(io.raw(path), snapshot, "containment supporting authority source " + path);
    assert.equal(io.mode(path), snapshot.mode, "containment supporting authority mode");
  }
}
export const containmentSupportPaths = () => Object.keys(supportBindings);

export function assertContainmentGroup(root: string, overrides: Partial<ContainmentIO> = {}) {
  const adapter = createContainmentAdapter(root, overrides), io = adapter.io;
  const contract = assertContainmentContract(io.raw(containmentFixturePath));
  const current = new Map<string, Buffer>();
  for (const row of contract.records) {
    const bytes = io.raw(row.path);
    current.set(row.path, bytes);
    inverseContainmentRecord(row, bytes, io);
  }
  assertContainmentRegistration(io.raw("web/tests/ui-regression/parity.test.mts").toString("utf8"), "./ui188-cp-containment-source-delta.test.mts");
  assertContainmentRegistration(current.get(containmentPaths[4])!.toString("utf8"), "./command-deadline.test.mts");
  const harness = ts.createSourceFile("harness.mts", current.get(containmentPaths[1])!.toString("utf8"), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const moduleImports = harness.statements.filter(node => ts.isImportDeclaration(node) && ts.isStringLiteral(node.moduleSpecifier) && node.moduleSpecifier.text === "./browser-command-deadline.mts");
  assert.equal(moduleImports.length, 1, "containment added deadline module is directly imported by the actual harness");
  const owner = current.get(containmentPaths[0])!;
  assertContainmentOwnership(io.object(containmentBeforeCommit, containmentPaths[0]), owner, io.raw);
  assertContainmentSupportSources(io);
  return { records: contract.records.length, modified: contract.records.filter(row => row.before !== null).length, additions: additions.length, owners: 51, unchangedOwners: 47, supportSources: 6 };
}
