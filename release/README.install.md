# AutoStream Control Panel Host Install

This archive contains the Control Panel Linux binary, systemd example,
placeholder configuration, and matching web assets. The current automatic
update architecture uses the separate `autostream-host-agent` release artifact
on every physical host. Its outbound-only `pull_v2` Agent drives a root Local
Executor over a private Unix socket. The standalone central
`autostream-updater` and SSH `autostream-update-host` path are legacy migration
inputs and are not installed by this guide.

In `pull_v2`, a Stage error is not proof that the root-owned ledger is absent.
The Host Agent preserves the immutable plan and the next lease generation uses
reconcile only, without restaging or reapplying. A `stage_required` response
means that the exact job has no durable mutation ledger or apply-authorized
state. It does not claim that no staged directory existed: the root executor
first safely removes only an exact orphan stage left before ledger commit and
fsyncs its parent, or fails closed with `state_unavailable`. Only then may the
Agent finish as `failed`/100 with `remote_stage_missing`; matching durable state
is reconciled instead. After `reconciling`/99, progress moves directly to a
terminal 100-percent result and never back to `health_checking`/90.

A permanently stale lease or sequence drops only that unusable report cursor;
the active job and plan remain for an exact recovery lease. The Panel never
authorizes an active-cursor clear with a bare boolean: it returns a structured
terminal job, which the Agent must match to the complete active immutable
intent. A successful terminal report must also return the committed job body
with matching job/Agent IDs, lease generation, sequence, status, progress,
code, and result digests before the Agent acknowledges it. v1.9.9 and v1.9.10
Agents predate this terminal-proof contract. Panel v1.9.11 returns
`system_update_terminal_proof_upgrade_required` with HTTP 409 to a registered
Agent older than v1.9.11 before terminal cursor recovery, so a legacy client
cannot interpret the structured proof as a bare clear. Do not create a
replacement job, cancel the nonterminal job, delete journals/ledgers, or invoke
a manual restage/reapply while recovery is pending.

## Requirements

- Linux amd64 or arm64 matching the archive name.
- On the operator machine: `gh` for GitHub artifact-attestation verification.
- On the server: `flock` (from `util-linux`), `jq`, `sha256sum`, `tar`, systemd, and
  `/usr/bin/mariadb-dump`. The installer creates the dedicated `autostream`
  account when it is missing.
- One unchanged Control Panel `.tar.gz` from the immutable GitHub Release.
- The matching verified `autostream-host-agent_<version>_linux_<arch>.tar.gz`
  artifact for the host.
- A reverse proxy with HTTPS for production.
- A production database and secret values supplied outside Git.

## Install or migrate the Control Panel

Download `autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz` on the operator
machine and verify its GitHub artifact attestation before transferring it. For
arm64, use the arm64 archive instead. The SHA-256 sidecar and external release
manifest remain published for automatic updater compatibility, but the manual
installer neither requires nor reads them.

```bash
gh attestation verify /tmp/autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz \
  --repo Kome-Lab/Autostream-ControlPanel \
  --signer-workflow Kome-Lab/Autostream-ControlPanel/.github/workflows/release-host.yml \
  --deny-self-hosted-runners
```

`--deny-self-hosted-runners` constrains the job that issues this attestation.
The expensive compilation, integration tests, and packaging run on the trusted
Blacksmith build job; the GitHub-hosted publication job independently checks the
downloaded artifact set and digests before attesting and publishing it. This
flag does not claim that compilation ran on a GitHub-hosted runner.

サーバーへ転送する release asset は、この `.tar.gz` 1 個だけです。Upload
it to `/tmp`, then copy it into the fixed root-owned artifact directory,
extract it as root, and leave the unchanged archive adjacent to the extracted
directory until installation completes:

```bash
sudo install -d -o root -g root -m 0755 /opt/autostream/releases/artifacts
sudo install -o root -g root -m 0644 /tmp/autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz /opt/autostream/releases/artifacts/
cd /opt/autostream/releases/artifacts
sudo tar --no-same-owner --no-same-permissions -xzf autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz
cd autostream-control-panel_vX.Y.Z_linux_amd64
sudo ./install-autostream-control-panel
```

