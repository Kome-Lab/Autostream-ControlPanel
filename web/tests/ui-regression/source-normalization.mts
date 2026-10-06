import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { inverseReleaseAssembly, releaseAssemblyBase, releaseAssemblyPaths } from "./release-assembly-deltas.mts";

import { assertCP174Original, cp174Base, cp174Paths, inverseCP174Source } from "./cp174-source-deltas.mts";

import { ciClosureBase, ciClosurePaths, inverseCIClosure } from "./ci-closure-deltas.mts";
import { cp176Base, cp176Paths, inverseCP176DependencyRepair } from "./cp176-dependency-deltas.mts";

type Replacement = { position: number; count: number; before: string; after: string };
type Mapping = { oldPath: string; newPath: string; acceptedSha256: string; currentSha256: string; replacements: Replacement[] };
type History = { fixedCommit: string; path: string; blob: string; originalSha256: string };
type Script = { old: string; new: string | null; original_command: string };
type Manifest = { schemaVersion: number; acceptedCommit: string; currentMappings: Mapping[]; historyOnly: History[]; packageScripts: Script[] };
type SourceIO = { raw: (path: string) => Buffer; exists: (path: string) => boolean; object: (commit: string, path: string) => Buffer };
const acceptedCommit = "ebfd475017b967f23d61deca7d5ab3168693de49";
const fixedHistory = "b266aabb896f0df9880a0354c1135130d6938e8d";
// Fixed, table-derived edits over the accepted Git raw; never regenerated in a test.
const manifestHash = "58594d1c2c0e681a9b4739849ebb80c1b1e54a03cadc3f98241731bfac8ed2f0";
const hash = (value: Buffer) => createHash("sha256").update(value).digest("hex");
const safePath = (path: string) => typeof path === "string" && /^[A-Za-z0-9_./-]+$/.test(path) && !path.startsWith("/") && !path.split("/").includes("..");

export function assertNormalizationManifest(raw: Buffer): Manifest {
  assert.equal(hash(raw), manifestHash, "normalization manifest differs from the fixed table-derived contract");
  const value = JSON.parse(raw.toString("utf8")) as Manifest;
  assert.equal(value.schemaVersion, 1); assert.equal(value.acceptedCommit, acceptedCommit);
  for (const field of ["oldPath", "newPath"] as const) {
    const paths = value.currentMappings.map(row => row[field]);
    assert.equal(new Set(paths.map(path => path.toLowerCase())).size, paths.length, "normalization path collision");
    assert.ok(paths.every(safePath), "normalization path must be relative and bounded");
  }
  assert.equal(value.historyOnly.length, 5);
  assert.equal(new Set(value.historyOnly.map(row => row.path)).size, 5);
  for (const row of value.historyOnly) { assert.equal(row.fixedCommit, fixedHistory); assert.ok(safePath(row.path)); }
  assert.equal(value.packageScripts.length, 6);
  return value;
}

export function inverseNormalization(row: Mapping, original: Buffer, current: Buffer): Buffer {
  assert.equal(hash(original), row.acceptedSha256, "normalization accepted Git raw hash mismatch");
  let forward = original.toString("utf8"), offset = 0, end = 0;
  for (const change of row.replacements) {
    assert.ok(Number.isSafeInteger(change.position) && change.position >= end, "normalization edit position/order");
    assert.equal(change.count, 1);
    assert.equal(forward.slice(change.position + offset, change.position + offset + change.before.length), change.before, "normalization exact original edit");
    const at = change.position + offset;
    forward = forward.slice(0, at) + change.after + forward.slice(at + change.before.length);
    end = change.position + change.before.length; offset += change.after.length - change.before.length;
  }
  if (cp174Paths.includes(row.newPath)) current = inverseCP174Source(row.newPath, current);
  if (ciClosurePaths.includes(row.newPath)) current = inverseCIClosure(row.newPath, Buffer.from(forward), current);
  if (releaseAssemblyPaths.includes(row.newPath)) current = inverseReleaseAssembly(row.newPath, Buffer.from(forward), current);
  assert.deepEqual(current, Buffer.from(forward), "normalization permits only exact specified name edits");
  assert.equal(hash(current), row.currentSha256, "normalization current raw hash mismatch");
  let inverse = current.toString("utf8");
  for (const change of [...row.replacements].reverse()) {
    offset -= change.after.length - change.before.length;
    const at = change.position + offset;
    assert.equal(inverse.slice(at, at + change.after.length), change.after, "normalization inverse position");
    inverse = inverse.slice(0, at) + change.before + inverse.slice(at + change.after.length);
  }
  assert.deepEqual(Buffer.from(inverse), original, "normalization inverse must restore accepted raw");
  return original;
}

export function createNormalizedReader(root: string, io: SourceIO = {
  raw: path => readFileSync(resolve(root, path)),
  exists: path => existsSync(resolve(root, path)),
  object: (commit, path) => execFileSync("git", ["show", commit + ":" + path], { cwd: root, maxBuffer: 32 * 1024 * 1024 }),
}) {
  const raw = (path: string) => { assert.ok(safePath(path)); return io.raw(path); };
  const manifest = assertNormalizationManifest(raw("web/tests/fixtures/ui-regression/source-normalization.json"));
  const object = io.object;
  const dependencies = (path: string, current: Buffer) => cp176Paths.includes(path)
    ? inverseCP176DependencyRepair(path, object(cp176Base, path), current) : current;
  const read = (path: string): Buffer => {
    assert.ok(safePath(path));
    const historical = manifest.historyOnly.find(row => row.path === path);
    if (historical) {
      assert.equal(io.exists(path), false, "history-only source must be absent from current");
      const bytes = object(historical.fixedCommit, historical.path);
      assert.equal(hash(bytes), historical.originalSha256, "fixed history raw mismatch");
      const blob = createHash("sha1").update(`blob ${bytes.length}\0`).update(bytes).digest("hex");
      assert.equal(blob, historical.blob, "fixed history blob mismatch");
      return bytes;
    }
    const row = manifest.currentMappings.find(item => item.oldPath === path);
    if (!row) {
      const bytes = dependencies(path, raw(path));
      if (cp174Paths.includes(path)) assertCP174Original(path, object(cp174Base, path));
      const prior = cp174Paths.includes(path) ? inverseCP174Source(path, bytes) : bytes;
      const current = ciClosurePaths.includes(path) ? inverseCIClosure(path, object(ciClosureBase, path), prior) : prior;
      return releaseAssemblyPaths.includes(path)
        ? inverseReleaseAssembly(path, object(releaseAssemblyBase, path), current)
        : current;
    }
    if (row.oldPath !== row.newPath) assert.equal(io.exists(row.oldPath), false, "obsolete current alias must not remain");
    const current = dependencies(row.newPath, raw(row.newPath));
    if (cp174Paths.includes(row.newPath)) assertCP174Original(row.newPath, object(cp174Base, row.newPath));
    return inverseNormalization(row, object(manifest.acceptedCommit, row.oldPath), current);
  };
  return { manifest, raw, read };
}
