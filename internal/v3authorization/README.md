# CP Authorization producer boundary

This package implements CP Authorization only.
The CP-owned v3 SQL Authority/Target/Policy source and its explicit provisioning
boundary are documented in [AUTHORITY.md](AUTHORITY.md). It does not implement the other
twelve Guard Producers, v2 Grant conversion, target Mutation, a D1 clock, Agent
delivery, independent Continuity, service application or deployment.

`Service.Issue` resolves a complete immutable semantic intent, checks its
requesting principal against the server-authenticated user, authorizes its exact
target/action/content, durably claims its issuance liability, obtains qualified
Validity and authenticated canonical bytes, and conditionally saves the result
under `Authority.WithCurrent`. That authority must serialize relevant CP-owned
capability, target, Policy and Trust changes through the save. Neither a stale
local copy nor a CP transaction establishes target-side Barrier completion.

`Record.Immutable` includes principal, all semantic intent, original Binding,
Scope/Execution, BackendReadFrame, configured Issuer and configured Audience.
Evidence IDs, issued/expiry times, Integrity and later observations do not rewrite
it. Unique realm/request, realm/execution-key and realm/operation/attempt/step
constraints reject aliases and content conflicts. A pending liability is not
automatically taken over after a crash or unknown acknowledgement. CLOSED is an
unissued terminal HTTP outcome; it is not signed negative Evidence or a Backend
Negative Receipt. These implementation choices are proposals, not a new adoption
decision for all AutoStream operations.

`Service.Read` requires current query authority, exact Scope/Execution and optional
Evidence ID. It verifies saved bytes and reports current applicability separately
from historical issuance. It never issues, renews, consumes, repairs or restarts.
`Result.AuthorizesExecution` is always false. Missing/restored records are UNKNOWN,
not proof of non-issuance or reusable authority.

`SQLStore` borrows the existing CP-owned MariaDB pool; it never opens/closes it.
The explicit `ApplyMigrations` operation owns a separate version/checksum/dirty
journal. Its migration is deliberately outside the existing v2 auto-migration
directory. No normal Open/Read migrates or repairs. Migration/test execution is
authorized only against the disposable synthetic CI schema in this change.

`httpapi.Server.V3AuthorizationHandler` is an opt-in composition point. Existing
session/CSRF checks establish only the active user's identity, never v3 permission.
The default server does not register it. Exact canonical bytes bypass the legacy
JSON redactor because redacting `cp_authorization` would invalidate Integrity.

## Production connection status

| Dependency | Existing source / this change | Production status |
|---|---|---|
| Session principal + CSRF | Existing CP `authenticate` / `requirePermission` | Reused as identity only; v3 additionally requires active status |
| v2 permissions / Grants | Existing role and updater stores | Not v3 authority; never converted |
| v3 capability + canonical Realm/Host/Resource/Backend | Mandatory `Authority` dependency | Product SQLAuthority + dedicated v3 records implemented; management/provisioning and real qualification remain unavailable |
| Policy / Trust current revisions | Mandatory Authority snapshot + synchronous `WithCurrent` | Dedicated v3 Policy/Trust owner; epoch-first InnoDB locks and same-transaction SQLStore save; no v2 conversion |
| Canonical P-A request / Binding | Contracts shared Schema1/Binding1 pinned to790be850e98c; product CanonicalBindingResolver | Implemented; exact complete Intent mapping; no business/identity authority supplied |
| Canonical Envelope | Fixed Contracts v3evidence pseudo-version | Real Codec used; structural validation is not authenticity |
| Integrity / keys | Mandatory `IntegrityProvider` | Algorithm, key profile/storage/rotation not selected or connected |
| Qualified Validity clock | Mandatory `ValidityProvider` | Time base, TTL/skew and cross-Boot qualification not selected/connected |
| Current validity / effective revocation | Mandatory `ApplicabilityReader` | Unconnected; issuance requires CURRENT, reads label UNKNOWN honestly |
| Issuance liability persistence | Real SQLStore + separate explicit migration | Synthetic MariaDB tested; no existing DB migrated |
| Drain / Boot / Process / Continuity | Owned outside CP Authorization | Not self-attested by this Producer |

No nil dependency succeeds. No test signer, fixture resolver or clock is a product
default. Until the unconnected dependencies qualify, real issuance remains
unavailable even for a logged-in v2 administrator. The product CanonicalBindingResolver now delegates strict decoding, canonical
bytes and exact Binding verification to the reviewed shared module. It does not
copy C10's internal implementation or introduce a CP format. Contracts and Updater
remain read-only in this task.

## Evidence and limitations

