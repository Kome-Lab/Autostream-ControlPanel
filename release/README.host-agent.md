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

For a claimed software update, every Stage error preserves the active immutable
plan because a response failure or a root-ledger durability error can make the
Stage result uncertain. The next lease generation performs reconcile only,
without restaging or reapplying that job. A `stage_required` response means the
exact job has no durable mutation ledger or apply-authorized state. It never
claims that no staged directory existed: before returning it, the root executor
safely removes only the exact orphan stage left before ledger commit and fsyncs
the parent, or fails closed with `state_unavailable`. The Agent then reports
terminal `failed` at 100 percent with code `remote_stage_missing`; matching
durable executor state is reconciled instead. Once the job reports
`reconciling` at 99 percent, its next accepted report is a terminal 100-percent
result; progress never returns to `health_checking` at 90.

A stale lease or sequence rejection drops only the unusable report cursor while
retaining the active job and plan for exact recovery. The Panel must return a
structured terminal job before the Agent clears an active cursor; a bare
`clear_active_job_id` boolean is rejected, and the terminal job must match the
active immutable intent. A terminal report is acknowledged only when its HTTP
success body exactly matches the committed job/Agent IDs, lease generation,
sequence, status, progress, code, and result digests. v1.9.9 and v1.9.10 Agents
predate this terminal-proof contract. Panel v1.9.11 returns
`system_update_terminal_proof_upgrade_required` with HTTP 409 to a registered
Agent older than v1.9.11 before terminal cursor recovery, so a legacy client
cannot interpret the structured proof as a bare clear. Do not create a
replacement job, delete either journal/ledger, or manually restage/reapply
while recovery is pending.

## Verify the release

Download `autostream-host-agent_vX.Y.Z_linux_amd64.tar.gz` on the operator
machine and verify the archive attestation from
`.github/workflows/release-host.yml`. The SHA-256 sidecar,
`host-agent-manifest.json`, and its sidecar remain published for automatic
updater compatibility; the manual installers neither require nor read them.

```bash
gh attestation verify /tmp/autostream-host-agent_vX.Y.Z_linux_amd64.tar.gz \
  --repo Kome-Lab/Autostream-ControlPanel \
  --signer-workflow Kome-Lab/Autostream-ControlPanel/.github/workflows/release-host.yml \
  --deny-self-hosted-runners
```

`--deny-self-hosted-runners` constrains the job that issues this attestation.
The expensive compilation, integration tests, and packaging run on the trusted
Blacksmith build job; the GitHub-hosted publication job independently checks the
downloaded artifact set and digests before attesting and publishing it. This
flag does not claim that compilation ran on a GitHub-hosted runner.

Transfer that one unchanged archive to the server. The server requires `flock`
(from `util-linux`), `jq`, `sha256sum`, `tar`, and systemd. Copy and extract it
only through the fixed root-owned artifact directory:

```bash
sudo install -d -o root -g root -m 0755 /opt/autostream/releases/artifacts
sudo install -o root -g root -m 0644 /tmp/autostream-host-agent_vX.Y.Z_linux_amd64.tar.gz /opt/autostream/releases/artifacts/
cd /opt/autostream/releases/artifacts
sudo tar --no-same-owner --no-same-permissions -xzf autostream-host-agent_vX.Y.Z_linux_amd64.tar.gz
cd autostream-host-agent_vX.Y.Z_linux_amd64
```

Leave the original archive adjacent to the extracted directory until both Host
Agent and Local Executor installation commands are complete. Each privileged
entry point independently stable-copies and safely re-extracts that archive,
verifies the complete internal checksum inventory and exact
`artifact-manifest.json`, and binds the complete binary build identities before
persistent mutation.

## Recommended first installation

Prepare the account, Host Agent binary/unit/state directory, and the
same-release local executor binary/service/socket/tmpfiles definitions with
one command:

```bash
sudo ./install/install-autostream-host-agent --prepare
```

