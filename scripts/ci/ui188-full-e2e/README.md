# UI188 Linux Full E2E

This is a manual Actions implementation for the reviewed UI188 product sources.
The implementation and its contract tests are separate from natural Actions CI,
the short Linux bootstrap, and formal live acceptance. The source table is `source-lock.json`, and every
product path is bound by `product-files.json`. The latter is generated Git
blob/mode/SHA256 data (2,068 records), not executable source or a code-size waiver.

The existing `ci.yml`, `release-host.yml`, `ui-regression.yml`, source-owner map,
Go modules and npm locks are retained byte-for-byte from the product candidate.
`ui188-integration.yml` adds a small push/PR check. `ui188-full-e2e.yml` has only
`workflow_dispatch`; no provider, publishing, tag, PR-target or fork trigger exists.

## Trust and execution

The default-branch dispatcher first runs `gate.py authorize` on a standard runner.
It requires the exact owner, main, first attempt, an enabled flag, matching trusted
CP/BOT commits and source-lock hash, explicit billing acknowledgement and an owner
approval record. Only then can the `ui188-hosted-linux` group allocate its approved
`ui188-linux-8core` GitHub-hosted runner. The live components all use that one runner.
The authorization job does not share any live DB or network with the live job.

`host.py` exports fresh exact sources, initializes DAVE's pinned recursive
submodules, verifies product bytes and prepares an image. Go 1.26.5 and Node
26.5.0 images are fixed by registry manifest digests. Package downloads and the
DAVE native build happen during online preparation, with private command output.
The base apt package set and browser version are recorded as environment hashes;
apt repositories themselves are not a reproducible snapshot. Image compilation,
package availability and sandbox-capable Chrome still require the authorized run.

The runtime namespace has an internal Docker network, no published ports or Docker
socket, and OUTPUT deny for IPv4/IPv6. It permits loopback and only the fresh DB's
TCP3306; the Docker DNS proxy is blocked. After setup, network administration is
dropped. Product, sink, receiver and headed Chromium share this namespace.
Worker B has its own cgroup, PID namespace, state and read-only configuration
volume; it cannot mount the runtime's private configuration or original secrets. A real positive
VM internet control, process denial probes, DB positive control and real normal
sandbox browser canary must pass before any product process starts. A Chrome
startup limitation blocks acceptance; no sandbox-disabling switch is added.

The host VM has internet. This design confines the live runtime; it does not claim
to make the entire GitHub VM offline. UID1000, fresh schema, UUID, project, volumes,
protected synthetic credentials and fixed roles are created for each run. No
Discord or external media provider is a runtime destination.

## Execution and evidence

`port.py` checks eleven inherited runner hashes and exact replacement counts in
an image copy. Zero replacements, unknown inputs and repeated application fail.
The changes bind fresh identity, both stream browser/media producers, the
filtered-FD broker and separate Worker configuration to the Linux controller.
The original product checkout is unchanged. `bot-adapter.patch` is separately
applied to a test copy and checked against `source-lock.json:test_adapter.tree`.
The old CB16 diagnostic adapter is rejected. It is never a product BOT overlay.
The production BOT and the tagged external-transport adapter are separately built.
The adapter does not prove the real external Discord/DAVE handshake.

`execute.py` invokes the real R9 S0–S5, lost-stop-response, Archive and independent
media runners. Both stream source processes, fixed PID/start identities and the
separate Worker B are observed across the full 1,000-second overlap. Archive has
exactly three current-run artifacts per stream and actual DB/API owner matching.
Normal DELETE, A/B/C/OFF, real HLS580/900, saved preview, native200, 390px, JA/EN,
per-socket drop0, AAC50ms, actual-PTS PCM250–400/580–700/800–950, watermark/frame
and the formal receiver remain required. No silence filling, retiming or shorter
successful clip is introduced.

One canonical manifest binds the UUID, case, schema, six services, GitHub run ID,
attempt, source lock and source SHAs through Python, Node and the actual Go
constructor. All reject closed identities, missing bindings and duplicate JSON.
The timestamp is an integer string to preserve nanoseconds across JSON/Node.

