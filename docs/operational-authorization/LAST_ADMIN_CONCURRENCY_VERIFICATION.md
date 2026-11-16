# Last-admin concurrency: source, reproduction, and verification

Date: 2026-11-14 JST. Accepted baseline: fa475d8d96ded641d3fab8f9009e5a1a296423bb. Repository: Kome-Lab/Autostream-ControlPanel, PR #12, branch codex/v3-cp-authorization-20261113, base v3-dev.

## Contract and scope

A normal management mutation must not remove the last active super_admin from a state containing active administrators. The canonical definition is the existing CountActiveSuperAdmins join: users.status='active' and an assigned Role named super_admin. The guard row stores no count, Principal, Permission, Grant or recovery authority.

Role=PRESERVE, Scope=HYBRID and Workload Identity=SEPARATE remain unchanged. Permission/Subject/delegation, Session/CSRF, self-Role change, immutable super_admin Role, and assignment exceptions are retained. The accepted S13 evaluator and HTTP delegation semantics are not redesigned.

## Source inventory (inspection, not execution proof)

| Existing route | Permission and Handler | Canonical mutation |
|---|---|---|
| POST /users/{id}/disable | users.disable; disableUser -> setUserStatus; existing super_admin actor rule | MariaDBAuthStore.SetUserStatus(disabled) |
| POST /users/{id}/lock | users.disable; lockUser -> setUserStatus; same actor rule | SetUserStatus(locked) |
| DELETE /users/{id} | users.delete; deleteUser; self-delete, super_admin actor and ordinary subset checks | DeleteUser |
| PUT /users/{id}, role_ids present | users.update + roles.assign; updateUser -> validateUserRoleUpdate; self-Role, assignment/subset and typed delegation checks | UpdateUser, including its Role assignment replacement |
| POST /users/{id}/force-password-change | users.reset_password; forcePasswordChange -> setUserStatus; existing status actor rule | SetUserStatus(pending_password_change) |
| POST /users/{id}/reset-password | users.reset_password; resetPassword; password policy and password-reset actor/subset checks | ResetPassword, which also sets pending_password_change |

The last two routes are included because their existing SQL removes the User from the same active count. Rejecting the sole active administrator's transition to pending_password_change is necessary for this invariant; it is a last-admin refusal, not a new approval process.

User creation/OAuth provisioning add Users/assignments; new manual Users start pending_password_change. Unlock and ChangePassword activate Users. These do not reduce active cardinality. Username/email-only UpdateUser and ordinary Custom Role CRUD retain their normal behavior. HTTP super_admin Role update/delete protections prevent removing the canonical Role through Role management.

All production writes to users.status and user_roles were inspected. RecordLoginFailure is a competing authentication writer. It joins the same guard -> User -> Operational epoch order while retaining its exact existing threshold/status SQL. RecordLoginSuccess does not change status; ChangePassword only sets active.

## Executed reproduction before product changes

The controlled test ran at commit 410fe456aea70c94068a4132573ada52ccf962f5, with product code identical to the accepted baseline.

- Two independent Server objects, two real MariaDBAuthStore objects, two separate sql.DB pools and physical connection IDs share only the disposable CI database.
- A permitted synthetic Custom Role actor sends PUT /users/la-admin-a and PUT /users/la-admin-b with role_ids=[].
- Each real HTTP helper performs its actual SQL CountActiveSuperAdmins query. A test-only barrier holds each returned count until both queries have read 2. It does not replace the count or perform the mutations.
- Both actual Store transactions are then released.

Observed result: **HTTP 200 / 200; active super_admin count 0**. The invariant assertion failed for the intended reason.

