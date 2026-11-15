# S12-B2-B Identity transaction integration

## Scope and qualification

Only ControlPanel's PR #12 branch `codex/v3-cp-authorization-20261113`, base `v3-dev`, is changed. Baseline is `60137bb7f6bbcf830485ead3f5850809b767c5a4`. Contracts/Updater, dependency pins, existing migrations and pure canonical codecs are preserved. No real Provider, secret, identity, DB, deployment, merge or media operation is used.

Implementation and synthetic proof are distinct. The final CI evidence records exact checkout/tree, root/subcase counts and Skip/Fail. Local Go execution is unavailable; local source snapshots are not a git checkout/clean certification. Changes use the connected GitHub object API followed by non-force compare-and-swap of the authorized branch head. A new local workspace held no prior repository or user modifications; earlier workspaces were read without changes.

## Six slices

1. Exact operation/class contracts distinguish Human, pending onboarding, preauth proof and purpose-limited System cleanup. Trusted Authorizer composition resolves target, Flow, already-verified ProofReference, coverage and expected Identity revision. HTTP input cannot mint the private request context. Unsupported operation/class/profile refuses. Non-Human Identity cannot enter Authority/Management/Bootstrap/Evidence/Workload. System cleanup has contract coverage only, no new scheduler or live cleanup writer.
2. Auth Store methods share B0's existing same-DB/private-context/owner/callback SQL participant. Local Commit stages participation; only B0 performs real Commit. Foreign stores/pools, retained handles and swallowed rollback fail. Update/readback uses the same connection before participant Commit. Secret-bearing production capture is absent.
3. User/Role/Password/Session and bookkeeping have opt-in Store seams. Identity history, actual post-DML A revision and Identity revision CAS share the business transaction. Role assignment nil/empty, existing guard SQL and permission catalog remain. Snapshot-bound local login locks guard then User, rechecks status/password snapshot, performs success bookkeeping then session INSERT. Snapshot-bound password change shares existing session invalidation; direct old password API is closed for explicit L2. No token or password snapshot enters the Ledger.
4. OAuth state/Link/provisioning/Provider/default Role and settings/secret alternate writers remain unconnected. The explicit-L2 public HTTP composition rejects their namespaces before mux/external effects. Integration credentials remain a separate inventory, not Human completion.
5. Recovery code one-use is connected only with its current protected version and sameTx factor mutation. Other unsafe MFA/Passkey/Email stages remain required-closed. Consumption is not undone after a later separate stage fails, and it is not called atomic with Session issuance.
6. All 87 original entries and the 58 Memory mappings remain in `B2_CONNECTION_INVENTORY.md` and `b2-coverage.json`. Closed/unconnected/source-only entries are visible. Startup still refuses explicit L2 before DB/migration. There is no product-successful Gate constructor or environment opt-in.

## Transaction, locks and phases

B0 alone owns Observe, Reserve, SQL effect/real Commit, Finalize, Receipt and Complete. No second state machine, retry, repair, Abort/Reconcile, slot release or quarantine clear exists.

Identity write order is Lifecycle head/projection prefix -> existing last-admin guard where needed -> User -> Session/assignment/factor -> each actual Operational trigger. The trusted Identity fixture validator takes no User/C lock ahead of the guard. A future product authorizer must satisfy this prefix contract; the immutable fixture grant is not production proof. C and Operational reader ordering retain B1 rules. Actual A revision is read after each transaction's trigger effects; it is never assumed to advance once.

No Ledger/OAuth/SMTP/Turnstile call is added under product SQL locks. The synthetic SQL probes test native NOWAIT exclusion and external stage lock release. Public HTTP does not wrap a whole network flow in a SQL transaction.

## Projection and secrets

New explicit `migrations/002_identity.sql` adds a dedicated schema journal, projection and append-only history. It is never applied by constructors/startup and seeds no identities or projection. Tests apply it only to an explicitly disposable Actions loopback DB. Dirty/checksum/engine/trigger/qualified-profile mismatches refuse.

`IdentityProjection` uses opaque fixture references, actual A/C revisions, Identity revision and count. Full stored projection is compared with actual fixture capture before effects; revision/count equality alone cannot make a restored column current. The capture of synthetic password/session/MFA values exists only in tagged `_test.go`. Production code receives references only. Revision is included in the fixture Checkpoint content so successful no-op writes can finish without fabricating a business change.

Coverage is explicitly `identity-fixture-v1`, `Qualified=false`. This is not a selected D4 real commitment, Key/Root/rotation profile or qualified clock. Real secret coverage has no successful product composition. Password/hash, MFA/recovery values, session/CSRF tokens, OAuth secrets/keys/ciphertexts are not copied into Ledger DTO/history/audit/log. Captured successful secrets are withheld until B0 Complete and are not reproduced by Inspect.

## HTTP and uncertainty

The changed auth handlers use error-aware sources. Lifecycle Held/Conflict returns 503 `identity_lifecycle_held`; UNKNOWN has priority and returns 503 `identity_lifecycle_unknown`. Tokens/cookies/success are withheld. Confirmed legacy authentication denials retain 401/403 when L2 is off. Login/logout/password helpers no longer swallow L2 mutation uncertainty.

Complete public Identity/Policy composition remains unavailable: only GET /auth/me and POST /auth/logout proceed to protected middleware in an explicitly gated Server; all other Identity/security/setup/OAuth administration namespaces close before mux. Handler-level tests prove the bridge separately from this public composition refusal. Other Media/Service domains are not controlled by this specific HTTP boundary; it does not grant them any new authority or send stop/start/kill/resume.

Untracked legacy auth-session audit writes are not opened under explicit L2. Denial audit remains a B1 follow-up; this is a disclosed closed/unconnected path.

Any post-Reserve error follows B0 UNKNOWN/quarantine, including a known local rollback. It cannot be relabeled a confirmed denial or reused reservation. Same-Attempt Inspect is separately authorized and pure. Deployment isolation remains whole-deployment.

## Proof, baseline and limitations

Mandatory B2 inventory uses real Store/MariaDB and dedicated tagged process tests in addition to ordinary class/HTTP contracts. Tagged normal, Race and CGO-free runs are explicit and reject Skip/Fail; existing B0/B1/S13/full suites remain. SourceBoundary reconstructs complete baseline files from literal reviewed edits, including the accepted last-admin baseline test. No source is broadly excluded.

The new opt-in participation API does not exist at the baseline. Its proof therefore uses positive/negative real transaction, version, rollback, no-fallback and uncertainty assertions; a baseline compile failure is not claimed as a functional red. Legacy inferred password/pending/challenge/counter races are not represented as newly reproduced historical defects. The snapshot-bound new API's stale-proof rejection is tested separately; unsafe factor flows remain closed.

Synthetic tests do not certify all real FK closures, all production authentication flows, mixed versions, independent restore/admin/resource domains, actual provider transport, full Human L2 or product-wide acceptance. T01–T40 mapping and final test evidence distinguish covered subsets, closed checks and unperformed work.

## Maintained state

Full PRESERVE; existing v2 authorization; Role=PRESERVE; Scope=HYBRID; Workload Identity=SEPARATE; A/B/C separation; no reuse of old Execution Authority; automatic-start hold; UNKNOWN/HELD never success; real Evidence disabled.

L2 deployment NOT_QUALIFIED; real L2 NOT_RUN; product-wide acceptance NOT_RUN; Phase 1 closure held; production migration unauthorized; formal v2 return NOT_CERTIFIED. D2/D4, S12-A/C/D, B3/B4 and real startup/old-writer/host qualification remain.