This command verifies that all bundled executor preparation assets are present
and installs them atomically with the Host Agent. It does not write partial
self-update slot binding markers. It creates neither
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
the canonical bytes; normal configure never overwrites a different file.
Policy, sidecars, and identity are re-read and digest/revision-bound before activation.
The possible initial systemd sidecars are fixed to Control Panel, Worker,
Encoder Recorder, Discord Bot, and Observability. Control Panel receives an
initial canonical sidecar but remains ineligible for runtime port changes.
The four-field identity still does not receive or persist a host ID, ownership
epoch, target policy, GitHub token, SSH setting, or local command.

### Recover one verified live systemd sidecar

`--adopt-live-systemd-sidecar` is a narrow, explicit recovery mode for a host
where exactly one existing sidecar still matches the current root policy but a
service is already running on the staged policy's different loopback port. Do
not use it for initial installation, ordinary port changes, hand-edited files,
multiple mismatches, or Control Panel itself. A missing sidecar is never the
adoption candidate and remains governed by normal create-if-absent behavior
for the other managed targets. Explicit adoption still requires exactly one
differing existing sidecar.

A v1.9.7 binary does not provide this recovery flag. On a managed v1.9.7 host,
first verify and extract the version-matched v1.9.8 Host Agent archive, leave
its unchanged archive adjacent to the extracted directory, and run the managed
Host bridge upgrade:

```bash
cd /opt/autostream/releases/artifacts/autostream-host-agent_v1.9.8_linux_amd64
sudo ./install/install-autostream-host-agent --upgrade
```

That installer upgrades the Host Agent and Local Executor as one matched pair
and performs their bounded installer-controlled restarts. Those restarts must
finish successfully to install the new flag. After the upgrade succeeds, do
not delete or edit the sidecar and do not manually restart the Host Agent or
the affected target service until explicit adoption Configure finishes.

Only after that upgrade succeeds, issue a fresh Configure Token for the same
Panel URL and Node, then add only the boolean recovery flag to the generated
command. Enter the token only at the protected TTY/stdin prompt; it is never
accepted in argv or an environment variable:

```bash
sudo /usr/local/bin/autostream-host-agent configure \
  --panel-url https://control.example.com \
  --node registered-update-agent-service-id \
  --config /etc/autostream-host-agent/identity.json \
  --adopt-live-systemd-sidecar
```

The command accepts no caller-selected service, path, port, revision, digest,
or force value. Before changing a canonical pathname it requires the current
managed identity and policy to bind the same Panel, Node, host, Agent UID/GID,
and fixed target profile; all three policy revisions must strictly advance;
the endpoint and config revisions must be unchanged; and the only target
change may be its loopback port and locally derived sidecar SHA-256. Exactly
one existing sidecar must be the canonical bytes for the current policy.
Worker, Encoder Recorder, Discord Bot, and Observability are eligible;
Control Panel is not.

Recovery also refuses any active or applied port ledger. On Linux it verifies
the fixed unit ID and final `EnvironmentFile`, managed release checksums and
executable, stable MainPID/start time/cgroup, listener ownership on the staged
port, the unused old port, and direct no-proxy `/health` and
`/updater/version` identity/version/config revision. It performs no restart or
daemon reload. Only after that proof does it atomically exchange the one
root-owned `0600` sidecar, fsync the directory, and repeat the same live proof.
The exact old inode is retained until policy and identity installation
succeeds, then removed with another durability fence. A known pre-identity
failure exchanges the old inode back; an ambiguous result is preserved and
reported rather than overwritten or blindly rolled back.

If recovery fails after staging, the Configure Token is consumed. Leave both
services in their reported state, do not restart the Host Agent, inspect the
error, issue a new Configure Token, and retry only after the unsafe or
concurrent condition has been removed. A crash after the sidecar exchange can
leave the verified live sidecar with the prior policy; Local Executor mutation
then fails closed, and a fresh normal configure for the same staged target
converges because the sidecar is already byte-exact.

