# AutoStream local executor

The local executor is the root-owned, bounded half of the SSH-free Host Agent
architecture. It opens no TCP listener. The non-root Host Agent reaches it
through `/run/autostream-local-executor/executor.sock`. Protocol v1 remains
compatible for fixed target probes; protocol v2 adds fixed software
`stage`/`apply`/`reconcile`, systemd
`port_reconfigure`/`port_reconfigure_reconcile`, and host-runtime self-update
operations.

IPC never accepts a generic command, unit, path, endpoint, repository, image,
or URL. The root-owned policy resolves all privileged target values. A v2
request carries only the immutable release plan, ownership epoch, policy
revision, and (for state-changing apply/reconcile operations) a short-lived
one-time mutation grant.

A Stage response is not itself a durable-ledger absence proof. Any Stage error
therefore leaves the Host Agent's immutable plan active so the next lease
generation can call reconcile without restaging or reapplying. When this root
executor returns `stage_required` during recovery, it has proved only that the
exact job has no durable mutation ledger or apply-authorized state. It does not
claim that no staged directory existed. An exact orphan stage left before
ledger commit is safely removed and its parent fsynced first; unsafe or failed
cleanup returns `state_unavailable`. The Agent may then report terminal
`failed`/100 with `remote_stage_missing`. If matching state exists, reconcile
settles it instead. A `reconciling`/99 report is nonterminal and may move only
to a terminal 100-percent result.

A stale lease or sequence drops only its rejected report cursor while the
active job and plan stay durable. Clearing that cursor later requires the
Panel's structured terminal job to match the exact active immutable intent; a
bare clear is invalid. Likewise, a terminal report is acknowledged only after
the HTTP response exactly proves the committed job/Agent IDs, lease generation,
sequence, status, progress, code, and result digests. v1.9.9 and v1.9.10 Agents
predate this terminal-proof contract. Panel v1.9.11 returns
`system_update_terminal_proof_upgrade_required` with HTTP 409 to a registered
Agent older than v1.9.11 before terminal cursor recovery, so a legacy client
cannot interpret the structured proof as a bare clear. Do not delete this
executor's ledger or invoke a manual restage/reapply to bypass recovery. A
managed Host runtime `--upgrade` also rejects an active durable mutation unless
the operator selects the explicit v1.9.11 recovery path below.

Use only the same unchanged
`autostream-host-agent_vX.Y.Z_linux_amd64.tar.gz` whose GitHub archive
attestation was verified as described in `README.md`. Keep that one archive
adjacent to the root-owned extracted directory until this installer finishes.
The external SHA-256 and Host Agent manifest assets are automatic-updater
compatibility inputs and are not manual installer inputs. This entry point
independently stable-copies and safely re-extracts the adjacent archive,
verifies its internal checksum inventory and exact `artifact-manifest.json`,
and binds both Host Agent and Local Executor build identities before persistent
mutation.

## Prerequisite

Install the Host Agent with `install-autostream-host-agent --prepare` first.
That one command creates the dedicated `autostream-host-agent` user and group,
prepares this same-release executor binary and units, and creates the fixed
root-owned policy and sidecar directories without starting either service or
writing an identity/policy. It does enable the fixed root A/B recovery timers;
the Agent, Executor, and socket remain inactive/disabled. Run the Control Panel generated
`autostream-host-agent configure` command next. Configure writes the exact
server-generated policy and missing canonical systemd sidecars before
activating the staged four-field identity.

When this host owns a Control Panel or Observability database, install the
verified fixed backup script from that service release and prepare:

```text
/etc/autostream-local-executor/mariadb-backup.cnf  root:root 0600
/var/backups/autostream/control-panel              root:root 0700
/var/backups/autostream/observability              root:root 0700
```

Grant the dedicated MariaDB account only the required read privileges and run
each backup script successfully before configure. The database credential file
is operator-provisioned; neither the Host Agent nor configure creates,
transmits, or rewrites it.

## Prepare the policy

For the recommended first installation, do not copy or edit the example.
`autostream-host-agent configure` installs the canonical policy directly at:

```console
/etc/autostream-local-executor/policy.json
```

The Control Panel derives it from the active `pull_v2` policy, server-owned
execution host, and each target's applied endpoint/config state. Configure
looks up the real Agent account:

```console
id -u autostream-host-agent
getent group autostream-host-agent
```