The installer makes a stable private copy of the adjacent archive, computes its
SHA-256, rejects unsafe archive entries, safely re-extracts it, and verifies
the checksummed `artifact-manifest.json`, architecture, and complete Control
Panel/updater build identities before changing the host. It then creates the
`autostream` account when needed, seeds the rollback release, installs the unit
and backup executable, creates state directories, and exposes:

- `/usr/local/bin/control-panel`
- `/usr/share/autostream-control-panel`

Existing direct-install files are retained under
`/var/backups/autostream/install-migrations/control-panel`. Existing
`/etc/autostream/control-panel.env` content is preserved byte-for-byte.
`/opt/autostream/control-panel/releases` and
`/opt/autostream/control-panel/current` are installer-owned updater and
rollback state; do not edit or recreate them manually.

## Prepare the updater backup command

A Control Panel target is fail-closed unless its fixed backup command exists
and succeeds. The installer creates the private backup directory, installs
`/usr/local/sbin/autostream-backup-control-panel`, and safely creates or
preserves `/etc/autostream-local-executor/mariadb-backup.cnf`. Only the real
database credentials, account, grant, and database name remain manual.

Set the root-only defaults file to a dedicated backup account. It deliberately
lives outside `/etc/autostream`, which remains invisible to the Local Executor.
A shared Control Panel and Observability host may reuse this account/file after
granting both databases separately:

```ini
[client]
host=127.0.0.1
port=3306
protocol=tcp
user=autostream_backup
password=replace-with-a-long-random-password
```

From an interactive MariaDB root session, create the account if necessary.
Replace the password before executing the `CREATE USER` statement; do not put
the real password in a shell command or shell history:

```sql
CREATE USER IF NOT EXISTS 'autostream_backup'@'127.0.0.1' IDENTIFIED BY 'replace-with-a-long-random-password';
```

The script defaults to `autostream_control_panel`. If `DATABASE_URL` uses a
different database, pass its exact name as the single fixed argument. The name
must contain 1-64 ASCII letters, digits, underscores, or hyphens and must start
with a letter or digit.

Select the database name once below, then keep the same shell open. The same
exact `DATABASE_NAME` must be used for the MariaDB grant, the real dump, and the
server-owned target setting. In this example, replace the default with the
final path component of the real `DATABASE_URL` when they differ:

```bash
set -euo pipefail
DATABASE_NAME='autostream_control_panel'
if [[ ! "$DATABASE_NAME" =~ ^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$ ]]; then
  echo "Invalid DATABASE_NAME" >&2
  exit 1
fi

sudo mariadb <<SQL
GRANT SELECT, SHOW VIEW, TRIGGER ON \`${DATABASE_NAME}\`.* TO 'autostream_backup'@'127.0.0.1';
SQL

test "$(sudo stat -c '%u:%a' /etc/autostream-local-executor/mariadb-backup.cnf)" = "0:600"
test "$(sudo stat -c '%u:%a' /usr/local/sbin/autostream-backup-control-panel)" = "0:700"
sudo /usr/local/sbin/autostream-backup-control-panel "$DATABASE_NAME"
printf 'Database name to save in System Updates: %s\n' "$DATABASE_NAME"
```

Save this exact database name in **Application Info > System Updates** on the
Control Panel target. It is persisted as the server-owned `database_name` and
combined only with the compiled fixed backup executable. Do not edit
`/etc/autostream-local-executor/policy.json`; the Host Agent configure flow
generates it from the saved target settings. The configure command, Host Agent
identity, and update jobs never accept an arbitrary backup executable or argv.

The script uses `umask 077` and atomically renames a timestamped, non-empty
dump only after `mariadb-dump` succeeds. Configure retention and encrypted
off-host copying separately. The updater rejects a missing backup executable,
a symlink, or a path that is not root-owned or is writable by group/other
users; a nonzero dump exit aborts the update before stopping the Control Panel.

## Review settings and start the service

Edit `/etc/autostream/control-panel.env` with the real environment-specific
values. Keep this public web path:

```ini
AUTOSTREAM_WEB_DIR=/usr/share/autostream-control-panel
```

For a first installation, run:

