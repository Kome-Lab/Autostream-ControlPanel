import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { once } from "node:events";
import { lstatSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, symlinkSync, unlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, isAbsolute, resolve } from "node:path";
import { run } from "node:test";
import { spec } from "node:test/reporters";
import ts from "typescript";
import {
  pollingCurrentBase, pollingCurrentTree, pollingLegacyFixturePath, pollingLegacyFixtureSHA256,
} from "./polling-current-source-contract.mts";
import { pollingBlob, pollingHash } from "./polling-current-source-delta.mts";

export type CompletedParityTest = Readonly<{ name: string; passed: boolean; skipped: boolean; todo: boolean }>;
export type ParitySummary = Readonly<{ success: boolean; counts: Readonly<Record<string, number>> }>;
type LegacySource = Readonly<{ path: string; mode: string; blob: string; sha256: string; tests: readonly string[] }>;
export type LegacyParityInventory = Readonly<{
  schemaVersion: number; commit: string; entry: string; topLevelCount: number;
  nestedCount: number; executionCount: number; requiredPlatform: string;
  nestedTests: readonly string[]; sources: readonly LegacySource[];
}>;
const git = (root: string, ...args: string[]) => execFileSync("git", ["-C", root, ...args], { maxBuffer: 128 * 1024 * 1024, timeout: 60_000 });
const exactCount = (value: unknown, expected: number, reason: string) => assert.equal(value, expected, reason);

export function assertLegacyParityInventory(bytes: Buffer): LegacyParityInventory {
  assert.equal(pollingHash(bytes), pollingLegacyFixtureSHA256, "polling fixed legacy inventory pin");
  const value = JSON.parse(bytes.toString("utf8")) as LegacyParityInventory;
  assert.equal(value.schemaVersion, 1);
  assert.equal(value.commit, pollingCurrentBase);
  assert.equal(value.entry, "web/tests/ui-regression/parity.test.mts");
  assert.equal(value.requiredPlatform, "linux");
  exactCount(value.sources.length, 12, "polling twelve immutable legacy test sources");
  exactCount(value.topLevelCount, 58, "polling fixed legacy top-level denominator");
  exactCount(value.nestedCount, 1, "polling fixed legacy nested denominator");
  exactCount(value.executionCount, 59, "polling fixed legacy execution denominator");
  assert.deepEqual(value.nestedTests, ["Linux native overall shell; Windows never discovers or launches a Bash/WSL alias"]);
  const names = value.sources.flatMap(source => [...source.tests]);
  exactCount(names.length, 58, "polling all legacy positive/negative registrations");
  exactCount(new Set(names).size, 58, "polling legacy test identity uniqueness");
  exactCount(new Set(value.sources.map(source => source.path)).size, 12, "polling legacy source uniqueness");
  for (const source of value.sources) {
    assert.match(source.path, /^web\/tests\/ui-regression\/[A-Za-z0-9_-]+\.test\.mts$/);
    assert.equal(source.mode, "100644");
    assert.match(source.blob, /^[a-f0-9]{40}$/);
    assert.match(source.sha256, /^[a-f0-9]{64}$/);
  }
  return value;
}

export function assertParityExecution(summary: ParitySummary | undefined, completed: readonly CompletedParityTest[], names: readonly string[]) {
  assert.ok(names.length > 0, "polling parity expected denominator is nonzero");
  exactCount(new Set(names).size, names.length, "polling parity required identities are unique");
  assert.ok(summary, "polling parity runner requires an actual Node summary");
  assert.equal(summary.success, true, "polling parity Node execution succeeded");
  exactCount(summary.counts.tests, names.length, "polling parity exact executed denominator");
  exactCount(summary.counts.passed, names.length, "polling parity exact pass denominator");
  for (const field of ["failed", "cancelled", "skipped", "todo"]) exactCount(summary.counts[field], 0, "polling parity zero " + field);
  exactCount(completed.length, names.length, "polling parity exact completed denominator");
  assert.deepEqual(completed.map(result => result.name).sort(), [...names].sort(), "polling parity every positive/negative identity executes once");
  assert.ok(completed.every(result => result.passed && !result.skipped && !result.todo), "polling parity no skipped, todo or unsuccessful case");
}