and binds its non-root UID/GID into the stage/activation commitment. The
client cannot choose a host ID, target path, unit, command, policy digest, or
revision. Ports must be in `1024..65535`. Systemd unit, executable, release
root, current link, binary path, smoke user, and required paths are fixed by
`service_type`; changing them is rejected. For a systemd Control Panel or
Observability target, the only variable backup authority is the validated
server-owned `database_name` saved in System Updates. It is combined with the
compiled fixed backup executable; a missing/invalid name or a name on any other
service fails closed. Docker authority still fails closed during automatic
configuration. Auto Configure generates only the systemd root policy and
sidecars; it never derives Docker authority from Node registration values. A
Docker target is eligible only when a root-owned fixed target policy and an
approved frozen Compose baseline already provide every required field.

The subsequent installer keeps the executor prepared by the Host Agent as the
fixed `/usr/local/libexec/autostream-local-executor` A/B symlink. It accepts
that symlink only when `current` selects `slots/a` or `slots/b`, the resolved
parent chain is root-owned and not group/other-writable, the target is
`root:root 0755`, and its SHA-256 equals the binary from this same verified
archive. It rejects every other symlink and any cross-release or unsafe chain.
An older standalone regular-file installation remains a supported upgrade
input.

Systemd sidecars are created only at these fixed paths:

```text
/opt/autostream/local-executor/ports/control-panel.env
/opt/autostream/local-executor/ports/worker.env
/opt/autostream/local-executor/ports/encoder-recorder.env
/opt/autostream/local-executor/ports/discord-bot.env
/opt/autostream/local-executor/ports/observability.env
```

Each is an exact two-line file: the service-specific bind variable followed by
`AUTOSTREAM_CONFIG_REVISION`. The directory is `root:root 0700`; files are
`root:root 0600`. Configure creates a missing canonical sidecar, preserves an
identical one, and normally refuses to overwrite any differing file. A later failure
rolls back newly installed sidecars/policy when the outcome is known; an
uncertain identity commit is left as a consistent pair and reported for
operator recovery.

The explicit Host Agent `configure --adopt-live-systemd-sidecar` recovery mode
is available in v1.9.8 and later. A managed v1.9.7 host must first run the
version-matched v1.9.8 `install-autostream-host-agent --upgrade` bridge so the
installer upgrades and performs its bounded restarts of the Host Agent and
Local Executor as one pair. After that upgrade succeeds, do not manually
restart the Host Agent or affected target service until explicit adoption
Configure finishes. Issue a fresh Configure Token only then and enter it at
the protected TTY/stdin prompt, never through argv or an environment variable.

Recovery may replace exactly one differing existing sidecar only when it is
still the canonical bytes for the current root policy and the already-running
eligible systemd service is proved to match the staged loopback port, service
identity, config revision, managed executable, unit/cgroup/listener ownership,
and HTTP version before and after an atomic Linux inode exchange. Current and
staged Panel/host/Agent/profile authority must match, policy lineage must
strictly advance, endpoint/config revisions must be unchanged, the old port
must be unused, and no port ledger or applied overlay may exist. It accepts no
target or filesystem value in argv, excludes Control Panel, and performs no
service restart. A missing sidecar is never the adoption candidate and remains
governed by normal create-if-absent behavior for the other managed targets;
explicit adoption still requires exactly one differing existing sidecar.
Hand-edited or ambiguous differing sidecars, and multiple differing existing
sidecars, remain rejected.

The example policy remains a schema reference and an expert recovery aid. Do
not hand-edit the live policy to bypass the server projection. Without a
server-pinned matching `local_executor_policy_sha256`, target observations and
mutation remain unavailable and fail closed. With a matching active policy and
positive server-owned ownership epoch, the Host Agent can claim jobs and ask
this executor to stage/apply/reconcile them without SSH.

Control Panel participates in initial configure so its existing loopback port
is represented by a canonical sidecar. Runtime Control Panel port changes
remain unsupported because they also require reverse-proxy coordination. For a
systemd port job the executor accepts only Worker, Encoder Recorder, Discord
Bot, and Observability fixed adapters. It checkpoints the old exact sidecar,
atomically stages the new two-line sidecar, restarts only the fixed unit, and
verifies listener ownership, service identity, health, version, config
revision, and sidecar SHA-256. A failed verification restores and re-verifies
the old sidecar/port. An uncertain result is reconciled from the durable ledger
without reapplying. `rollback_failed` is a terminal local quarantine with no
applied overlay; the Control Panel keeps both reservations until explicit
recovery proves an exact effective state.

