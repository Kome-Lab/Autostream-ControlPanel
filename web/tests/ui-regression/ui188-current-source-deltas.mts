import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import ts from "typescript";

type CurrentDelta = Readonly<{
  path: string; beforeCommit: string; beforeSha256: string;
  afterCommit: string; afterSha256: string;
  specifications: readonly string[]; allowedChanges: string; behaviorTests: readonly string[];
}>;
type Contract = Readonly<{ schemaVersion: number; purpose: string; records: readonly CurrentDelta[] }>;
const contractSha256 = "d2338ca9121ef3b83f25e08833b1df3ac1b909165cb2a096aa4065a8571fe08c";
const hash = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");
const safePath = (path: string) => /^[A-Za-z0-9_./-]+$/.test(path) && !path.startsWith("/") && !path.split("/").includes("..");

export function assertUI188ContractRegistration(source: string): void {
  const parsed = ts.createSourceFile("parity.test.mts", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const registrations = parsed.statements.filter(statement => ts.isImportDeclaration(statement)
    && ts.isStringLiteral(statement.moduleSpecifier) && statement.moduleSpecifier.text === "./ui188-current-source-deltas.test.mts"
    && statement.importClause === undefined);
  assert.equal(registrations.length, 1, "UI188 finite contract must be registered exactly once as a top-level side-effect import");
}

export function assertUI188CurrentContract(bytes: Buffer): Contract {
  assert.equal(hash(bytes), contractSha256, "UI188 independent finite contract bytes changed");
  const contract = JSON.parse(bytes.toString("utf8")) as Contract;
  assert.equal(contract.schemaVersion, 1);
  assert.equal(contract.records.length, 48, "exact reviewed path set, not all changed product files");
  assert.equal(new Set(contract.records.map(row => row.path.toLowerCase())).size, 48, "UI188 finite path collision");
  for (const row of contract.records) {
    assert.ok(safePath(row.path), "UI188 bounded relative path");
    assert.match(row.beforeCommit, /^[a-f0-9]{40}$/);
    assert.equal(row.afterCommit, "d3e78bb125c3f5090bef84ddd08bec224a1baf31");
    assert.match(row.beforeSha256, /^[a-f0-9]{64}$/); assert.match(row.afterSha256, /^[a-f0-9]{64}$/);
    assert.notEqual(row.beforeSha256, row.afterSha256);
    assert.ok(row.specifications.length && row.specifications.every(id => /^F(?:0[1-9]|1[01])$/.test(id)));
    assert.ok(row.allowedChanges.length && row.behaviorTests.length && row.behaviorTests.every(safePath));
  }
  return contract;
}

// This is a historical lineage layer only. It never runs reconstructed old
// source as current behavior, and a missing/changed current source cannot fall
// back to history. The independent product tests use the unmodified raw reader.
export const ui188CurrentContract = assertUI188CurrentContract(readFileSync(new URL("../fixtures/ui-regression/ui188-current-source-deltas.json", import.meta.url)));
export const ui188CurrentPaths = Object.freeze(ui188CurrentContract.records.map(row => row.path));

export function inverseUI188CurrentSource(path: string, before: Buffer, current: Buffer): Buffer {
  const row = ui188CurrentContract.records.find(item => item.path === path);
  assert.ok(row, "unknown UI188 current source path");
  assert.equal(hash(before), row.beforeSha256, "UI188 fixed original hash mismatch");
  assert.equal(hash(current), row.afterSha256, "normalization permits only exact specified name edits or reviewed finite UI188 source bytes");
  return before;
}

export function historicalUI188Source(path: string, current: Buffer, object: (commit: string, path: string) => Buffer): Buffer {
  const row = ui188CurrentContract.records.find(item => item.path === path);
  return row ? inverseUI188CurrentSource(path, object(row.beforeCommit, path), current) : current;
}