Setup applies owner-only modes before changing ownership. It uses only the
listed setup capabilities, then execs UID1000 with Eff/Bnd/Amb all zero and
no-new-privileges. PID1 also loses setup rights. A separate UID1000 broker alone
retains NET_RAW, validates the two exact SDP ports and passes kernel-filtered FDs
to capability-free consumers. No privileged or sandbox bypass is used.
The runtime mounts only its separate root-owned setup credential directory.
Compose's DB file secrets remain host-readable in a different private directory;
neither that directory nor the runtime credential copy is mounted into Worker B.

The denominator is 28 groups: S0–S5 (6), F01–F11 (11), plus OVERLAP, ARCHIVE,
COVER, HLS, RTP, SOCKET, AAC, PCM, FRAME, RECEIVER and PROVENANCE (11). Original
nonzero exits stay FAIL; missing or unreachable proof stays NOT_RUN. Safe
independent analyses continue after a failed browser/media step. Source/identity,
egress or safety failure prevents unsafe dependent work. Original raw output stays
in the new private run directory. It is never printed through tee or uploaded.
`stages26.py` maps those groups and execution receipts to 26 original scenario
obligations, separately from historical process/admin exit rows. Both browser
producers require A/B/C/OFF and HLS580/900; F07 also requires all RTP/socket/AAC/
PCM/frame gates. The final host-observed normal shutdown completes T26. A driver
result alone cannot establish Full E2E PASS.

`seal.py` freezes an independently computed required input set, and the controller checks both recordings and
all five built binary hashes after analysis. `evaluate.py` rejects wrong stream,
owner, run, stop receipt, resends, partial Archive and reduced denominators.
`publish.py` projects closed-schema fields; it never recursively uploads evidence.
Real execution UUID/PID/time/source/output/exit receipts are required for all 18
pre-receiver producers. Empty seals, unexecuted producers, stale output, missing
or extra input and duplicate invocations fail independently of hash integrity.
The diagnostic receiver has a separate output and cannot satisfy formal gates.
Only schema-validated JSON and a generated 28-cell status PNG are packaged. The
PNG is a sanitized status visualization, not a browser screenshot or UI proof.
Unreviewed browser PNGs, raw logs, tokens, cookies, nonce, DSNs, DBs and recordings
stay private. Metadata records `PRIVATE_EVIDENCE_NOT_UPLOADED`. Raw evidence is
ephemeral and is not a durable private backup after the hosted runner is disposed.
`private_raw_storage=NOT_CONFIGURED` is retained. Public failure metadata is
limited to stage/code, numeric time offsets/counts, collection enums and hashes.

On the named CP feature's first-attempt natural push, the standard Ubuntu job
can run `linux_smoke.py` for at most 12 minutes. It builds a small bootstrap
image and an actual Go constructor test, without DAVE, Chromium or the product
stack. It checks UID/caps, loss of setup permissions, protected secret reads,
Worker namespace/cgroup/mount separation, DB readiness, filtered RTP FD transfer
and original normal exits. Its closed JSON artifact states `full_e2e=NOT_RUN`.
This smoke does not establish browser, media quality or real Worker acceptance.

Before upload: exact path allowlist, deny-by-default fields, synthetic-secret
variants/pattern scan, generated-PNG byte match, per-member hashes, archive CRC,
member count and outside ZIP SHA256 are checked. Retention is three days. This is
local package verification; GitHub's final upload wrapper needs post-run checking.

## Bounded checks

From the repository root, without any live service:

```text
python3 -B scripts/ci/ui188-full-e2e/source.py --root . --component cp
python3 -B -m unittest discover -s scripts/ci/ui188-full-e2e -p 'test_*.py' -v
actionlint -config-file scripts/ci/ui188-full-e2e/actionlint-config.yaml .github/workflows/ui188-full-e2e.yml .github/workflows/ui188-integration.yml
```

The custom-label registry in actionlint is named `self-hosted-runner` by that
tool's configuration format. It does not provision or authorize a self-hosted
runner. The workflow requires `runner.environment == github-hosted`.

See `docs/release/ui188-actions-runbook.md`, `ui188-rc-notes.md` and
`ui188-source-map.md` for introduction order, compatibility and delivery gates.
