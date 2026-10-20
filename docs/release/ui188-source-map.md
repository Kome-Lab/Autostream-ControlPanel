# UI188 fixed source and ownership map

The product logical trees below were reconstructed before adding the new Actions layer.
The full final integration tree also contains the new workflow, harness, source data and release documents.
It must receive a new signed commit only after delivery approval. No tree is a substitute for that commit SHA.

| Component | Fixed base/commit | Product tree | Contracts pin |
| --- | --- | --- | --- |
| cp | `a9efbed3e058bf016b171e50c22650075f20ef50` | `44ae3a676b6e9a462ea2a437bb8391f2198e7af4` | `v1.2.12-0.20261012062538-f62ddfaf1144` |
| bot | `826f0b631f59a4672e54b9a5bdcc6acfe1f997ca` | `1141c416accf9fcb5d828f1174a7641863404972` | `v1.2.12-0.20260903202917-82716abd84f2` |
| encoder | `c51339fbe3d05b7ad96620c5a1adb83d2e5fc145` | `99fa3b8e34f5cefb0dd1ed296dde6e172e137fbc` | `v1.2.12-0.20260929140016-7edb888d585e` |
| worker | `381790940e0fed8532d9396f0a2a40c0bfd0a612` | `91f3f6846b319eb35d327f293b53660a12a71911` | `v1.2.12-0.20260903202917-82716abd84f2` |

DAVE gitlink160000: `52cd56dc550f447fb354b3a06c9e2d2e2a4309c6`. Observability reference: `c2daf86c3ae621372dfde1c5afc3bfb54164ed62`.
CP Contracts full commit: `f62ddfaf1144d502966b2a58e3a93133cfbfded0`; BOT/Worker: `82716abd84f2a048e0d530aa7e021d22f6b4d4ff`; Encoder: `7edb888d585ec7001d2dc1feee9256db620f8cfd`.
The distinct declared pins are retained with Go1.26.5 and GOWORK=off.

The tagged adapter is separately reconstructed; its corrected tree and patch SHA256
are bound in `scripts/ci/ui188-full-e2e/source-lock.json:test_adapter`.
The old tree `28581547236782094ddbdbccc0cd8647a61bd573` is rejected for a fresh Actions run.
Its reviewed patch is kept in the CP harness and applied only to the BOT test build. Product BOT bytes are unchanged by that patch.

## Product change paths

