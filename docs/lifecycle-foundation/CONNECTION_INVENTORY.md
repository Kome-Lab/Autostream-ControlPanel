# Lifecycle connection inventory

Product baseline: ee26856bbf7e808424a658345dd5f362d5500bb6.
B0 adds only internal/lifecyclefoundation, explicit opt-in persistence, synthetic
collaborators/tests, documentation and exact CI extensions. **No product entry is
connected or enabled.** Source paths below refer to that fixed baseline.

| Owner / source | Current transaction and behavior | Required participation | B0 status |
| --- | --- | --- | --- |
| cmd/control-panel/main.go, database.RunEmbeddedMigrations | Standard embedded migration is a startup writer before Server composition | Preflight before relevant startup writes; known deployment/schema/guard/current ledger | B1 pending; product source unchanged |
| internal/v3authorization/sql_authority.go: Read | Nonlocking authority snapshot, Principal/current target/policy | R admission bound to same Snapshot, identity and current Checkpoint | B1 pending |
| Same: ResolveTarget, ResolvePolicyTrust, AuthorizeRead | Resolver/snapshot/current historical-read authorization | Same R context, no stale cache; distinguish pure codecs | B1 pending |
| authority_store.go: Generation | Independent generation query | R if used to authorize; diagnostic-only use is not authorization | B1 pending |
| authority_store.go: Save | Own or private bound transaction; epoch CAS and History | W participant, same expected generation and checkpoint; no bare Save bypass | B1 pending; no signature or lock change |
| management_sql.go: Bootstrap | C epoch -> User -> Session -> Marker/Grant/audit direct writes; does not call Save | Independent approval/Reserve before locks, same-Tx Consumption/Marker/History/checkpoint | B1 plus S12-A/C pending; no real origin/Ceiling |
| management_sql.go: Apply | C epoch-first transaction, current ManagementGrant and bound Save | W and same-Tx participant; preserve operations/Subject/delegation | B1 pending |
| management_sql.go: Inspect | Current identity/ManagementGrant checks and audit transaction | R current admission; separate pure recovery Q | B1 pending |
| internal/httpapi/server_v3_authorization_management.go | Session/CSRF and independent ManagementGrant API | Trusted actor/opaque Session context; Service and internal callers also closed | B1 pending; no route added |
| internal/operationalauthorization/service.go: Current, Authorize, ValidateDelegation | Fresh A Snapshot/Revision; typed delegation differs from ordinary use | R with exact route target/Subject; preserve ordinary DelegationNone use | B1 and D2 pending |
| internal/store/operational_authorization_mariadb.go | Operational epoch lock, READ COMMITTED nonlocking source reads, ends before management write | Reviewed participant ownership before any new shared lock; preserve union and Role identity | B1 pending |
| server_users_roles.go; store/admin.go; oauth_login.go provisioning | Role create/update, manual and OAuth-default User assignment; existing subset/super_admin/typed delegation conditions | W with exact persisted rights; canonical v2 rows stay authoritative | B1/D2 pending; no CRUD or policy change |
| internal/v3authorization/service.go: Issue | Read -> Claim in a separate transaction -> prepare/seal -> WithCurrent -> Finish | Admission before Claim; PENDING and every durable substep belong to same Attempt | B1 pending; real Evidence remains disabled |
| sqlstore.go: Claim, Finish, Lookup; service.go: Read/readRecord | PENDING identity, bound Finish, persisted history plus current read checks | W participation through Claim/Finish, R for current Read/Replay; no takeover/re-seal | B1 pending |
| sql_authority.go: WithCurrent / authorityTransaction.finish | C epoch -> User/Owner -> issuance; private one-shot same-Tx Finish | Preserve existing ownership and one-shot context; add reviewed checkpoint participation | B1 pending; existing implementation byte-identical |
| internal/store/admin.go | User update/status/delete/reset/force-change, assignments and Session deletion | Approved identity W with target state, guarded changes and content coverage | D2/B2 pending |
| auth_user_credentials_mariadb.go; auth_credential_values.go | Password/status/authentication material updates; ChangePassword may reactivate | Identity W and old-credential/new-Session restore evidence | D2/B2 pending |
| auth_sessions_mariadb.go | Session creation/refresh/individual/all invalidation; expired-session cleanup | Identity W, opaque Session binding/revocation; approved expiry/refresh coverage | D2/B2 pending |
| oauth_login.go | Provider/identity link creation/search/removal, provisioning | Verified issuer/subject/User binding, removals and provider interpretation | D2/B2 pending |
| auth_mfa_mariadb.go; auth_passkeys_mariadb.go; auth_email_change_mariadb.go | MFA/Recovery-code/Passkey/challenge/email writes and consumption | Identity W for all authentication-affecting writers; no raw secret export | D2/B2 pending |
| security_settings_mutation.go; HTTP authentication/policy callers | Security/Provider/MFA applicability policies affect identity acceptance | Close exact policy inputs and role dependencies | D2/B2 pending |
| operational_last_admin_mariadb.go; admin.go; auth_sessions_mariadb.go | guard -> User -> Session/Assignment -> Operational trigger, one Commit, Unknown on lost ACK | Reserve before transaction; no late C epoch/common-lock inversion | B1/B2 pending; accepted guard/protections retained |
| User cascades, cleanup and background writers in the sources above | Some changes are single DML or helper/cascade effects rather than separate routes | Include cascades and implicit writers; do not certify from HTTP route coverage | D2/B2 pending |
| Unresolved inspection / Abort / Reconcile / Recovery | No qualified product entry/origin presently configured | Pure Q; changes require purpose-specific independent approval and old-writer fence | Contract types / synthetic inspection only; S12-C/D pending |
| Contracts v3evidence canonical decode/encode | Pure structural/request/binding contract | No ledger I/O in codecs; current authority belongs to caller | Outside Gate itself, unchanged |
| Updater guard/ContinuityReadPort | Independent Host/Executor execution qualification and continuity dependency | Separate from CP L2; never convert Human Role into workload permission | Read-only reference; unchanged |

## B0 implemented seams

Binding/Checkpoint/Attempt/Approval/Receipt types and Port; Service ordering and
separate trusted local Authorizer; private callback participant; CP same-Tx
target/Consumption/Checkpoint/History; separately committed Receipt confirmation;
immutable uncertainty journal and pure Inspect. The SQL ledger is test-only.
These are synthetic connections, not the product integrations above.

## Migration and source boundary

Only the new package migration can create its own prefixed tables/triggers when
explicitly invoked. No existing standard migration, module pin or entrypoint is
changed. Current startup behavior is inventoried, not silently replaced.

The inventory is a fixed-source connection plan, not an assertion of complete
live writer/fleet coverage. Existing F1-F7 and S13 inventories retain their own
scope and provenance. Before B1/B2, actual caller closure, identity policy
coverage and common lock order must be verified; unknowns stay held.

## Basis

- [Existing VPS deployment design](https://chatgpt.com/space/page_9cfcd94df42c81918ba48a694288a921)
- [L2 minimal connection contract](https://chatgpt.com/space/page_1f5aac960cd8819195002ea23197549e)
- [L2 candidates and failure-domain limits](https://chatgpt.com/space/page_2a3224dea4f88191be22c6430239c9ea), AWS proposal excluded
- CP PR #12 accepted S13/delegation/last-admin reports; S11 and F1-F7 as linked in those designs
