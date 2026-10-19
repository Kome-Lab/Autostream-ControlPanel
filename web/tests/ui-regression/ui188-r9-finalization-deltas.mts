import assert from "node:assert/strict";
import { createHash } from "node:crypto";

const anchor = "a9efbed3e058bf016b171e50c22650075f20ef50";
const hash = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");
// Independent, literal finite specification. History is never executed as current behavior.
export const r9Deltas = Object.freeze([
  {
    path: "web/src/lib/foundation/status/observability-presenters.ts",
    before: "238d6b2f51cbee5fababbcfc98e02360c7bc53664c9b8f1475b18a53556046d4",
    additions: [[
      '  request: { labelKey: "statusAuditRequest", tone: "warning", icon: "clock-3" },\n',
      '  pending: { labelKey: "statusAuditRequest", tone: "warning", icon: "clock-3" },\n',
    ]], specifications: ["F11"], behavior: "tests/ui-foundation-status.test.mts",
  },
  {
    path: "web/src/lib/i18n.ts",
    before: "fb5ee4a11b6b0e57b187e9d9f07a6862eeb85d20bdcd7dadb263933129aacf49",
    additions: [
      ['    streamDeleteTitle: "配信枠を削除",\n', '    streamDeleteFinalizationPending: "配信の終了処理または録画の登録処理中のため削除できません。処理完了後に最新状態を確認してください。",\n'],
      ['    streamDeleteTitle: "Delete stream slot",\n', '    streamDeleteFinalizationPending: "This stream cannot be deleted while stream finalization or recording registration is in progress. Check the latest state after processing completes.",\n'],
    ], specifications: ["F03", "F10"], behavior: "tests/streams-foundation-migration.test.mts",
  },
  {
    path: ".github/workflows/ci.yml",
    before: "3de8fd1218ad2afa52f4981386708b39f0be0aaf72afda9ed0348631665059ca",
    additions: [[
      `          jq -s -e '([.[] | select(.Action=="skip" or .Action=="fail")]|length)==0 and ([.[]|select(.Action=="pass" and .Test=="TestUI188MariaDBAtomicCreateCoverSnapshotsPreviewAndStopReceipt")]|length)==1' "\${result}" >/dev/null\n`,
      "          python3 -B scripts/ci/ui188-r4-chain/test_artifact_transport.py\n          python3 -B scripts/ci/ui188-r4-chain/test_archive_oracle.py\n",
    ]], specifications: ["F03"], behavior: "scripts/ci/ui188-r4-chain/test_artifact_transport.py",
  },
] as const);

export function inverseR9FinalizationSource(path: string, before: Buffer, current: Buffer): Buffer {
  const row = r9Deltas.find(item => item.path === path);
  assert.ok(row, "unknown R9 finite source path");
  assert.equal(hash(before), row.before, "R9 fixed original hash mismatch");
  let expected = before.toString("utf8");
  for (const [needle, addition] of row.additions) {
    assert.equal(expected.split(needle).length, 2, "R9 original literal must occur once");
    assert.equal(expected.includes(addition), false, "R9 insertion must be new");
    expected = expected.replace(needle, needle + addition);
  }
  assert.equal(current.equals(Buffer.from(expected)), true, "R9 unreviewed current finalization bytes");
  return before;
}

export function historicalR9FinalizationSource(path: string, current: Buffer, object: (ref: string, path: string) => Buffer): Buffer {
  if (path === "internal/store/stream_queries_mariadb.go") return inverseR9ArchiveProcessingSource(object(anchor,path),current);
  if (r9ArchiveDeltas.some(row => row.path === path)) return inverseR9ArchiveGuardSource(path, object(anchor,path),current);
  return r9Deltas.some(row => row.path === path)
    ? inverseR9FinalizationSource(path, object(anchor, path), current) : current;
}