`/etc/autostream-host-agent/identity.json` is the canonical identity. The
legacy `/etc/autostream/host-agent.json` is a read-only runtime fallback only
when the canonical file is absent and reachable. The non-root Agent securely
loads and validates the canonical identity before checking that fallback. On a
normal application host, `/etc/autostream` remains `root:root 0750`; an
`EACCES` result for the legacy pathname cannot influence an already validated
canonical identity, so the Agent keeps using the canonical file without
weakening the application-secret directory. A missing canonical identity plus
an inaccessible legacy path still fails closed, as does any visible current
and legacy pair or unexpected filesystem error.

Every root-owned identity writer has the stronger view: configure checks that
the legacy path is absent before the Configure Token is read, immediately
before identity installation, and again after installation. Runtime Token
rotation performs the same checks before mutation, immediately before the
active identity replacement, and before reporting completion. A managed
legacy installation binds the source inode, metadata, and SHA-256 while
installing the canonical file, then attempts a best-effort zero overwrite/sync
and requires the legacy secret to be unlinked. If unlink fails, it keeps the
recoverable canonical identity but leaves the Agent stopped. New writes never
target the legacy path.

v1.9.10 and later remove the need for the temporary execute-only ACL workaround on
`/etc/autostream`. On every affected v1.9.9 host where that directory is
`root:root 0750`, keep one bounded execute-only ACL bridge through the upgrade,
even if the old Agent is currently active. `--upgrade` deliberately requires a
healthy A/B runtime, and a failed candidate must be able to restart v1.9.9
during rollback. Use the following block only after installing and verifying
the matching current Control Panel and Host archive. It proves the installed Host Agent
is exactly v1.9.9, the canonical identity is valid, both forms of the legacy
pathname are absent, the parent is the expected non-symlink directory, and any
pre-existing Host Agent ACL is exactly the known access-only `--x` bridge:

```bash
set -euo pipefail
sudo /usr/local/bin/autostream-host-agent --version |
  grep -Fx 'autostream-host-agent v1.9.9'
sudo /usr/local/bin/autostream-host-agent validate-config \
  --config /etc/autostream-host-agent/identity.json
sudo test ! -e /etc/autostream/host-agent.json
sudo test ! -L /etc/autostream/host-agent.json
sudo test -d /etc/autostream
sudo test ! -L /etc/autostream
sudo env LC_ALL=C stat -c '%F %U:%G %a' -- /etc/autostream |
  grep -Fx 'directory root:root 750'
sudo apt-get update
sudo apt-get install -y --no-install-recommends acl
command -v getfacl
command -v setfacl
sudo getfacl -cp -- /etc/autostream
! sudo getfacl -cp -- /etc/autostream |
  grep -q '^default:user:autostream-host-agent:'
if sudo getfacl -cp -- /etc/autostream |
  grep -q '^user:autostream-host-agent:'; then
  sudo getfacl -cp -- /etc/autostream |
    grep -Fx 'user:autostream-host-agent:--x'
else
  sudo setfacl --modify 'u:autostream-host-agent:--x' -- /etc/autostream
fi
sudo getfacl -cp -- /etc/autostream |
  grep -Fx 'user:autostream-host-agent:--x'
sudo -u autostream-host-agent \
  /usr/local/bin/autostream-host-agent validate-config \
  --config /etc/autostream-host-agent/identity.json
sudo systemctl restart autostream-host-agent.service
sudo systemctl is-active --quiet autostream-host-agent.service
cd /opt/autostream/releases/artifacts/autostream-host-agent_vX.Y.Z_linux_amd64
sudo ./install/install-autostream-host-agent --upgrade
sudo /usr/local/bin/autostream-host-agent --version |
  grep -Fx 'autostream-host-agent vX.Y.Z'
sudo /usr/local/libexec/autostream-local-executor --version |
  grep -Fx 'autostream-local-executor vX.Y.Z'
```