Tests call the actual product CanonicalBindingResolver, Service, Handler and
SQLStore. Existing tests retain synthetic Authority collaborators. New SQLAuthority tests
use real owner/resolver/transaction code; Integrity, time and revocation remain
explicit synthetic collaborators. Historical tests with fixture resolvers remain synthetic; the new
Resolver tests use the reviewed shared Request contract.
The separate interop workflow exports an actual C10 request from a pinned disposable
Updater checkout, runs the actual CP issue/store/read Service, and imports its exact
bytes into the real C10 Codec/source/authenticator/Preflight boundary. Its other
twelve Producers, clocks, authentication and Continuity remain doubles. The
temporary Updater test is never pushed to that repository.

Schema checksum and a valid saved Envelope do not prove post-restore freshness.
Independent continuity/authority freshness must qualify restored state before
real issuance. CP save acknowledgement, target verification and Backend commit
remain distinct. No current claim is made about real keys/clocks, power-loss
durability, all-Actor exclusion, arm64 native execution, production eligibility,
Phase 1 closure or formal v2 return. Product-wide acceptance remains NOT_RUN.

Before real issuance, authorize and connect v3 ownership management/provisioning
(no production data is seeded here), then select and connect Integrity, qualified
Validity and revocation under the adopted Authority/Barrier model. Compare a
CP-owned injected signer with an external signing service by key custody, failure
behavior, rotation/verification availability and operational cost; neither is
selected here. Numeric TTL/skew require the chosen time authority and measured
transport/operation limits. Do not use Context deadlines as evidence Validity or D1.

## Product canonical Resolver connection

Compose explicitly with `Dependencies{Resolver: NewCanonicalBindingResolver(), ...}`.
NewService, its missing-dependency refusal and the default unregistered Handler
remain unchanged. No successful test dependency is a product default. Constructing
the Resolver alone cannot enable issuance.

The fixed dependency is `v0.0.0-20261113091349-790be850e98c`, reviewed Contracts
commit `790be850e98c06119330907a8fc2ae091e2bdc85`. CI checks the module version,
absence of a replacement for that module, and source blob identities. Existing
legacy Contracts replacement and all unrelated dependency versions are retained.

Resolve snapshots input bytes, checks Context cancellation, calls the shared
DecodeRequest/ValidateRequest/CanonicalBytes/ComputeBinding/VerifyBinding, copies
every semantic field and checks cancellation again. No CP JSON Marshal/hash or
alternate normalization computes Binding. The existing private `immutable` JSON
record is only CP issuance-liability equality; it never creates Request.Binding.
Decode/schema/shape errors map to safe INVALID, Binding mismatch to CONFLICT.
Resolver success is not authentication, permission, freshness, Consume or D1.

| Intent | Shared Command / Request |
|---|---|
| Scope / Execution / Action | Command.Scope / Execution / Operation |
| RequestingPrincipal / AuthorizationIssuer | Same-named Command fields |
| CandidateRevision / CandidateContentPin | Same-named Command fields |
| ExpectedRevision / ExpectedStateVersion | Command.Expected.Revision / StateVersion |
| Required.Policy / Trust / Membership | Command.RequiredGuard.Policy / Trust / Membership |
| Required.OwnershipEpoch / FenceEpoch | Same-named RequiredGuard fields |
| Required.Generation / BootID / ProcessIncarnation | Same-named RequiredGuard fields |
| TimeBase / DeadlineTick / OriginalBudget | Command.Timing.TimeBase / DeadlineTick; Command.OriginalBudget |
| Binding | Request.Binding; excluded from its own preimage |

Contracts has `Expected.Revision`, not another nested `Expected.Reference` field.
Evidence Protocol3, Canonical Request1 and Binding1 retain distinct meanings.
Binding remains exact C10-compatible PascalCase Command JSON, not a hash. Full
Scope/Execution, Candidate content pin, uint64 Expected version, all eight Guard
fields, Principal/Issuer, time base/deadline and original Budget are retained.
Frame and later proof/Receipt metadata remain outside canonical Binding.

Service.Issue still compares the resolved RequestingPrincipal with the active
server-authenticated Principal and configured Issuer, then calls Authority.Read
and Authority.WithCurrent. A different valid request with a recomputed binding
must still receive current business/target authorization; equality cannot supply
it. The existing pending claim, conditional save, UNKNOWN, replay, exact-byte
read and current read-permission behavior is unchanged. Tests exercise these
through actual Service/SQLStore/HTTP paths with only the remaining unimplemented
authority/security/time dependencies doubled.

The interop bridge now uses this product Resolver, not its old synthetic Request
shapes/resolver. Its disposable Updater checkout temporarily selects the SAME
reviewed Contracts version, compares actual C10 canonical bytes/binding and imports
the actual CP Service's saved/read Envelope. Neither consumer source changes nor
that temporary dependency edit are pushed to Updater/Contracts. The real CP-owned SQL Authority is now implemented separately (AUTHORITY.md).
Its management/provisioning, Integrity/keys, qualified time, current revocation and
independent Continuity remain unavailable; real issuance remains disabled. No startup registration,
Migration, GuardControl, Reference mutation or service operation is added.
