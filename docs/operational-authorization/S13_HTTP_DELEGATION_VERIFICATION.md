# S13 HTTP Role Delegation — Verification

Date: 2026-11-14 JST. Follow-up baseline: 62e0ea3863c476a38b1d6900eeec9788bbbda337. PR #12, branch codex/v3-cp-authorization-20261113, base v3-dev.

## Actual red/green reproduction

These results were executed for this change, using synthetic active Human sessions, the real Server mux, and existing Handlers. They are distinct from the review's source-only observation.

| Case | Before production fix | After fix |
|---|---|---|
| roles.create Global/Subset + streams.start Global/None; POST /roles delegated-start | Typed ValidateDelegation denied; HTTP 201; Roles 1 -> 2 | Typed denied; HTTP 403; Roles 1 -> 1 |
| users.update + roles.assign Global/None + streams.start; PUT /users/{id} | Typed ValidateDelegation denied; HTTP 200; target Role IDs 0 -> 1 | Typed denied; HTTP 403; target Role IDs 0 -> 0 |
| Ordinary streams.start use with DelegationNone | Authorize and ordinary Permission projection allowed | Still allowed; actual POST /streams/missing-synthetic-stream/start reaches the existing Handler's 404 missing-target response, with no dispatcher or Workload identity |

The initial two regression tests failed before production edits. They were retained unchanged as mandatory tests. No review-side executed reproduction is claimed.

Additional typed red tests showed the super_admin assignment exception accepted explicit None, revoked, scoped and Subject-limited operation grants. The fix preserves assignment of unheld known operations, while requiring represented operations to have a complete, active, delegable covering grant. A legitimate covering grant in another Role still permits delegation; None is not a deny override over that union. The existing v2 projection is Global/Subset, so this adds no administrator reapproval or legacy permission narrowing.

## Actual HTTP connections

| HTTP path | Handler / helper | Typed effect |
|---|---|---|
| POST /roles | createRole -> validateOperationalRoleDelegation | Proposed Role permissions, Global, unrestricted Subjects, Subset |
| PUT /roles/{id} | updateRole -> validateOperationalRoleDelegation | Proposed replacement permissions and canonical existing Role ID, Global, unrestricted Subjects, Subset |
| POST /users | createUser -> validateRoleAssignments -> validateRoleAssignmentsForSubject | All canonical Role definitions; future Principal has no client-supplied ID |
| PUT /users/{id} | updateUser -> validateUserRoleUpdate -> validateRoleAssignmentsForSubject | All canonical Role definitions, bound to the loaded target User ID |
| POST /integrations/oauth-providers | createOAuthProvider -> validateOAuthProviderRolePolicy -> validateRoleAssignments | Default Roles for unknown future Principals |
| PUT /integrations/oauth-providers/{id} | updateOAuthProvider -> validateOAuthProviderRolePolicy -> validateRoleAssignments | Replacement default Roles for unknown future Principals |

Production call sites of validateRoleAssignments and all Role/User permission mutation routes were inspected. OAuth auto-provisioning consumes the previously validated provider policy; no new login-time approval flow is introduced. Other deletion/status/password checks retain their existing semantics.

Current() still returns ordinary-use operations with DelegationNone. The HTTP hooks separately call typed ValidateDelegation before any Role/User/provider save. The roles.assign use gate evaluates its trusted target context separately; it is not a substitute for delegation validation.

The actor is the authenticated context User. Role assignments are loaded from the canonical RoleStore, never a body-supplied permission list. Legacy Role persistence grants Global/unrestricted/Subset effects; body/query scope or Subject claims cannot attenuate the validation descriptor and then save broader rights. Unknown future User IDs and provider default Roles require an unrestricted assignment entry. Known User paths may use a matching Subject-restricted roles.assign entry.

The entry snapshot revision is retained in currentUser and passed as ExpectedRevision to subsequent typed checks. Snapshot validation and the final Revision check remain required. A changed generation, failed read, revoked/UNKNOWN/HELD/inconsistent source, or invalid Workload Principal refuses the operation. No fallback is introduced.

This is freshness at authorization decisions, not a claim of atomicity from authorization through every DB or remote side effect. Concurrent mutation after the final revision check remains outside this correction's certification.

## Executed local verification

Focused local run: **27 roots / 157 tests including subcases passed; zero skips and failures**, across httpapi and operationalauthorization. This includes 13 new mandatory roots, existing S13 administrator/stale-Role tests, and existing v2 Role assignment/protection regressions.

Covered behavior:

- Six real HTTP paths: permitted Global/Subset succeeds; operation None or entry None returns 403.
- Complete saved Role/User/provider records compare equal after rejected mutations, including username/email updates and mixed multi-Role requests. Validation of an earlier Role does not save a partial assignment when a later Role is nondelegable.
- Ordinary operation use survives None. A legitimate delegable operation in another assigned Role preserves the union.
- Global widening from limited or split Scope/Subject grants, self-limited operations, forged body claims, and a wrong target User are refused. Exact target Subject assignment succeeds.
- Source absence/error, late snapshot/revision failure, changed generation, UNKNOWN/HELD, foreign Principal, invalid Role relation/operation/delegation and revocation all refuse without saves.
- Legacy unconfigured and explicitly configured live v2 projection both permit the same six normal mutations.
- Reduced super_admin assignment of an unheld operation succeeds, but Role create/update retains Permission subset restrictions. Explicit new entry/operation constraints remain binding.
- Self Role changes, immutable super_admin Role, last active super_admin, Custom Role assignment limits, Session, CSRF and active User checks remain enforced. Existing baseline S13 and v2 protection tests are retained.

Tests use isolated in-memory synthetic data. No local DB or real Role assignments were read. Full tests, SQL, Race and cross-build proof are supplied by final CI, not inferred from focused local results.

## Source boundary and required CI

The frozen v2 commit is unchanged: 93181192d8f036bae40b490957489c9ee8290769. The 409-file / 99-Permission / 261-Route inventory remains the source basis.

server_users_roles.go has seven explicit hook changes. TestOperationalHTTPRoleSourceBoundary checks the immutable v2 fixture SHA256, replays only those exact reviewed transformations over it, and compares the entire formatted result. The full-history shell SourceBoundary independently verifies the original Git blob has that same SHA256. No Handler is excluded. In-memory tamper probes against the last-admin condition, self guard, super_admin protection, delegation hook and route permission must fail comparison.

The shell SourceBoundary invokes that mandatory test for this specific file; all other protected paths retain their previous checks. Module files, existing migrations, Evidence/Authority source and the original CP CI workflow stay immutable.

The required inventory now has **57 named roots**: 43 Operational roots plus the 14 existing Workload roots. All new roots use the existing TestOperational selection and are included in required, CGO-free and full Race executions. No existing successful test was removed, skipped, or weakened. The operational workflow also checks formatting of the modified legacy Handler.

The first follow-up CI attempt exposed a test-environment dependency: the original new source guard required a historical Git object unavailable in the existing CP workflow's shallow checkout. Its v3 product/SQL tests passed, but the subsequent full Go run failed that guard. The checksum-anchored v2 fixture removes the history requirement from ordinary Go tests without skipping verification. The full-history Operational workflow still validates the original Git object. No B/C implementation or existing test was altered.

Final proof must match the final head tree in both required jobs:

- v3-operational-authorization-checks: mandatory inventory with zero skip/failure; isolated MariaDB; all Go tests; vet; full Race; CGO-free; build; Linux amd64/arm64 cross-build; SourceBoundary and unchanged dependencies.
- v3-cp-authorization-checks: independent Evidence/Authority/Canonical and fixed Updater interop checks.

Final HEAD, tested merge commit/tree, exact run URLs and results are recorded in the [PR validation section](https://github.com/Kome-Lab/Autostream-ControlPanel/pull/12). Earlier S13 CI counts in S13_VERIFICATION.md are baseline evidence, not this follow-up's final verification.

## Not performed / still unconnected

No Contracts/Updater changes, existing migration changes, new migration, DB connection, live data read, migration application, deployment, merge, tag/release, or protection configuration change. Source-only references remain Contracts 45aaa9932bcd92c728c86c95349853724fe1accc and Updater cdc106ab795623e41e2fe42a73c598c782e5c8de.

New scoped Grant persistence/CRUD/UI, Bootstrap, independent ledger, real signing keys, Qualified Clock, real Evidence issuance, independent Continuity, fleet/provider runtime acceptance, and end-to-end side-effect atomicity remain unconnected or unperformed. Human operational privileges do not replace Service/Node/Host/Worker/Updater/Executor credentials, Mutation Grants, or Host Self Update Grants. Independent Evidence and Trust & Authority Management remain unchanged.

Full PRESERVE; old Execution Authority not reused; automatic-start hold maintained; UNKNOWN/HELD non-success; real Evidence disabled. Product-wide acceptance NOT_RUN; Phase 1 held; production migration unauthorized; formal v2 return NOT_CERTIFIED. PR remains Open/unmerged.