If the directory is not the exact `root:root 0750` non-symlink above, or an
unexpected access/default Host Agent ACL is present, stop instead of changing
permissions. After the upgrade, verify the new binary as the service user,
confirm that no legacy pathname including a dangling symlink exists, remove
only the exact named access ACL, validate again without it, and restart the
Agent:

```bash
set -euo pipefail
sudo /usr/local/bin/autostream-host-agent --version |
  grep -Fx 'autostream-host-agent vX.Y.Z'
sudo /usr/local/libexec/autostream-local-executor --version |
  grep -Fx 'autostream-local-executor vX.Y.Z'
sudo -u autostream-host-agent \
  /usr/local/bin/autostream-host-agent validate-config \
  --config /etc/autostream-host-agent/identity.json
sudo test ! -e /etc/autostream/host-agent.json
sudo test ! -L /etc/autostream/host-agent.json
sudo test -d /etc/autostream
sudo test ! -L /etc/autostream
sudo env LC_ALL=C stat -c '%F %U:%G %a' -- /etc/autostream |
  grep -Fx 'directory root:root 750'
! sudo getfacl -cp -- /etc/autostream |
  grep -q '^default:user:autostream-host-agent:'
sudo getfacl -cp -- /etc/autostream |
  grep -Fx 'user:autostream-host-agent:--x'
sudo setfacl --remove 'u:autostream-host-agent' -- /etc/autostream
! sudo getfacl -cp -- /etc/autostream |
  grep -Eq '^(default:)?user:autostream-host-agent:'
sudo -u autostream-host-agent \
  /usr/local/bin/autostream-host-agent validate-config \
  --config /etc/autostream-host-agent/identity.json
sudo systemctl restart autostream-host-agent.service
sudo systemctl is-active --quiet autostream-host-agent.service
```

Do not use `setfacl -b`: unrelated pre-existing ACLs belong to the operator.
Do not use `chmod 0751`, add the Agent to an application group, or grant read
permission. If the exact named entry was not added as this workaround, stop
and review it instead of deleting it. On arm64, use the verified arm64 vX.Y.Z
release directory instead of the amd64 path above.

The Ubuntu `acl` package is needed only for this bounded v1.9.9 recovery and
cleanup; it is not a Host Agent runtime dependency. If package installation is
not authorized or the repository cannot be reached, stop instead of weakening
the directory mode manually.

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

## Upgrade an existing managed Host runtime

Verify and transfer the new Host Agent archive exactly as described above,
leave the unchanged archive adjacent to its extracted directory, and run:

```bash
cd /opt/autostream/releases/artifacts/autostream-host-agent_vX.Y.Z_linux_amd64
sudo ./install/install-autostream-host-agent --upgrade
```

`--upgrade` is only for an already configured, healthy managed A/B install. It
upgrades the Host Agent and Local Executor as one version-matched pair while
preserving `/etc/autostream-host-agent/identity.json` and
`/etc/autostream-local-executor/policy.json`. Except for the narrowly bounded
recovery-service migration below, the installed systemd and tmpfiles templates
must be byte-identical to the new bundle. Before running it, confirm that the
active Control Panel satisfies the bundle's `minimum_panel_version`; the
offline host cannot independently prove a remote Panel version.

### Explicit active-job recovery for v1.9.11

This recovery option is limited to an existing managed A/B runtime whose live
Host Agent and Local Executor are an exact paired v1.9.9 or v1.9.10 release.
Their version, commit, and build date must match; both public binary links and
both systemd processes must resolve to the same current slot, and the live
Executor must expose mutation and recovery protocol 2. The old Agent must begin
enabled and active with a live MainPID. The Executor service and socket plus
both fixed recovery timers must be active; the Agent, socket, and timers must
be enabled. This is not a generic bridge for every Agent older than v1.9.11, a
mixed-version pair, a standalone install, or an inactive or mismatched
Executor.

