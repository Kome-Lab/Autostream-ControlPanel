# AutoStream Control Panel Host Install

This archive contains the Control Panel Linux binary, systemd example,
placeholder configuration, and matching web assets. The current automatic
update architecture uses the separate `autostream-host-agent` release artifact
on every physical host. Its outbound-only `pull_v2` Agent drives a root Local
Executor over a private Unix socket. The standalone central
`autostream-updater` and SSH `autostream-update-host` path are legacy migration
inputs and are not installed by this guide.

## Requirements

- Linux amd64 or arm64 matching the archive name.
- `gh`, `jq`, `sha256sum`, `tar`, systemd, and `/usr/bin/mariadb-dump`. The
  installer creates the dedicated `autostream` account when it is missing.
- All four files listed below, obtained from the same immutable GitHub Release.
- The matching verified `autostream-host-agent_<version>_linux_<arch>.tar.gz`
  artifact for the host.
- A reverse proxy with HTTPS for production.
- A production database and secret values supplied outside Git.

## Install or migrate the Control Panel

Download these four files from the same immutable GitHub Release and keep them
in one directory:

- `autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz`
- `autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz.sha256`
- `release-manifest.json`
- `release-manifest.json.sha256`

For arm64, use the two arm64 artifact files instead. Copy the four downloaded
files into the root-owned artifact directory, then verify the copied manifest
as your ordinary login user:

```bash
sudo install -d -o root -g root -m 0755 /opt/autostream/releases/artifacts
sudo install -o root -g root -m 0644 /tmp/autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz /opt/autostream/releases/artifacts/
sudo install -o root -g root -m 0644 /tmp/autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz.sha256 /opt/autostream/releases/artifacts/
sudo install -o root -g root -m 0644 /tmp/release-manifest.json /opt/autostream/releases/artifacts/
sudo install -o root -g root -m 0644 /tmp/release-manifest.json.sha256 /opt/autostream/releases/artifacts/
cd /opt/autostream/releases/artifacts
gh attestation verify autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz \
  --repo Kome-Lab/Autostream-ControlPanel \
  --signer-workflow Kome-Lab/Autostream-ControlPanel/.github/workflows/release-host.yml \
  --deny-self-hosted-runners
gh attestation verify release-manifest.json \
  --repo Kome-Lab/Autostream-ControlPanel \
  --signer-workflow Kome-Lab/Autostream-ControlPanel/.github/workflows/release-host.yml \
  --deny-self-hosted-runners
```

After verification succeeds, extract and run the installer:

```bash
cd /opt/autostream/releases/artifacts
sudo tar --no-same-owner --no-same-permissions -xzf autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz
cd autostream-control-panel_vX.Y.Z_linux_amd64
sudo ./install-autostream-control-panel
```

The installer verifies the archive sidecar, release manifest, architecture,
inner checksums, and binary version before changing the host. It then creates
the `autostream` account when needed, seeds the rollback release, installs the
unit and backup executable, creates state directories, and exposes:

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
binary. Releases without `release-manifest.json` remain manual-only; publish a
new release instead of modifying an existing release asset.

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
Observability systemd targets to it. Set **MariaDBデータベース名** to the
exact final component of each service's real `DATABASE_URL`. The packaged
defaults are:

- Control Panel: `autostream_control_panel`
- Observability: `autostream_observability`

Replace either default when the real `DATABASE_URL` differs. Saving binds each
value as server-owned `database_name`; it does not place either database name
in the configure command.

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

Activate the exact generated root policy from the same extracted Host Agent
release, then enable the Agent:

```bash
sudo ./install/install-autostream-local-executor \
  --policy /etc/autostream-local-executor/policy.json
sudo systemctl enable --now autostream-host-agent.service
```

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