```bash
sudo systemctl enable --now autostream-control-panel
sudo systemctl status autostream-control-panel
```

For an already-running older installation, the installer preserves its
settings and running process. Restart explicitly after reviewing the settings:

```bash
sudo systemctl restart autostream-control-panel
sudo systemctl status autostream-control-panel
```

Use the host's configured loopback port if it differs from `8080`.
`/updater/version` is the loopback endpoint used by the update helper. The
existing Control Panel `/version` route remains the authenticated Application
Info API and must not be configured as a target `version_url`. Block the exact
`/updater/version` path at any public reverse proxy.

Do not fabricate `.artifact-sha256` or `.version` from an unverified local
binary. External checksum/manifest assets are updater inputs, not manual
installer inputs. Never modify or recompress an attested archive.

Do not commit real `.env` files, provider credentials, tokens, SSH private
keys, logs, screenshots, or verification records.

## Install the pull_v2 Host Agent and Local Executor

Install one Host Agent for the physical host, not one Agent per service. On a
host that runs both Control Panel and Observability, assign both the synthetic
`control-panel` target and the registered Observability service to that one
execution host.

Before configuring the Agent:

- seed verified managed `current` releases for both services;
- make both systemd units execute their managed `current` binaries and load
  their optional fixed sidecars after the base environment files;
- verify Control Panel `/health` and `/updater/version` on its configured
  loopback port, and do the same for Observability;
- install and successfully run both fixed backup scripts;
- prepare `/etc/autostream-local-executor/mariadb-backup.cnf` as
  `root:root 0600` and the two backup directories as `root:root 0700`.

Verify and extract the matching Host Agent release artifact, enter its extracted
directory, then prepare the non-root Agent and root executor boundary:

```bash
sudo ./install/install-autostream-host-agent --prepare
```

In **Application Info > System Updates**, create one Update Agent using
`pull_v2` and a stable execution-host ID. Assign the Control Panel and
Observability systemd targets to it. For every non-Control-Panel systemd
target, set **ローカル待受ポート** to the port where that service actually
listens on `127.0.0.1`. This is not the public reverse-proxy or Cloudflare
Tunnel port. For example, keep a public `https://observability.example.com:443`
endpoint and enter `8082` when the local origin is `127.0.0.1:8082`. Confirm
the active environment or sidecar instead of assuming a default. Packaged
defaults are Encoder / Recorder `8081`, Observability `8082`, Discord Bot
`8083`, and Worker `8084`; the Control Panel listener is derived directly from
`AUTOSTREAM_BIND_ADDR` and is not entered here.

Set **MariaDBデータベース名** to the exact final component of each service's
real `DATABASE_URL`. The packaged defaults are:

- Control Panel: `autostream_control_panel`
- Observability: `autostream_observability`

Replace either default when the real `DATABASE_URL` differs. Saving binds each
value as server-owned `database_name`; it does not place either database name
in the configure command.

After upgrading a policy created before local-listener bindings were
available, a target whose public endpoint uses privileged port `443` has no
safe legacy local-port fallback. Enter its verified local listener, save the
Updater settings, issue a fresh Configure Token, and only then rerun
`autostream-host-agent configure`. Do not rewrite the registered public
endpoint to loopback merely to make configure pass.

Copy the generated command and run it on this host. It has this form:

```bash
sudo /usr/local/bin/autostream-host-agent configure \
  --panel-url https://control.example.com \
  --node registered-update-agent-service-id \
  --config /etc/autostream-host-agent/identity.json
```

Enter the one-time Configure Token at the protected prompt. The command reads it
from the TTY or bounded standard input with echo disabled. It atomically stages
`/etc/autostream-host-agent/identity.json`, the canonical
`/etc/autostream-local-executor/policy.json`, and any missing fixed port
sidecars. It refuses incomplete applied target state and never accepts target
paths, systemd units, backup commands, DB credentials, or Runtime Tokens in
argv.

If normal configure on v1.9.7 reports that one existing systemd sidecar
differs, preserve that sidecar and the live service state. First verify and
extract the version-matched v1.9.8 Host Agent archive, leave the unchanged
archive adjacent, and install the Host bridge that provides the recovery flag:

