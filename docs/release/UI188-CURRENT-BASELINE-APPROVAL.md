# UI188 Current Product Baseline Re-establishment
## Technical review and approval package

**Document ID:** UI188-BASELINE-REAPPROVAL-001  
**Status:** REVIEW DRAFT — NOT APPROVED  
**Candidate commit:** 0141c66f345a7e77aa78eb72c2d77292f77dae80  
**Candidate branch at review start:** diagnostic/ci017-auth-expiry  
**Historical comparison commit:** 452e82d87a95d63448952a9e1cfd822f942990c7  
**Review branch:** review/ui188-baseline-reapproval

---

## 1. Decision requested

Approve the *direction* of replacing the historical UI188 Control Panel source baseline with a newly reconstructed current-product baseline, subject to the gates in this document.

This is not approval to update the source manifest, source lock, test overlay, workflow, default branch, release tag, deployment, or production system. Those changes require a separately reviewed implementation and exact-SHA verification.

## 2. Immutable comparison evidence

The full Git Trees were compared directly because the GitHub Compare API response is capped at 300 changed files.

| Measure | Historical | Candidate |
|---|---:|---:|
| Commit | 452e82d87a95d63448952a9e1cfd822f942990c7 | 0141c66f345a7e77aa78eb72c2d77292f77dae80 |
| Git tree | 5879ad5924c892a128ea9e764b85edcc554a2ab1 | 7eaccacad50fd5ca0470593226b2bff4175316f0 |
| Blob files | 1,521 | 1,685 |

Exact file comparison:

- Added: 164
- Modified: 171
- Removed: 0
- Total changed paths: 335

The prior 300-file Compare API result was incomplete and must not be used as the final denominator.

## 3. Historical Manifest reconciliation

The candidate still contains the historical 1,474-entry product manifest.

| Measure | Count |
|---|---:|
| Historical manifest entries | 1,474 |
| Entries whose bytes differ at candidate | 159 |
| Entries still identical | 1,315 |
| New candidate files outside historical manifest | 164 |
| Modified candidate files outside historical manifest | 12 |
| Historical manifest paths missing from candidate | 0 |

The 159 changed manifest entries comprise:

| Area | Changed manifest entries |
|---|---:|
| Web product source | 55 |
| Web tests | 60 |
| Internal HTTP API | 13 |
| Internal store | 16 |
| Internal media assets | 3 |
| Internal stream visual | 3 |
| CI / governance | 4 |
| Dependency manifests / other | 5 |

The 164 added paths comprise 107 test files, 12 Web product files, 5 database migrations, 17 internal/store files, 5 media-assets files, 3 HTTP API files, and 15 CI/governance files.

The 12 modified paths outside the historical manifest are documentation and CI/governance files.

## 4. Technical change classification

### 4.1 Product implementation — candidate for inclusion after review

- HTTP API, Store, media-assets and stream-visual implementation changes.
- Upload operation identity, retry/recovery and candidate filename handling.
- Stream create/update CAS and revision precision.
- Node metadata and polling authority changes.
- Web media upload ownership, protocol, reconciliation and status UI.
- Resource and stream action/runtime changes.

These are product changes, not merely source-baseline metadata. They need ownership review and focused behavioral evidence before being admitted to the new product baseline.

### 4.2 Database migrations — high-risk approval gate

Five additive/precision migrations were found:

| Migration | Observed purpose | Required review |
|---|---|---|
| 085_media_asset_file_deletions.sql | Durable retry state for content-addressed file deletion | Retry bounds, orphan handling, retention and cleanup safety |
| 086_upload_operation_and_candidate_names.sql | Upload operation/session identifiers and unique key; candidate display name widened to 255 | Existing rows, nullable uniqueness semantics, duplicate/replay behavior |
| 087_stream_settings_revision_precision.sql | stream_settings.updated_at becomes DATETIME(6) | Existing data preservation and CAS monotonicity |
| 088_profile_and_drive_revision_precision.sql | profiles and drive_destinations updated_at become DATETIME(6) | Existing data preservation and CAS monotonicity |
| 089_system_settings_revision_precision.sql | system_settings.updated_at becomes DATETIME(6) | Existing data preservation and CAS monotonicity |

No migration should be run against production as part of this re-baselining review. Require forward-migration testing from the historical schema, populated-data preservation checks, restart/replay tests, and a separately documented rollback/restore strategy. A down migration must not be assumed to be lossless.

### 4.3 Dependencies

- Go module replacement changes the Autostream Contracts pseudo-version from v1.2.12 (2026-10-12 source) to v1.2.13 prerelease (2026-10-28 source).
- Go module and checksum files must be reviewed together.
- Web runtime dependency versions are unchanged in package.json; package scripts and test entrypoints changed.
- Verify the committed package lock remains consistent with package.json and that the new contract version is the intended compatible source.

### 4.4 CI and release workflow