Docker software update uses the fixed Compose authority described below.
Worker, Encoder Recorder, Discord Bot, and Observability also support a
dedicated Docker `port_reconfigure`/reconcile transaction. It separately binds
the advertised port (`1..65535`), localhost published port (`1024..65535`),
and container listen port (`1024..65535`). The published host is always
`127.0.0.1`. The executor changes only the fixed per-service port environment
and Compose service, then verifies the container/image/repository identity,
Compose revision/digest, environment digest, and health. Failure or an unknown
response is rolled back/reconciled from the durable ledger without replaying
the mutation.

The Docker port operation is unavailable when policy/baseline/current mapping
is missing, stale, busy, drifting, or recovering. It never rewrites Nginx,
Caddy, or another reverse proxy; update and verify that origin separately.

Host-runtime self-update uses a dedicated directive and grant, fixed A/B slots,
and a root recovery supervisor. It is not production-ready merely because the
protocol source exists; require a published immutable Host Release plus real
systemd restart/reboot/heartbeat/probe/rollback canaries before enabling it.
The recovery supervisor does not accept `systemctl restart` alone as proof: it
requires the socket and service active, an unchanged positive `MainPID`, an
exact healthy-slot `/proc/<pid>/exe`, and matching version and
mutation/recovery protocol across a bounded stability interval before it
restarts the Host Agent and clears `rolling_back`.

An operator-managed offline Host runtime update uses the verified Host Agent
archive entry point, not this component installer and not the internal root
helper directly. Leave the unchanged archive adjacent to the newly extracted
directory and enter that concrete new Host Agent release root first:

```bash
# Keep ../autostream-host-agent_vX.Y.Z_linux_amd64.tar.gz unchanged and adjacent.
cd /opt/autostream/releases/artifacts/autostream-host-agent_vX.Y.Z_linux_amd64
sudo ./install/install-autostream-host-agent --upgrade
```

That command stages and activates the Host Agent and Local Executor together,
preserves this policy, and reuses the same durable A/B rollback state. A
standalone Local Executor replacement cannot establish the paired runtime,
blocker, process-identity, and rollback proof required by this boundary.
The archive-contained Host runtime guide is `README.md`.

This recovery option supports only an existing managed A/B runtime whose live
Host Agent and Local Executor are an exact paired v1.9.9 or v1.9.10 release.
Their version, commit, and build date must match; both public binary links and
both systemd processes must resolve to the same current slot, and this live
Executor must expose mutation and recovery protocol 2. The old Agent must begin
enabled and active with a live MainPID. The Executor service and socket plus
both fixed recovery timers must be active; the Agent, socket, and timers must
be enabled. It does not support every older Agent, a mixed-version pair, a
standalone install, or an inactive or mismatched Executor.

For an exact active journal on that permitted pair, first install and verify
Control Panel v1.9.11. Then use the verified matching Host archive and
explicitly run:

```bash
cd /opt/autostream/releases/artifacts/autostream-host-agent_v1.9.11_linux_amd64
sudo ./install/install-autostream-host-agent --upgrade --recover-active-job
```

Use `linux_arm64` on arm64. Ordinary `--upgrade` still rejects an active job;
this is the only active-job form. It needs no Configure Token and runs no
`configure`. The candidate Agent runs once as the service account, may only
reconcile and report the journal's exact job, cannot claim a new job, and cannot
invoke Stage or Apply. The installer arms a transient systemd guard before it
stops the old Agent. Once exact recovery succeeds, the Agent remains
intentionally stopped while the same command hands the pinned pair to the A/B
upgrader; this live Executor and the stopped on-disk Agent must still match.
The guard is disarmed only after an active managed Agent is proved. A failure
reacquires the canonical locks, restores and proves the exact previous pair,
and restarts the previous Agent; if that cannot be done safely, the Agent stays stopped
and the guard stays armed. Do not delete or edit the Host Agent journal, this
root ledger, staged release, or Panel job, and do not create a replacement job,
restage, or reapply.

Self-update grant recovery uses only the root-owned hash, immutable binding,
and consumed receipt; it never persists the raw grant. A reboot with stable
old runtime plus prepared/consumed stage state fails that exact generation
closed and removes the orphan ledger. A consumed stage receipt that exactly
matches already-durable staged state is marked applied without repeating the
download or slot mutation. Prepared/consumed reconcile leftovers are burned or
marked applied only when their immutable generation/runtime binding agrees
with durable state; another mutation needs another Control Panel grant.

After a Control Panel Runtime Token `emergency-revoke`, both credential slots
are invalid. Stop the Host Agent, issue a new Configure Token for the same
`pull_v2` Node, and run the generated `autostream-host-agent configure` command
before crossing the root-only recovery boundary:

