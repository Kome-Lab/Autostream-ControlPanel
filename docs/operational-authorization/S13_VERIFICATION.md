# S13 Verification and Handoff

Date: 2026-11-14 JST. This report distinguishes implemented behavior, local proof, required CI proof, fixed-source confirmation, and unperformed environmental work.

## Scope and implemented behavior

- Role=PRESERVE; Scope=HYBRID; Workload Identity=SEPARATE.
- Operational Authorization is a separate package and an explicit existing-HTTP composition option. It does not alter Evidence Authorization, Authority Management, Canonical Request/Binding, Contracts, Updater, existing migration files, or module pins.
- Role contents and assignments remain canonical. All 99 Permission names map to closed Operations. Grant IDs identify Principal/Role/Operation; revision/state are evaluated independently.
- Current Role identities and operations come from one snapshot. No copied Role-name template, super_admin catalog bypass, implicit admin expansion, or fallback from a failed configured source.
- Global/typed exact Scope, Subject constraints, delegation containment, fresh revision, revocation, and UNKNOWN/HELD refusal are implemented. Even the super_admin assignment exception remains within its entry Grant Scope/Subject boundary.
- Existing 175 single-Permission entries, 10 Any entries and the 1 video-cover Handler-selected Permission entry are connected. Session-only APIs and independent B/C authentication are preserved.
- A dedicated explicit MariaDB migration adds a checksum/dirty journal, generation, append-only history and 14 triggers over four existing authorization tables/history. Existing v2 rows are not copied, erased, renamed or default-expanded.

## Fixed-source confirmation

| Item | Evidence |
|---|---|
| v2 baseline | 93181192d8f036bae40b490957489c9ee8290769 |
| CP implementation baseline | d42a0fb2212c2378b822effc9b8dfd7031d457cb |
| Contracts read-only reference | 45aaa9932bcd92c728c86c95349853724fe1accc |
| Updater read-only reference | cdc106ab795623e41e2fe42a73c598c782e5c8de |
| v2 production Go / SQL | 316 / 93, 409 of 409 cached source blobs revalidated |
| HTTP registration / expanded routes | 246 / 261 |
| Permission catalog | 99; sorted-catalog SHA256 bc4e06d45fcea5d4137d4014e55df34095173caee1e1e49dd048af7f6716d89d |
| additional fresh HTTP source review | 101 fixed-v2 production HTTP Go files, zero retrieval failures |
| live Role/User/credential export | NOT_PERFORMED |

The four preserved review documents contain source anchors, all Permission and Route rows, target/state/assignment/delegation conditions, and historical future-decomposition candidates. Their S13 preface distinguishes that historical design from this compatibility implementation.

## Local execution evidence

Commands use Go 1.26.5, readonly modules, GOMAXPROCS=2 and -p 1.

| Check | Result |
|---|---|
| Core Operational tests | PASS: 13 roots, 127 including subcases |
| Focused Store/HTTP Operational tests | PASS for non-SQL paths: administrator continuity, all 99 Permission entries, CSRF, Role refresh, live revocation, Any semantics, Handler-selected show/hide, scope binding, Human/Workload separation |
| Real Stream API Hybrid test | PASS: existing Global reads retained; exact Stream scope permits its target/HEAD, refuses other targets/collection; revocation never falls back to old Global permissions |
| Delegation regression red signal | Confirmed failure before adding entry Scope/Principal containment; same focused test passes after correction |
| AST source boundary | PASS in core tests |
| protected source/module/migration diff | No changes to protected B/C/source/migration/module files |
| local SQL integration | NOT_RUN: no DSN supplied; SQL cases deliberately skip locally |

A locally passing parent whose SQL subcases skipped is not counted as SQL proof. Synthetic credentials are fixtures only. No local database was connected.

## Required final CI