- ci.yml adds SQL-backed polling/create/upload witnesses, isolated MariaDB checks, actual Next UI/browser evidence and bounded artifacts. The Contracts source SHA is updated.
- The observed workflow retains push, pull_request and workflow_dispatch triggers and read-only contents permission. No new production deployment trigger was identified in the inspected diff.
- release-host.yml updates download-artifact from v8.0.0 to v8.0.2 and adds GH_REPO to the job environment.
- The release-host workflow remains high-impact because its publish job has contents:write and id-token:write. Review exact conditions, artifact provenance and tag behavior before accepting it into the baseline.
- Do not dispatch a release workflow during baseline review.

### 4.5 Test and integration assets

The candidate adds substantial Go and browser test coverage, SQL evidence helpers, polling/create/upload parity tests, and UI188 integration harness material.

Classify each path as one of:
1. product source;
2. product test source;
3. CI/integration harness;
4. generated evidence schema or source inventory;
5. release documentation;
6. temporary diagnostic-only asset.

Do not merge these classes into one undifferentiated product tree.

### 4.6 CI017 diagnostic-only assets

The following are temporary investigation assets and must not be silently included in the product baseline:

- .github/workflows/ci017-auth-expiry-diagnostic.yml
- web/tests/ci017-auth-expiry-diagnostic.test.mts

Either exclude them from the product tree or explicitly move them to a separate diagnostics component with its own retention/ownership policy.

## 5. Critical Source Integrity finding

The candidate does not yet define a new current-product source authority.

The inspected scripts/ci/ui188-full-e2e/source.py still pins:
- the historical manifest SHA256;
- the historical CP product tree 44ae3a676b6e9a462ea2a437bb8391f2198e7af4;
- the historical BOT product tree 1141c416accf9fcb5d828f1174a7641863404972;
- the historical CP manifest count of 1,474;
- the exact three-path historical CP test overlay.

The candidate source-lock.json still identifies the CP verified tree as the historical product plus the exact test overlay. The cp-test-overlay.json still references historical base commit 452e82d and the historical product tree.

Therefore:
- Updating only source-lock.json is insufficient.
- Replacing the historical manifest with every file in the candidate Git tree is not acceptable.
- The candidate Git tree includes workflow, documentation, integration harness and diagnostic assets that are not automatically product source.
- A new source authority must explicitly separate CP product, CP test source, CI/integration harness, diagnostic assets, and the pinned BOT/Encoder/Worker/Contracts components.

The new source.py verifier must validate the new schema and exact path sets without silently widening the product boundary.

## 6. Provenance and signature findings

- Candidate commit 0141c66 is an unsigned commit according to GitHub's commit verification response.
- The current diagnostic branch points at this candidate commit; the original working branch remains at 6cfb0f0976d7c5c57e757d10b6ea8985dd80f7b5.
- The candidate commit is therefore suitable for source review, not yet a signed delivery authority.
- The source lock records a CP base_commit of a9efbed3e058bf016b171e50c22650075f20ef50, while the test contract base and overlay base identify 452e82d. These identities have different declared roles and must be reconciled in the new provenance model rather than overwritten or conflated.

## 7. Verification evidence and limitations

Recorded evidence includes:
- Normal CI Attempt 2 at the candidate source: 30 jobs completed successfully, including Go/MariaDB, ST-PORT integration and build checks.
- CI017 isolated auth-expiry diagnostic passed.
- UI188 historical Source Integrity preflight remained blocked because the candidate no longer matches the historical source authority.
- The UI188 full 1,000-second two-stream acceptance, all 28 groups and 26 obligations, is not established by ordinary CI or the short bootstrap.
- No release or production deployment is authorized by these results.

The exact CI run, job denominators, artifacts and source SHA must be rechecked against the final newly generated candidate SHA after the source authority is reconstructed.

## 8. Required gates before new baseline approval

| Gate | Required evidence | Current state |
|---|---|---|
| G1 — Complete inventory | Exact Git Tree inventory and path classifications | PARTIAL: counts verified; all paths not yet individually dispositioned |
| G2 — Product/test/harness boundary | Approved component path sets and owners | NOT READY |
| G3 — DB migration review | Populated-data forward migration, replay, recovery and rollback strategy | NOT READY |
| G4 — Dependency provenance | Contract compatibility and lockfile consistency | NOT READY |
| G5 — Workflow security review | Trigger, permission, release and artifact provenance review | PARTIAL |
| G6 — Diagnostic exclusion | Explicit disposition of CI017-only files | NOT READY |
| G7 — Source verifier redesign | New schema, exact path-set validation and negative tests | NOT STARTED |
| G8 — Exact-SHA verification | Normal blocking CI and required UI188 gates on the new SHA | NOT RUN |
| G9 — Signed source authority | Signed commit/tag or approved immutable attestation | NOT READY |
| G10 — Full UI188 acceptance | All 28 groups, 26 obligations, two-stream overlap and original exits | NOT RUN |

## 9. Proposed implementation sequence after approval

