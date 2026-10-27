import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { lstatSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import ts from "typescript";

// This current lineage layer is deliberately outside every frozen UI188
// contract. Its output is source evidence, never executable product behavior.
export const streamCreateCurrentBase = "f64cd5cd423afda31644afbfaddfbc52313a1648";
export const streamCreateCurrentTree = "6faec1199df72ddd1628a72d7c9bf1badc30d43d";
export const streamCreateCurrentFixturePath = "web/tests/fixtures/ui-regression/stream-create-current-source-delta.json";
const fixtureSha256 = "ad66febe30034dfd59b079ed917be926b1dbb872ce75a49fd5e7944db801d683";
export const streamCreateCurrentPaths = Object.freeze([
  ".github/workflows/ci.yml",
  "internal/httpapi/control_platform_features.go",
  "internal/httpapi/control_platform_visual_cover.go",
  "internal/httpapi/server_stream_assignments.go",
  "internal/httpapi/server_stream_lifecycle_test.go",
  "internal/httpapi/server_stream_settings.go",
  "internal/httpapi/server_stream_settings_test.go",
  "internal/httpapi/server_streams.go",
  "internal/mediaassets/memory_repository.go",
  "internal/mediaassets/sql_repository.go",
  "internal/mediaassets/types.go",
  "internal/store/memory_stream_settings.go",
  "internal/store/stream_mutations_mariadb.go",
  "internal/store/streams.go",
  "internal/streamvisual/memory.go",
  "internal/streamvisual/repository.go",
  "scripts/ci/source-owner-map.json",
  "web/package.json",
  "web/src/components/admin/i18n-provider.tsx",
  "web/src/components/admin/theme-provider.tsx",
  "web/src/features/streams/stream-action-control.tsx",
  "web/src/features/streams/stream-visual-draft.ts",
  "web/src/lib/foundation/api-errors/registry.ts",
  "web/src/lib/i18n.ts",
  "web/tests/helpers/browser-fetch-settlement-cases.mts",
  "web/tests/helpers/browser-harness.mts",
  "web/tests/ui-refresh-layout-browser.test.mts",
  "web/tests/ui-regression/browser-contracts.test.mts",
  "web/tests/ui-regression/ci-closure-deltas.test.mts",
  "web/tests/ui-regression/cp174-source-deltas.test.mts",
  "web/tests/ui-regression/parity.test.mts",
  "web/tests/ui-regression/release-assembly-deltas.test.mts",
  "web/tests/ui-regression/run-browser.mts",
  "web/tests/ui-regression/source-normalization.mts",
  "web/tests/ui-regression/ui188-cp-containment-source-delta.test.mts",
  "web/tests/ui-regression/ui188-current-source-deltas.test.mts",
  "web/tests/ui-regression/ui188-r3-session-delta.test.mts",
  "web/tests/ui-regression/ui188-r4-delete-delta.test.mts",
  "web/tests/ui-regression/ui188-r5-readiness-delta.test.mts",
] as const);

type Snapshot = Readonly<{ mode: string; blob: string; sha256: string }>;
type Hunk = Readonly<{ offsetBefore: number; offsetAfter: number; before: string; after: string }>;
export type StreamCreateCurrentRecord = Readonly<{ path: string; before: Snapshot; after: Snapshot; hunks: readonly Hunk[] }>;
export type StreamCreateCurrentContract = Readonly<{ schemaVersion: number; beforeCommit: string; beforeTree: string; records: readonly StreamCreateCurrentRecord[] }>;
export type StreamCreateCurrentIO = {
  raw: (path: string) => Buffer;
  mode: (path: string) => string;
  object: (ref: string, path: string) => Buffer;
  entry: (ref: string, path: string) => { mode: string; blob: string } | undefined;
  tree: (ref: string) => string;
};
const hash = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");
const blob = (bytes: Buffer) => createHash("sha1").update(`blob ${bytes.length}\0`).update(bytes).digest("hex");
const keys = (value: object, expected: string[]) => assert.deepEqual(Object.keys(value).sort(), expected.sort(), "stream-create closed metadata schema");
const safePath = (path: string) => /^[A-Za-z0-9_./-]+$/.test(path) && !path.startsWith("/") && path.split("/").every(part => part !== "." && part !== ".." && part !== "");

function assertSnapshot(value: Snapshot) {
  keys(value, ["mode", "blob", "sha256"]);
  assert.equal(value.mode, "100644", "stream-create permits no mode changes");
  assert.match(value.blob, /^[a-f0-9]{40}$/);
  assert.match(value.sha256, /^[a-f0-9]{64}$/);
}

export function assertStreamCreateCurrentContract(bytes: Buffer): StreamCreateCurrentContract {
  assert.equal(hash(bytes), fixtureSha256, "stream-create independently pinned current fixture bytes changed");
  const value = JSON.parse(bytes.toString("utf8")) as StreamCreateCurrentContract;
  keys(value, ["schemaVersion", "beforeCommit", "beforeTree", "records"]);
  assert.equal(value.schemaVersion, 1);
  assert.equal(value.beforeCommit, streamCreateCurrentBase);
  assert.equal(value.beforeTree, streamCreateCurrentTree);
  assert.deepEqual(value.records.map(row => row.path), streamCreateCurrentPaths, "stream-create exact reviewed current path set");
  assert.equal(new Set(value.records.map(row => row.path.toLowerCase())).size, 39);
  for (const row of value.records) {
    keys(row, ["path", "before", "after", "hunks"]);
    assert.ok(safePath(row.path), "stream-create bounded current path");
    assertSnapshot(row.before); assertSnapshot(row.after);
    assert.notEqual(row.before.sha256, row.after.sha256, "stream-create records contain actual changes only");
    assert.ok(row.hunks.length > 0 && row.hunks.length <= 64, "stream-create finite contextual hunk count");
    let beforeEnd = 0, afterEnd = 0;
    for (const hunk of row.hunks) {
      keys(hunk, ["offsetBefore", "offsetAfter", "before", "after"]);
      assert.ok(Number.isSafeInteger(hunk.offsetBefore) && hunk.offsetBefore >= beforeEnd);
      assert.ok(Number.isSafeInteger(hunk.offsetAfter) && hunk.offsetAfter >= afterEnd);
      assert.ok(typeof hunk.before === "string" && hunk.before.length > 0 && typeof hunk.after === "string" && hunk.after.length > 0);
      assert.notEqual(hunk.before, hunk.after);
      beforeEnd = hunk.offsetBefore + Buffer.byteLength(hunk.before);
      afterEnd = hunk.offsetAfter + Buffer.byteLength(hunk.after);
    }
  }
  return value;
}

export function streamCreateCurrentIO(root: string, overrides: Partial<StreamCreateCurrentIO> = {}): StreamCreateCurrentIO {
  const git = (args: string[]) => execFileSync("git", args, { cwd: root, maxBuffer: 32 * 1024 * 1024 });
  return {
    raw: path => readFileSync(resolve(root, path)),
    mode: path => {
      const stat = lstatSync(resolve(root, path));
      assert.ok(stat.isFile() && !stat.isSymbolicLink(), "stream-create actual source must be a regular file");
      return stat.mode & 0o111 ? "100755" : "100644";
    },
    object: (ref, path) => git(["show", ref + ":" + path]),
    entry: (ref, path) => {
      const output = git(["ls-tree", "-z", ref, "--", path]).toString("utf8");
      if (!output) return undefined;
      const match = /^([0-7]{6}) blob ([a-f0-9]{40})\t([^\0]+)\0$/.exec(output);
      assert.ok(match && match[3] === path, "stream-create exact immutable tree entry");
      return { mode: match[1], blob: match[2] };
    },
    tree: ref => git(["rev-parse", ref + "^{tree}"]).toString("ascii").trim(),
    ...overrides,
  };
}

function assertBytes(bytes: Buffer, expected: Snapshot, reason: string) {
  assert.equal(hash(bytes), expected.sha256, reason);
  assert.equal(blob(bytes), expected.blob, reason + " Git blob");
}

function contextualSplice(bytes: Buffer, hunks: readonly Hunk[], side: "before" | "after"): Buffer {
  let restored = bytes;
  for (const hunk of [...hunks].reverse()) {
    const literal = Buffer.from(hunk[side]);
    const replacement = Buffer.from(hunk[side === "before" ? "after" : "before"]);
    const offset = side === "before" ? hunk.offsetBefore : hunk.offsetAfter;
    const first = restored.indexOf(literal);
    assert.ok(first >= 0 && restored.indexOf(literal, first + 1) < 0, "stream-create contextual literal must occur uniquely");
    assert.equal(first, offset, "stream-create exact contextual byte offset");
    restored = Buffer.concat([restored.subarray(0, offset), replacement, restored.subarray(offset + literal.length)]);
  }
  return restored;
}

export function inverseStreamCreateCurrentRecord(row: StreamCreateCurrentRecord, current: Buffer, io: StreamCreateCurrentIO): Buffer {
  assert.ok(streamCreateCurrentPaths.some(path => path === row.path), "stream-create unknown reviewed path");
  assertBytes(current, row.after, "stream-create unreviewed current source bytes");
  assert.equal(io.mode(row.path), row.after.mode, "stream-create actual current source mode");
  assert.equal(io.tree(streamCreateCurrentBase), streamCreateCurrentTree, "stream-create fixed base tree");
  assert.deepEqual(io.entry(streamCreateCurrentBase, row.path), { mode: row.before.mode, blob: row.before.blob }, "stream-create fixed base blob/mode");
  const before = io.object(streamCreateCurrentBase, row.path);
  assertBytes(before, row.before, "stream-create fixed base source bytes");
  assert.deepEqual(Buffer.from(current.toString("utf8")), current, "stream-create current UTF-8 round trip");
  assert.deepEqual(Buffer.from(before.toString("utf8")), before, "stream-create base UTF-8 round trip");
  const restored = contextualSplice(current, row.hunks, "after");
  assert.deepEqual(restored, before, "stream-create inverse restores complete fixed base bytes");
  assert.deepEqual(contextualSplice(before, row.hunks, "before"), current, "stream-create only reviewed finite contextual changes are accepted");
  return restored;
}

// Construction is lazy. Call only after obtaining actual raw bytes, exactly
// once before containment or any older historical source projection.
export function createStreamCreateCurrentAdapter(root: string, overrides: Partial<StreamCreateCurrentIO> = {}) {
  const io = streamCreateCurrentIO(root, overrides);
  return {
    inverse(path: string, current: Buffer): Buffer {
      if (!streamCreateCurrentPaths.some(listed => listed === path)) return current;
      const contract = assertStreamCreateCurrentContract(io.raw(streamCreateCurrentFixturePath));
      return inverseStreamCreateCurrentRecord(contract.records.find(row => row.path === path)!, current, io);
    },
    io,
  };
}

export function assertStreamCreateCurrentRegistration(source: string) {
  const target = "./stream-create-current-source-delta.test.mts";
  const file = ts.createSourceFile("parity.test.mts", source, ts.ScriptTarget.Latest, true);
  const direct = file.statements.filter(node => ts.isImportDeclaration(node) && ts.isStringLiteral(node.moduleSpecifier)
    && node.moduleSpecifier.text === target && node.importClause === undefined).length;
  let dynamic = 0;
  const visit = (node: ts.Node) => {
    if (ts.isCallExpression(node) && node.arguments.some(arg => ts.isStringLiteral(arg) && arg.text === target)) dynamic++;
    ts.forEachChild(node, visit);
  };
  visit(file);
  assert.equal(direct, 1, "stream-create current tests register exactly once at top level");
  assert.equal(dynamic, 0, "stream-create current registration cannot be dynamic or skipped");
}
