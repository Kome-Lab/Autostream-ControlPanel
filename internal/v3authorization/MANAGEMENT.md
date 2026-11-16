# v3 Authority Management / Provisioning

This continuation adds an explicit management service and opt-in HTTP handler.
It does not install the management schema, create a real initial administrator,
mount new default routes, or enable evidence issuance.

## Ownership and trust origin

Authenticated identity is the existing active users row and current sessions row.
V2 roles, user_roles, role_permissions, Grants, update_agent_policies and existing
Host assignments confer no v3 management authority.

ManagementGrant is a separate v3 record. It carries an exact Realm/Host/Resource/
Backend Scope, Action, explicit management operations and allowed Subjects.
Issue and ReadHistory remain independent CapabilityRecord flags.

The initial trust origin is **UNSELECTED / NOT CONNECTED**. Existing source does
not establish an independent bootstrap authority. BootstrapAuthorizer is a port
only: no product implementation, initial password, default approval, CLI, HTTP
bootstrap route, first-login rule or v2-admin conversion is provided. Tests use
a clearly synthetic approval double. A nil or unknown approval refuses; an
uncertain approval or Commit returns UNKNOWN and quarantines that service.

Operator decisions still required before real initial registration:

1. The independently authenticated person/service and authority that approves
   the first grant, plus the exact Realm, Scope, operations and Subjects.
2. An authenticated offline provisioning ceremony or separately approved
   administrative channel; its credential custody and approval/audit mechanism.
3. Durable one-shot approval consumption, multi-process coordination and
   post-restore/Commit-unknown recovery without automatically granting again.

The internal Bootstrap method checks a known UNINITIALIZED marker, current
active user/session, generation CAS and independent approval. Grant, immutable
grant history, epoch, one-way INITIALIZED marker and audit commit atomically.
Missing/unknown/dirty marker state never means uninitialized. Initialization
cannot be repeated or reset through this API, including after revoking all
administrators. Inspect is a current authorized read, not a repair or retry.

## Permission and target boundaries

Operations are read, realm.write, host.write, resource.write, backend.write,
candidate.write, capability.write, policy.write, trust.write, membership.write,
administrator.write and subjects.delegate. There are no wildcard operations.

Normal reads/writes require a current ACTIVE exact-Scope, exact-Action grant.
An administrator title does not confer any operation, other Scope or Issue right.
Other-principal changes require explicit Subject coverage; subjects.delegate is
a distinct explicit permission. Delegation cannot add operations, Subjects or
target-provisioning permission beyond the actor's current grant.

ProvisionTargets is an explicit optional grant flag (wire provision_targets,
default false). With administrator.write it permits *grant-only* enrollment of
a precise new Scope within the same Realm and Action. One existing ACTIVE grant
must cover the entire proposed grant set; permissions are never unioned across
grants. It does not permit direct writes or reads at the new target. The new
exact grant must first be saved before target registration. Revoking one precise Scope grant does not revoke a separately held provisioning
grant: that independent authority can explicitly register a new grant after fresh
authorization and CAS. Revoke both when that is the intended policy. Cross-Realm
enrollment is refused. Unknown/malformed authority is refused. A new Realm's independent
initial trust remains an operator decision.

realm.write and host.write explicitly modify shared Realm/Host owner records;
their effects can invalidate descendant resources. They are not resource-local
flags. Backend/Resource/Candidate, Policy/Trust, Capability and Membership changes
are restricted to their declared mapping and permitted Subjects. Active children
require existing active parents (including same-batch parents). Candidate APPROVED
requires the authenticated approver. Registration never infers remote Host,
Backend, qualified Trust, Fence, Drain, Boot or clock evidence.

## Transaction and uncertainty

All supported owner writes and conditional issuance use the existing InnoDB epoch
row first. Management uses READ COMMITTED and locks epoch, users identity, sessions,
bootstrap/grants and relevant owner records. Fresh authorization, expected global
generation and same-Scope operation/Subject checks precede the bound Save.

A private one-shot database/expected-generation binding lets SQLAuthority.Save use
the *same sql.Tx* without a nested connection/transaction. Save's sorted records,
history, epoch CAS, identical-value generation advancement and ABA prevention
remain intact. Management grants/history and mandatory per-target audit use this
same transaction. Audit failure rolls everything back; no partial batch succeeds.
The existing WithCurrent -> SQLStore.Finish transaction path is unchanged.

A context-aware per-service gate covers Bootstrap, Apply and Inspect through the
Commit acknowledgement and uncertainty publication. This closes the window where
InnoDB releases the epoch lock before the client's Commit result is known.
Cancellation/timeout/deadlock/lock timeout are not successful outcomes. Mutations
queued behind unknown Commit are refused UNKNOWN; Inspect may observe the current
authorized state but never clears uncertainty. There is no automatic retry,
regrant, takeover or repair. Process restart, multiple service instances and
post-restore freshness require the independent durable approval/recovery design
above; this process-local gate does not certify them.

