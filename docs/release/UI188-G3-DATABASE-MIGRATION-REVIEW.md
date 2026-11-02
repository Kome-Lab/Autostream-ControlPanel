# UI188 G3 — Database Migration Review

**Status:** REVIEW COMPLETE — 086 BLOCKED; 085/087/088/089 CONDITIONAL
**Candidate commit:** `0141c66f345a7e77aa78eb72c2d77292f77dae80`
**MariaDB CI target:** `mariadb:11.4.12` (pinned image digest in `.github/workflows/ci.yml`)
**Scope:** read-only source and test review; no database was modified.

## 1. Executive decision

| Migration | Review result | Main reason |
|---|---|---|
| 085 media asset file deletions | CONDITIONAL | Additive `CREATE TABLE IF NOT EXISTS`; MariaDB restart/retry coverage exists. Require upgrade-path and key-format verification. |
| 086 upload operation and candidate names | BLOCKED | Two non-idempotent ALTER statements in one migration; partial DDL application can make retry fail before schema_migrations records completion. |
| 087 stream settings precision | CONDITIONAL | DATETIME(6) widening and concurrent CAS tests exist; legacy populated-row preservation across migration is not directly tested. |
| 088 profile and Drive precision | CONDITIONAL | DATETIME(6) widening and resource revision tests exist; legacy populated-row preservation across migration is not directly tested. |
| 089 system settings precision | CONDITIONAL | DATETIME(6) widening and concurrent settings CAS tests exist; legacy populated-row preservation across migration is not directly tested. |

## 2. Migration runner behavior relevant to recovery

`internal/database/migrations.go` applies each SQL statement separately through `db.ExecContext`. It inserts the migration ID into `schema_migrations` only after every statement in that migration has succeeded. The runner does not wrap these DDL statements in a transaction. MariaDB DDL may commit independently.

Consequently, a migration containing multiple DDL statements must be safe to resume after a process interruption or an error between statements. A successful earlier ALTER can remain applied even though the migration ID has not been recorded.

## 3. Individual review

### 085_media_asset_file_deletions.sql

Schema change:
- Adds `media_asset_file_deletions` with a binary/case-sensitive ASCII `storage_key` primary key.
- Adds retry attempt count, safe error code, timestamps and `(updated_at, storage_key)` index.
- Uses `CREATE TABLE IF NOT EXISTS` and does not update or delete existing asset rows.

Implementation and evidence:
- `internal/mediaassets/file_deletions.go` enqueues file keys transactionally, locks each key, checks live references before physical removal, persists a failure code and attempt count, and resumes collection after repository restart.
- `internal/database/stream_cover_deletion_mariadb_test.go` verifies failed physical removal persists retry state, retry converges after repository restart, and replaying removal is idempotent.

Residual gates:
- Confirm every production storage key is ASCII and fits VARCHAR(255).
- Verify upgrade from a populated pre-085 database, then restart/replay the migration runner.
- Confirm the intended operational meaning of bounded retry: the batch is limited, but `attempts` is a monotonically incremented counter rather than a capped retry count.

Disposition: conditional approval for schema-tree inclusion after storage-key and populated-upgrade evidence.

### 086_upload_operation_and_candidate_names.sql

Schema change:
- Adds nullable `client_operation_id VARCHAR(36)` and `client_operation_session_id VARCHAR(64)` to `media_assets`.
- Adds unique key `(client_operation_session_id, client_operation_id)`.
- Widens `stream_video_cover_candidates.display_name` from VARCHAR(128) to VARCHAR(255).

Positive evidence:
- `internal/mediaassets/sql_repository.go` writes and reads the new operation columns.
- `internal/mediaassets/upload_operation.go` and `upload_operation_sql.go` scope idempotent upload lookup by session and operation ID.
- `internal/httpapi/media_upload_operation_mariadb_test.go` verifies concurrent replay converges to one asset and read-only recovery does not allocate a duplicate.
- The unique key's nullable columns allow historical rows with NULL operation identity to remain present while new non-NULL pairs are unique.

Blocking finding:
- The migration contains two separate ALTER TABLE statements.
- Neither ALTER uses `IF NOT EXISTS`.
- The runner records the migration only after both statements complete.
- If the first ALTER commits and the second ALTER fails, a subsequent startup retries the first ALTER. The already-added columns or unique key can then cause a duplicate-column/index error, leaving the migration stuck.