```console
sudo systemctl stop autostream-host-agent.service

sudo /usr/local/libexec/autostream-local-executor \
  recover-runtime-credential \
  --rotation-id "<ROTATION_ID>" \
  --confirm-emergency-revoked

sudo systemctl restart autostream-host-agent.service
```

It accepts no path or token argument and validates the root ledger, policy
digest, fixed host/policy/protocol fences, replacement identity, and applicable
staged TTL before recording `manual_recovered` and cleaning the exact staged
identity. The policy digest and all source/projection/executor revisions must be
unchanged by Configure.

`claim_prepared`, `cancel_ready`, `activated`, and `expired` recover
immediately. `stage_bound` is immediate only when the fixed staged identity is
absent. An exact staged file discovered in `claim_prepared` compatibility state
or `stage_bound` is first promoted to `staged`; `staged`, `local_staged`, and
`proof_ready` recover only after the staged TTL. `claim_prepared` has no staged
token hash until a staged credential is bound, so only the previous token hash
is available there; afterward both revoked slot hashes are enforced. A missing
or new server directive does not authorize the executor to discard a bound
ledger. After restart, the replacement-token poll finalizes the exact terminal
rotation and removes the remaining root ledger before another rotation starts.

The executor consumes the one-time grant only at the root mutation boundary.
Release assets are fetched anonymously from the fixed Kome-Lab GitHub
repositories, with canonical origins, redirect rejection, immutable release
identity, manifest/checksum, and asset digest verification. No GitHub token or
Runtime Token is stored in this policy.

Docker-mode targets use only the dedicated root-owned credential file
`/etc/autostream-local-executor/docker/config.json`; the executor never reads
`/root/.docker` or another service secret under `/etc/autostream`. Runtime
Token bytes are not present in policy or grants. The dedicated credential-stage
operation is the narrow request exception: its private fixed Unix-socket wire
message carries the raw token to the root boundary without logging or durable
request storage. Rotation/recovery may read or atomically replace only the
fixed canonical/staged identity paths under `/etc/autostream-host-agent`; no
request can select another path, and generic operations cannot carry a token.
Every production credential operation requires the legacy
`/etc/autostream/host-agent.json` identity to be absent before mutation,
immediately before active-identity replacement, and before successful
completion. A visible legacy identity or an error proving its absence makes
the root executor fail closed without accepting a caller-selected exception.
Prepare the Docker credential before activating a Docker target:

```console
sudo install -d -o root -g root -m 0700 \
  /etc/autostream-local-executor/docker
sudo env HOME=/ DOCKER_CONFIG=/etc/autostream-local-executor/docker \
  docker login ghcr.io
sudo chown root:root \
  /etc/autostream-local-executor/docker/config.json
sudo chmod 0600 \
  /etc/autostream-local-executor/docker/config.json
```

The installer rejects any other entry in that credential directory. Docker
version pins are fixed files directly under
`/opt/autostream/local-executor/docker`; Docker port environments are under its
fixed `ports/<service>.env` subdirectory. Systemd port sidecars alone use the
separate `/opt/autostream/local-executor/ports/<service>.env` path. The
credential directory and every executor runtime directory/subdirectory are
`root:root 0700`; generated environment files are `root:root 0600`.

Docker authority is not supplied by the Host Agent request. The executor
accepts only `/usr/bin/docker`, project `autostream`, project directory
`/opt/autostream`, Compose file `/opt/autostream/compose.yml`, and the
read-only base environment `/opt/autostream/.env`. Each service has one fixed
Compose service, GHCR repository, and private version overlay:

| service type | Compose service | image repository | version overlay |
| --- | --- | --- | --- |
| `control_panel` | `control-panel` | `ghcr.io/kome-lab/autostream-docker/control-panel` | `docker/control-panel.env` |
| `encoder_recorder` | `encoder-recorder` | `ghcr.io/kome-lab/autostream-docker/encoder-recorder` | `docker/encoder-recorder.env` |
| `observability` | `observability` | `ghcr.io/kome-lab/autostream-docker/observability` | `docker/observability.env` |
| `discord_bot` | `discord-bot` | `ghcr.io/kome-lab/autostream-docker/discord-bot` | `docker/discord-bot.env` |
| `worker` | `worker` | `ghcr.io/kome-lab/autostream-docker/worker` | `docker/worker.env` |