export const r9ArchiveDeltas = Object.freeze([
  { path: "internal/store/memory.go", before: "f3c2ccb768ae0fa7103f7b8bf51e958466e138a68fb2c2b594dcafeb1e4d8300", after: "0874fabad6898f2c5c16d6447a23ab1fbd6344d73e031fda5d4ac5caf3bcf6ed", allowed: "ListArchiveProcessingStreams delegates only its archive predicate to the shared finite run guard", behavior: "internal/store/ui188_r9_archive_finalization_test.go" },
  { path: "internal/store/service_assignment_guard.go", before: "7bd03c08d3ad3d7a0a0843fb36b287ce5011a7056abdb628fa4ddf5c579fa4a4", after: "4652827abf792f7245dbe7d71440284339bde332b0161e8b948dd60f25221722", allowed: "Preserve active and durable pending fences; protect valid profileless modern terminal run until its exact report commits", behavior: "internal/store/ui188_r9_archive_finalization_mariadb_test.go" },
] as const);

export function inverseR9ArchiveGuardSource(path: string, before: Buffer, current: Buffer): Buffer {
  const row = r9ArchiveDeltas.find(item => item.path === path);
  assert.ok(row, "unknown R9 archive guard path");
  assert.equal(hash(before),row.before,"R9 archive guard fixed original mismatch");
  assert.equal(hash(current),row.after,"R9 archive guard unreviewed current bytes");
  return before;
}

export function inverseR9ArchiveProcessingSource(before: Buffer, current: Buffer): Buffer {
  assert.equal(hash(before), "56bbe63fde2fe30b8f2c23e257c7c95542bf9fab55d8c72fcd683d8db76c2eae", "R9 archive fixed original source digest");
  const old = "      COALESCE(TRIM(ss.archive_profile_id), '') <> ''\n";
  const replacement = "      (COALESCE(TRIM(ss.archive_profile_id), '') <> ''\n        OR (COALESCE(TRIM(s.archive_run_id), '') <> ''\n          AND s.archive_started_at > '0001-01-01 00:00:00'))\n";
  assert.equal(before.toString().split(old).length, 2, "R9 archive finite predicate count");
  assert.equal(current.equals(Buffer.from(before.toString().replace(old, replacement))), true, "R9 archive unreviewed current source digest");
  return before;
}

export function historicalR9OwnerMap(path:string,current:Buffer,object:(ref:string,path:string)=>Buffer):Buffer {
  if(path!=="scripts/ci/source-owner-map.json")return current;
  const before=object(anchor,path);
  assert.equal(hash(before),"d1eb7e00e926da399099f87bc96ac5f62f21819d68c0bfba913c92229034e1e6","R9 owner fixed original mismatch");
  let expected=before.toString();
  for(const [old,next] of [
    ["56bbe63fde2fe30b8f2c23e257c7c95542bf9fab55d8c72fcd683d8db76c2eae","abb4193851ead7c7eb8f3299dcc1ffae76ca1c4708cddb87570c82e22b9ee36f"],
    ["f3c2ccb768ae0fa7103f7b8bf51e958466e138a68fb2c2b594dcafeb1e4d8300","0874fabad6898f2c5c16d6447a23ab1fbd6344d73e031fda5d4ac5caf3bcf6ed"],
  ]) {assert.equal(expected.split(old).length,2,"R9 owner finite replacement count");expected=expected.replace(old,next)}
  for(const [tail,old,next] of [["streamSelectFields",167,169],["NewMemoryStreamStore",278,271]] as const) {
    const literal=`          "${tail}"\n        ],\n        "physical_lines": ${old}\n`;
    assert.equal(expected.split(literal).length,2,"R9 owner finite line metadata count");
    expected=expected.replace(literal,literal.replace(`: ${old}\n`,`: ${next}\n`));
  }
  assert.equal(current.equals(Buffer.from(expected)),true,"R9 owner map permits exactly the two current archive source digests and line metadata");return before;
}