[PR #12 checks](https://github.com/Kome-Lab/Autostream-ControlPanel/pull/12/checks) are the authoritative live result for the final PR HEAD; this committed report does not substitute a previous run for final-source execution.

The new workflow requires **44 named roots: 30 Operational/HTTP/SQL roots and 14 pre-existing Workload binding roots**. Each must run once, pass once, and have no skip/failure. JSON output reports root/subcase totals. SQL tests require the exact workflow-created autostream_s13_ci user/database, GitHub Actions, and loopback TCP; they cannot use a developer/environment DSN.

CI requirements on the exact final tree:

1. Protected-source boundary and strict gofmt.
2. All required roots, including MariaDB source expansion, actual v2/v3 Permission union, explicit migration replay, dirty/checksum/trigger failure, invalid engine, concurrent installers, rollback, generation CAS/ABA, and all 93 existing embedded migrations.
3. Full go test ./... and go vet ./....
4. CGO-free affected tests and full go test -race ./....
5. Existing entrypoint build and Linux amd64/arm64 CGO-free cross-build.
6. Unchanged go.mod/go.sum and final source boundary.
7. The separate existing v3-cp-authorization-checks remains required for B/C/Canonical interoperability proof.

The workflow logs the tested Commit and tree. The final PR report must verify that tree against the final branch HEAD and link the concrete successful runs.

## Scope, identity and authority boundaries

| Boundary | Implementation / limitation |
|---|---|
| legacy Global scopes | Derived as Global; existing Handler ownership/state/assignment policies remain additional conditions |
| newly authored exact Scoped grants | Evaluator and HTTP adapter supported through typed Store; persistent Scoped Grant CRUD/UI is not connected to the legacy SQL projection |
| legacy super_admin | Actual operations preserved; independent assignment exception; original self/last-admin/password/status restrictions retained |
| last-admin concurrency | Existing single-request guard retained/tested; legacy cross-request race is not certified as a new atomic Recovery guarantee |
| Grant revision | SQL triggers participate in every source authorization mutation; no cached authority |
| in-flight side effects | Fresh at authorization entry; remote Handler side effects are not atomically committed with later Session/Role revocation |
| memory source | Isolated fixture adapter; unobserved source ABA is not production continuity proof |
| SQL change history | DB-local history; not an independent ledger, qualified clock, or DB-admin/restore tamper guarantee |
| Workload | Existing Service Token Scope, job/host ownership, Mutation/Self Update Grant and execution policy checks remain independent |

The ordinary v2 Role CRUD API is not changed into a new Scoped Grant management API. Unknown stored Permission values remain in source and refuse Operational evaluation; they are not silently discarded or interpreted.

## Rollback and recovery conditions

Permission-set loss or gain, Scope widening/narrowing, lost Role membership, missing super_admin behavior, stale revision acceptance, dirty/inconsistent schema, cross-identity credential substitution, or protected-source drift blocks cutover.

A failed source DML rolls back generation/history/source together. MariaDB DDL is not transactional; interrupted or invalid installs retain dirty state and refuse automatic repair/reissue. A clean migration is published only after source-engine and locked structural validation. Unknown commit results are errors, never success.

No environment migration occurred. No instruction to drop production history, edit journals, reset grants, reuse old Tokens, or restore old Execution Authority is provided. Actual environment rollback/restore and formal v2 return are not certified by these source/CI fixtures.

## Unperformed environmental work and preserved gates

Real Custom Role/User inventory, migration of deployed data, actual administrator cutover, production/staging SQL, Hosts/Agents/providers, persistent new Scoped Grant administration, real keys, Qualified Clock, real Evidence, independent Continuity/ledger, Trust Root/Bootstrap/Recovery and deployments are not connected.

Full PRESERVE; old Execution Authority not reused; automatic-start hold maintained; UNKNOWN/HELD non-success; real Evidence disabled. Product-wide acceptance NOT_RUN; Phase 1 closure held; production migration unauthorized; formal v2 return NOT_CERTIFIED. PR stays Open/unmerged.