```bash
cd /opt/autostream/releases/artifacts/autostream-host-agent_v1.9.8_linux_amd64
sudo ./install/install-autostream-host-agent --upgrade
```

The installer upgrades the Host Agent and Local Executor together and performs
their bounded installer-controlled restarts. After that upgrade succeeds, do
not delete/edit the sidecar and do not manually restart the Host Agent or the
affected target service until explicit adoption Configure finishes. The
v1.9.8-or-later Host Agent supports this narrow recovery only when that file is
the exact canonical sidecar for the current root policy and the live eligible
service is already running on the staged port with the same config and
endpoint revisions. A missing sidecar is never the adoption candidate and
remains governed by normal create-if-absent behavior for the other managed
targets; explicit adoption still requires exactly one differing existing
sidecar. Only then issue a fresh Configure Token for the same Node, enter it
through the protected TTY/stdin prompt (never argv or an environment variable),
and rerun the generated command with the single boolean
`--adopt-live-systemd-sidecar` flag. The command rejects Control Panel,
multiple mismatches, caller-selected target values, port-ledger state, or any
failure to prove the managed unit/process/listener/HTTP identity before and
after the atomic exchange. It never restarts a service.

Activate the exact generated root policy from the same extracted Host Agent
release, then enable the Agent:

```bash
sudo ./install/install-autostream-local-executor \
  --policy /etc/autostream-local-executor/policy.json
sudo systemctl enable --now autostream-host-agent.service
```

For later Host runtime releases, verify and extract the new matching Host Agent
archive, leave that unchanged archive adjacent to the newly extracted release
directory, and enter that concrete new Host Agent release root before running
the archive installer:

```bash
# Keep ../autostream-host-agent_vX.Y.Z_linux_amd64.tar.gz unchanged and adjacent.
cd /opt/autostream/releases/artifacts/autostream-host-agent_vX.Y.Z_linux_amd64
sudo ./install/install-autostream-host-agent --upgrade
```

This mode preserves the installed identity and Local Executor policy, uses the
fixed A/B slots, rejects active durable mutations and downgrades, and rolls back
both processes together when activation proof fails. See the archive-contained
`README.md` for the exact prerequisites and recovery semantics. Do not replace
the Host Agent or Local Executor binary separately.

### Recover an active v1.9.9 or v1.9.10 Host job while upgrading

`--recover-active-job` is a bounded bridge only for an existing managed A/B
runtime whose live Host Agent and Local Executor are an exact paired v1.9.9 or
v1.9.10 release. Their version, commit, and build date must match; both public
binary links and both systemd processes must resolve to that same current slot,
and the live Executor must expose mutation and recovery protocol 2. It is not a
generic recovery path for every pre-v1.9.11 release, a mixed-version pair, a
standalone install, or an inactive or mismatched Executor. The old Agent must
also begin enabled and active with a live MainPID. The Executor service and
socket plus both fixed recovery timers must be active; the Agent, socket, and
timers must be enabled.

For such an exact pair with an active journal that normal `--upgrade` rejects,
install and verify the Control Panel v1.9.11 release first. Then verify and
extract the matching v1.9.11 Host archive and explicitly opt into the one-shot
recovery path:

```bash
cd /opt/autostream/releases/artifacts/autostream-host-agent_v1.9.11_linux_amd64
sudo ./install/install-autostream-host-agent --upgrade --recover-active-job
```

Use the matching `linux_arm64` archive and directory on arm64. This is the only
supported active-job upgrade form; ordinary `--upgrade` still rejects an active
job. It needs no Configure Token and must not rerun `configure`. The verified
candidate runs once as `autostream-host-agent`, is bound to the journal's exact
active job, and can only reconcile and report that job. It cannot claim a new
job or invoke Stage or Apply. A bare cursor-clear response and any terminal
proof or committed terminal report body that does not exactly match are
rejected.