When that exact v1.9.9 or v1.9.10 pair has an active journal, it remains a
blocker to ordinary `--upgrade`. Install and verify the Control Panel v1.9.11
release first, then verify and extract the matching v1.9.11 Host archive and
run only this explicit recovery form:

```bash
cd /opt/autostream/releases/artifacts/autostream-host-agent_v1.9.11_linux_amd64
sudo ./install/install-autostream-host-agent --upgrade --recover-active-job
```

Use the matching `linux_arm64` archive and directory on arm64. There is no
automatic recovery mode: normal `--upgrade` continues to reject an active job.
Do not issue a Configure Token or rerun `configure`. The installer verifies the
exact active journal, arms a transient systemd restart guard, stops the old
Agent, and runs the verified candidate once as the existing
`autostream-host-agent` account. That recovery-only process is bound to the
journal's active job and may reconcile and report it; it cannot claim a new job
or invoke Stage or Apply. The paired A/B upgrade begins only after strict
terminal proof has safely cleared that exact active cursor. The Agent remains
intentionally stopped during that handoff; the live Executor and the stopped
on-disk Agent must still prove the pinned, exact pre-stop pair.

The guard is disarmed only after an active managed Agent is proved. On any
recovery or upgrade failure, the installer first reacquires the canonical
setup and lifecycle locks, restores and proves the exact previous pair, and
restarts the previous Agent. If it cannot reacquire those locks or prove that result,
the Agent remains stopped and the guard remains armed. Do not delete or edit
the Agent journal, root ledger, staged release, or Panel update row, and do not
create a replacement job, restage, or reapply. Preserve those durable records
and retry the same explicit command only after fixing the reported cause.

### Forward-only recovery service migration

The recovery service template is the only unit-change exception handled by
`--upgrade`. The verified bundle must contain the corrected template with
SHA-256
`d0a994dc4a0dc5dd27131f3878de4e9652d5679a4681174660249b66eb1813fd`.
The installed template at the canonical path
`/etc/systemd/system/autostream-host-self-update-recovery@.service` must be
either that exact corrected file or the one exact legacy file with SHA-256
`751c69c970407b4873d403971a192b33320d44b352aba58a9ab56c2fa1e1309c`.
Any other candidate or installed digest fails closed before unit mutation.

For both `autostream-host-self-update-recovery@a.service` and `@b.service`,
PID 1's effective `FragmentPath` must resolve to that canonical template. The
optional transitional directory
`/etc/systemd/system/autostream-host-self-update-recovery@.service.d` may be
absent or contain only either or both of these exact, regular, single-link
`root:root 0644` files under a `root:root 0755` directory:

- `10-executable-guard.conf`, SHA-256
  `264b1b3e55d6f4551af36daa2cc34d19baa162b21b0c724d0c62459eefe006fe`;
- `20-bootstrap-state-guard.conf`, SHA-256
  `1964442535eb9f85ce594cb54c880fd8b92338951e3e103fac1fd5b88c85bf10`.

The effective `DropInPaths` must match exactly the known files present on disk.
An unknown drop-in, fragment override, modified known file, unsafe metadata, or
unexpected daemon-reload state fails closed without replacing the template.
The retry-only exception is `NeedDaemonReload=yes` after the corrected template
has already been installed; even then, every effective drop-in path must still
be one of the two known paths.

Only after the normal durable blocker checks pass does the installer atomically
replace the exact legacy template, sync the file and parent directory, and run
`systemctl daemon-reload`. It then removes the known transitional drop-ins,
syncs their removal, reloads again, and requires both recovery instances to
report the canonical `FragmentPath`, empty `DropInPaths`, and
`NeedDaemonReload=no`. An already-corrected, fully converged installation is an
idempotent no-op. If the first reload fails after replacement, the corrected
template is retained and the known drop-ins remain for a later `--upgrade` retry
to converge.