| Repository / transition | Path | Owner / disposition |
| --- | --- | --- |
| cp / HEAD-to-R9 | `.github/workflows/ci.yml` | CP; existing CI; preserve original denominator |
| cp / HEAD-to-R9 | `internal/httpapi/ui188_r9_archive_finalization_test.go` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `internal/store/memory.go` | CP; product source |
| cp / HEAD-to-R9 | `internal/store/service_assignment_guard.go` | CP; product source |
| cp / HEAD-to-R9 | `internal/store/stream_queries_mariadb.go` | CP; product source |
| cp / HEAD-to-R9 | `internal/store/ui188_r9_archive_finalization_mariadb_test.go` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `internal/store/ui188_r9_archive_finalization_test.go` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/source-owner-map.json` | CP; source integrity metadata |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/archive_barrier.py` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/artifact_transport.py` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/audit_pending.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/audit_preflight.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/browser.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/capture-final-runtime-proof.py` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/capture-live-authority.py` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/cover_observation.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/host_r9.py` | CP; legacy Windows test driver; retained, not invoked by Linux job |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/owned_cdp.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/packet_capture.py` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/probe_cover_cache.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/real_browser.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/receiver_observation.py` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/rtp_trace.py` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/run_r7_chain.py` | CP; legacy Windows test driver; retained, not invoked by Linux job |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/runtime.py` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/s0_negative.py` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/s2.py` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/s4.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/s4_state.py` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/s5.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/stale_authority.py` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/test_archive_oracle.py` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/test_artifact_transport.py` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/test_packet_capture.py` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `scripts/ci/ui188-r4-chain/test_stop_event_order.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `web/src/features/streams/stream-action-controller.ts` | CP; product source |
| cp / HEAD-to-R9 | `web/src/lib/foundation/status/observability-presenters.ts` | CP; product source |
| cp / HEAD-to-R9 | `web/src/lib/i18n.ts` | CP; product source |
| cp / HEAD-to-R9 | `web/tests/helpers/ui-foundation-contract-imports.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `web/tests/helpers/ui-foundation-status-authority.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `web/tests/streams-foundation-migration.test.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `web/tests/ui-foundation-status.test.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `web/tests/ui-regression/cp174-source-deltas.test.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `web/tests/ui-regression/current-copy.test.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `web/tests/ui-regression/release-assembly-deltas.test.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `web/tests/ui-regression/ui188-current-source-deltas.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `web/tests/ui-regression/ui188-current-source-deltas.test.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `web/tests/ui-regression/ui188-r3-session-delta.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `web/tests/ui-regression/ui188-r3-session-delta.test.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `web/tests/ui-regression/ui188-r4-delete-delta.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `web/tests/ui-regression/ui188-r4-delete-delta.test.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / HEAD-to-R9 | `web/tests/ui-regression/ui188-r9-finalization-deltas.mts` | CP; test-only source; public-safe synthetic fixtures |
| cp / R9-to-CB15 | `internal/httpapi/server_current_discord_bindings.go` | CP; product source |
| cp / R9-to-CB15 | `internal/httpapi/server_runtime_config.go` | CP; product source |
| cp / R9-to-CB15 | `internal/httpapi/ui188_cb15_admin_preview_test.go` | CP; test-only source; public-safe synthetic fixtures |
| cp / R9-to-CB15 | `internal/httpapi/ui188_cb15_current_binding_test.go` | CP; test-only source; public-safe synthetic fixtures |
| cp / R9-to-CB15 | `internal/store/current_discord_credential_binding.go` | CP; product source |
| cp / R9-to-CB15 | `internal/store/current_discord_credential_binding_mariadb.go` | CP; product source |
| cp / R9-to-CB15 | `internal/store/current_discord_credential_binding_test.go` | CP; test-only source; public-safe synthetic fixtures |
| cp / R9-to-CB15 | `internal/store/current_discord_credential_lease_test.go` | CP; test-only source; public-safe synthetic fixtures |
| cp / R9-to-CB15 | `internal/store/runtime_secret_leases.go` | CP; product source |
| cp / CB15-to-CB16 | `internal/httpapi/server_runtime_config.go` | CP; product source |
| cp / CB15-to-CB16 | `internal/httpapi/ui188_cb16_snapshot_retry_test.go` | CP; test-only source; public-safe synthetic fixtures |
| cp / CB15-to-CB16 | `internal/store/current_discord_credential_binding.go` | CP; product source |
| cp / CB15-to-CB16 | `internal/store/ui188_cb16_snapshot_policy_test.go` | CP; test-only source; public-safe synthetic fixtures |
| cp / CB15-to-CB16 | `internal/store/ui188_cb16_snapshot_retry_test.go` | CP; test-only source; public-safe synthetic fixtures |
| bot / HEAD-to-R9 | `.github/workflows/ci.yml` | BOT; existing CI; preserve original denominator |
| bot / HEAD-to-R9 | `cmd/discord-bot/ui188_r4_chain_linux_test.go` | BOT; test-only source; public-safe synthetic fixtures |
| bot / HEAD-to-R9 | `cmd/discord-bot/ui188_r9_stale_authority_linux_test.go` | BOT; test-only source; public-safe synthetic fixtures |
| bot / HEAD-to-R9 | `internal/discord/ui188_chain_voice_linux.go` | BOT; product source |
| bot / R9-to-CB15 | `cmd/discord-bot/current_discord_bindings.go` | BOT; product source |
| bot / R9-to-CB15 | `cmd/discord-bot/main.go` | BOT; product source |
| bot / R9-to-CB15 | `cmd/discord-bot/ui188_cb15_current_binding_test.go` | BOT; test-only source; public-safe synthetic fixtures |
| bot / R9-to-CB15 | `cmd/discord-bot/ui188_mc13_heartbeat_linux_test.go` | BOT; test-only source; public-safe synthetic fixtures |
| bot / R9-to-CB15 | `cmd/discord-bot/ui188_mc13_heartbeat_policy_linux_test.go` | BOT; test-only source; public-safe synthetic fixtures |
| bot / R9-to-CB15 | `cmd/discord-bot/ui188_r4_chain_linux_test.go` | BOT; test-only source; public-safe synthetic fixtures |
| bot / R9-to-CB15 | `cmd/discord-bot/ui188_r5_gateway_lifecycle_test.go` | BOT; test-only source; public-safe synthetic fixtures |
| bot / R9-to-CB15 | `internal/control/client.go` | BOT; product source |
| bot / R9-to-CB15 | `internal/control/current_discord_bindings.go` | BOT; product source |
| bot / R9-to-CB15 | `internal/control/current_discord_bindings_test.go` | BOT; test-only source; public-safe synthetic fixtures |

## New integration owner

