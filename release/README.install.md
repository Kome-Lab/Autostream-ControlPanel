# AutoStream Control Panel Host Install

This archive contains the Control Panel and central `autostream-updater` Linux
binaries, systemd examples, placeholder configuration, and matching web assets.
The central updater is installed once. Managed hosts use the separate
non-resident `autostream-update-host` bootstrap artifact.

## Requirements

- Linux amd64 or arm64 matching the archive name.
- A dedicated `autostream` user and group.
- Authenticated `gh`, `jq`, `sha256sum`, and `curl` for release verification,
  plus `/usr/bin/mariadb-dump` for the required pre-update backup.
- OpenSSH client access from the central updater host to every managed host.
- A reverse proxy with HTTPS for production.
- A production database and secret values supplied outside Git.

## Install a verified managed release for the Control Panel target

Use this section only when the Control Panel itself will be updated as a managed
target with rollback. Installing only the central updater does not require
migrating an existing `/usr/local/bin/control-panel` and
`/usr/share/autostream-control-panel` installation. For that existing direct
layout, skip to **Install the central updater once**.

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
sudo install -d -o root -g root -m 0755 /etc/autostream
if ! sudo test -e /etc/autostream/mariadb-backup.cnf; then
  sudo install -o root -g root -m 0600 /dev/null /etc/autostream/mariadb-backup.cnf
else
  echo "preserving existing /etc/autostream/mariadb-backup.cnf"
fi
sudo chown root:root /etc/autostream/mariadb-backup.cnf
sudo chmod 0600 /etc/autostream/mariadb-backup.cnf
```

Set the root-only defaults file to a dedicated backup account. A shared host
may reuse this account/file for Observability after granting that database
separately:

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
second `backup_argv` item. In this example, replace the default with the final
path component of the real `DATABASE_URL` when they differ:

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

test "$(sudo stat -c '%u:%a' /etc/autostream/mariadb-backup.cnf)" = "0:600"
test "$(sudo stat -c '%u:%a' /usr/local/sbin/autostream-backup-control-panel)" = "0:700"
sudo /usr/local/sbin/autostream-backup-control-panel "$DATABASE_NAME"
printf 'Use this exact database name as the second backup_argv item: %s\n' "$DATABASE_NAME"
```

Copy the value printed by that command into the root-owned host policy. It is
never supplied by an update job or the browser:

```json
"backup_argv": [
  "/usr/local/sbin/autostream-backup-control-panel",
  "replace-with-the-exact-DATABASE_NAME-printed-above"
]
```

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

## Install the central updater once

The central updater is the only persistent updater process. It claims jobs from
the Control Panel and opens outbound, host-key-pinned SSH connections. It has no
sudo rule, Docker socket, `systemctl` authority, or root helper. Privileged
target policy remains on each managed host in root-owned
`/etc/autostream/update-host.json`.

Install the central binary and service directly from the extracted Control Panel
archive. This procedure uses the existing `/usr/local/bin` Control Panel layout
and `/usr/share/autostream-control-panel`; it does not assume that
`/opt/autostream/control-panel/current/bin` exists. Replace `vX.Y.Z` below with
the extracted release version.

```bash
getent group autostream-updater >/dev/null 2>&1 || \
  sudo groupadd --system autostream-updater
id -u autostream-updater >/dev/null 2>&1 || \
  sudo useradd --system --gid autostream-updater \
    --home /var/lib/autostream-updater --shell /usr/sbin/nologin \
    autostream-updater
sudo install -d -o autostream-updater -g autostream-updater -m 0700 \
  /var/lib/autostream-updater
sudo install -d -o root -g root -m 0755 /etc/autostream
cd /opt/autostream/releases/artifacts/autostream-control-panel_vX.Y.Z_linux_amd64
sudo install -o root -g root -m 0755 \
  bin/autostream-updater /usr/local/bin/autostream-updater
sudo install -o root -g root -m 0644 \
  systemd/autostream-updater.service.example \
  /etc/systemd/system/autostream-updater.service
sudo systemctl daemon-reload
```

Create exactly one `Update Agent` Node in the Control Panel for this central
updater. Do not create one for every managed host. Copy the command shown in the
Node Configuration view and run it once on the central host:

```bash
sudo /usr/local/bin/autostream-updater configure --panel-url "https://control.example.com" --node "central-updater"
```

