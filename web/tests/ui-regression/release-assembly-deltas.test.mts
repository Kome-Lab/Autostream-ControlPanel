import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { inverseReleaseAssembly, releaseAssemblyBase, releaseAssemblyPaths, releaseAssemblySource } from "./release-assembly-deltas.mts";
import { createNormalizedReader } from "./source-normalization.mts";

const root = fileURLToPath(new URL("../../..", import.meta.url));
const object = (commit: string, path: string) => execFileSync("git", ["show", commit + ":" + path], { cwd: root, maxBuffer: 32 * 1024 * 1024 });
const raw = (path: string) => object("HEAD", path);
const io = { raw, object, exists: (path: string) => existsSync(resolve(root, path)) };
const expectedPaths = [
  ".github/workflows/ci.yml",
  "go.mod",
  "go.sum",
  "internal/httpapi/server_runtime_secrets.go",
  "internal/httpapi/server_stream_lifecycle_test.go",
  "internal/httpapi/server_stream_start_completion.go",
  "internal/httpapi/server_stream_start_orchestration.go",
  "internal/servicecall/client.go",
  "internal/servicecall/client_start_orchestration.go",
  "internal/servicecall/client_worker_video_test.go",
  "internal/store/discord_youtube_live_notification_outbox.go"
];

test("UI-RELEASE-ASSEMBLY-001: exactly eleven existing sources bind both fixed Git sides while prior UI authority stays unchanged", () => {
  assert.deepEqual(releaseAssemblyPaths, expectedPaths);
  assert.equal(new Set(releaseAssemblyPaths).size, 11);
  const reader = createNormalizedReader(root, io);
  for (const path of releaseAssemblyPaths) {
    const before = object(releaseAssemblyBase, path), current = raw(path);
    assert.deepEqual(current, object(releaseAssemblySource, path));
    assert.deepEqual(inverseReleaseAssembly(path, before, current), before);
    if (path !== ".github/workflows/ci.yml") assert.deepEqual(reader.read(path), before);
  }
  for (const path of ["protected.json", "source-normalization.json", "approved-source-deltas.json", "ci-source-deltas.json"]) {
    const full = "web/tests/fixtures/ui-regression/" + path;
    assert.deepEqual(raw(full), object(releaseAssemblyBase, full));
  }
});

test("UI-RELEASE-ASSEMBLY-002: missing, extra, truncated, reverted and wrong-original source bytes fail closed", () => {
  for (const path of releaseAssemblyPaths) {
    const before = object(releaseAssemblyBase, path), current = raw(path);
    for (const mutant of [Buffer.alloc(0), Buffer.concat([current, Buffer.from("\n")]), current.subarray(0, current.length - 1), before]) {
      assert.throws(() => inverseReleaseAssembly(path, before, mutant), /exact specified name edits/);
    }
    assert.throws(() => inverseReleaseAssembly(path, Buffer.concat([before, Buffer.from("drift")]), current), /fixed original hash/);
    assert.throws(() => inverseReleaseAssembly("unknown", before, current), /unknown release assembly path/);
  }
});

test("UI-RELEASE-ASSEMBLY-003: the reader cannot replace missing or changed current files with historical bytes", () => {
  const missing = new Error("controlled current assembly source missing");
  for (const path of ["go.mod", ".github/workflows/ci.yml"]) {
    const absent = createNormalizedReader(root, { ...io, raw: p => { if (p === path) throw missing; return raw(p); } });
    assert.throws(() => absent.read(path), error => error === missing);
    const changed = createNormalizedReader(root, { ...io, raw: p => p === path ? Buffer.concat([raw(p), Buffer.from("drift")]) : raw(p) });
    assert.throws(() => changed.read(path), /exact specified name edits/);
  }
  const badOriginal = createNormalizedReader(root, { ...io, object: (commit, path) => path === "go.mod" ? Buffer.alloc(0) : object(commit, path) });
  assert.throws(() => badOriginal.read("go.mod"), /fixed original hash/);
});

test("UI-RELEASE-ASSEMBLY-004: unlisted application sources retain their actual bytes without assembly substitution", () => {
  const path = "web/src/lib/navigation.ts", mutant = Buffer.concat([raw(path), Buffer.from("drift")]);
  assert.equal(releaseAssemblyPaths.includes(path), false);
  const reader = createNormalizedReader(root, { ...io, raw: p => p === path ? mutant : raw(p) });
  assert.deepEqual(reader.read(path), mutant);
  assert.notDeepEqual(reader.read(path), object(releaseAssemblyBase, path));
});
