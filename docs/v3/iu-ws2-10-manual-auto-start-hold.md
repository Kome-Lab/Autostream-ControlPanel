# IU-WS2-10: durable Manual Stop Hold and explicit Resume

This unit implements OP-CP-001, V3-MD-021, OP-MD-013, V3-EX-023,
V3-TR-031 and V3-TR-037 across ControlPanel, DiscordBot, Encoder-Recorder
and Worker. It does not qualify a production Ledger, Provider, host or release.

## Ownership and transaction boundaries

ControlPanel owns the Stream's Hold, its record revision, append-only history,
original Resume request, Start admission correlation and critical audit. A Human
Stop installs a private Store binding; Bot automatic Stop and cleanup do not.
The existing atomic Stop claim persists the effective held intent, history and
audit in one transaction. It commits before any target HTTP call.

Managed Start reserves its globally unique original request ID in the existing
Start claim transaction before Stream/Run mutation. Reusing an ID cannot first
change the CP state and then fail at admission save. Memory commits the equivalent
marker only with a successful claim. The marker contains only immutable public
request metadata; an unknown commit is inspected, never replayed.

Encoder and Worker close admission under the same durable, cross-process
exclusion used by their actual process/job launch. Their fsynced acknowledgement
is required before CP confirms the Hold. A failed acknowledgement leaves the
effective intent held and its outcome unknown. A known physical Stop receipt is
separate evidence. Closing admission does not itself stop existing media work.

Resume saves one immutable request with the Human, Stream, original Stream CAS,
settings revision, existing Run, expected Hold revision and required targets.
An intent claim and a final CAS surround the target closure calls. Both SQL
transactions finish before external HTTP. Current Session, CSRF and operational
permission are checked again after the calls and before the child Start claim.
The child retains the original confirmation frame and parent Attempt, and cannot
adopt settings that changed while Resume was in progress.

Bot's pure Presence lookup observes actual SDK presence and the selected
Stream/assignment frame. It does not use a cached Hold as authority. This avoids
coupling a pending Resume's Hold revision change to a different presence frame.
Automatic Start still strictly requires a known, unheld, current Hold observation.
Failure to refresh the CP source disables automatic dispatch; an old cache never
becomes a new observation. Manual Start does not clear Hold. Resume while already
running clears future automatic-start Hold without starting or stopping media.

## Existing G0 records

The fixed Contracts module is d32ea3514b1c1010391caae0348b0ff9e751f447.
`X-AutoStream-Start-Admission` carries the existing CC02 Record with the existing
ExplicitResume profile; no authority, Permit, new generation or fence schema is
created. Membership, Admission, Ownership, Fence, Boot, Process, Run and Config
are explicit existing axes. Missing facts are not filled with defaults.

The membership equality snapshot uses the existing primary assignment's
`AssignedAt` at database microsecond precision, together with its actual selected
service and resource. It is not a newly issued monotonic execution epoch. The
ownership reference also binds the registered ownership/endpoint/config revisions.
Config is the Stream's existing settings CAS, not the target's local config revision.
Run is the actual existing CP archive-run identity. The existing Start claim
allocates that identity for managed starts even without an archive upload profile.
Existing local archive completion/report guards remain; an empty profile does not
prove that every local recording duty is absent. Hold revision remains
an independent CP record CAS. No secret or hash of a private Start body enters
the shared records or target journals.

Targets verify the original saved CP record at acceptance and immediately before
their real Starter/factory/output operation. Encoder Prepare already creates the
process, so both Prepare and the later prepared Commit participate. Targets keep
durable managed enrollment, all seen request IDs and pending/known boundary
observations across restart. A used request ID is never another Start. An abandoned
cross-process exclusion is HELD; timeouts cannot break it automatically.

## Pure lookups and result knowledge

User APIs are `GET /streams/{id}/auto-start-hold`,
`POST /streams/{id}/auto-start/resume` and
`GET /streams/{id}/auto-start/resume-attempts/{request_id}`. The POST is the explicit
confirmation and requires the original request ID and all expected revisions.
Duplicate confirmation only reads that original request. The UI warns that a
satisfied start condition can start streaming and preserves an ambiguous request
for pure same-ID lookup, including after reload. Stop sends its original client ID
before dispatch; the existing Stop-outcome lookup can additionally bind that ID.
The scoped read-only `GET /streams/{id}/auto-start/hold-attempts/{request_id}`
also exposes Hold evidence without treating a force-Stop intent as a normal
physical Stop receipt.

Target identity, close and result lookup are authenticated service APIs. CP's
target inspection route reads only the exact saved original admission and checks
the current primary assignment, Hold, Stream, configuration and actual target
incarnation. Bot automatic Start has its own authenticated, pure original-request
lookup. None of these lookups retries a mutation, clears Hold or releases HELD.

The CP Hold receipt, CP Start outcome projection and actual target ServiceApply
receipts are distinct. HTTP status alone proves neither no effect nor a remote
Start. CP records an accepted Start projection only after its successful child
orchestration and known actual target apply receipts. A late pure lookup can expose
target facts while a different boundary remains UNKNOWN. Ledger stage inspection
uses the existing B0 pure Inspect and verifies the original intent/actor. An
explicitly configured B0 composition uses deterministic original stage IDs and
the existing Service.Run; normal startup supplies no synthetic Ledger constructor.

## Migration and validation

094_manual_auto_start_hold.sql is new and explicitly excluded from ordinary
startup migration enumeration. Its application is limited here to disposable
synthetic databases. It creates current Hold, history, immutable attempt,
admission and original Start request tables with RESTRICT references. Genuine new Stream constructors create
an observed clear genesis in their existing transaction when the schema exists.
Historical or missing rows are UNKNOWN, not an unheld backfill. A restored current
Hold that disagrees with its surviving history is rejected. Co-restoring every CP
record cannot establish independent rollback detection; production L2 remains
NOT_QUALIFIED.

The dedicated required inventory executes MSR-01 through MSR-15 and the ten
delayed-Start scenarios under MSR-09, MSR-11 and MSR-12. Its strict lane requires a
disposable MariaDB, actual independent CP/Bot/Encoder/Worker test processes and
barriers. Missing prerequisites, skipped roots and failures fail that lane. Existing
S13, last-admin, B0/B1/B2-B, canonical bytes, module pins and existing required CI
remain separately required. Native execution, cross-build and actual provider or
environment qualification are separate evidence; none can substitute for another.

The required migration, compatible four-component versions and host/journal
ownership must be qualified before deployment. The refusal of an unenrolled or
unknown environment is not completion evidence for the successful product paths.
This unit authorizes no deployment, real provider, production migration, Bootstrap,
Recovery, Evidence issuance, merge or release. Product acceptance remains NOT_RUN.