Paste the separately displayed Configure Token into the prompt. The updater
reads it from the TTY or bounded standard input with echo disabled, so it is not
placed in the command, process arguments, or shell history. In this one run the
updater stages the identity, atomically creates
`/etc/autostream/updater.json` as `root:autostream-updater 0640`, validates it,
and activates the Runtime Token. The generated file contains only the Control
Panel connection identity. Do not edit it and do not add a GitHub Release
Token, host, target, or SSH setting to it.

Enable the service after configure succeeds:

```bash
sudo -u autostream-updater test -r /etc/autostream/updater.json
sudo -u autostream-updater test -w /var/lib/autostream-updater
sudo systemd-analyze verify /etc/systemd/system/autostream-updater.service
sudo systemctl enable --now autostream-updater
sudo systemctl status autostream-updater
```

Open **Application Info > System Updates**, select the central updater, and
configure:

- the GitHub Release Token. The GitHub Release Token is required for every
  managed update, whether the repository is public or private. It is write-only
  in the Control Panel and is never shown after saving.
- the loopback API port and the poll and heartbeat intervals;
- each host ID, name, address, SSH port, SSH user, and architecture;
- the complete SSH server public key, verified through an independent channel;
- the targets assigned to each host.

Do not trust `ssh-keyscan` output by itself. Compare the fingerprint with the
server console or another independent inventory before saving the complete host
public key. The Control Panel stores the GitHub Release Token as an encrypted
secret and delivers it only once to the updater that claims an authorized
update job.

Saving starts automatic pull and validation. No service restart is required.
For every new host the updater generates a separate Ed25519 client key and
reports only its public key to the System Updates page. Copy that reported
public key into a file for that host's bootstrap administrator; never copy the
private key.

Each remote host is installed from the separate
`autostream-update-host_<version>_linux_<arch>.tar.gz` artifact. Follow its
`README.bootstrap.md`, using the reported client public key as
`--authorized-key`. The bootstrap installs the root-owned
`/etc/autostream/update-host.json`, forced SSH command, and non-resident helper,
not a daemon or token.

The settings view reports `applied`, `pending`, or `failed`. `applied` means the
updater accepted the desired revision and is running with it; it does not mean
every host is reachable. A missing helper, an uninstalled client public key, a
server-key mismatch, or a remote-policy mismatch is shown separately as host
`unreachable`. The updater retries host probes automatically. If an update job
is active when settings are saved, it defers applying the new revision until
that job reaches a safe terminal state. A failed revision leaves the previous
applied settings active.

The unit is intentionally hardened with `NoNewPrivileges`, an empty capability
set, a read-only system image, and a single writable state directory. If it
cannot start, fix the connection identity, state ownership, or OS systemd
compatibility. Do not weaken the unit or add a broad sudo rule.

## Database backup and Docker credentials

Control Panel and Observability targets still require a root-owned backup
command on the host that owns the database. Docker targets still require that
host's root Docker credential store when pulling private GHCR images. These are
remote target prerequisites and are not credentials for the central updater.
Configure and test them during the managed-host bootstrap.

The non-resident helper refuses an unverified rollback baseline. A legacy
release without an immutable manifest remains manual-only. Publish and manually
deploy a new manifest-bearing release, verify health and version, then approve
it as the initial rollback baseline. Never add assets to an existing tag.

## Update the central updater binary

The central updater is not one of its own managed targets. It stays at the
fixed `/usr/local/bin/autostream-updater` path. Wait until no update job is
active, verify and extract the new Control Panel host artifact, then replace it
explicitly. Replace `vX.Y.Z` with the extracted release version:

```bash
cd /opt/autostream/releases/artifacts/autostream-control-panel_vX.Y.Z_linux_amd64
sudo systemctl stop autostream-updater
sudo install -o root -g root -m 0755 \
  bin/autostream-updater \
  /usr/local/bin/autostream-updater.next
sudo mv -f /usr/local/bin/autostream-updater.next \
  /usr/local/bin/autostream-updater
/usr/local/bin/autostream-updater --version
sudo systemctl start autostream-updater
```

Update remote helper binaries through an explicit, verified maintenance
bootstrap after active jobs finish. Re-running a central Control Panel update
does not silently replace helpers on other hosts.
