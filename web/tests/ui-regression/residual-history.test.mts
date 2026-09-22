import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import test from "node:test";
import "./history-execution.test.mts";
import {
  nodeResidualEntry,
  orderedNodeResidualEvidence,
  validateNodeResidualRecords,
  verifyNodeResidualSources,
  type NodeResidualSourceAuthority,
} from "../helpers/wave3c-residual-source.mts";

import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

test("Wave 3C residual ADR has zero unowned items and exact non-zero later-owner evidence", () => {
  const inventory = JSON.parse(readFileSync(new URL("../fixtures/ui-foundation-wave3c-residual-inventory.json", import.meta.url), "utf8")) as ResidualInventory;
  assert.doesNotThrow(() => validateResidualInventory(inventory));
  assert.equal(inventory.unownedResidualCount, 0);
  assert.equal(inventory.reviewedLaterOwnerRecordCount, 10);
  assert.equal(inventory.expectedEvidenceRecordCount, 10);
  assert.deepEqual(new Set(inventory.records.map(({ ownerTask }) => ownerTask)), new Set([
    "UI-FOUNDATION-001C-C01-STRUCTURAL-DECOMPOSITION",
    "Node A3",
  ]));
});

test("the residual validator rejects the required expected-count-zero mutant", () => {
  const inventory = JSON.parse(readFileSync(new URL("../fixtures/ui-foundation-wave3c-residual-inventory.json", import.meta.url), "utf8")) as ResidualInventory;
  assert.throws(
    () => validateResidualInventory({ ...inventory, expectedEvidenceRecordCount: 0 }),
    /expected evidence count must be positive/,
  );
});

test("Node residual source migration rejects omitted evidence, stale hashes, and disconnected imports", () => {
  const inventory = JSON.parse(readFileSync(new URL("../fixtures/ui-foundation-wave3c-residual-inventory.json", import.meta.url), "utf8")) as ResidualInventory;
  const authority = inventory.nodeSourceAuthority;
  assert.ok(authority, "Node residual source migration requires fixed code authority");
  assert.throws(() => validateResidualInventory({ ...inventory, nodeSourceAuthority: undefined }), /fixed Node code authority is required/);
  assert.throws(() => validateResidualInventory({
    ...inventory, nodeSourceAuthority: { ...authority, sources: authority.sources.slice(1) },
  }), /exact residual source inventory/);
  assert.throws(() => validateResidualInventory({
    ...inventory, nodeSourceAuthority: { ...authority, sources: authority.sources.map((source, index) => index === 0 ? { ...source, sourceSha256: "0".repeat(64) } : source) },
  }), /raw source digest/);
  const segments = authority.evidenceOrder["japanese-line"];
  assert.throws(() => validateResidualInventory({
    ...inventory, nodeSourceAuthority: { ...authority, evidenceOrder: { ...authority.evidenceOrder, "japanese-line": [...segments, segments[0]] } },
  }), /consumed twice/);
  assert.throws(() => validateResidualInventory({
    ...inventory, nodeSourceAuthority: { ...authority, evidenceOrder: { ...authority.evidenceOrder, "japanese-line": segments.slice(1) } },
  }), /all residual source lines/);
  assert.throws(() => validateResidualInventory({
    ...inventory, records: inventory.records.map((record) => record.id === "W3C-NODES-HARDCODED-COPY" ? { ...record, ownerTask: "UI-FOUNDATION-001C-C01-STRUCTURAL-DECOMPOSITION" } : record),
  }), /later owner/);
  const entry = readResidualSource(authority.entryPath, authority.head).toString("utf8");
  const disconnected = entry.replace(/^import \{ RegisteredNodeGroup \} from "\.\/registered-node-group";\r?\n/mu, "");
  assert.notEqual(disconnected, entry, "import-disconnection mutant reached the real owner edge");
  const mutatedBytes = Buffer.from(disconnected, "utf8");
  const mutatedAuthority = {
    ...authority,
    sources: authority.sources.map((source) => source.path === authority.entryPath
      ? { ...source, sourceSha256: createHash("sha256").update(mutatedBytes).digest("hex") }
      : source),
  };
  assert.throws(() => verifyNodeResidualSources(mutatedAuthority, (path) => path === authority.entryPath ? mutatedBytes : readResidualSource(path, authority.head)), /disconnected from entry imports/);
});