export async function runExactParityFile(webRoot: string, file: string, names: readonly string[]) {
  const expectedFile = resolve(webRoot, file);
  let summary: ParitySummary | undefined;
  const completed: CompletedParityTest[] = [];
  const tests = run({ cwd: webRoot, files: [expectedFile], concurrency: false, forceExit: true, timeout: 180_000, execArgv: ["--no-warnings"] });
  tests.on("test:complete", result => {
    const fileWrapper = result.nesting === 0 && typeof result.file === "string" && resolve(result.file) === expectedFile
      && (result.name.replaceAll("\\", "/") === file.replaceAll("\\", "/") || isAbsolute(result.name) && resolve(result.name) === expectedFile);
    if (fileWrapper) return;
    completed.push({ name: result.name, passed: result.details.passed, skipped: Boolean(result.skip), todo: Boolean(result.todo) });
  });
  tests.on("test:summary", result => { summary = { success: result.success, counts: { ...result.counts } }; });
  const report = tests.compose(spec);
  report.pipe(process.stdout, { end: false });
  await once(report, "end");
  assertParityExecution(summary, completed, names);
  return { summary, completed };
}

type TreeEntry = Readonly<{ path: string; blob: string }>;
function fixedEntries(root: string): TreeEntry[] {
  const entries = git(root, "ls-tree", "-rz", "--full-tree", pollingCurrentBase).toString("utf8").split("\0").filter(Boolean).map(row => {
    const match = /^100644 blob ([a-f0-9]{40})\t(.+)$/.exec(row);
    assert.ok(match, "polling fixed source has only regular mode100644 blobs");
    return { path: match[2], blob: match[1] };
  });
  exactCount(entries.length, 1566, "polling fixed source exact tree denominator");
  return entries;
}

function batchBlobs(root: string, entries: readonly TreeEntry[]) {
  const output = execFileSync("git", ["-C", root, "cat-file", "--batch"], {
    input: entries.map(entry => entry.blob).join("\n") + "\n", maxBuffer: 128 * 1024 * 1024, timeout: 60_000,
  });
  const values = new Map<string, Buffer>();
  let offset = 0;
  for (const entry of entries) {
    const end = output.indexOf(10, offset);
    assert.ok(end >= offset, "polling fixed batch header present");
    const match = /^([a-f0-9]{40}) blob ([0-9]+)$/.exec(output.subarray(offset, end).toString("ascii"));
    assert.ok(match && match[1] === entry.blob, "polling fixed batch exact blob identity");
    const length = Number(match[2]);
    assert.ok(Number.isSafeInteger(length) && length >= 0);
    const data = output.subarray(end + 1, end + 1 + length);
    assert.equal(data.length, length);
    assert.equal(output[end + 1 + length], 10, "polling fixed batch frame terminator");
    assert.equal(pollingBlob(data), entry.blob, "polling fixed raw Git body identity");
    values.set(entry.path, data);
    offset = end + 2 + length;
  }
  assert.equal(offset, output.length, "polling fixed batch has no omitted/extra records");
  return values;
}

function sourceFiles(root: string, prefix = ""): string[] {
  return readdirSync(resolve(root, prefix), { withFileTypes: true }).flatMap(entry => {
    const path = prefix ? prefix + "/" + entry.name : entry.name;
    if (path === ".git" || path === "web/node_modules") return [];
    assert.equal(entry.isSymbolicLink(), false, "polling fixed source cannot gain symbolic links");
    return entry.isDirectory() ? sourceFiles(root, path) : [path];
  });
}

