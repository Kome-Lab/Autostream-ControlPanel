# AutoStream Control Panel Host Install

This archive contains only the Control Panel Linux binary, its systemd example,
placeholder configuration, backup helper, installer, and matching web assets.
The Agent, Local Executor, Docker/systemd mutation runtime, and their installers
are owned and released independently by
[Kome-Lab/Autostream-Updater](https://github.com/Kome-Lab/Autostream-Updater).
They are not embedded in this archive.

The Control Panel remains the authorization, policy, orchestration, central
job-state, and audit authority. Its updater integration is an HTTP adapter; it
does not execute host commands or provide a fallback embedded runtime. Every
managed application continues to own its exact `/updater/version` identity probe.

## Requirements

- Linux amd64 or arm64 matching the archive name.
- On the operator machine: `gh` for GitHub artifact-attestation verification.
- On the server: `flock`, `jq`, `sha256sum`, `tar`, systemd, and
  `/usr/bin/mariadb-dump`.
- One unchanged Control Panel `.tar.gz` from the immutable GitHub Release.
- A reverse proxy with HTTPS for production.
- Production database and secret values supplied outside Git.

Install the independent Updater runtime only from its own verified release and
follow its repository-contained installation and recovery documentation. Never
copy an Agent or Executor binary from a Control Panel archive.

## Install or migrate the Control Panel

Download `autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz` on the
operator machine and verify its GitHub artifact attestation before transferring
it. For arm64, use the arm64 archive instead.

```bash
gh attestation verify /tmp/autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz \
  --repo Kome-Lab/Autostream-ControlPanel \
  --signer-workflow Kome-Lab/Autostream-ControlPanel/.github/workflows/release-host.yml \
  --deny-self-hosted-runners
```

サーバーへ転送する release asset は、この `.tar.gz` 1 個だけです。Upload it
to `/tmp`, copy it into the fixed root-owned artifact directory, extract it
as root, and leave the unchanged archive adjacent to the extracted directory
until installation completes:

```bash
sudo install -d -o root -g root -m 0755 /opt/autostream/releases/artifacts
sudo install -o root -g root -m 0644 /tmp/autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz /opt/autostream/releases/artifacts/
cd /opt/autostream/releases/artifacts
sudo tar --no-same-owner --no-same-permissions -xzf autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz
cd autostream-control-panel_vX.Y.Z_linux_amd64
sudo ./install-autostream-control-panel
```

The installer makes a stable private copy of the adjacent archive, verifies its
SHA-256, rejects unsafe archive entries, re-extracts it, and checks the complete
Control Panel artifact manifest and binary identity before host mutation. It
creates the dedicated `autostream` account when needed, installs the unit and
backup executable, preserves existing configuration, and exposes:

- `/usr/local/bin/control-panel`
- `/usr/share/autostream-control-panel`

Existing direct-install files are retained under
`/var/backups/autostream/install-migrations/control-panel`. Existing
`/etc/autostream/control-panel.env` content is preserved byte-for-byte.
`/opt/autostream/control-panel/releases` and
`/opt/autostream/control-panel/current` are installer-owned release and
rollback state; do not edit or recreate them manually.

## Prepare the fixed database backup

A Control Panel target is fail-closed unless its fixed backup command exists and
succeeds. The installer creates the private backup directory, installs
`/usr/local/sbin/autostream-backup-control-panel`, and safely creates or
preserves `/etc/autostream-local-executor/mariadb-backup.cnf`. The path is
retained as a cross-version compatibility input for the independent Updater.
Only the real database credentials, account, grant, and database name remain
manual.

Configure the root-only defaults file:

```ini
[client]
host=127.0.0.1
port=3306
protocol=tcp
user=autostream_backup
password=replace-with-a-long-random-password
```

Create the MariaDB account from an interactive root session, with the real
password supplied outside shell history:

```sql
CREATE USER IF NOT EXISTS 'autostream_backup'@'127.0.0.1' IDENTIFIED BY 'replace-with-a-long-random-password';
```

Select the database name once and keep the same shell open. The same exact
`DATABASE_NAME` must be used for the MariaDB grant, the real dump, and the
server-owned target setting:

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
combined only with the fixed backup executable. The adapter never accepts an
arbitrary backup executable or argv.

The backup script uses `umask 077` and atomically renames a timestamped,
non-empty dump only after `mariadb-dump` succeeds. Configure retention and
encrypted off-host copying separately.

## Review settings and start the service

Edit `/etc/autostream/control-panel.env` with the real environment-specific
values. Keep this public web path:

```ini
AUTOSTREAM_WEB_DIR=/usr/share/autostream-control-panel
```

For a first installation:

```bash
sudo systemctl enable --now autostream-control-panel
sudo systemctl status autostream-control-panel
```

For an already-running older installation, the installer preserves its settings
and process. Restart explicitly only after reviewing the settings:

```bash
sudo systemctl restart autostream-control-panel
sudo systemctl status autostream-control-panel
```

Use the configured loopback port when it differs from `8080`.
`/updater/version` is the no-store, application-owned identity endpoint.
The authenticated Application Info `/version` route is not a target probe.
Block the exact `/updater/version` path at the public reverse proxy.

## Connect the independent Updater

Use the verified release, installer, systemd units, and recovery instructions
from `Kome-Lab/Autostream-Updater`. The installed compatibility names may still
include `autostream-host-agent` and `autostream-local-executor` during the
replacement wave, but their source, packaging, CI, and release authority belong
exclusively to that repository.

In **Application Info > System Updates**, register one `pull_v2` execution
host, assign the desired targets, save the exact local listener/database
settings, and run the generated Host Agent configure command. Confirm the first
observe-only heartbeat and policy digest before activating ownership.

The supported mixed-fleet behavior is fail-closed:

- new Control Panel + new Updater: v2 operations may proceed;
- new Control Panel + no Updater: blocked, with no embedded fallback;
- old Control Panel + new Updater: no unsafe mutation; compatibility is explicit;
- new Control Panel + embedded-only runtime: prohibited and blocked.

Do not delete legacy identity or recovery state merely because new ownership is
healthy. Removal of legacy host state remains a separately authorized EOL task.

## Roll back to an older Control Panel writer

Before starting an older Control Panel binary, confirm no update, recovery, or
token-rotation job is active and deactivate `pull_v2` ownership. Drain every
Control Panel instance and run exactly one selected writer. Never overlap old
and new writers.

A pre-revision-bound writer must be treated as read-only for Updater policy
settings. Roll forward to the current Control Panel, re-save the exact target
settings, and reactivate ownership only after the independent Updater has
applied the matching configure revision and reported healthy observations.

Do not fabricate artifact digests or version files, modify or recompress an
attested archive, commit real environment files or credentials, or expose
`/updater/version` through the public proxy.
