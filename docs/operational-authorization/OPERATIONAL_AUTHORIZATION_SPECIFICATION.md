# S13 Operational Authorization Specification

Date: 2026-11-14 JST. Decisions: **Role=PRESERVE / Scope=HYBRID / Workload Identity=SEPARATE**.

## Ownership and composition

A (Human Operational Authorization), B (Evidence Authorization), and C (Trust & Authority Management) are separate authorities. This package owns A only. It does not import the v3 Evidence/Management services, issue Evidence, approve Bootstrap, manage an independent ledger, or possess signing/Recovery authority.

ControlPanel baseline is `d42a0fb2212c2378b822effc9b8dfd7031d457cb`. v2 inventory baseline is `93181192d8f036bae40b490957489c9ee8290769`. Contracts `45aaa9932bcd92c728c86c95349853724fe1accc` and Updater `cdc106ab795623e41e2fe42a73c598c782e5c8de` remain read-only.

`WithOperationalAuthorization(source)` explicitly composes the Operational Service into the existing **permission-bearing** Panel routes, including the legacy Session-wrapper video-cover route whose Handler selects show/hide. Existing routes, methods, request/response formats, CSRF checks and Handler/Store policies remain. Missing/unhealthy explicitly configured sources deny; they do not silently use the old permission list. No migration runs during Server construction.

Session-only APIs retain their existing identity boundary. In particular, B's existing `requirePermission("")` use remains identity/CSRF protection only. C's independent ManagementGrant checks are unchanged. Human role changes neither create nor revoke B/C grants.

## Principal, Role, Operation and Grant

| Concept | Source and meaning |
|---|---|
| Human Principal | Exact existing User ID; neither username nor email is an identity key |
| Role | Exact existing Role ID/name and the actual operations in its Permission rows |
| Assignment | Existing User-to-Role IDs, resolved from current source |
| Operation | Closed catalog of all 99 existing DefaultPermissions, with their complete route/Handler behavior |
| Grant | Principal + assigned Role + actual Operation + explicit Scope/constraints + current Revision/state |
| Effective permissions | Union of matching grants from all currently assigned Roles |
| Revision | Current authorization generation; never a timestamp or qualified clock |
| Revocation | Removal/change of Role, assignment, operation, or active identity; no authorization cache |

This is an expansion of Role contents and assignments, not authorization by a copied Role name. `super_admin` does not receive an implicit catalog bypass. A reduced existing `super_admin` Permission set remains reduced. Stable grant identity does not make a later re-grant the same revision.

The operational namespace deliberately preserves the existing 99 Permission strings. A single operation may cover several existing HTTP actions (including Update/Port reconfiguration); the complete action set and secondary checks are retained in the Route Inventory. Earlier proposed SPLIT mappings were design candidates for finer future contracts, not implemented B/C privileges. No proposed split is allowed to drop an existing operation.

Unknown stored operations are preserved in source and refused by the closed Operational evaluator; they are never guessed, discarded from the database, or interpreted as a wildcard.

## Hybrid Scope and secondary policy

A grant matches an exact typed Scope/ID, or an explicit Global Scope with an empty ID. An empty scoped ID, wildcard, prefix match, inferred ownership, or automatic Realm-to-Host hierarchy is not a substitute for explicit scope.

Global is the current CP/Store instance boundary; it is not authority over another CP database. Existing v2 Role Permission rows have no per-Role target-scope column. Their logical grants therefore remain Global. Existing Handler/Store ownership, primary/standby assignment, stream state, media ownership, secret masking, host/job ownership, and revision/grant checks continue as the second policy stage. A Global entry permission does not remove those checks.

The new evaluator also supports exact scoped grants. The HTTP adapter obtains a scope only from a known registered single-target path parameter. Stream, Profile-kind/ID, Integration-kind/ID, Secret name, and explicit Host ID are separate namespaces. Collection requests, unclassified targets and body-selected targets require Global grants at entry. Query parameters, request bodies and scope headers cannot widen the boundary. Profile and Integration kind are included in IDs to prevent cross-kind ID collisions. HEAD uses the registered GET pattern.

Not every operation requires every Scope type. Scoped capability persistence and management must not become a second unrestricted Role authority or reset a scoped restriction to Global when an old Role is edited.

## Evaluation and freshness

The identity boundary checks Session existence/expiry, User and CSRF. Operational evaluation then validates current Principal, assigned Role identities, Grant integrity/state, closed Operation, exact Scope, Subject constraints, Delegation constraints, additional policy, and current Revision. A revision change observed between snapshot and final revision check refuses this evaluation rather than granting from a stale snapshot.

The same validated snapshot supplies **both** effective operations and Role names used by existing special conditions. Stale `super_admin` identity from an earlier read cannot retain the Role-assignment exception after the Operational snapshot has removed it.

Current() retains DelegationNone operations for ordinary use. Subject-restricted grants require a matching trusted context and are not flattened into unrestricted permissions. HTTP Role and User mutation hooks separately validate typed delegation against the complete Global/Subset Role effect and authenticated entry revision. No independent grants may be combined to manufacture one broader scope/constraint.