type ResidualRecord = Readonly<{
  id: string;
  category: string;
  path: string;
  matcher: "japanese-line" | "danger-confirm-line" | "legacy-write-line";
  matchCount: number;
  sha256: string;
  disposition: string;
  ownerTask: string;
  ownerMilestone: string;
}>;

type ResidualInventory = Readonly<{
  schemaVersion: number;
  scope: readonly string[];
  unownedResidualCount: number;
  reviewedLaterOwnerRecordCount: number;
  expectedEvidenceRecordCount: number;
  records: readonly ResidualRecord[];
  nodeSourceAuthority?: NodeResidualSourceAuthority;
}>;

function validateResidualInventory(inventory: ResidualInventory) {
  assert.equal(inventory.schemaVersion, 1);
  assert.equal(inventory.unownedResidualCount, 0);
  assert.ok(inventory.expectedEvidenceRecordCount > 0, "expected evidence count must be positive");
  assert.equal(inventory.expectedEvidenceRecordCount, inventory.records.length);
  assert.equal(inventory.reviewedLaterOwnerRecordCount, inventory.records.length);
  assert.equal(new Set(inventory.records.map(({ id }) => id)).size, inventory.records.length);
  validateNodeResidualRecords(inventory.records);
  assert.ok(inventory.nodeSourceAuthority, "fixed Node code authority is required");
  const nodeSources = inventory.nodeSourceAuthority
    ? verifyNodeResidualSources(inventory.nodeSourceAuthority, (path) => readResidualSource(path, inventory.nodeSourceAuthority!.head))
    : undefined;
  for (const record of inventory.records) {
    assert.equal(record.disposition, "reviewed-later-owner", record.id);
    assert.ok(record.ownerTask && record.ownerMilestone, `${record.id} owner`);
    assert.ok(inventory.scope.includes(record.path), `${record.id} scope`);
    const evidence: string[] = nodeSources && inventory.nodeSourceAuthority && record.path === nodeResidualEntry
      ? orderedNodeResidualEvidence(record.matcher, inventory.nodeSourceAuthority, nodeSources, residualMatcher(record.matcher))
      : readResidualSource(record.path).toString("utf8")
        .split(/\r?\n/u)
        .filter((line) => residualMatcher(record.matcher).test(line))
        .map((line) => line.trim());
    assert.equal(evidence.length, record.matchCount, `${record.id} count`);
    assert.equal(createHash("sha256").update(evidence.join("\n"), "utf8").digest("hex"), record.sha256, `${record.id} hash`);
  }
}

const historicalCommit = "b266aabb896f0df9880a0354c1135130d6938e8d";
const nodeCodeCommit = "8863c785aaf70f443e396ed3f90045bdee71d15e";
const historicalRoot = resolve(process.env.AUTOSTREAM_UI_HISTORY_ROOT || fileURLToPath(new URL("../../../", import.meta.url)));

function readResidualSource(path: string, codeCommit = historicalCommit) {
  assert.ok(codeCommit === historicalCommit || codeCommit === nodeCodeCommit, "only fixed historical code authorities");
  const git = (...args: string[]) => execFileSync("git", ["-c", `safe.directory=${historicalRoot.replaceAll("\\", "/")}`, ...args], { cwd: historicalRoot });
  assert.equal(git("rev-parse", "--verify", `${codeCommit}^{commit}`).toString("utf8").trim(), codeCommit);
  const fixed = git("cat-file", "blob", `${codeCommit}:${path}`);
  if (codeCommit === nodeCodeCommit) {
    assert.deepEqual(git("cat-file", "blob", `${historicalCommit}:${path}`), fixed, `${path}: accepted history retains Node authority`);
  }
  return fixed;
}

function residualMatcher(matcher: ResidualRecord["matcher"]) {
  if (matcher === "japanese-line") return /[ぁ-んァ-ヶ一-龠々ー]/u;
  if (matcher === "danger-confirm-line") return /\bDangerConfirm\b/u;
  return /\bapi(?:Post|Put|Delete)\s*</u;
}
