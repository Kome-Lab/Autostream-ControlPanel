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
- A dedicated `autostream` user and group.
- Authenticated `gh`, `jq`, `sha256sum`, and `curl` for release verification,
  plus `/usr/bin/mariadb-dump` for the required pre-update backup.
- The matching verified `autostream-host-agent_<version>_linux_<arch>.tar.gz`
  artifact for the host.
- A reverse proxy with HTTPS for production.
- A production database and secret values supplied outside Git.

## Install a verified managed release for the Control Panel target

The managed layout is required when the Control Panel itself will be updated
with rollback. An existing direct `/usr/local/bin/control-panel` and
`/usr/share/autostream-control-panel` installation must first be migrated to
the verified release tree below. The compatibility `/usr/local/bin/control-panel`
link may remain, but it must resolve through the managed `current` link.

The systemd unit runs the Control Panel through
`/opt/autostream/control-panel/current`. Seed that link from the same immutable
release manifest and checksums that supplied the archive. Automated updates
refuse an unseeded target because it would have no verified rollback release.
When replacing an existing Control Panel manually, record the current link and
complete a database backup before running the switch below.

```bash
set -euo pipefail
VERSION="${VERSION:?export VERSION=vX.Y.Z before continuing}"
ARCH="${ARCH:-amd64}"
ASSET="autostream-control-panel_${VERSION}_linux_${ARCH}.tar.gz"
ARTIFACT_ROOT=/opt/autostream/releases

sudo install -d -o root -g root -m 0755 "$ARTIFACT_ROOT"
sudo install -d -o "$USER" -g "$USER" -m 0755 "$ARTIFACT_ROOT/artifacts"
gh release download "$VERSION" \
  --repo Kome-Lab/Autostream-ControlPanel \
  --pattern "$ASSET" \
  --pattern "$ASSET.sha256" \
  --pattern release-manifest.json \
  --pattern release-manifest.json.sha256 \
  --dir "$ARTIFACT_ROOT/artifacts" \
  --clobber
(cd "$ARTIFACT_ROOT/artifacts" && sha256sum --check --strict "$ASSET.sha256")
(cd "$ARTIFACT_ROOT/artifacts" && sha256sum --check --strict release-manifest.json.sha256)

DIGEST="$(awk 'NR == 1 { print $1 }' "$ARTIFACT_ROOT/artifacts/$ASSET.sha256")"
[[ "$DIGEST" =~ ^[0-9a-f]{64}$ ]]
jq -e --arg version "$VERSION" --arg asset "$ASSET" --arg sha "$DIGEST" \
  '.schema_version == 1 and .release_id == $version and .channel == "host" and
   ([.components[] | select(.service == "control-panel" and .source_version == $version) |
     .artifacts[] | select(.name == $asset and .sha256 == $sha)] | length == 1)' \
  "$ARTIFACT_ROOT/artifacts/release-manifest.json"

RELEASE_ROOT=/opt/autostream/control-panel/releases
RELEASE_DIR="$RELEASE_ROOT/${VERSION}-${DIGEST:0:12}"
sudo test ! -e "$RELEASE_DIR"
sudo install -d -o root -g root -m 0755 "$RELEASE_DIR"
sudo tar --no-same-owner --strip-components=1 -xzf "$ARTIFACT_ROOT/artifacts/$ASSET" -C "$RELEASE_DIR"
(cd "$RELEASE_DIR" && sha256sum --check --strict checksums.txt)
sudo test -d "$RELEASE_DIR/share/autostream-control-panel"
printf '%s\n' "$DIGEST" | sudo tee "$RELEASE_DIR/.artifact-sha256" >/dev/null
printf '%s\n' "$VERSION" | sudo tee "$RELEASE_DIR/.version" >/dev/null
sudo chown root:root "$RELEASE_DIR/.artifact-sha256" "$RELEASE_DIR/.version"
sudo chmod 0444 "$RELEASE_DIR/.artifact-sha256" "$RELEASE_DIR/.version"
sudo /usr/sbin/runuser -u autostream -- "$RELEASE_DIR/bin/control-panel" --version | grep -F -- "$VERSION"
```

## Prepare the updater backup command

A Control Panel target is fail-closed unless its fixed backup command exists
and succeeds. Install the verified script from this release and prepare its
private directory and MariaDB client defaults before enabling the updater:

```bash
set -euo pipefail
VERSION="${VERSION:?export VERSION=vX.Y.Z before continuing}"
ARCH="${ARCH:-amd64}"
ASSET="autostream-control-panel_${VERSION}_linux_${ARCH}.tar.gz"
ARTIFACT_ROOT=/opt/autostream/releases
DIGEST="$(awk 'NR == 1 { print $1 }' "$ARTIFACT_ROOT/artifacts/$ASSET.sha256")"
[[ "$DIGEST" =~ ^[0-9a-f]{64}$ ]]
RELEASE_DIR="/opt/autostream/control-panel/releases/${VERSION}-${DIGEST:0:12}"
sudo test -d "$RELEASE_DIR"
test -x "$RELEASE_DIR/backup/autostream-backup-control-panel"
sudo install -d -o root -g root -m 0700 /var/backups/autostream/control-panel
sudo install -o root -g root -m 0700 "$RELEASE_DIR/backup/autostream-backup-control-panel" /usr/local/sbin/autostream-backup-control-panel
sudo install -d -o root -g root -m 0700 /etc/autostream-local-executor
if ! sudo test -e /etc/autostream-local-executor/mariadb-backup.cnf; then
  sudo install -o root -g root -m 0600 /dev/null \
    /etc/autostream-local-executor/mariadb-backup.cnf
else
  echo "preserving existing /etc/autostream-local-executor/mariadb-backup.cnf"
fi
sudo chown root:root /etc/autostream-local-executor/mariadb-backup.cnf
sudo chmod 0600 /etc/autostream-local-executor/mariadb-backup.cnf
```

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

## Activate the managed release

Only after the real backup succeeds, switch the managed link and install the
unit. Recompute the release directory from the already verified sidecar so this
separate shell cannot silently select a different archive:

```bash
set -euo pipefail
VERSION="${VERSION:?export VERSION=vX.Y.Z before continuing}"
ARCH="${ARCH:-amd64}"
ASSET="autostream-control-panel_${VERSION}_linux_${ARCH}.tar.gz"
ARTIFACT_ROOT=/opt/autostream/releases
DIGEST="$(awk 'NR == 1 { print $1 }' "$ARTIFACT_ROOT/artifacts/$ASSET.sha256")"
[[ "$DIGEST" =~ ^[0-9a-f]{64}$ ]]
RELEASE_DIR="/opt/autostream/control-panel/releases/${VERSION}-${DIGEST:0:12}"
CURRENT_LINK=/opt/autostream/control-panel/current
sudo test -d "$RELEASE_DIR"
test "$(sudo cat "$RELEASE_DIR/.version")" = "$VERSION"

sudo ln -s "$RELEASE_DIR" "${CURRENT_LINK}.next"
sudo mv -Tf "${CURRENT_LINK}.next" "$CURRENT_LINK"
sudo ln -sfn "$CURRENT_LINK/bin/control-panel" /usr/local/bin/control-panel
sudo install -d -o autostream -g autostream /var/lib/autostream/control-panel
sudo install -o root -g root -m 0644 "$RELEASE_DIR/systemd/autostream-control-panel.service.example" /etc/systemd/system/autostream-control-panel.service
if ! sudo test -e /etc/autostream/control-panel.env; then
  sudo install -o root -g root -m 0640 "$RELEASE_DIR/.env.example" /etc/autostream/control-panel.env
else
  echo "preserving existing /etc/autostream/control-panel.env; review .env.example for new settings"
fi
sudo sed -i 's#^AUTOSTREAM_WEB_DIR=.*#AUTOSTREAM_WEB_DIR=/opt/autostream/control-panel/current/share/autostream-control-panel#' /etc/autostream/control-panel.env
sudo grep -qx 'AUTOSTREAM_WEB_DIR=/opt/autostream/control-panel/current/share/autostream-control-panel' /etc/autostream/control-panel.env
```

Edit `/etc/autostream/control-panel.env` with real environment-specific values.
Keep `AUTOSTREAM_WEB_DIR` pointed at the managed `current` link, then run:

```bash
set -euo pipefail
VERSION="${VERSION:?export VERSION=vX.Y.Z before continuing}"
sudo systemctl daemon-reload
sudo systemctl enable autostream-control-panel
sudo systemctl restart autostream-control-panel
PID="$(sudo systemctl show --property=MainPID --value autostream-control-panel)"
EXPECTED="$(sudo readlink -f /opt/autostream/control-panel/current/bin/control-panel)"
test "$(sudo readlink -f "/proc/$PID/exe")" = "$EXPECTED"
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:8080/health >/dev/null
test "$(curl --fail --silent --show-error --max-time 10 \
  http://127.0.0.1:8080/updater/version | jq -r '.version')" = "$VERSION"
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
