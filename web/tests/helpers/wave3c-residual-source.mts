import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { posix } from "node:path";
import ts from "typescript";

export type ResidualMatcher = "japanese-line" | "danger-confirm-line" | "legacy-write-line";
export type ResidualEvidenceSegment = Readonly<{ path: string; start: number; count: number }>;
export type NodeResidualSourceAuthority = Readonly<{
  baselineHead: string;
  head: string;
  entryPath: string;
  sources: readonly Readonly<{ path: string; sourceSha256: string }>[];
  evidenceOrder: Readonly<Record<ResidualMatcher, readonly ResidualEvidenceSegment[]>>;
}>;

export const nodeResidualEntry = "web/src/features/nodes/node-registration-view.tsx";
export const nodeResidualSourcePaths = Object.freeze([
  nodeResidualEntry,
  "web/src/features/nodes/node-registration-model.tsx",
  "web/src/features/nodes/node-status-summary.tsx",
  "web/src/features/nodes/node-metrics-summary.tsx",
  "web/src/features/nodes/node-endpoint-state-view.tsx",
  "web/src/features/nodes/node-configuration-secret-block.tsx",
  "web/src/features/nodes/registered-node-group.tsx",
]);

// The original residual identities, evidence and later-owner fence stay fixed.
// A physical source move does not complete Node A3 or migrate these actions.
export function validateNodeResidualRecords(records: readonly Readonly<{
  id: string; category: string; path: string; matcher: ResidualMatcher; matchCount: number;
  sha256: string; disposition: string; ownerTask: string; ownerMilestone: string;
}>[]) {
  const expected = [
    ["W3C-NODES-HARDCODED-COPY", "hard-coded-user-facing-strings", "japanese-line", 145, "729e323754923d67ea3a7a7becb52258435c3aa568a0a8d5a3190fd42d91aa1f"],
    ["W3C-NODES-MISSING-I18N", "missing-ja-en-keys", "japanese-line", 145, "729e323754923d67ea3a7a7becb52258435c3aa568a0a8d5a3190fd42d91aa1f"],
    ["W3C-NODES-LEGACY-CONFIRMATION", "old-direct-confirmation-path", "danger-confirm-line", 5, "a22890389d0156450a671bc62163d82cfa4b44c59f66dfe2a1fe891bd687dc0b"],
    ["W3C-NODES-LEGACY-WRITES", "old-unguarded-action-path", "legacy-write-line", 5, "d8a666008d8daa88cbd6c8588e0a5d821df696dc7d38b4a6ccfb32da878f30ec"],
  ];
  const nodeRecords = records.filter(({ id, path }) => id.startsWith("W3C-NODES-") || path === nodeResidualEntry);
  assert.deepEqual(nodeRecords.map(({ id, category, matcher, matchCount, sha256 }) => [id, category, matcher, matchCount, sha256]), expected, "Node residual identity and evidence remain fixed");
  for (const record of nodeRecords) {
    assert.equal(record.path, nodeResidualEntry, `${record.id} entry owner`);
    assert.equal(record.ownerTask, "Node A3", `${record.id} later owner`);
    assert.equal(record.ownerMilestone, "V2_RC", `${record.id} later milestone`);
    assert.equal(record.disposition, "reviewed-later-owner", `${record.id} remains residual`);
  }
}

// Hash raw source bytes before decoding them. The authority is generated only
// from a fixed code commit; no after-source snapshot updater runs in this test.
export function verifyNodeResidualSources(authority: NodeResidualSourceAuthority, readSource: (path: string) => Buffer) {
  assert.equal(authority.baselineHead, "792c6c56506c26ffd81b18c1793211e8e78be44d", "residual baseline authority");
  assert.match(authority.head, /^[a-f0-9]{40}$/, "fixed code authority");
  assert.notEqual(authority.head, authority.baselineHead, "extracted source authority must follow baseline");
  assert.equal(authority.entryPath, nodeResidualEntry, "residual entry owner");
  assert.deepEqual(authority.sources.map(({ path }) => path).sort(), [...nodeResidualSourcePaths].sort(), "exact residual source inventory");
  assert.deepEqual(Object.keys(authority.evidenceOrder).sort(), ["danger-confirm-line", "japanese-line", "legacy-write-line"], "exact residual matcher inventory");
  const sources = new Map<string, string>();
  for (const source of authority.sources) {
    assert.match(source.sourceSha256, /^[a-f0-9]{64}$/, `${source.path} digest shape`);
    const raw = readSource(source.path);
    assert.equal(createHash("sha256").update(raw).digest("hex"), source.sourceSha256, `${source.path} raw source digest`);
    sources.set(source.path, raw.toString("utf8"));
  }
  const visited = new Set<string>();
  const visit = (path: string) => {
    if (visited.has(path)) return;
    visited.add(path);
    const source = sources.get(path);
    assert.notEqual(source, undefined, `${path} source owner`);
    const tree = ts.createSourceFile(path, source!, ts.ScriptTarget.Latest, true);
    for (const statement of tree.statements) {
      if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) continue;
      if (!statement.moduleSpecifier.text.startsWith("./")) continue;
      if (statement.importClause?.isTypeOnly) continue;
      const bindings = statement.importClause?.namedBindings;
      if (!statement.importClause?.name && bindings && ts.isNamedImports(bindings) && bindings.elements.every(({ isTypeOnly }) => isTypeOnly)) continue;
      const base = posix.join(posix.dirname(path), statement.moduleSpecifier.text);
      const target = [base, `${base}.ts`, `${base}.tsx`].find((candidate) => sources.has(candidate));
      assert.ok(target, `${path} imports an unowned extracted source: ${statement.moduleSpecifier.text}`);
      visit(target);
    }
  };
  visit(authority.entryPath);
  assert.deepEqual([...visited].sort(), [...nodeResidualSourcePaths].sort(), "residual source disconnected from entry imports");
  return sources as ReadonlyMap<string, string>;
}

// Segments address matching-line indices, preserving the original evidence
// order without changing its hash. Every actual matching line must be consumed
// exactly once, including duplicates; omitted or newly introduced lines fail.
export function orderedNodeResidualEvidence(matcher: ResidualMatcher, authority: NodeResidualSourceAuthority, sources: ReadonlyMap<string, string>, pattern: RegExp) {
  const rows = new Map([...sources].map(([path, source]) => [path, source.split(/\r?\n/u).filter((line) => pattern.test(line)).map((line) => line.trim())]));
  const total = [...rows.values()].reduce((count, evidence) => count + evidence.length, 0);
  const consumed = new Set<string>();
  const evidence: string[] = [];
  for (const segment of authority.evidenceOrder[matcher]) {
    assert.ok(Number.isInteger(segment.start) && segment.start >= 0 && Number.isInteger(segment.count) && segment.count > 0, "residual segment bounds");
    const sourceRows = rows.get(segment.path);
    assert.ok(sourceRows && segment.start + segment.count <= sourceRows.length, "residual segment source range");
    for (let index = segment.start; index < segment.start + segment.count; index += 1) {
      const key = `${segment.path}:${index}`;
      assert.equal(consumed.has(key), false, "residual source line consumed twice");
      consumed.add(key);
      evidence.push(sourceRows[index]);
    }
  }
  assert.equal(consumed.size, total, "all residual source lines must be consumed");
  return evidence;
}
