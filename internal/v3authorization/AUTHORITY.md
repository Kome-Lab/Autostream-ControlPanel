# CP v3 Authority ownership and transaction boundary

This increment adds a CP-owned MariaDB registration source and connects the real
SQLAuthority, Target resolver and Policy/Trust resolver to the existing product
Service/SQLStore. It does not provision an existing environment or enable issuance.
There is no qualified Integrity/key/clock/applicability provider or startup route.

## Source survey and ownership

The survey baseline is CP 8dd8eab0bdc09ce899b74f1593623da9c3422d85.
The shared Contracts module remains pinned to 790be850e98c; Updater is read-only.

| Item | Existing source/classification before this increment | Implemented owner in this increment |
|---|---|---|
| Authenticated user Principal | Product: httpapi authenticate/session, store/auth*.go, users.id/status | Existing users row, with exact ID and active status; locked during WithCurrent |
| Service Principal | Product identity/token paths exist for v2 services; no qualified v3 service-principal mapping | Unconnected; absence from the authenticated user source refuses authorization |
| Capability | v2-only roles/user_roles/role_permissions; v3 Authority was interface/test doubles | Dedicated v3 CapabilityRecord with exact Principal/Scope/Action and separate Issue/ReadHistory |
| Realm | v3 contract only; synthetic authority mapping | Dedicated RealmRecord |
| Host | v2-only system_update_execution_hosts and service/node registration | Dedicated HostRecord, explicit CP administrative OwnerDomain; no inference from v2 ownership epochs |
| Resource | v2-only services and assignments; no v3 canonical mapping | Dedicated ResourceRecord: complete Scope and CP-owned expected reference metadata |
| Backend | v3 contract only/test doubles | Dedicated BackendRecord: registered Realm/Host/Backend |
| Candidate | Existing cover/release candidates have different v2 meanings | Dedicated CandidateRecord: exact Scope/Revision/ContentPin and approval state |
| Policy | v2-only update_agent_policies and updater projections | Dedicated PolicyRecord; never reused as target-side Policy Evidence |
| Trust | v3 contract/interface/test doubles only | Dedicated TrustRecord; never claims cryptographic or Host trust qualification |
| Membership | v3 contract/test doubles only | Dedicated MembershipRecord: Principal/Realm/OwnerDomain/revision |
| Authority Revision | Test doubles only | Transactionally incremented v3_cp_authority_epoch generation |
| Target Revision | Test doubles only | Highest immutable generation of the resolved Realm/Host/Resource/Backend/Candidate records |

The new record owner is internal/v3authorization/authority_store.go and
authority_records.go. Current records and append-only history use separate
v3_cp_authority_* InnoDB tables. The real CP users table is the identity owner;
roles, Grants, tokens, policies and old Host assignments supply no v3 authority.
A canonical request cannot register, repair or approve its own target.

No v3 owner data existed at the baseline. New records are intentionally empty on
migration. SQLAuthority.Save is a low-level trusted-owner persistence API, with
optimistic generation CAS and immutable change history. It has no HTTP/CLI caller
or automatic import. A separately authorized v3 management/provisioning workflow
must authenticate and authorize its caller before using it. The tests populate
only synthetic registrations in the explicitly disposable CI schema. Do not
describe that fixture population as real-environment integration or qualification.

## Read and resolvers

A coherent REPEATABLE READ, read-only snapshot reads the epoch before the
canonical records. Read verifies the authenticated exact user identity, dedicated
Capability, registered active Realm/Host/Resource/Backend, Host administrative
OwnerDomain membership, Candidate approval/content, CP expected reference/version,
current Policy/Trust revisions and revocation states. It returns the CP Authority
and Target generations. Unknown/missing/malformed source never becomes ALLOWED.

Target resolution compares the entire canonical Scope and rejects changes,
inactive records, unknown owners, unapproved Candidates and mismatched pins.
Policy/Trust resolution checks Scope, Action, revision, active state, revocation
and immutable generation. Stored update time and database read time are
informational acquisition metadata, never qualified Validity, TTL or D1.

The CP-owned expected reference is a registry expectation, not an observation or
commit of the remote Backend. Required ownership/fence/Boot/process/generation
fields are preserved by the Canonical Resolver but are not self-qualified here.
No remote Barrier, Drain or independent Continuity is asserted by this owner.