Overlay paths in the table are relative to
`/opt/autostream/local-executor`. They are created with mode `0600`.
Docker port overlays are separate fixed files under
`/opt/autostream/local-executor/docker/ports/<service>.env`; they contain only
the service port mapping and configuration revision selected by the approved
plan.
Durable Docker checkpoints stay under
`/var/lib/autostream-local-executor`; the Compose project directory remains
read-only to the executor.

The systemd sandbox makes `/etc/autostream` inaccessible, so the executor
cannot read another service secret. Its dedicated MariaDB backup credential is
the read-only `/etc/autostream-local-executor/mariadb-backup.cnf`; the policy
and Docker credential also live under that separate configuration directory.
The only writable database backup paths are
`/var/backups/autostream/control-panel` and
`/var/backups/autostream/observability`; the unit does not grant the parent
backup tree. It grants `/etc/autostream-host-agent` only for the dedicated
fixed-path Runtime Token rotation/recovery implementation described above;
generic target mutation cannot access a caller-selected identity or token.
Writable release paths are listed per supported service; the unit never grants
write access to `/opt/autostream` as a whole.

## Install

From the extracted Host Agent release, activate the policy generated by
configure:

```console
sudo ./install/install-autostream-local-executor \
  --policy /etc/autostream-local-executor/policy.json
```

The installer:

- requires a regular `root:root 0600` policy source;
- requires only the unchanged adjacent Host Agent `.tar.gz` as release input;
- requires `flock` (from `util-linux`), `jq`, `sha256sum`, and `tar` on the
  server;
- validates it with the packaged binary before replacing any live file;
- requires its numeric Agent UID/GID to match the installed Host Agent;
- installs the policy as
  `/etc/autostream-local-executor/policy.json` (`root:root 0600`);
- installs the binary under `/usr/local/libexec`;
- creates a `root:autostream-host-agent 0750` runtime directory;
- creates `/var/lib/autostream-local-executor` as `root:root 0700` for the
  durable stage/apply/reconcile ledger;
- creates dedicated `root:root 0700` port and Docker-version directories under
  `/opt/autostream/local-executor` and the read-only Docker credential
  directory under `/etc/autostream-local-executor/docker`;
- keeps `/etc/autostream` inaccessible while making the separately
  operator-provisioned MariaDB backup credential read-only and granting only
  the two fixed Control Panel/Observability backup directories for writes;
- enables the `root:autostream-host-agent 0660` systemd Unix socket and starts
  the root service. The service may make outbound HTTPS connections but
  `SocketBindDeny=any` prevents it from opening an inbound network listener.

Verify the installed boundary:

```console
sudo /usr/local/libexec/autostream-local-executor validate-policy \
  --policy /etc/autostream-local-executor/policy.json
sudo systemctl status autostream-local-executor.socket
sudo systemctl status autostream-local-executor.service
sudo stat -c '%U:%G:%a %n' \
  /etc/autostream-local-executor/policy.json \
  /var/lib/autostream-local-executor \
  /run/autostream-local-executor \
  /run/autostream-local-executor/executor.sock
```

Record the printed `policy_sha256` in the server-owned Host Agent policy. Never
copy it from an unvalidated working file.

## Uninstall

Before uninstalling, stop new jobs and self-updates, wait for recovery to
finish, and revoke/disable the Host Agent Runtime Token and Node in the Control
Panel. Purging local files does not revoke a server-side credential.

The default path preserves both the root policy and durable executor state:

```console
sudo ./install/uninstall-autostream-local-executor
```

To remove the policy and state too:

```console
sudo ./install/uninstall-autostream-local-executor --purge
```

`--purge` is intentionally the first local removal command. Before touching
the durable Executor ledger it stops/disables the Host Agent, both fixed A/B
recovery timers, and both recovery service instances, then verifies that none
remains active or enabled. A freeze failure
leaves state untouched. Any later failure leaves these producers frozen so a
timer tick cannot recreate state; fix the reported condition and rerun
`--purge`.

The Host Agent account and its token-bearing identity are owned by the Host
Agent package and are never removed by this uninstaller.
Docker credentials, service port sidecars, and Docker version pins are target
configuration and are preserved by both uninstall modes.

Purge this Local Executor boundary before purging the Host Agent. This
uninstaller does not remove the Host Agent identity or Docker credential.
Identity deletion, mandatory unlink, and the SSD/copy-on-write/snapshot
physical-erasure caveat belong to the Host Agent purge procedure. Legacy
`ssh_v1` updater/helper/SSH/8090 assets are outside this package and remain
until the separate Bridge-removal release.