[Executed red CI](https://github.com/Kome-Lab/Autostream-ControlPanel/actions/runs/43591233191)

Initial attempts using an InnoDB metadata wait-observer fixture timed out before establishing their intended schedule. They are fixture failures, not evidence that protection passed or failed. The retained reproduction instead directly controls the real SQL precheck/update gap.

## Minimal product correction

A new forward-only migration, 093_user_admin_guard.sql, creates one InnoDB singleton lock row. It does not edit any of the 93 existing migrations, create administrator identities, copy permissions, reset counts, or migrate real data.

For SetUserStatus, DeleteUser, ResetPassword and role-bearing UpdateUser:

1. Begin an explicit READ COMMITTED transaction.
2. Lock user_admin_guard(singleton=1) FOR UPDATE.
3. Lock the target User row FOR UPDATE and read canonical status/Role membership.
4. Recheck active administrator cardinality inside this transaction when the intended change removes active super_admin capability.
5. Execute the existing source writes and commit once.

UpdateUser takes these locks before username/email writes when Role assignments are present. DeleteUser locks the User before deleting Sessions and assignments. The legacy disabled/locked refusal for inactive super_admin Role holders is retained. No new counter or duplicated Role authority is introduced.

RecordLoginFailure acquires the same guard and target User lock before executing its unchanged failed-login counter/lockout statement. This prevents management transactions from committing from a count read before a competing automatic lockout. It does not disable authentication lockout or add recovery.

The new guard's absent row/table fails rather than being recreated by a request. A state with zero administrators is never automatically repaired. Migration replay is tested only on the dedicated disposable database.

## Lock order and transaction boundaries

| Path | Held locks/order |
|---|---|
| Last-admin management writes | dedicated guard -> target User -> existing Session/assignment rows -> Operational epoch/history via unchanged triggers |
| Authentication failure writer | dedicated guard -> target User -> Operational epoch/history |
| Existing S13 Current/Revision reads | Operational epoch -> nonlocking READ COMMITTED source reads; transaction finishes before the management Store mutation |
| Existing Evidence/Authority management | independent Authority epoch -> User -> Session/own authority records and audit, according to the existing source |

The dedicated guard has no foreign keys to Users/Roles or either epoch. No existing writer can acquire it late through a foreign-key check. The new paths never acquire an Authority epoch. Existing v3 product source and its migrations are unchanged.

Runtime tests reproduce the existing C epoch/User/Session SQL lock sequence against real rows and contend the new management paths against it. This is a shared-row lock-order test, not a claim that all C operations or cross-domain side effects became one transaction. The unchanged CP required workflow separately runs its actual SQL-owner HTTP suite.

## Commit failure, rollback, and response contract

A Commit error returns ErrUserAdministrationUnknown joined with the underlying failure. There is no transaction replay, automatic success conversion, re-grant, or recovery path. Existing generic failure responses remain for DB/Commit failures; they do not prove that the server rolled back.

A late last-admin refusal from the Store maps to the existing 409 last_super_admin response in User Role update and password reset. Existing status/delete 409 mappings remain. A preexisting sole-admin self-delete still returns cannot_delete_self. Other permission and error ordering remains in place.

Real-driver fault injection covers:
- a successful partial SQL statement followed by a response error, then transaction rollback;
- rollback/physical connection close with a lost Commit response;
- a real successful Commit whose acknowledgement is replaced by a transport error.

The last case leaves the committed canonical state with one active administrator while the HTTP caller receives failure. A fresh independent request must re-read that state and refuse removal of the remaining administrator. Commit is invoked once. Failed/unknown requests must not write a success audit.

The rollback oracle compares complete target User/Role state, target Session count, Operational generation/history and audit count. Password hashes, cookies and tokens are never printed.

## Verification inventory and evidence status

The required inventory contains 68 named roots, including all prior 57 and the 11 last-admin/source roots. All new roots match the existing TestOperational required/CGO-free selection and the full Race run. No prior successful test is deleted, weakened or skipped.

| Proof | Evidence |
|---|---|
| Baseline source inference | Split count/update and HTTP-only Role removal count; additional pending_password_change transitions identified above |
| Actual pre-fix failure | Two real SQL counts of 2, followed by HTTP 200/200 and count 0 |
| First actual corrected reproduction | Commit 861ebe4903d3795039599f24d4b01d4226c88b3e: HTTP 200/409 and count 1; both required workflows succeeded |
| Mixed/same-User matrix | 21 operation pairs across different Users and the same User: 42 controlled cases; retained as mandatory tests |
| Native DB exclusion | During every protected operation, independent NOWAIT probes must fail on both the guard and target User rows |
| Unrelated operations | Profile update without role_ids and Custom Role create/update/delete complete while a protected mutation retains its guard |
| Addition/removal order | Shared guard acquired in a controlled order; addition first allows both; removal first from one administrator refuses removal and then permits addition |
| Rollback/unknown | Real MariaDB transactions with the three driver-level failures above; final exact-HEAD result recorded in PR validation |
| Missing guard / replay / zero state | No successful protected mutation without its guard; replay preserves source rows; runtime does not recreate a missing guard or restore administrator assignments |
| Authentication policy | Automatic lockout serialized before management makes management recheck/refuse; the existing standalone authentication lockout policy remains |
| C shared-row order | Actual Authority epoch, User and Session row locks; management completes after their release without inverted Session-before-User acquisition |

At the intermediate expanded-suite run, the new fault fixture mistakenly referred to audit_events. It was corrected to the source-confirmed audit_logs and both endpoint constructors now connect the real MariaDBAuditStore. That fixture failure is not classified as a product failure or as executed Commit-unknown proof.

Local execution is limited to compilation, focused non-SQL compatibility and immutable-source tests. No local MariaDB or full/race suite is run. All database proof uses the exact Actions-only loopback autostream_s13_ci schema/user and synthetic data. Merely setting an arbitrary DSN cannot enable these tests.

Final HEAD, both final CI URLs, tested merge SHA/tree, exact inventory run/pass counts and remaining limits are recorded in [PR #12](https://github.com/Kome-Lab/Autostream-ControlPanel/pull/12). Intermediate successes are not substituted for the final candidate's proof.

## Source preservation

- admin.go: replay six exact reviewed transaction-hook transformations over the checksum-anchored immutable v2 source; compare the entire formatted result.
- auth_sessions_mariadb.go: one exact shared-lock delegation hook; the lockout threshold and SQL are unchanged.
- server_users_roles.go: the seven accepted S13 delegation hooks plus two specific last-admin error mappings; compare the whole file.
- 093_user_admin_guard.sql: exact reviewed SQL checksum.
- Full-history SourceBoundary independently validates the fixed Git blobs and all 93 existing migrations. Only the exact new migration gets its dedicated verifier; no protected file is excluded wholesale.
- Module files, Contracts, Updater, existing migrations and Evidence/Authority product source remain unchanged.

## Limits and unperformed work

This guarantee is for the ordinary management removal paths above and the count/writes inside their database transactions. Existing authentication lockout can itself lock the sole remaining administrator; that authentication policy is preserved, not redefined as recovery or full account availability. Direct SQL/DBA changes, restore continuity, fleet-wide recovery and existing zero-admin states are outside the claim.

No live database, actual Role assignments, staging/production migration, deployment, merge, tag/release, ruleset change, Bootstrap, recovery re-grant, Scoped Grant CRUD/UI, independent ledger, real key, Qualified Clock or real Evidence is used or added. No startup applies the opt-in Evidence/Authority migrations. No whole-HTTP or remote-side-effect atomicity is certified.

Full PRESERVE; old Execution Authority not reused; automatic-start hold retained; UNKNOWN/HELD never success; real Evidence disabled. Product-wide acceptance NOT_RUN; Phase 1 held; production migration unauthorized; formal v2 return NOT_CERTIFIED. PR remains Open/unmerged.
