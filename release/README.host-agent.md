# AutoStream Host Agent

This archive installs one non-root Host Pull Agent on one physical host. The
agent makes outbound HTTP(S) requests to the Control Panel and does not open an
inbound API or status port. It registers, heartbeats, refreshes server-owned
policy, and reports target availability. Once a positive ownership epoch and
matching executor-policy digest are active, the same outbound `pull_v2` loop
claims jobs and drives bounded stage/apply/reconcile operations through the
root local executor.

Install one Host Agent per physical host, not one Agent per service. A host
running both Control Panel and Observability uses one execution-host identity
whose server-owned policy contains both targets. No SSH key, `known_hosts`,
inbound updater port, or central updater daemon is part of this `pull_v2`
boundary.

## Verify the release

Verify the archive sidecar with `sha256sum --check --strict`, verify
`host-agent-manifest.json` and its sidecar, and verify the manifest provenance
attestation from `.github/workflows/release-host.yml` before extracting or
installing the archive. Extract the verified archive into a root-owned
directory whose parent chain is not group/other-writable. The installers also
bind every consumed release source by inode identity and SHA-256 while copying,
so a source swap fails closed; those checks do not replace the outer checksum
and attestation verification.

## Recommended first installation

Prepare the account, Host Agent binary/unit/state directory, and the
same-release local executor binary/service/socket/tmpfiles definitions with
one command:

```bash
sudo ./install/install-autostream-host-agent --prepare
```

This command verifies that all bundled executor preparation assets are present
and installs them atomically with the Host Agent. It creates neither
`/etc/autostream-host-agent/identity.json` nor `policy.json`. It creates
`/etc/autostream-local-executor`, `/opt/autostream/local-executor`, and
`/opt/autostream/local-executor/ports` as `root:root 0700`, then leaves both
services and the executor socket disabled and inactive. The fixed root A/B
self-update recovery timers are the exception: preparation enables and starts
them so an activation deadline can restore the healthy slot even when the new
Agent cannot run. An existing directory must already be a non-symlink
`root:root 0700` directory. A failed preparation keeps valid pre-existing
directories and removes only empty directories whose inodes were created by
that attempt.

Register the Update Agent service in the Control Panel and assign the systemd
targets on this physical host. A Control Panel or Observability target also
requires its exact existing database name in System Updates. The value is saved
as server-owned `database_name` and combined only with the compiled fixed
backup executable.

Before configure, install and test the fixed backup scripts, create the fixed
backup directories, and provide the dedicated root-only credential at
`/etc/autostream-local-executor/mariadb-backup.cnf`. The operator owns this
file: configure does not create or transmit the credential and never accepts
the database name in argv. It only projects the saved non-secret identifier
into the root policy.

Save the policy, then run the generated command. It has this form:

```bash
sudo /usr/local/bin/autostream-host-agent configure \
  --panel-url https://control.example.com \
  --node registered-update-agent-service-id \
  --config /etc/autostream-host-agent/identity.json
```

Enter the one-time Configure Token at the protected TTY/stdin prompt. It is
never accepted in argv or an environment variable. The command stages the
server-bound `pull_v2` identity and the Control Panel's canonical Local
Executor policy. It rejects an inbound API endpoint, a root Agent UID/GID, an
old configure protocol, incomplete applied target state, or a changed
stage/activation binding.

Configuration atomically installs:

- exactly four identity fields as `root:autostream-host-agent 0640`;
- `/etc/autostream-local-executor/policy.json` as `root:root 0600`;
- each missing fixed systemd port sidecar under
  `/opt/autostream/local-executor/ports` as `root:root 0600`.

Each sidecar contains exactly the service-specific bind variable and
`AUTOSTREAM_CONFIG_REVISION`, terminated by LF. An existing sidecar must match
the canonical bytes; configure never overwrites a different file. Policy,
sidecars, and identity are re-read and digest/revision-bound before activation.
The possible initial systemd sidecars are fixed to Control Panel, Worker,
Encoder Recorder, Discord Bot, and Observability. Control Panel receives an
initial canonical sidecar but remains ineligible for runtime port changes.
The four-field identity still does not receive or persist a host ID, ownership
epoch, target policy, GitHub token, SSH setting, or local command.