export function verifyFixedParityCheckout(root: string, dependencies: string, inventory: LegacyParityInventory) {
  assert.equal(git(root, "rev-parse", "HEAD").toString("ascii").trim(), pollingCurrentBase, "polling actual fixed worktree HEAD");
  assert.equal(git(root, "rev-parse", "HEAD^{tree}").toString("ascii").trim(), pollingCurrentTree, "polling actual fixed worktree tree");
  const entries = fixedEntries(root), blobs = batchBlobs(root, entries);
  assert.deepEqual(sourceFiles(root).sort(), entries.map(entry => entry.path).sort(), "polling fixed worktree has every source and no extra source");
  for (const entry of entries) {
    const path = resolve(root, entry.path), stat = lstatSync(path);
    assert.ok(stat.isFile() && !stat.isSymbolicLink() && !(stat.mode & 0o111), "polling exact fixed source mode");
    assert.deepEqual(readFileSync(path), blobs.get(entry.path), "polling actual fixed raw source bytes: " + entry.path);
  }
  const nodeModules = resolve(root, "web/node_modules");
  assert.ok(lstatSync(nodeModules).isSymbolicLink(), "polling dependency alias is the sole explicit source-tree exception");
  assert.equal(realpathSync(nodeModules), realpathSync(dependencies), "polling exact dependency alias target");
  for (const source of inventory.sources) {
    const bytes = blobs.get(source.path);
    assert.ok(bytes);
    assert.equal(pollingBlob(bytes), source.blob);
    assert.equal(pollingHash(bytes), source.sha256);
    const parsed = ts.createSourceFile(source.path, bytes.toString("utf8"), ts.ScriptTarget.Latest, true);
    const names: string[] = [];
    for (const statement of parsed.statements) {
      if (!ts.isExpressionStatement(statement) || !ts.isCallExpression(statement.expression)) continue;
      const call = statement.expression;
      if (!ts.isIdentifier(call.expression) || call.expression.text !== "test") continue;
      assert.ok(call.arguments.length >= 2 && ts.isStringLiteral(call.arguments[0]), "polling fixed registration is an unconditional literal test");
      names.push(call.arguments[0].text);
    }
    assert.deepEqual(names, source.tests, "polling fixed registration AST and inventory agree");
  }
  assert.equal(git(root, "status", "--porcelain", "--untracked-files=no").length, 0, "polling fixed tracked source stays clean");
  return { head: pollingCurrentBase, tree: pollingCurrentTree, entries: entries.length, rawBytesEqual: true };
}

export async function runFixedLegacyParity(root: string) {
  assert.equal(process.platform, "linux", "fixed legacy skip0 proof requires real Linux; Windows is NOT_VERIFIED");
  const inventory = assertLegacyParityInventory(readFileSync(resolve(root, pollingLegacyFixturePath)));
  const dependencies = resolve(root, "web/node_modules");
  assert.ok(lstatSync(dependencies).isDirectory(), "polling current installed dependencies exist");
  const lock = git(root, "hash-object", "--path=web/package-lock.json", "--", "web/package-lock.json").toString("ascii").trim();
  const expectedLock = git(root, "rev-parse", pollingCurrentBase + ":web/package-lock.json").toString("ascii").trim();
  assert.equal(lock, expectedLock, "polling fixed/current dependency lock identity");
  const oldPackage = JSON.parse(git(root, "cat-file", "blob", pollingCurrentBase + ":web/package.json").toString("utf8"));
  const currentPackage = JSON.parse(readFileSync(resolve(root, "web/package.json"), "utf8"));
  for (const key of ["dependencies", "devDependencies", "optionalDependencies", "overrides"]) assert.deepEqual(currentPackage[key], oldPackage[key], "polling fixed/current dependency graph: " + key);
  const temporary = mkdtempSync(resolve(realpathSync(tmpdir()), "autostream-polling-parity-"));
  const fixed = resolve(temporary, "source");
  let registered = false, linked = false, primary: unknown, proof: unknown;
  try {
    execFileSync("git", ["-C", root, "-c", "core.autocrlf=false", "worktree", "add", "--detach", fixed, pollingCurrentBase], { timeout: 60_000, stdio: "pipe" });
    registered = true;
    symlinkSync(dependencies, resolve(fixed, "web/node_modules"), "dir");
    linked = true;
    const before = verifyFixedParityCheckout(fixed, dependencies, inventory);
    const result = await runExactParityFile(resolve(fixed, "web"), "tests/ui-regression/parity.test.mts", [...inventory.sources.flatMap(source => [...source.tests]), ...inventory.nestedTests]);
    const after = verifyFixedParityCheckout(fixed, dependencies, inventory);
    assert.deepEqual(after, before, "polling fixed source proof remains identical after all legacy tests");
    proof = { phase: "immutable-65a-legacy-parity", before, after, ...result };
  } catch (error) {
    primary = error;
  } finally {
    try {
      assert.equal(dirname(fixed), temporary);
      assert.equal(basename(fixed), "source");
      assert.notEqual(fixed, resolve(root));
      if (linked) unlinkSync(resolve(fixed, "web/node_modules"));
      if (registered) git(root, "worktree", "remove", "--force", fixed);
      rmSync(temporary, { recursive: true });
    } catch (cleanup) {
      if (primary) throw new AggregateError([primary, cleanup], "polling legacy test and owned worktree cleanup failed");
      throw cleanup;
    }
  }
  if (primary) throw primary;
  assert.ok(proof, "polling legacy execution produced no proof");
  return proof;
}
