# UI188-R4 real-service acceptance runner

This test-only runner controls external Discord Gateway/Voice, RTMPS destination,
and notification delivery. CP authentication, MariaDB, BOT coordination, Worker,
Encoder, receipts, applied state, and media are real implementations. It does
not replace their responses or synthesize successful media.

Fixed starting sources:

| Component | Input |
| --- | --- |
| CP | de8ef12171e069c5dc93610df9d2b58dd3481b8b plus reviewed R3 draft |
| BOT | 18016ea5c7ebb49e311f7fc46ff46babed18ca14 plus reviewed R3 draft and Linux test adapter |
| Contracts | f62ddfaf1144d502966b2a58e3a93133cfbfded0 |
| Encoder | 1ccac1d0cd81edccffa61accc1f444d035b480da |
| Worker | 381790940e0fed8532d9396f0a2a40c0bfd0a612 |
| Observability | c2daf86c3ae621372dfde1c5afc3bfb54164ed62 |
| DAVE | 52cd56dc550f447fb354b3a06c9e2d2e2a4309c6 |

R5 reuses these tracked test runners with the reviewed R4 drafts and the R5
credential-selection correction. The `ui188-r4-chain` path and tagged test
names remain stable; they do not identify the run or allow reuse of old results.
Record each new run's source trees, unique attempt, stream/job/generation and
final signed commits independently.

Use `GOWORK=off` and an owned internal-only Linux test network with a MariaDB alias `db`,
Go 1.26.5 normal CGO/DAVE, Node 26.5.0, FFmpeg, and existing locked dependencies.
The R5 runtime expects a writable owned volume at `/r5` containing exact exported
sources and built binaries: `bin/cp-r3-final`, `bin/bot-test-r3` (compiled with
`go test -c ./cmd/discord-bot`), `bin/worker`, and `bin/encoder`. Build the CP web
output from that source at `/r5/cp/web/out` with demo mode disabled. Use
`UI188_BOT_BINARY` and `UI188_ENCODER_BINARY` to select reviewed formal builds;
the BOT external transport adapter requires the `ui188_chain` build tag.
It supplies actual packets to production batching and forwarding. It does not
exercise the external Discord/DAVE Voice handshake.

`UI188_TEST_CREDENTIALS` must point to a protected synthetic JSON file containing
`db_password`, `encryption_key`, `ingest_key`, `setup_token`, and `admin_password`.
Provision only a fresh allowlisted `ui188_r5_acceptance`, `ui188_r5_final` or
`ui188_r5_final2` schema for the test app user. Panel
node registration `configuration_yaml` is stored verbatim in protected files.
Worker B uses a separate owned filesystem while sharing the test network namespace
so its receipt journal cannot collide with Worker A. The only host browser entry
is loopback port18841 forwarded byte-for-byte to the test CP.

Set `UI188_EVIDENCE_DIRECTORY`, `UI188_PROBE_ATTEMPT` (unique), and
`UI188_CP_BINARY` pointing to the actual candidate build before running `python3 -B runtime.py`.
It records S0, performs normal create/reassignment, then admits a new human join
through the external Gateway peer. It keeps owned processes alive until the
`chain-finished-<attempt>` marker is created. Do not run against production data.

The companion `browser.mts` runs in a normal sandbox-capable Chrome context with
the same protected test credential file and matching evidence directory.
`AUTOSTREAM_BROWSER_PATH` selects an existing supported Chrome executable.
`UI188_BROWSER_ATTEMPT` makes each evidence set unique. It performs real cover
PUTs through the UI and samples decoded HLS frames. `s2.py` tests BOT-only/wrong-VC
rejection before reassigning into pre-existing human presence. `s5.mts` controls
the owned Chrome profile's actual native200% setting and real preset upload/edit.
`media.py` reads and hashes the same Encoder's real HLS/recording output and
checks the original 50ms audio packet criterion, independent PCM/tone, and
every decoded recording frame from start-cover A through the predeclared OFF
boundary. The same job must advance past 410 seconds, including the original
250-400 second failure band; do not select a shorter successful clip. All
independent analyses execute before returning an aggregate failure.

`s4.mts` loses only a real successful stop response and confirms the receipt
by reads, then performs regular deletion through button confirmation.
`s4_state.py` observes the real recording hash before deletion, retains the
other live stream, and verifies stopped/never-started deletion and assignment
release. Scope-owned filesystem barriers coordinate evidence capture only.
Use unique attempts; preserve original nonzero logs and failed schema/media.
No acceptance is inferred from a single HTTP200 or from unrelated runs.

No complete acceptance is implied by individual runner successes. The R3 run
stopped on a real BOT-only auto-start failure before S2 completion and S4. The
audio packet continuity check also failed; it is not waived. Lost stop response
support in `owned_cdp.mts` was prepared but not executed. S5's initial absolute
versus relative image-src comparison was corrected after its original failure,
without re-running Gate B after the product stop. These sources remain drafts;
no signed delivery or natural CI was performed for R3.