`/etc/autostream-host-agent/identity.json` is the canonical identity. The
legacy `/etc/autostream/host-agent.json` is a read-only runtime fallback only
when the canonical file is absent. A managed legacy installation binds the
source inode, metadata, and SHA-256 while installing the canonical file, then
attempts a best-effort zero overwrite/sync and requires the legacy secret to be
unlinked. If unlink fails, it keeps the recoverable canonical identity but
leaves the Agent stopped. If both files exist, the Agent and installer fail
closed instead of choosing one. New writes and Runtime Token rotation never
target the legacy path.

Activate the prepared root-owned boundary with the exact generated policy:

```bash
sudo ./install/install-autostream-local-executor \
  --policy /etc/autostream-local-executor/policy.json
```

The prepared executor entry point is the fixed symlink
`/usr/local/libexec/autostream-local-executor` ->
`/opt/autostream/host-agent/current/bin/autostream-local-executor`. The Local
Executor installer preserves that link only when `current` selects fixed slot
`a` or `b`, every directory in the resolved chain is root-owned and not
group/other-writable, the target is `root:root 0755`, and its SHA-256 matches
the executor in this same verified release. Any other symlink, writable
intermediate, cross-release binary, or ownership/mode drift fails closed.
Standalone upgrades from an existing regular `root:root 0755` binary remain
supported.

Only after both configuration and Local Executor activation succeed, explicitly
enable the Host Agent:

```bash
sudo systemctl enable --now autostream-host-agent.service
```

This separation guarantees that an unconfigured Host Agent cannot be enabled
or started by the installer. The first successful heartbeat remains
observe-only with ownership epoch zero. A separate Control Panel ownership
activation is required before this host can claim update or port jobs.

## Legacy one-step installation

For an already provisioned identity, copy
`autostream-host-agent.json.example` to a temporary root-controlled location
and replace every placeholder. The JSON object must contain exactly:

- `panel_url`
- `node_id` (the registered Update Agent service ID)
- `runtime_token`
- `service_name`

Do not add an API port, SSH setting, host ID, ownership epoch, target policy,
GitHub token, or local command. Host ownership is resolved by the Control
Panel's token/service binding, never from caller-controlled CLI values.

Do not pass the Runtime Token in an argument or environment variable. Keep the
prepared file under a root-owned, non-symlink parent chain as `root:root 0600`
until installation. The installer binds that source by inode identity and
SHA-256 while staging it. Then run:

```bash
sudo ./install/install-autostream-host-agent \
  --config /root/autostream-host-agent.json
```

This backward-compatible mode validates the identity with the packaged binary
before replacing the active files. It creates the dedicated
`autostream-host-agent` system user without supplementary groups, installs it as
`root:autostream-host-agent 0640`, and starts the sandboxed non-root unit.

Installed paths:

```text
/usr/local/bin/autostream-host-agent
/usr/local/libexec/autostream-local-executor
/etc/autostream-host-agent/identity.json
/etc/autostream-host-agent/identity.staged.json (only during token rotation)
/etc/autostream-host-agent/.identity.staged.wipe (temporary during exact-digest cleanup)
/etc/autostream-local-executor/policy.json
/etc/systemd/system/autostream-host-agent.service
/etc/systemd/system/autostream-local-executor.service
/etc/systemd/system/autostream-local-executor.socket
/etc/tmpfiles.d/autostream-local-executor.conf
/var/lib/autostream-host-agent/
/var/lib/autostream-local-executor/
/opt/autostream/local-executor/ports/
```

For database-owning targets the operator additionally provisions
`/etc/autostream-local-executor/mariadb-backup.cnf` as `root:root 0600` and the
fixed `/var/backups/autostream/control-panel` and/or
`/var/backups/autostream/observability` directory as `root:root 0700`. They are
not installed, rewritten, or removed by the Host Agent package.

The state directory is intentionally distinct from the legacy central updater's
`/var/lib/autostream-updater`.

## Verify

```bash
sudo systemctl status autostream-host-agent.service
sudo stat -c '%U:%G:%a %n' \
  /usr/local/bin/autostream-host-agent \
  /etc/autostream-host-agent/identity.json \
  /etc/systemd/system/autostream-host-agent.service \
  /var/lib/autostream-host-agent
sudo -u autostream-host-agent \
  /usr/local/bin/autostream-host-agent validate-config \
  --config /etc/autostream-host-agent/identity.json
sudo /usr/local/libexec/autostream-local-executor validate-policy \
  --policy /etc/autostream-local-executor/policy.json
sudo systemctl status autostream-local-executor.socket
sudo systemctl status autostream-local-executor.service
sudo ss -lntup
```