Required remediation before approval:
1. Make the first ALTER resumable using MariaDB-supported `ADD COLUMN IF NOT EXISTS` and `ADD UNIQUE KEY/INDEX IF NOT EXISTS`, or split the change into separately tracked migrations with safe recovery semantics.
2. Keep the display-name widening independently replay-safe.
3. Add a migration recovery test that deliberately stops after the first DDL has committed, reruns the migration runner, and verifies convergence.
4. Add a populated legacy upgrade test that preserves existing media_assets and cover candidate rows, and verifies a 255-character display name round-trips.
5. Verify duplicate non-NULL operation pairs are rejected while historical NULL rows remain untouched.

Disposition: BLOCKED; do not include migration 086 in an approved source baseline until the recovery defect and evidence gaps are resolved.

### 087_stream_settings_revision_precision.sql

Schema change: widens `stream_settings.updated_at` from DATETIME to DATETIME(6). No row rewrite or column removal is specified.

Evidence:
- `internal/database/stream_settings_revision_mariadb_test.go` verifies DATETIME_PRECISION=6, exact microsecond persistence, two competing writers producing one success and one revision conflict, lifecycle-only writes not changing the settings revision, and runtime settings advancing the same revision authority.

Gap:
- The test checks the post-migration schema and then seeds a future timestamp; it does not explicitly seed a populated DATETIME(0) row before applying migration 087 and compare the value afterward.

Disposition: conditional approval after a populated legacy-row precision-widening test and supported MariaDB version confirmation.

### 088_profile_and_drive_revision_precision.sql

Schema change: widens `profiles.updated_at` and `drive_destinations.updated_at` to DATETIME(6).

Evidence:
- `internal/store/profiles_update_mariadb_test.go` verifies profile precision.
- `internal/store/resource_revision_mariadb_test.go` verifies precision for resource revision tables and exercises concurrent profile/Drive updates, stale revision rejection and monotonic clock advancement.

Gap:
- The current evidence applies embedded migrations before assertions; it does not explicitly seed legacy populated profile and Drive rows at DATETIME(0), migrate them, and compare their preserved values.

Disposition: conditional approval after populated legacy-row preservation tests for both tables.

### 089_system_settings_revision_precision.sql

Schema change: widens `system_settings.updated_at` to DATETIME(6).

Evidence:
- `internal/store/system_settings_revision_mariadb_test.go` verifies precision, concurrent writers, independent settings authority and microsecond monotonicity for revision-aware and legacy writers.

Gap:
- A populated legacy system_settings row is not explicitly shown to retain its timestamp through the precision migration itself.

Disposition: conditional approval after populated legacy-row preservation test.

## 4. Compatibility and rollout constraints

- The blocking CI workflow pins MariaDB 11.4.12 by image digest.
- The review did not establish a broader supported production MariaDB version matrix. Do not infer compatibility with other major/minor versions solely from the CI result.
- Migrations 087–089 are precision widenings, not rollback-ready transformations. A downgrade to DATETIME(0) would discard microseconds and must not be described as lossless.
- No production migration, rollback, or restore was run as part of this review.

## 5. G3 gate status

| Gate | Required evidence | Status |
|---|---|---|
| G3-085 | Storage key format + populated upgrade + restart replay | PARTIAL |
| G3-086 | Resumable DDL + interrupted migration recovery + populated upgrade + 255-char round trip | BLOCKED |
| G3-087 | Legacy populated timestamp preservation + CAS evidence | PARTIAL |
| G3-088 | Legacy profile/Drive timestamp preservation + CAS evidence | PARTIAL |
| G3-089 | Legacy settings timestamp preservation + CAS evidence | PARTIAL |
| G3-COMPAT | Supported MariaDB version matrix | NOT ESTABLISHED |

## 6. Decision

G3 is not approved as a whole. Migration 086 is a hard blocker. Migrations 085 and 087–089 remain conditional pending the stated evidence. This review does not authorize editing migrations, regenerating the manifest/source lock, running migrations on production, changing the default branch, creating a release tag, or deploying.

## 7. Follow-up — migration 086 remediation verification

**Fix branch:** `fix/ui188-migration-086-resumable`
**Verified commit:** `0b47336be86276c61e02008709f90da0d7626e08`
**CI run:** `41412414583`