CP owns the new two workflows, `scripts/ci/ui188-full-e2e/` and these `docs/release/ui188-*` documents.
The existing per-repository CI files and public product owners are not reassigned. No private evidence,
recording, DB, credential file, host log or local agent policy belongs in this source set.
The generated `source-owners.json` binds every product transition and lists the new integration responsibility.
The historical `product-files.json` binds every one of the2,068 fixed product paths to mode/blob/SHA256.
It remains byte unchanged. `cp-test-overlay.json` applies exactly three named test
paths: the parity registration, the finite CB15/CB16 delta helper and its negative
suite. Each edit includes the historical row (or explicit absence) and the exact
new mode/blob/SHA256. `source.py` verifies all1,474 historical CP paths, applies
only these test edits, and verifies the resulting1,476-path subtree against
`source-lock.json`. All1,473 other historical CP paths remain byte unchanged.
The historical product tree and the new verified test subtree are separate values;
neither is the final integration tree containing the Actions harness itself.

## Required case responsibilities

| F | Required original behavior |
| --- | --- |
| F01 | Required Encoder/Worker roles; rejected create does not persist |
| F02 | One normal stop POST, lost response correlation, durable receipt, list/detail convergence |
| F03 | Same-run report3/3 per stream; DB/API owner/run/start authority, pending guards, normal DELETE |
| F04 | Old session/epoch cannot start/dispatch/join; new human/existing presence starts once |
| F05 | Button confirmation with typed inputs0 |
| F06 | BOT-only/unknown/wrong VC rejection; selected connections, deletion release and other-stream isolation |
| F07 | Both streams' A/B/C/OFF, HLS580/900 and continuous RTP/socket/AAC/PCM/frame acceptance |
| F08 | Saved image decode/URL identity, draft restore/cancel and blob lifecycle |
| F09 | Settings preview versus actual HLS,580/900, native200 and390px |
| F10 | Japanese actual desired/applied/OFF and terminal state observations |
| F11 | JA/EN pending/success audit distinction with immutable raw row and action ID |

All rows require the same run; unit fixture success is not an F01–F11 live acceptance.
See the RC notes for the unresolved media and platform limitations.

## 26-stage compatibility map

Historical raw process/admin exit rows vary between retained snapshots. They are
preserved verbatim. T01–T26 name scenario obligations for the new controller;
they do not renumber old exits or turn historical evidence into a current PASS.
`stages26.py` binds each obligation to current execution receipts and the groups
below. Missing execution is NOT_RUN. T26 requires separate host shutdown proof.

| Obligation | Retained work | Current binding |
| --- | --- | --- |
| T01 | Normal sandbox and closed-network positive/negative canary | browser-canary |
| T02 | Separate Worker B readiness and source lifetime | OVERLAP and Worker liveness |
| T03 | Cover cache preflight | cache-preflight |
| T04 | Native200 and JA/EN audit preflight | audit-preflight |
| T05 | S0 required-role and negative input checks | runtime; S0/F01 |
| T06 | Current human starts S1 once | runtime; S1 |
| T07 | Existing presence starts S2 once; stale authority rejected | s2; S2/F04 |
| T08 | Live same-run ownership readback | live-authority; PROVENANCE |
| T09 | Both streams' A/B/C/OFF applied state | browser-S1/S2; COVER |
| T10 | Same two sources overlap 1,000 seconds | overlap; OVERLAP |
| T11 | Both actual HLS preview snapshots | hls-snapshot; HLS |
| T12 | S5 saved images, preview, layout and audit | s5; S5/F08–F11 |
| T13 | Normal stop, lost response and button-only delete | s4; S4/F02/F05 |
| T14 | Report3/3, Archive and other-stream isolation | state; ARCHIVE/F03/F06 |
| T15 | Final fixed product/binary proof | final-proof; PROVENANCE |
| T16 | Original S2 driver completion | s2 execution receipt |
| T17 | Original runtime driver completion | runtime execution receipt |
| T18 | Original receiver sampler completion | sampler execution receipt |
| T19 | Actual owned consumer capture | capture execution receipt |
| T20 | BOT A audio journal and source RTP | capture; RTP |
| T21 | BOT B audio journal and source RTP | capture; RTP |
| T22 | Actual cgroup and socket observations | capture/sampler; SOCKET |
| T23 | Independent same-run media analysis for both streams | media-S1/S2; AAC/PCM |
| T24 | Formal receiver, source RTP and continuous audio | receiver; RECEIVER/F07 |
| T25 | Every-frame and ON/OFF/Watermark checks | media-S1/S2; FRAME |
| T26 | Original normal Worker/runtime/DB/broker shutdown | host finalization |

Full E2E needs all 26 obligations and all 28 groups. The short Linux bootstrap
does not execute this scenario denominator and cannot satisfy any missing media
or UI acceptance gate.
