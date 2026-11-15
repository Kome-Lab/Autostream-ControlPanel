# S12-B0 Lifecycle Foundation

This is a provider-independent, **unmounted** foundation. It does not enable a
product Gate, enroll a deployment, connect an independent ledger, alter identity
state, issue Evidence or execute Bootstrap/Recovery. Existing v2, S13, delegation,
last-admin, Evidence/Authority and Canonical product source remain byte-identical
to ee26856bbf7e808424a658345dd5f362d5500bb6.

## Scope and trust

The Port, closed types, opt-in SQL CP participant and Service own ordering and
uncertainty. The only accepted profile is explicitly named b0-synthetic-v1.
Commitments and Root references are opaque synthetic references; they are not an
adopted cryptographic profile or authenticity proof. An unknown profile, missing
dependency (including typed nil), missing deployment, unknown state or changed
schema refuses admission. Constructors perform no I/O or migration.

Authorizer is a trusted composition dependency. Resolve supplies the Human actor;
Request contains no Actor or already-authorized Scope. Intent names desired
operation, opaque synthetic operation-content commitment, exact target Scope and Subject, and ValidateTx rechecks the configured
local source in the CP transaction. It must not perform external I/O. The B0
fixture source is synthetic and is not a product authorization adapter.

Transaction uses an unexported context key, exact Store/request ownership and
callback lifetime. It exposes no Commit/Rollback. Borrowed owners, wrong requests,
unbound contexts, expired callbacks and read-mode business writes are rejected.
SQL itself is trusted owner code; this API is not a sandbox for arbitrary SQL.
Existing bound WithCurrent/Finish and Bootstrap transactions are not connected
in B0; B1 must implement their reviewed transaction ownership and lock prefixes.

## State and persistence

ObserveLatest -> Reserve -> CP transaction -> Finalize -> CP Receipt transaction
-> Complete -> result publication.

Lifecycle sequence advances on reservation, while phase transitions retain that
sequence. CP DB incarnation, ledger/store identity and A/C/identity revisions are
different fields. A read also obtains an admission and records its participation
and Receipt, but cannot advance authority or write business state.

CP target changes, Consumption, Checkpoint, participant record and History use
one transaction. Callback failure rolls all of them back. The external Approval
consumption is a different boundary and is not silently restored by CP rollback.
The slot remains held after Finalize. SavedReceipt has private confirmation
minted only after the CP Receipt commit acknowledgement; a serialized Finalize
receipt cannot manufacture that confirmation.

Any ambiguous mutation returns UNKNOWN with its Attempt/stage, without automatic
retry. A bounded, cancellation-independent safety insert records CP quarantine;
it performs no business change, Finalize or slot release. The uncertainty
journal is immutable. Restart cannot clear it, and Inspect cannot repair it.
Failure to persist the safety journal is itself returned as UNKNOWN, never
success. Both-store unavailability cannot be made into a durable success proof.

Inspect is separately authorized and reads CP and ledger stages without changing
either. It distinguishes actual persisted stages from the caller's lost
acknowledgement. It never repeats an effect, consumes Approval, clears quarantine,
completes a reservation or issues authority. Abort/Reconcile have contract types
only; qualified recovery is a later stage.

## Explicit migration

ApplyMigrations must be invoked explicitly against the intended empty B0 schema.
It is not imported or called by any product startup. The new SQL file is under
this package, outside the standard embedded migration namespace.

The journal records checksum, dirty state, generation and sealed table/trigger
definition. Installation uses a database-scoped advisory lock, publishes dirty
before MariaDB DDL, then CAS-publishes clean generation 1 and an immutable history
row in one transaction. Interrupted DDL remains dirty; retries refuse repair.
Replay checks checksum, generation, complete structural identity and history.
A request never recreates missing schema or deployment state.

History and quarantine have update/delete rejection triggers. This is an
append-only API/SQL boundary, not WORM protection against database administrators
or whole-host restore. The synthetic fixture alone seeds deployment state.
Existing migration files and module pins are unchanged.

## Synthetic verification

The SQL ledger implementation is exclusively in synthetic_test.go. CP and ledger
use distinct transaction boundaries and tables in an explicitly disposable
Actions-only schema. This does not prove independent infrastructure or durable
physical storage. No production SQLite adapter or new driver is added.

The fixture accepts only existing CI loopback schemas/users and their exact
synthetic DSNs, with GITHUB_ACTIONS=true. It refuses arbitrary DSNs. It does not
enumerate or modify real User/Role/Session or existing product tables.

Required roots cover W/R, source/Scope/Subject/Workload rejection, permanent
Approval consumption, private participation, slot/Receipt ordering, rollback,
actual SQL connection termination, cancellation, each commit boundary's lost ACK
before rollback or after real commit, schema corruption/partial DDL/replay,
stale responses, partial restore, independent-process CAS and process termination
at each persisted stage.

The common-restore fixture restores all mutable synthetic CP/ledger roots,
including consumption/dedupe/history, and demonstrates that old matching state
can again authorize an operation. This is deliberately recorded as the
**non-detection limit** when every comparison root is rolled back, not a PASS for
L2 independence. It is a controlled SQL restore model, not a VPS snapshot test.

The two independent child processes have separate Services, SQLStores and pools.
A stdin/stdout barrier pauses both after observation and before reservation.
Exactly one native database CAS wins. Separate real child processes are killed
after Reserve/CP Commit/Finalize/Receipt/Complete and restarted; the original
effect is never replayed.

## S12-A and later work

Current ManagementService has Marker/ManagementGrant/audit transactions and an
error-only, unconfigured BootstrapAuthorizer. It has no integrated independent
Approval origin, durable Approval Consumption/Ceiling or Lifecycle ownership.
B0 defines and exercises participation/consumption with synthetic data; it does
not complete S12-A or provide a usable Bootstrap authority.

B1: connect all inventoried entries and preserve C epoch/User/Session,
WithCurrent/Finish and guard ordering. B2: approved identity/role coverage and
availability semantics. B3: qualified actual ledger adapter and transport Root.
B4: qualify an existing independent host, resources, restore and management
domains. AWS/new contracts/new VPS are excluded. D1-D4 remain pending.

## Source and CI

The original S13 boundary is extended only with exact new file paths and a
literal reviewed workflow transformation. b0-source-boundary.py verifies every
pre-existing product file remains identical to the accepted baseline. Existing
CI steps are preserved; only a B0 step and CP checkout history depth are added.
The new tests have their own required inventory, CGO-free/Race/vet and Linux
amd64/arm64 package builds. Existing full tests, vet, build, cross-build, module
and S13 source checks still run.

Local command execution failed with CreateProcessSecurityEnvironment
0x8007000F. No local working tree was reset, cleaned, overwritten or represented
as clean. Changes are constructed from the leased remote baseline; executable
evidence must come from the final CI commit/tree, not local test claims.

Final HEAD, actual required-root/subcase counts and CI URLs are recorded in PR
#12 after final verification. Until those checks finish they are NOT_RUN/pending,
not inferred PASS.

Full PRESERVE; existing v2 effective rights; Role=PRESERVE, Scope=HYBRID,
Workload Identity=SEPARATE; no old Execution Authority reuse; automatic-start
hold; UNKNOWN/HELD non-success; real Evidence disabled.
L2 deployment NOT_QUALIFIED; real L2 verification NOT_RUN; product acceptance
NOT_RUN; Phase 1 held; production migration unauthorized; formal v2 return
NOT_CERTIFIED.