The expected file identities are `root:root:755`,
`root:autostream-host-agent:640`, `root:root:644`, and
`autostream-host-agent:autostream-host-agent:700`. The Host Agent must not
appear in the listening-socket output. `SocketBindDeny=any` also enforces that
boundary at the systemd service level.

## Rotate the Runtime Token

Do not use the generic immediate token replacement for a `pull_v2` Host Agent;
it fails with `staged_runtime_token_rotation_required`. The dedicated rotation
is:

1. an administrator stages a new credential;
2. the Agent claims it exactly once with the old token;
3. the Local Executor installs
   `/etc/autostream-host-agent/identity.staged.json` and returns a bound local
   acknowledgement;
4. the old-token heartbeat publishes that receipt and the staged token proves
   the same host, ownership, policy, and protocol fences;
5. the Control Panel activates the staged token, and only then the Local
   Executor atomically promotes it to the canonical identity and the old token
   is revoked.

Response loss is reconciled only for the exact claim ID/revision from the
durable claim and root ledger. The same credential may be re-exposed only to
that exact response-loss replay, never to a different claim, and the root
mutation is not reapplied. An unclaimed staged credential can be canceled
server-side. After claim, cancel first records root `cancel_ready`, wipes the
staged identity, then uses the old token for the Control Panel acknowledgement
before retiring the staged token and ledgers. Cancel does not revert an
activated identity.

Emergency revocation selects the previous or staged slot for audit binding,
but revokes both tokens, marks the Agent offline, clears its heartbeat,
capabilities, and sealed credential, and sets `recovery_required`. This is a
break-glass operation. Confirm `emergency_revoked` in the Control Panel and
record the rotation ID. Use this order:

1. stop `autostream-host-agent.service`;
2. generate a new one-time Configure Token for the same `pull_v2` Node and run
   the Control Panel's generated `autostream-host-agent configure` command;
3. run the root recovery command below;
4. restart `autostream-host-agent.service`.

Keep the same Node ID and Panel URL. Configure installs a new four-field
identity at the canonical path as `root:autostream-host-agent 0640`; the new
Runtime Token must differ from both revoked slots and is entered only at the
protected TTY/stdin prompt. If a Configure stage response is lost, local
identity installation fails, or activation expires, generate another new
Configure Token and rerun Configure while the Agent remains stopped. The new
stage invalidates the abandoned staged secret. Never put either token in argv,
an environment variable, logs, or shell history.

```bash
sudo systemctl stop autostream-host-agent.service

# Run the new generated configure command and enter its Configure Token at
# the protected prompt.
sudo /usr/local/bin/autostream-host-agent configure \
  --panel-url https://control.example.com \
  --node registered-update-agent-service-id \
  --config /etc/autostream-host-agent/identity.json

sudo /usr/local/libexec/autostream-local-executor \
  recover-runtime-credential \
  --rotation-id "<ROTATION_ID>" \
  --confirm-emergency-revoked

sudo systemctl restart autostream-host-agent.service
```

The root-only command accepts no caller-selected path or token. It verifies the
fixed identity, durable ledger, policy digest, host/policy/protocol fences, and
that the new identity digest differs from the locally known revoked slots. The
policy SHA-256 and source, projection, and Local Executor revisions must remain
unchanged across Configure; any mismatch fails closed and must not be bypassed
by editing the policy or ledger.

Recovery is immediate from `claim_prepared`, `cancel_ready`, `activated`, and
`expired`. It is also immediate from `stage_bound` only when the fixed staged
identity file does not exist. An exact staged file found with
`claim_prepared` compatibility state or `stage_bound` is bound and promoted to
`staged`, so it follows the TTL rule. `staged`, `local_staged`, and
`proof_ready` require the staged credential TTL to elapse. Before a staged
credential has been bound, `claim_prepared` has no staged-token hash and the
replacement is compared with the previous slot; after binding, both previous
and staged token hashes are checked.

After recovery records `manual_recovered` and exact-digest wipes the staged
identity through the fixed temporary quarantine path, restart the Host Agent
with the replacement identity. Its new-token poll/finalize pass removes the
remaining root ledger and server claim, then unlocks the next rotation. A
missing/new server directive does not silently retire a locally bound
`stage_bound` or later ledger after emergency revocation; explicit root
recovery is required. Do not manually delete the ledger, staged identity, or
wipe quarantine when recovery fails.

