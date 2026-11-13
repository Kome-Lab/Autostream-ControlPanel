# CP Authorization producer boundary

This package implements CP Authorization only. It does not implement the other
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
| v3 capability + canonical Realm/Host/Resource/Backend | Mandatory `Authority` dependency | No qualified owner/production implementation connected |
| Policy / Trust current revisions | Mandatory Authority snapshot + synchronous `WithCurrent` | v2 updater policy is not reused; actual owner/serializer unconnected |
| Canonical P-A request / Binding | C10 internal v1 `CanonicalBinding`; Contracts transports opaque Binding | Public shared Command/Binding contract missing; production Resolver unconnected |
| Canonical Envelope | Fixed Contracts v3evidence pseudo-version | Real Codec used; structural validation is not authenticity |
| Integrity / keys | Mandatory `IntegrityProvider` | Algorithm, key profile/storage/rotation not selected or connected |
| Qualified Validity clock | Mandatory `ValidityProvider` | Time base, TTL/skew and cross-Boot qualification not selected/connected |
| Current validity / effective revocation | Mandatory `ApplicabilityReader` | Unconnected; issuance requires CURRENT, reads label UNKNOWN honestly |
| Issuance liability persistence | Real SQLStore + separate explicit migration | Synthetic MariaDB tested; no existing DB migrated |
| Drain / Boot / Process / Continuity | Owned outside CP Authorization | Not self-attested by this Producer |

No nil dependency succeeds. No test signer, fixture resolver or clock is a product
default. Until the unconnected dependencies qualify, real issuance remains
unavailable even for a logged-in v2 administrator. A shared public binding contract
must preserve C10's exact rules; do not copy its internal implementation or add an
independent CP format. Contracts and Updater are read-only in this task.

## Evidence and limitations

Tests call the actual Service, Handler and SQLStore. Authority, canonical binding
qualification, Integrity, time and revocation are explicit synthetic collaborators.
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

Before real issuance, resolve the public Binding profile and CP-owned current
capability/target/revision source, then select and connect Integrity, qualified
Validity and revocation under the adopted Authority/Barrier model. Compare a
CP-owned injected signer with an external signing service by key custody, failure
behavior, rotation/verification availability and operational cost; neither is
selected here. Numeric TTL/skew require the chosen time authority and measured
transport/operation limits. Do not use Context deadlines as evidence Validity or D1.