AuthorizeRead verifies present active identity, dedicated ReadHistory capability,
current Domain membership and the exact persisted Principal/Scope/Execution.
Evidence ID alone does not select a broader scope. History permission is separate
from Issue permission: a revoked issue capability, Candidate or current Policy
does not rewrite a historical envelope; a separate history revocation denies its
disclosure. Current applicability/Integrity checks in Service.Read still apply.

## WithCurrent and actual SQLStore transaction

The callback now receives a transaction-bound Context in addition to Decision.
This is an internal CP interface adjustment: the previous callback had no way to
pass the same transaction to the existing Store.Finish. All existing test doubles
and the interop fixture are adapted without removing their behavior assertions.
Public Request/Envelope formats, v2 APIs and default handlers are unchanged.

The READ COMMITTED transaction takes locks in this order:

1. Existing singleton v3_cp_authority_epoch row, SELECT FOR UPDATE.
2. Existing users row, SELECT FOR UPDATE.
3. Every relevant canonical owner record, SELECT FOR UPDATE.
4. Existing PENDING issuance row, through SQLStore.Finish in the SAME transaction.

Every supported v3 Save takes the same epoch row before any record/history write.
Its CAS increments the epoch, even for identical-value/ABA updates. Target
generations advance on target changes. Missing rows fail before the callback.
Read's Decision is re-derived and compared in full; changed Capability, Realm,
Host, Resource, Backend, Membership, Policy/Trust or generations skips the callback.

The callback runs synchronously once, with no automatic retry. A private,
request-local context value binds the exact database pool, transaction,
Principal, Intent and Decision to SQLStore.Finish. It rejects a different pool,
different intent, repeated finish or retained context after callback return.
There is no shared mutable transaction slot or separate autocommit issuance save.
Legacy non-transactional SQLStore callers retain their previous behavior.

SQLAuthority verifies the migration checksum/dirty journal and actual InnoDB
engines for the authority epoch/records/history, users and issuance tables. It
does not assume a server default isolation/engine. The old issuance migration
remains byte-identical; ApplyAuthorityMigrations is separate, explicit, empty of
authority seeds and absent from startup/Read/Resolve. No existing DB was migrated.

Actual conflicting user writers are store/admin.go status/password/delete,
store/auth_sessions_mariadb.go lockout, and
store/auth_user_credentials_mariadb.go credential/status updates. They acquire the
same physical users record implicitly through UPDATE/DELETE; no new v2 lock
domain or permission is introduced. Other v2 stores do not write the new tables.
All future v3 owner writers must use Save's epoch-first lock order. Unsupported
direct administrator SQL, parallel DDL and post-restore freshness are not
certified by this application transaction contract.

Callback failure rolls back its issuance update. Commit acknowledgement failure
is ErrStoreUnknown; Service.Issue preserves UNKNOWN even when a real commit
physically happened. Cancellation/timeout/deadlock never returns successful
issuance. PENDING is not automatically repaired, taken over or reissued. A future
provider must honor its callback Context; CP locking does not certify remote Host
fencing or cancellation of a provider that violates its own contract.

## Verification boundary

The required named inventory is scripts/ci/v3-authority-required-tests.txt.
CI requires each listed test to run/pass exactly once with skip/fail zero in the
dedicated package JSON run, then retains all existing tests/full suite, vet,
CGO-free tests, affected Race tests and Linux amd64/arm64 builds.

Tests use product SQLAuthority/Save/Target/Policy/Trust/Service/SQLStore with only
synthetic registrations and remaining security/time hooks. NOWAIT probes at the
actual callback/lock boundary prove that concurrent v3 changes and existing user
disable writers cannot cross it. A separate connection observes PENDING while
the callback's issued update is uncommitted. Native MariaDB lock timeout,
context cancellation and an intentionally constructed InnoDB deadlock are
exercised. A test-only connector wrapper loses acknowledgements after real
commit and rollback to verify both physically possible UNKNOWN outcomes.

The old C10 bridge remains in CI, with only the internal callback signature
adapted. Contracts/Updater sources and refs remain unchanged. This increment
does not implement other twelve Producers, real Integrity/keys, qualified
Validity/TTL/skew, current Revocation service, independent Continuity, delivery,
Registry mutation, GuardControl, service/media/provider operations or deployment.

Real evidence issuance stays disabled. CP v3-dev protection remains unconfigured
and is a merge blocker; this task never changes it or merges the PR.
Full PRESERVE; no old execution-authority reuse; manual automatic-start hold and
UNKNOWN/HELD non-success remain. Product-wide acceptance NOT_RUN; Phase 1 closure
on hold; production migration unauthorized; formal v2 return NOT_CERTIFIED.