1. Freeze candidate input SHA and generate a complete path-by-path inventory.
2. Obtain owners' disposition for every changed product, test, migration, CI, documentation and diagnostic path.
3. Define the new CP product tree separately from test and integration trees.
4. Reconcile the CP base commit, historical test-contract base and other component pins.
5. Generate new manifest and source lock from the approved exact path sets.
6. Implement source.py schema/verification changes and negative tests.
7. Verify exact bytes, Git blob IDs, file modes, path count and tree hashes.
8. Run normal CI and UI188 source preflight on the exact new SHA.
9. Obtain separate approval for any default-branch, release, runner-cost, Full E2E or production action.

## 10. Approval record

Requested decision: approve the B direction and the gated reconstruction process, not the current candidate as an immediately accepted baseline.

- [ ] Approve B direction: current product becomes the new source baseline after review.
- [ ] Approve the product/test/integration/diagnostic separation model.
- [ ] Approve the five database migrations for inclusion after their individual review.
- [ ] Approve the dependency/Contracts update after compatibility verification.
- [ ] Approve CI and release workflow changes after security review.
- [ ] Approve the final regenerated manifest/source-lock only after G1–G9 pass.

**Explicit exclusions:** No Manifest or Source Lock replacement, no default-branch update, no PR merge, no tag, no Release, no paid Full E2E runner allocation, no migration against production, and no production deployment is authorized by this document.

## 11. G1 complete path inventory (provisional classification)

The full 335-path inventory is committed separately:

- `docs/release/UI188-CURRENT-BASELINE-INVENTORY.csv`
- Inventory commit: `18853f9bae96e65da12223b50a618febbf7a0f0a`
- Review branch: `review/ui188-baseline-reapproval`

Exact Git Tree comparison:
- Historical tree: `5879ad5924c892a128ea9e764b85edcc554a2ab1` (1,521 blobs)
- Candidate tree: `7eaccacad50fd5ca0470593226b2bff4175316f0` (1,685 blobs)
- Added 164; modified 171; deleted 0; total changed paths 335.

Path-based provisional classification (change entries, not approvals):

| Classification | Paths |
|---|---:|
| Product source candidates | 127 |
| Test source candidates | 166 |
| CI/integration harness | 26 |
| Database migrations | 5 |
| Workflow security | 2 |
| Dependency manifests | 3 |
| Documentation | 2 |
| Web build configuration | 2 |
| Diagnostic-only | 2 |
| **Total** | **335** |

These are initial routing labels based on path and filename. They are not owner acceptance. In particular, test fixtures, generated evidence helpers, build scripts, migration tests, and release tooling require file-level review. The inventory records base and candidate Git blob IDs plus file mode so the review can be reconciled against exact source bytes.

G1 is complete for exact path enumeration and provisional routing only. G1 is not complete for final per-file disposition or owner approval. G2 remains open until the product/test/harness/diagnostic boundaries are approved.

## 12. Review branch record

- Review branch: `review/ui188-baseline-reapproval`
- Inventory commit: `18853f9bae96e65da12223b50a618febbf7a0f0a`
- This branch contains review documentation only; it does not alter product code, source manifest, source lock, source verifier, default branch, or release workflows.

## 13. G2 product / test / harness boundary map

A second, more precise boundary map has been created from the exact 335-path inventory:

- `docs/release/UI188-CURRENT-BASELINE-BOUNDARY-MAP.csv`
- Boundary map commit: `120137b139cdbd870dc005eec452a4f94d22593a`
- Review branch: `review/ui188-baseline-reapproval`

Provisional component counts (335 changed paths):

| Component | Paths |
|---|---:|
| Go runtime | 60 |
| Web runtime | 67 |
| Go test source | 42 |
| Web test source | 49 |
| Browser test harness | 39 |
| Web test support | 31 |
| Test fixture data | 5 |
| CI integration harness | 20 |
| Source governance | 6 |
| Database schema | 5 |
| CI / release policy | 2 |
| Go dependency lock | 2 |
| Web tooling | 1 |
| Web build configuration | 2 |
| Documentation | 2 |
| Diagnostic-only | 2 |
| **Total** | **335** |

Important boundary refinements:
- `web/tests/helpers/**` is classified as browser test harness, not test-case source.
- `web/tests/**` is separated into executable test cases, test support/scenario modules, and fixture data.
- `scripts/ci/ui188-full-e2e/**`, polling evidence scripts, source-owner-map and source inventories are CI/integration or source-governance components, not product runtime.
- The two CI017 diagnostic files are excluded from both the runtime product tree and the standard test baseline unless separately retained as a diagnostics component.
- `web/package.json` is treated as Web tooling because the observed change is in scripts/test commands; dependency versions in that file are unchanged.
- `go.mod` and `go.sum` are a separate dependency authority because the Contracts replacement version changed.
- The five database migrations form a separate schema authority and remain subject to G3.

G2 status: the path-level component map is complete for the 335 changed paths, but runtime reachability, owner approval, and final inclusion/exclusion decisions remain open. No new product tree has been generated.