Revision freshness is an authorization-entry guarantee. The existing v2 Handlers are not rewritten into the B/C issuance transaction; in-flight remote side effects and Session revocation after entry are not certified as atomic with a later response. Revocations affect the next fresh decision. DB-local generations are not independent restore-continuity evidence.

## Delegation and super_admin compatibility

The unchanged Handler/Store protections remain binding:

- ordinary Role assignment cannot exceed the actor's current Permission set;
- only a permitted `super_admin` actor can assign the `super_admin` Role;
- the existing `super_admin` assignment exception remains distinct from Role create/update subset validation;
- actor self Role reassignment is refused;
- the `super_admin` Role body cannot be modified/deleted through the API;
- last active `super_admin` protection, password/status special conditions, and deletion subset checks remain;
- permissions such as `roles.assign`, `users.update`, `users.reset_password` remain required at their original entry points.

The evaluator's delegation checks are connected before save in Role create/update, User create/update Role assignments, and the common helper used by OAuth Provider default Role create/update. Ordinary actors must satisfy operation, Scope and Subject containment. Even the assignment exception remains inside the Scope/Subject/delegation boundary of the entry roles.assign Grant. It preserves assignment of unheld known operations but cannot erase explicit constraints or revocation on represented operations. The checked target Principal is bound to the loaded target User; new Users and provider policies have an unknown future Principal. Body identity/scope claims are never authoritative.

The authenticated entry revision is passed as ExpectedRevision to subsequent typed delegation checks. See S13_HTTP_DELEGATION_VERIFICATION.md for executed red/green HTTP results, preserved legacy exceptions, mandatory tests and precise limits.

The legacy assignment exception is confined to known operational permissions; it cannot grant Bootstrap approval, Trust Root changes, ledger administration, Recovery quorum, signing keys or independent approver roster management.

MariaDB last-administrator management writes now recheck canonical active membership and commit inside a shared InnoDB guard transaction. The guard precedes User, Session and assignment locks; existing generation/history triggers remain inside the mutation transaction. The competing authentication lockout writer joins the same lock order without changing its policy. See LAST_ADMIN_CONCURRENCY_VERIFICATION.md for source-derived routes, executed red/green proof and limits. No independent Recovery system or full account-availability guarantee is introduced.

## Store and migration boundary

Legacy Role/Permission/assignment rows remain the canonical operational source. Grants are derived from the actual current contents. There is no Role-name template, default all-admin conversion, imported Execution Grant, or second cached permission authority.

Persistent administration of newly authored scoped grants is not connected in the legacy SQL projection. The evaluator and HTTP adapter support exact scoped grants through the typed Store seam, tested with isolated fixtures; v2 conversion remains Global plus unchanged Handler policy. No scoped grant CRUD is silently added to the legacy Role API.

A separate, explicit Operational migration journal provides checksum/dirty-state detection and a DB-local generation/history boundary for legacy authorization mutations. Existing migrations are immutable. The new migration does not connect to deployed DBs, copy credentials, migrate real users, delete historical data, or initialize B/C authority. It must be tested for idempotence, partial DDL failure, dirty restart, transactional rollback and history invariants on an isolated CI database.

SQL authorization source must refuse missing/dirty/partial schema or inconsistent source. Current snapshot reads and revision validation cover Role contents, assignments and User authorization state. Append-only authorization history records metadata about authorization changes; credentials/passwords/tokens are excluded.

The in-memory source is for deterministic integration fixtures and local development; SQL/MariaDB remains production storage. Any in-memory generation observation limits must be reported separately from transactional SQL guarantees.

## Workload boundary

Human management requests (creating an Update, registering a Node, changing a policy, requesting a Token rotation) are not the resulting machine execution identity. Service Token Scope, Host/Node registration credentials, ownership epochs, Mutation Grant issue/consume, Host Self Update Grant, runtime Token rotation and Local Executor policy remain in their existing separate verification paths.

A Session holding all 99 Human operations is not a Service Token. No former Grant/Lease/Token becomes a Human Role. This implementation neither obtains real credentials nor connects to Hosts/Agents.

## Verification and acceptance

See `AUTHORITY_MIGRATION_ACCEPTANCE.md`, required test manifest `scripts/ci/s13-required-tests.txt`, and `v3-operational-authorization-ci.yml`. New required roots must execute exactly once, pass exactly once and never skip. Full Go tests, vet, build, Race, CGO-free checks, Linux amd64/arm64 cross-build, source boundary and unchanged module files are required on the final CI tree. B/C existing required CI is retained independently.

Actual deployment, production data migration, fleet/provider behavior, real Custom Role contents, qualified time, independent continuity, real keys and Evidence issuance are unperformed.

## Preserved global gates

Full PRESERVE. Old Execution Authority is not reused. Automatic-start hold stays in place. UNKNOWN/HELD never mean success. Real Evidence issuance remains disabled. Product-wide acceptance `NOT_RUN`; Phase 1 closure held; production migration unauthorized; formal v2 return `NOT_CERTIFIED`. The PR remains Open/unmerged.