Migration 086 was changed to use MariaDB-supported replay-safe DDL:
- `ADD COLUMN IF NOT EXISTS` for both operation identity columns;
- `ADD UNIQUE INDEX IF NOT EXISTS` for the operation identity pair;
- repeatable `MODIFY COLUMN display_name VARCHAR(255) NOT NULL` for the second statement.

A new MariaDB test, `TestMariaDBUploadOperationMigrationResumesAfterPartialDDL`, passed on the pinned MariaDB 11.4.12 CI service. It applies the first DDL, replays it as if the runner restarted before recording the migration, applies and replays the second DDL, and checks legacy rows, duplicate non-NULL operation identity rejection, nullable legacy identity, and 255-character display-name round trip.

The same Go job passed `go test ./...`, MariaDB persistence, SQL-backed polling/upload recovery, `go build ./...`, and all ST-PORT integration evidence gates.

Updated disposition for 086: **REMEDIATION VERIFIED IN CI; READY FOR BASELINE REVIEW**. This is not production migration approval. A populated historical database upgrade/restore rehearsal and exact final-baseline verification remain required before release.

CI run `41412414583` was still processing browser-regression matrix jobs at the time of this update. The UI188 historical Source Integrity preflight remains a separate failure because its verifier still pins the old source authority; no manifest or source-lock change was made.

## 8. Follow-up — populated legacy-row migration witnesses

**Fix branch:** `fix/ui188-migration-086-resumable`
**Verified candidate:** `a1934923cdf3c98a5b223209956ecaa949a3587b`
**CI run:** `41438118480`

Two additional MariaDB tests were added:
- `TestMariaDBAdditiveDeletionQueueMigrationPreservesExistingQueueRows`: starts with an existing deletion-queue row, replays migration 085, and verifies attempts/error metadata remain unchanged.
- `TestMariaDBRevisionPrecisionMigrationsPreserveExistingRows`: seeds DATETIME(0) rows in stream_settings, profiles, drive_destinations and system_settings; applies 087, 088 and 089; checks each updated_at column is DATETIME(6) and the original timestamp is preserved at microsecond precision.

On the current CI run, the following steps have passed:
- `go test ./...`
- `Test MariaDB persistence`
- `Verify UI188 atomic roles, cover snapshots, stop ownership, and safe deletion with MariaDB`

The migration-focused MariaDB step is therefore PASS on candidate `a193492`. Remaining steps in the same full CI run (SQL-backed UI witnesses, build and ST-PORT evidence) are still running. The UI188 historical Source Integrity preflight remains a separate failure and is not treated as a migration failure.

Updated G3 status: migrations 085–089 have focused migration/replay/legacy-row test evidence on the pinned MariaDB 11.4.12 CI service. G3 may be closed for source-baseline inclusion after the remaining full CI run completes successfully. Production upgrade/restore rehearsal and broader MariaDB version support are still release operational checks, not evidence already obtained.

## 9. G3 closure — focused MariaDB migration suite passed

**Verified candidate:** `a1934923cdf3c98a5b223209956ecaa949a3587b`
**CI run:** `41438118480`

The workflow step `Test MariaDB OAuth and Configure Token migrations` discovers every `TestMariaDB*` test in `internal/database`, excludes only the separately run physical-EOL suite, executes the discovered set with the MariaDB 11.4.12 service, and fails if any discovered migration test is skipped.

That step completed successfully on the candidate SHA. This includes:
- `TestMariaDBUploadOperationMigrationResumesAfterPartialDDL` (086)
- `TestMariaDBAdditiveDeletionQueueMigrationPreservesExistingQueueRows` (085)
- `TestMariaDBRevisionPrecisionMigrationsPreserveExistingRows` (087–089)
- the pre-existing MariaDB migration/replay tests.

The Go job also passed `go test ./...`, MariaDB persistence, UI188 atomic-role/cover/stop/safe-delete MariaDB witness, SQL-backed polling/upload recovery, OAuth/Configure Token migration checks, `go build ./...`, and ST-PORT full-chain evidence gates through the currently reported successful steps.

**G3 disposition: CLOSED for source-baseline inclusion.** Migrations 085–089 have focused migration/replay/legacy-data evidence on the pinned MariaDB 11.4.12 CI service. This closure does not claim a broader MariaDB version matrix or production upgrade/restore rehearsal; those remain Release Candidate operational checks. No production database was modified.

