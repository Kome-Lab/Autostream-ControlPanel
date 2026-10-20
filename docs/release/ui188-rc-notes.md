# UI188 Release Candidate notes

Status at 2026-10-20: product feature delivery and Actions contract correction.
Natural CI and short bootstrap results are evaluated per delivered immutable SHA
in the delivery report. `FULL_E2E_PASS` and `RELEASE_APPROVED` remain NOT_RUN.
The product trees are CP `44ae3a676b6e9a462ea2a437bb8391f2198e7af4` and BOT
`1141c416accf9fcb5d828f1174a7641863404972`. The product commits are CP
`6419d0fb04386bb86dba4b8f2aeb05cc2c055bb7` and BOT
`ea32df776c07119c6c4199d3db69971a1fd6686f`; the Actions successor is separate.
This candidate must not auto-promote to stable or
production and has no authorization for real Discord or public media output.

## Known-issue ledger

| Issue | Evidence retained | Release implication |
| --- | --- | --- |
| KNOWN_UNRESOLVED_MEDIA_ISSUE | Historical Phase C receiver drops: S1 14,028 / S2 2,887; MC13: S1 28,451 / S2 1,636 | Root cause remains unresolved; audio stability is not guaranteed. |
| Limited CB16 observation | Two simultaneous 600-second streams had zero observed drops and zero internal AAC uncovered gaps above50ms | This does not clear the historical failures or satisfy the formal 1,000-second denominator. |
| ENV_NOT_COVERED_BY_LINUX | Windows Docker/GPU/native desktop behavior is outside this Linux job | A future Linux PASS cannot establish resolution of the old Windows failures. |
| Full hosted runtime | Formal image/native build, Linux browser sandbox, canary and Full E2E are unexecuted | A short bootstrap is not product/media runtime acceptance. |
| Public evidence limits | Raw logs, recordings, DB and browser screenshots are withheld | Public status PNGs are metadata visualizations; they are not redacted UI proof. |

Audio root-cause/GDB investigation is deferred. No buffer, media timing, PTS,
FFmpeg product, quality threshold or existing failure record is changed here.

## Compatibility and update order

| Pair or boundary | Required handling |
| --- | --- |
| Old CP + new BOT | Unsupported current_discord_bindings must fail closed. Do not claim successful operation of this pair. |
| New CP + new BOT | Provisional target; validate exact current binding, assignment/lease, authentication and stream proof in CI/E2E. |
| New CP + old BOT | Not yet established by this work; review before any staged service switch. |
| Encoder / Worker | Keep fixed commits and each caller's existing Contracts version. Do not unify pins. |
| DAVE | Keep the exact gitlink and normal CGO/native dependency; the tagged adapter is test-only. |
| Schema | The added Actions layer introduces no migration. Validate the selected product's forward migrations and the actual pre-upgrade schema before deployment. |

The provisional sequence is CP first, verify the fresh current-binding contract,
then BOT; Encoder/Worker compatibility follows the fixed source map. Deployment
must separately establish real placement, active jobs, DB history, identity,
assignment authority, versioned assets and approved backups. No service restart,
real provider use or schema mutation is authorized by these notes.

## Rollback design

Before a later authorized deployment, retain immutable prior CP/BOT artifacts,
checksums, runtime identity and a consistent DB backup under private controls.
Keep the selected Encoder/Worker versions unchanged unless separately approved.
Drain or normally stop owned streams and require durable stop/Archive/assignment
receipts before changing services. Preserve failed-run evidence and active jobs.

If the new current-binding/readiness/secret/ownership contract fails, stop the
new BOT through its normal service mechanism and return to the previously
verified compatible BOT/CP pair. Do not leave a new BOT running against an old CP.
Confirm the prior pair's runtime identities, config revisions, API, assignment
fences and stream/Archive state before resuming an approved workload. The exact
service commands depend on the real placement and remain a deployment gate.

Do not perform reverse migrations or edit/delete lease rows, updater journals,
markers, checkpoints, grants or A/B runtime state to force convergence. A DB
restore is a separately approved recovery with verified version compatibility,
backup integrity and controlled service shutdown. A rollback failure is not a
successful release. No new tag may replace an immutable failed release tag.