Sessions are checked again immediately before commit. Existing session expiry
uses ordinary CP wall time for authentication; it is not qualified v3 evidence
time. Native user/session mutations conflict on the physical rows. CP locks do not
fence remote Host work.

## Explicit migration and audit

New management_migrations/001_management.sql has an independent checksum/dirty
journal and explicit ApplyManagementMigrations entry point. Existing migrations
are unchanged. Startup, constructors, reads and handlers never apply it.
No existing environment's database is migrated by this change.

The migration creates empty grant/audit/history storage and a known UNINITIALIZED
marker only. It seeds no user, grant, owner registration, Policy or Trust.
Partial/dirty schema is refused rather than repaired. Physical InnoDB engines
and required triggers are checked. Reapplying a completed migration checks the
marker's known state and generation without recreating a missing marker.

Audit records include random Event/Audit IDs, Correlation ID, actor, fixed
operation/target type, validated target ID, Scope/Action, before/after revision,
result and fixed reason Code. Parsed refusals preserve their intended operation
and target; transport/undecodable refusals use fixed generic metadata and a
server-generated correlation. No session token/hash, credential, key, SQL error
or full payload is stored. UTC_TIMESTAMP(6) is explicitly labelled
UNQUALIFIED_DB_OBSERVATION, never Qualified Time.

Success audit and changes are atomic. An UNKNOWN result additionally attempts a
fixed-code uncertainty-observation audit after rollback/Commit; it is never proof
that an authority change committed and never clears quarantine. Unavailable audit
storage keeps the request unsuccessful. Denial audit is an audit-only transaction;
if audit cannot be persisted the request remains refused. Append-only UPDATE/
DELETE triggers protect audit and grant history against ordinary DML, and the
bootstrap marker is one-way. A DB administrator able to alter/drop tables or
triggers remains outside this tamper-resistance claim.

## HTTP API

Server.V3AuthorityManagementHandler is an opt-in composition point and is not
registered on existing default/v2 routes. All routes require current existing
session authentication, active user, CSRF and current independent v3 grants.

All methods are POST under /v3/authority-management/:

| Route | Typed operation |
| --- | --- |
| realms | RealmRegistration |
| hosts | HostRegistration |
| resources | ResourceRegistration |
| backends | BackendRegistration |
| candidates | CandidateApproval |
| capabilities | V3Capability |
| policies | Policy ScopeRule |
| trust | Trust ScopeRule |
| memberships | DomainMembership |
| administrators | ManagementGrant |
| inspect | Current authorized records/grants and generation |

The fixed route chooses the record kind. There is no arbitrary union/SQL endpoint.
The bounded 64 KiB decoder rejects extra/missing/null/duplicate/incorrect-case
fields, invalid UTF-8, trailing JSON, invalid integers and excessive nesting or
batch size. Metadata is event_id, correlation_id, expected_revision, scope and
action; typed records are in records. Actor/session come only from trusted
authentication context. Inspection does not issue/reseal/extend evidence.

A previously successful actor/Event ID is a conflict, not an automatic replay.
A new explicit request must use the current generation. Unknown outcomes never
produce an automatic retry. Responses contain fixed public codes, no raw errors,
and no-store headers. Unknown/uninitialized/unavailable state refuses.

## Verification and remaining qualification

The management suite exercises real SQLAuthority/Save/management audit paths
against the existing disposable MariaDB CI schema only. HTTP unit tests use
transport/service doubles; tagged HTTP-to-SQL tests use the real management
service and a synthetic bootstrap trust anchor. Commit-ACK connectors inject
loss after a real commit or rollback. No double is a product default.

The required CI inventory preserves the earlier Authority cases and requires
new management roots to run/pass without skips. It also runs full Go tests/vet/
build, CGO-free checks, affected Race, Linux amd64/arm64 cross-builds, source
boundary and module/source invariance. C10-to-CP interop uses the fixed read-only
Updater cdc106ab795623e41e2fe42a73c598c782e5c8de; unrelated repositories are not
modified. Prior CP case counts are not new management evidence.

Real bootstrap/HTTP mounting/migration/operator provisioning remain unconnected.
Real Integrity, keys/signatures, qualified clock/TTL/skew, current Revocation,
independent Continuity and delivery remain unconnected. No environment received
records or evidence; no Host/media/provider/service action was taken.

Real evidence issuance disabled. Full PRESERVE, old Execution Authority not
reused, automatic-start hold and UNKNOWN/HELD non-success maintained. Product-wide
acceptance NOT_RUN; Phase 1 closure on hold; production migration unauthorized;
formal v2 return NOT_CERTIFIED.