The installer arms a transient systemd restart guard before stopping the old
Agent. After the recovery-only candidate obtains strict terminal proof, the
Agent stays intentionally stopped while the same command hands the pinned pair
to the paired A/B upgrader. The Executor remains live, and the stopped on-disk
Agent identity and live Executor identity must still match exactly. The guard
is disarmed only after the resulting managed Agent is active and proved. If
recovery or the paired upgrade fails, the installer restores and proves the
exact previous pair and restarts the previous Agent; if it cannot reacquire the required
locks or prove that result, the Agent remains stopped and the guard remains
armed for console recovery. Do not delete or edit the Host Agent journal, Local
Executor ledger, staged release, or update row before retrying. Do not create a
replacement job, restage, reapply, or issue a new Configure Token.

v1.9.10 and later include the Host Agent startup fix for service hosts where
`/etc/autostream` correctly remains `root:root 0750`. The matched runtime
securely loads the canonical `/etc/autostream-host-agent/identity.json`
without requiring an ACL on the application-secret directory, while every
root-owned configure and Runtime Token mutation still rejects a reachable
legacy `/etc/autostream/host-agent.json`. A v1.9.9 Agent already stopped by the
permission failure cannot enter the intentionally healthy-only `--upgrade`
path directly. Follow the archive-contained `README.md` for the bounded
v1.9.9-only execute-only ACL recovery bridge, matched current-release upgrade, and
immediate post-upgrade removal of only that named entry. Never use
`setfacl -b`, `chmod 0751`, a read ACL, or an application-group grant.

Return to **Application Info > System Updates**, confirm the first observe-only
heartbeat, review the projected targets and policy digest, then activate
ownership. Only a positive ownership epoch with a matching policy digest can
claim update jobs.

Verify the local boundary:

```bash
sudo /usr/local/libexec/autostream-local-executor validate-policy \
  --policy /etc/autostream-local-executor/policy.json
sudo systemctl status autostream-host-agent.service
sudo systemctl status autostream-local-executor.socket
sudo systemctl status autostream-local-executor.service
sudo stat -c '%U:%G:%a %n' \
  /etc/autostream-host-agent/identity.json \
  /etc/autostream-local-executor/policy.json \
  /etc/autostream-local-executor/mariadb-backup.cnf \
  /var/backups/autostream/control-panel \
  /var/backups/autostream/observability
```

No SSH key, `known_hosts`, `/etc/autostream/updater.json`, or central
`autostream-updater` daemon is part of this outbound-only `pull_v2` path. Keep a
legacy updater stopped after pull_v2 ownership and both target observations are
healthy. Do not delete its identity or forced-command authorization until the
new path has completed a staged update and rollback/reconcile exercise.

## Roll back to an older Control Panel writer

Before starting a Control Panel binary from before the revision-bound database
settings, first confirm that no update, recovery, or token-rotation job is
active, deactivate `pull_v2` ownership in System Updates, and stop the Host
Agent on the affected host. Perform a `single-writer` drain: stop every Control
Panel instance, verify that no old or new instance can write
`update_agent_policies`, then start exactly one selected binary. Never overlap
old and new Control Panel writers.

Treat a pre-059 Control Panel binary as read-only for updater policy settings.
Do not save System Updates settings with the old binary. It cannot advance the
policy row and its companion database-name bindings as one revision-bound
write. If an old writer nevertheless advances a policy revision, keep
`pull_v2` ownership inactive, stop it, and roll forward to the current Control
Panel as the sole writer. In **Application Info > System Updates**, re-save the
exact MariaDB database names for every Control Panel and Observability target,
then rerun the generated Host Agent configure command and validate the
installed Local Executor policy. Reactivate ownership only after the new
configure revision and target observations are applied.

## Database backup and Docker credentials

The Local Executor can read its dedicated
`/etc/autostream-local-executor/mariadb-backup.cnf`, but `/etc/autostream`
remains inaccessible. It can write only the two fixed database backup
directories, not `/var/backups/autostream` as a whole. A nonzero dump exit
aborts an update before stopping either service.

Docker credentials remain a separate root-owned Local Executor prerequisite for
Docker targets. They are not used by the Control Panel and Observability
systemd targets in this guide.

## Update the Host Agent and Local Executor

The Host Agent artifact contains both runtime binaries and fixed A/B recovery
units. Follow `README.md` in that artifact under **Host Agent and Local Executor
self-update**. Do not replace either active binary directly while an update,
token rotation, or recovery transaction is running.