When no durable Host self-update state exists, a recovery instance left in
`failed` with `MainPID=0` by the legacy bootstrap may receive
`systemctl reset-failed` only after the corrected unit has converged. A running
instance, a nonzero PID, or any other unexpected state remains a blocker. This
reset does not erase an existing self-update state or bypass any job, grant,
rotation, or checkpoint check.

This unit migration is forward-only. A later activation failure may switch the
A/B runtime back to the previous healthy Agent and Executor, but it never
restores the legacy recovery template or its transitional drop-ins. The
artifact manifest's `rollback_compatible: true` describes that paired A/B
runtime and state-protocol rollback; it does not promise byte-for-byte rollback
of this root-owned unit migration or authorize a downgrade to an unknown unit.

The command rejects a downgrade, a mixed installed Agent/Executor pair,
same-version content drift, an unsafe slot/link, or an in-progress Agent job,
credential rotation, service update, port update, any self-update grant, or an
unsafe/non-terminal update checkpoint. A grant, including terminal `applied` or
`failed` state, is a
read-only blocker and remains byte-for-byte unchanged; wait for the normal
healthy-slot Local Executor to converge it before retrying. Terminal
`succeeded` and `rolled_back` checkpoints are read-only non-blockers and remain
byte-for-byte unchanged. Re-running the exact already-active release is an
idempotent success only after the same durable blocker checks. Do not invoke
the internal `manual-upgrade-host-runtime` Executor subcommand directly; the
archive installer supplies its verified, credential-free artifact binding.
An exact same-version runtime may still perform the one-time forward-only
recovery service migration above; different same-version binary content remains
rejected.

During an accepted update the installer takes the shared Host setup and
lifecycle locks, the legacy update-host installer lock, plus every fixed,
policy-projected, and installed legacy helper target lock. It durably stages
the inactive slot and records the activation
fence before stopping the Agent, then switches `current` atomically. It verifies
the new Executor and Agent with
stable `MainPID`, exact `/proc/<pid>/exe`, version/commit/protocol identity, and
the Executor watchdog handshake before committing. An activation failure
switches back to the previous healthy slot and verifies both old processes. If
rollback itself cannot finish, the durable `rolling_back` fence remains for the
fixed recovery timers; the command does not report success.

Before the durable activation fence, a normal error synchronously restores slot
artifacts and removes invocation-created bootstrap state. A power loss in that
small bootstrap window may leave a compatible stable state file for the old
recovery binary. Once `activating` is durable, rollback deliberately follows the
same schema-v2 contract as a Control Panel update: `failed_generation` and the
failed inactive candidate may remain. Identity, policy, journals, target/port
ledgers, and pre-existing checkpoints are not rewritten by rollback.

The offline state binding proves which verified archive bytes the root operator
selected, but it is not publisher authentication. Perform the GitHub artifact
attestation check on the operator machine before transfer.

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
/etc/systemd/system/autostream-host-self-update-recovery@.service
/etc/systemd/system/autostream-host-self-update-recovery@.timer
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
  /etc/systemd/system/autostream-host-self-update-recovery@.service \
  /etc/systemd/system/autostream-host-self-update-recovery@.timer \
  /var/lib/autostream-host-agent
sudo sha256sum \
  /etc/systemd/system/autostream-host-self-update-recovery@.service
sudo systemctl show \
  autostream-host-self-update-recovery@a.service \
  autostream-host-self-update-recovery@b.service \
  --no-pager \
  --property=FragmentPath \
  --property=DropInPaths \
  --property=NeedDaemonReload
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
boundary at the systemd service level. The recovery service checksum must be
`d0a994dc4a0dc5dd27131f3878de4e9652d5679a4681174660249b66eb1813fd`.
Both recovery instances must report
`FragmentPath=/etc/systemd/system/autostream-host-self-update-recovery@.service`,
an empty `DropInPaths=`, and `NeedDaemonReload=no`.

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
