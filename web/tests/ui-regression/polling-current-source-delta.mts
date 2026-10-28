import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { lstatSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import {
  pollingCurrentBase, pollingCurrentTree, pollingCurrentFixturePath,
  pollingCurrentFixtureSHA256, pollingCurrentPaths, pollingInfrastructurePaths,
} from "./polling-current-source-contract.mts";

export type PollingSnapshot = Readonly<{ mode: string; blob: string; sha256: string }>;
export type PollingHunk = Readonly<{ offsetBefore: number; offsetAfter: number; before: string; after: string }>;
export type PollingRecord = Readonly<{ path: string; before: PollingSnapshot | null; after: PollingSnapshot; hunks: readonly PollingHunk[] }>;
export type PollingContract = Readonly<{ schemaVersion: number; beforeCommit: string; beforeTree: string; records: readonly PollingRecord[] }>;
export type PollingSourceIO = {
  raw: (path: string) => Buffer;
  mode: (path: string) => string;
  object: (ref: string, path: string) => Buffer;
  entry: (ref: string, path: string) => { mode: string; blob: string } | undefined;
  tree: (ref: string) => string;
};
export const pollingHash = (value: Uint8Array) => createHash("sha256").update(value).digest("hex");
export const pollingBlob = (value: Buffer) => createHash("sha1").update(`blob ${value.length}\0`).update(value).digest("hex");
const closed = (value: object, expected: string[]) => assert.deepEqual(Object.keys(value).sort(), expected.sort(), "polling closed source metadata schema");
const safePath = (path: string) => /^[A-Za-z0-9_./-]+$/.test(path) && !path.startsWith("/") && path.split("/").every(part => part !== "." && part !== ".." && part !== "");

function assertSnapshot(value: PollingSnapshot) {
  closed(value, ["mode", "blob", "sha256"]);
  assert.equal(value.mode, "100644", "polling source permits no mode changes");
  assert.match(value.blob, /^[a-f0-9]{40}$/);
  assert.match(value.sha256, /^[a-f0-9]{64}$/);
}

export function assertPollingCurrentContract(bytes: Buffer): PollingContract {
  assert.equal(pollingHash(bytes), pollingCurrentFixtureSHA256, "polling independently pinned current fixture bytes changed");
  const value = JSON.parse(bytes.toString("utf8")) as PollingContract;
  closed(value, ["schemaVersion", "beforeCommit", "beforeTree", "records"]);
  assert.equal(value.schemaVersion, 1);
  assert.equal(value.beforeCommit, pollingCurrentBase);
  assert.equal(value.beforeTree, pollingCurrentTree);
  assert.ok(value.records.length > 0, "polling current source denominator is nonzero");
  assert.deepEqual(value.records.map(record => record.path), pollingCurrentPaths, "polling exact reviewed current path set");
  assert.equal(new Set(value.records.map(record => record.path.toLowerCase())).size, value.records.length);
  for (const record of value.records) {
    closed(record, ["path", "before", "after", "hunks"]);
    assert.ok(safePath(record.path), "polling bounded source path");
    assertSnapshot(record.after);
    if (record.before !== null) {
      assertSnapshot(record.before);
      assert.notEqual(record.before.sha256, record.after.sha256, "polling records contain actual changes only");
    }
    assert.ok(record.hunks.length > 0 && record.hunks.length <= 64, "polling finite contextual hunk count");
    let beforeEnd = 0, afterEnd = 0;
    for (const hunk of record.hunks) {
      closed(hunk, ["offsetBefore", "offsetAfter", "before", "after"]);
      assert.ok(Number.isSafeInteger(hunk.offsetBefore) && hunk.offsetBefore >= beforeEnd);
      assert.ok(Number.isSafeInteger(hunk.offsetAfter) && hunk.offsetAfter >= afterEnd);
      assert.ok(typeof hunk.before === "string" && typeof hunk.after === "string" && hunk.after.length > 0);
      if (record.before !== null) assert.ok(hunk.before.length > 0);
      assert.notEqual(hunk.before, hunk.after);
      beforeEnd = hunk.offsetBefore + Buffer.byteLength(hunk.before);
      afterEnd = hunk.offsetAfter + Buffer.byteLength(hunk.after);
    }
    if (record.before === null) {
      assert.equal(record.hunks.length, 1, "polling introduced source has one complete addition");
      assert.deepEqual([record.hunks[0].offsetBefore, record.hunks[0].offsetAfter, record.hunks[0].before], [0, 0, ""]);
    }
  }
  return value;
}

export function pollingCurrentIO(root: string, overrides: Partial<PollingSourceIO> = {}): PollingSourceIO {
  const git = (args: string[]) => execFileSync("git", args, { cwd: root, maxBuffer: 64 * 1024 * 1024 });
  return {
    raw: path => readFileSync(resolve(root, path)),
    mode: path => {
      const stat = lstatSync(resolve(root, path));
      assert.ok(stat.isFile() && !stat.isSymbolicLink(), "polling actual source must be a regular file");
      return stat.mode & 0o111 ? "100755" : "100644";
    },
    object: (ref, path) => git(["cat-file", "blob", `${ref}:${path}`]),
    entry: (ref, path) => {
      const output = git(["ls-tree", "-z", ref, "--", path]).toString("utf8");
      if (!output) return undefined;
      const match = /^([0-7]{6}) blob ([a-f0-9]{40})\t([^\0]+)\0$/.exec(output);
      assert.ok(match && match[3] === path, "polling exact immutable tree entry");
      return { mode: match[1], blob: match[2] };
    },
    tree: ref => git(["rev-parse", ref + "^{tree}"]).toString("ascii").trim(),
    ...overrides,
  };
}

function assertBytes(bytes: Buffer, snapshot: PollingSnapshot, reason: string) {
  assert.equal(pollingHash(bytes), snapshot.sha256, reason);
  assert.equal(pollingBlob(bytes), snapshot.blob, reason + " Git blob");
  assert.deepEqual(Buffer.from(bytes.toString("utf8")), bytes, "polling source UTF-8 round trip");
}

function spliceContexts(bytes: Buffer, hunks: readonly PollingHunk[], side: "before" | "after") {
  let restored = bytes;
  for (const hunk of [...hunks].reverse()) {
    const literal = Buffer.from(hunk[side]);
    const replacement = Buffer.from(hunk[side === "before" ? "after" : "before"]);
    const offset = side === "before" ? hunk.offsetBefore : hunk.offsetAfter;
    const first = restored.indexOf(literal);
    assert.ok(literal.length > 0 && first >= 0 && restored.indexOf(literal, first + 1) < 0, "polling contextual literal must occur uniquely");
    assert.equal(first, offset, "polling exact contextual byte offset");
    restored = Buffer.concat([restored.subarray(0, offset), replacement, restored.subarray(offset + literal.length)]);
  }
  return restored;
}

export function inversePollingCurrentRecord(record: PollingRecord, current: Buffer, io: PollingSourceIO): Buffer | undefined {
  assert.ok(pollingCurrentPaths.some(path => path === record.path), "polling unknown reviewed path");
  assertBytes(current, record.after, "polling unreviewed current source bytes");
  assert.equal(io.mode(record.path), record.after.mode, "polling actual current source mode");
  assert.equal(io.tree(pollingCurrentBase), pollingCurrentTree, "polling fixed base tree");
  if (record.before === null) {
    assert.equal(record.hunks.length, 1, "polling introduced source has one complete addition");
    assert.deepEqual([record.hunks[0].offsetBefore, record.hunks[0].offsetAfter, record.hunks[0].before], [0, 0, ""], "polling introduced source exact addition offsets");
    assert.equal(io.entry(pollingCurrentBase, record.path), undefined, "polling introduced source must be absent at fixed base");
    assert.deepEqual(Buffer.from(record.hunks[0].after), current, "polling introduced source complete addition");
    return undefined;
  }
  assert.deepEqual(io.entry(pollingCurrentBase, record.path), { mode: record.before.mode, blob: record.before.blob }, "polling fixed base blob/mode");
  const before = io.object(pollingCurrentBase, record.path);
  assertBytes(before, record.before, "polling fixed base source bytes");
  const restored = spliceContexts(current, record.hunks, "after");
  assert.deepEqual(restored, before, "polling inverse restores complete fixed base bytes");
  assert.deepEqual(spliceContexts(before, record.hunks, "before"), current, "polling only reviewed bidirectional contextual changes are accepted");
  return restored;
}

export function createPollingCurrentAdapter(root: string, overrides: Partial<PollingSourceIO> = {}) {
  const io = pollingCurrentIO(root, overrides);
  return {
    inverse(path: string, current: Buffer): Buffer {
      if (!pollingCurrentPaths.some(listed => listed === path)) return current;
      const contract = assertPollingCurrentContract(io.raw(pollingCurrentFixturePath));
      const record = contract.records.find(candidate => candidate.path === path)!;
      const before = inversePollingCurrentRecord(record, current, io);
      assert.ok(before, "polling introduced source has no historical content");
      return before;
    },
    io,
  };
}

export function assertPollingChangedPathInventory(root: string) {
  const git = (...args: string[]) => execFileSync("git", args, { cwd: root, maxBuffer: 64 * 1024 * 1024 }).toString("utf8").split("\0").filter(Boolean);
  const paths = new Set([...git("diff", "--name-only", "-z", pollingCurrentBase), ...git("ls-files", "--others", "--exclude-standard", "-z")]);
  for (const infrastructure of pollingInfrastructurePaths) paths.delete(infrastructure);
  assert.deepEqual([...paths].sort(), [...pollingCurrentPaths], "polling exact current changed path inventory; no omitted or added source");
}