Migrate a legacy identity to the canonical path before attempting rotation.

## Host Agent and Local Executor self-update

Self-update uses a dedicated server directive and a dedicated short-lived,
one-time root grant, never a generic service-update command. The verified Agent
and Executor are staged together under fixed A/B slots at
`/opt/autostream/host-agent/slots/{a,b}`. Commit requires the new Agent
heartbeat and new Executor probe to prove the same pending generation and
protocol fences.

A fixed root recovery service/timer uses only the healthy-slot Executor to
restore the previous slot and restart the healthy Agent if activation expires
or the pending process dies. Before clearing the durable rollback fence, it
requires the Executor socket and service to remain active across a bounded
stability interval, requires one unchanged positive systemd `MainPID`, resolves
that PID's `/proc/<pid>/exe` to the exact healthy A/B slot binary, and verifies
the binary's version plus mutation/recovery protocol. It then restarts the
healthy Agent. Any failed check leaves `rolling_back` durable for the next
recovery pass. The pending binary cannot suppress or impersonate that rollback.

The root grant ledger is also reconciled after process or power loss without
persisting the raw token. A stable healthy runtime with a prepared or consumed
stage grant but no staged state records that exact attempt as
`failed_generation`, removes the orphan ledger durably, and reports the old
runtime so either a queued or atomically-staging Control Panel job terminates
as rolled back. An exact consumed receipt that already matches durable staged
state is marked applied without downloading or staging again. Reconcile grants
are burned or marked applied from the exact durable binding and state, then a
fresh grant is required for any later root mutation. Contradictory bindings
fail closed and do not change A/B state.

Source and focused local tests are not production evidence: a published
immutable Host Release, checksums/attestation, real GitHub download, real
systemd restart/reboot tests, amd64/arm64 canaries, and production
release/deployment have not been performed by this change.

## Remove

First stop new jobs, rotations, and self-updates and confirm there is no active
or recovery state. In the Control Panel, revoke the Runtime Token and disable
or delete the Node, then verify the old token is rejected. Local removal alone
does not revoke a server-side token.

Purge the Local Executor before purging the Host Agent:

```bash
sudo ./install/uninstall-autostream-local-executor --purge
```

This first purge step stops and disables the Host Agent, both fixed A/B
recovery timers, and both recovery service instances, then proves every
producer inactive before moving or deleting Executor state. If a producer
cannot be frozen, state is left in place and purge fails. If a later purge step
fails, those producers deliberately remain stopped/disabled; correct the
reported path or state issue and rerun the same command before the Host Agent
purge.

The default uninstaller removes the binary and unit but preserves identity and
state for recovery:

```bash
sudo ./install/uninstall-autostream-host-agent
```

After confirming that no migration or diagnostic state is needed, remove the
four-field identity, state, user, and group explicitly:

```bash
sudo ./install/uninstall-autostream-host-agent --purge
```

The Host Agent purge refuses to proceed while the Local Executor boundary is
still active or installed. It removes canonical, staged, and legacy identities
only after strict path/owner/mode checks. Zero overwrite and file/directory
sync are best-effort; unlink is mandatory and a surviving identity makes purge
fail. Even a successful unlink does not guarantee physical erasure on SSD,
copy-on-write filesystems, snapshots, or backups.

The legacy central Updater, SSH helper, keys, and port 8090 assets are
independent and are not removed by either command. Keep them during the Bridge.
Remove them only in a later release after every host has moved to `pull_v2`,
canary and rollback drills have passed, and no active/recovery job remains.

## Control Panel writer rollback

Before rolling the Control Panel back to an older binary, confirm that no
update, recovery, or token-rotation job is active, deactivate `pull_v2`
ownership in System Updates, and stop this Host Agent. Complete a
`single-writer` drain by stopping every Control Panel instance before starting
exactly one old or new writer; never run both generations concurrently.

Treat a pre-059 Control Panel binary as read-only for updater policy settings.
Do not save System Updates settings with the old binary. If it advances an
updater policy revision, keep ownership inactive and roll forward to the
current Control Panel as the sole writer. Re-save the exact MariaDB database
names for all Control Panel and Observability targets in **Application Info >
System Updates**, rerun the generated Host Agent configure command, and
validate the Local Executor policy. Start the Agent and reactivate ownership
only after the configure revision and target observations are applied.
