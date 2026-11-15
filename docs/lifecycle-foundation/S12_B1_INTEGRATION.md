# S12-B1 Product Lifecycle integration

Baseline: 2446b5b451cc3749a3245be640b5b43cf086183e (accepted B0).
This change connects optional product owners to the existing B0 Service/Port.
It does not configure a real provider or enable a production Gate.

## Configuration and ownership

A nil/unset owner Gate preserves its legacy path. Calling WithLifecycleGate,
including with nil, explicitly requires the Gate. NewRequiredGate has no working
provider. There is no successful production constructor; synthetic construction
is exclusively in product_export_test.go, unavailable to dependent product builds.
AUTOSTREAM_L2_REQUIRED rejects before DB open, migration and startup writers.
It is a requirement switch, not an enablement or Bootstrap switch. Version output
remains diagnostic. No actual configured-provider startup is qualified.

Evidence Service requires itself, its SQLAuthority and SQLStore to have the same
Gate and DB. A partial composition is rejected before Claim, including partial
composition hidden behind an otherwise legacy Service. No unconfigured provider,
inconsistent head, UNKNOWN or missing participant falls back.

SyntheticReference is only content equality under b0-synthetic-v1. It is not an
approved identity commitment or transport/signature profile. Session token/hash,
credentials and MFA material are not included in the call content. Existing
trusted Principal/ManagementActor values still originate in the authentication
boundary; existing SQL identity/Session/grant checks remain mandatory.

## Transaction and lock contract

Every durable stage calls B0 Service.Run. Observe/Reserve precede SQL locks;
Finalize and Complete occur after the actual CP Commit. There is no duplicated
Lifecycle state machine, timer release, mutation retry or repair.

B0 owns the real transaction. The private context identifies the exact SQLStore,
request, DB, actor and callback lifetime. BeginSQL returns a restricted executor,
not the raw *sql.Tx. Its local Commit stages participant completion; only B0
SQLStore.apply commits the real transaction and CP participation/Checkpoint.
An unfinished or rolled-back participant poisons the owner even if the caller
swallows the error. Retained contexts, a foreign DB and late/nested Gate admission
are rejected. Existing WithCurrent/Finish retains its private one-shot binding
and conditional PENDING -> ISSUED save in this same real transaction.

The new lock prefix is Lifecycle head before any product lock. Within it:
- C management/WithCurrent: C epoch -> User -> Session/owner -> issuance/audit.
- Operational SQL read: Operational epoch -> nonlocking canonical source reads.
- Future identity writer: Lifecycle prefix -> last-admin guard -> User ->
  Session/Assignment -> Operational trigger. Existing identity writers do not
  yet join this prefix; no new lock is taken after their User lock.
- Authority Save retains generation CAS and append-only authority history.

No Ledger call is added under SQL locks. Existing provider behavior inside
WithCurrent is not redefined as remote-effect atomicity. Native NOWAIT checks,
real owner transactions, a native Operational-epoch NOWAIT probe after the
real SQL Snapshot returns, independent-process CAS and existing regressions are
the evidence; source order alone is not execution proof.

## Connected stages

Authority readers/target/policy/history/Generation use R admission. Save uses W
and the same transaction as its CP participant. Management Apply preserves
current independent ManagementGrant authorization, identity, audit and bound
Save; Inspect uses R and preserves its existing critical-audit transaction.
Only the fixed existing management audit INSERT is allowed as an auxiliary
read-mode write; arbitrary read-mode business DML remains forbidden.

Evidence uses separate admissions for Authority Read, durable Claim, WithCurrent
plus bound Finish, explicit CLOSED persistence, Lookup and current history reads.
They bind exact existing Scope/issuance identity/content. These are stage
Attempts, not a distributed single commit or one slot held during signing.
Claim remains PENDING across a crash; a later caller cannot take it over.
Signing/clock work occurs after Claim completes and before Finish admission.
Issued/replay bytes are published only after the relevant stage completes.
Authority epoch does not advance for Evidence or reads. EVIDENCE_CHANGE permits
unchanged A/C/identity epochs and durable liability-state change/idempotent no-op;
the source still supplies the actual before/after Checkpoint.

The canonical Request/Binding decoder remains pure and unchanged. Authority
target/policy resolution has admission; structural decode/encode has no ledger I/O.
Management HTTP uses its existing Handler and the configured Service; no route,
Actor body field, CSRF or Session contract is added.

## UNKNOWN, inspection and remaining closed paths

An error after Reserve is conservatively UNKNOWN and quarantines via B0. Even
an observed rollback does not recycle the external reservation. Lost real Commit
ACK never publishes the captured product result. Restart rechecks durable state.
Gate.Inspect delegates to B0's separately authorized pure inspection; it never
completes, aborts, replays, consumes or clears quarantine.

Bootstrap immediately rejects when L2 is requested. No real Approval Origin,
Ceiling or purpose-specific Recovery is configured. Transport-denial/failure
audit is not allowed to open an untracked fallback transaction in an explicitly
gated Management owner: that path rejects and remains unconnected for its future
purpose-specific audit design. Legacy denial audit is unchanged.

User/Session/Identity Link/Credential/MFA/cleanup/background writers and all
Operational mutations remain B2/D2. No real identity state is synchronized by
B1. The identity lock-participation test is preparation, not integration of those
APIs. The accepted last-admin source/guard/trigger is unchanged; all its existing
success cases remain required. Startup without a provider is a refusal seam,
not a connected freshness check. See the separate connection matrix.

## Verification contract

New product tests are explicitly tagged lifecycle_product_integration and run
in their own mandatory step before legacy fixtures, with -p 1 and strict B0
Actions-only synthetic DSNs. They require an empty disposable product namespace,
create synthetic users/sessions/registrations, and drop only that namespace.
They refuse to overwrite even pre-existing test product tables. All ordinary
legacy full tests remain unchanged and untagged; new tests also run separately
with CGO_ENABLED=0, Race and tagged vet. No live DB or user data is read.

The new test factory and persistent provider are _test.go only. Independent
process tests construct new Services/Stores/pools against the same disposable
DB, control Reserve by pipes, and terminate actual product-owner processes at
Reserve/CP Commit/Finalize/Receipt/Complete. ACK tests distinguish committed and
rolled-back SQL state. Protocol PASS does not qualify physical durability,
independent hosts, identity rollback protection or real-provider authenticity.

The existing B0 accepted diff is still checked against its historical source.
The B1 guard applies every literal reviewed edit to complete B0 files, formats
Go with the pinned toolchain and compares the full result. Every new path must
be absent from B0. S13 allows only these reviewed edits; identity/last-admin,
existing migrations, module pins, HTTP and pure codecs remain immutable.
Operational's former standard-library/security-only rule adds exactly the Lifecycle
infrastructure path. It also checks that every production Lifecycle file imports
only the standard library, rejecting a transitive product-authority/workload/provider
bridge. Other foreign dependencies remain forbidden. Existing successful cases
are retained and no test is skipped.

Executable evidence and final commit/tree/CI URLs are recorded in PR #12 after
the final candidate succeeds. Initial/pending attempts are not claimed as PASS.
Local process startup is unavailable (0x8007000F); local status/tests are not
claimed. Remote updates use the exact expected branch head and non-force CAS.

## S12-A and handoff

Source confirms existing Marker/ManagementGrant/audit transactions and an
error-only BootstrapAuthorizer. Real independent Approval Origin, durable
product Approval Consumption and Ceiling remain incomplete. B0 synthetic
Consumption and B1 product participation do not complete S12-A.

B2/D2: close all approved identity/Role/policy/cleanup writers and outage semantics.
B3/D4: actual provider, local storage durability, authenticated transport and Root.
B4/D1: qualify an existing independent host, resource/backup/restore/admin domains.
S12-C/D and D3: independent approval roster/ceilings and purpose-specific recovery.
No AWS/new cloud/new VPS, actual enrollment, credentials, deployment or migration.

Full PRESERVE; v2 rights; Role=PRESERVE; Scope=HYBRID; Workload Identity=SEPARATE;
A/B/C distinct; no old Execution Authority; automatic-start hold;
UNKNOWN/HELD non-success; real Evidence disabled.
L2 deployment NOT_QUALIFIED; real L2 NOT_RUN; product acceptance NOT_RUN;
Phase 1 held; production migration unauthorized; formal v2 return NOT_CERTIFIED.
